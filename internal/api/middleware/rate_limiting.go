package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type UserRateLimiter struct {
	db          *sql.DB
	logger      *zap.Logger
	cleanupOnce sync.Once
}

func NewUserRateLimiter(db *sql.DB, logger *zap.Logger) *UserRateLimiter {
	rl := &UserRateLimiter{
		db:     db,
		logger: logger,
	}
	
	// Start cleanup goroutine once
	rl.cleanupOnce.Do(func() {
		go rl.startCleanupScheduler()
	})
	
	return rl
}

// UserRateLimit applies per-user rate limiting
func (rl *UserRateLimiter) UserRateLimit(requestsPerMinute int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			// Skip rate limiting for unauthenticated requests (use global rate limiting)
			c.Next()
			return
		}

		endpoint := c.Request.Method + " " + c.FullPath()
		
		if !rl.checkUserRateLimit(userID, endpoint, requestsPerMinute) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "User rate limit exceeded",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

func (rl *UserRateLimiter) checkUserRateLimit(userID, endpoint string, limit int) bool {
	now := time.Now()
	windowStart := now.Truncate(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to increment counter
	query := `
		INSERT INTO user_rate_limits (user_id, endpoint, request_count, window_start)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (user_id, endpoint, window_start)
		DO UPDATE SET 
			request_count = user_rate_limits.request_count + 1,
			updated_at = NOW()
		RETURNING request_count`

	var count int
	err := rl.db.QueryRowContext(ctx, query, userID, endpoint, windowStart).Scan(&count)
	if err != nil {
		rl.logger.Error("Failed to check user rate limit", 
			zap.Error(err),
			zap.String("user_id", userID),
			zap.String("endpoint", endpoint))
		return true // Allow on error
	}

	return count <= limit
}

// CleanupExpiredRateLimits removes old rate limit records
func (rl *UserRateLimiter) CleanupExpiredRateLimits() {
	cutoff := time.Now().Add(-2 * time.Hour)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	result, err := rl.db.ExecContext(ctx, "DELETE FROM user_rate_limits WHERE window_start < $1", cutoff)
	if err != nil {
		rl.logger.Error("Failed to cleanup expired rate limits", zap.Error(err))
		return
	}
	
	if rowsAffected, err := result.RowsAffected(); err == nil {
		rl.logger.Debug("Cleaned up expired rate limits", zap.Int64("rows_deleted", rowsAffected))
	}
}

// startCleanupScheduler runs periodic cleanup of expired rate limit records
func (rl *UserRateLimiter) startCleanupScheduler() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		rl.CleanupExpiredRateLimits()
	}
}