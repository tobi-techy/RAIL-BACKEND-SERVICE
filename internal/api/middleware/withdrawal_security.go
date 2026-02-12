package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WithdrawalSecurityConfig holds withdrawal security settings
type WithdrawalSecurityConfig struct {
	MaxRequestsPerDay     int             // Max withdrawal requests per day (default: 3)
	NewAccountDailyMax    decimal.Decimal // Daily max for accounts <30 days (default: $10,000)
	EstablishedDailyMax   decimal.Decimal // Daily max for accounts >30 days (default: $100,000)
	NewAccountAgeDays     int             // Days to be considered "new" (default: 30)
}

// DefaultWithdrawalSecurityConfig returns default security settings
func DefaultWithdrawalSecurityConfig() WithdrawalSecurityConfig {
	return WithdrawalSecurityConfig{
		MaxRequestsPerDay:   3,
		NewAccountDailyMax:  decimal.NewFromInt(10000),
		EstablishedDailyMax: decimal.NewFromInt(100000),
		NewAccountAgeDays:   30,
	}
}

// WithdrawalSecurityStore interface for withdrawal security checks
type WithdrawalSecurityStore interface {
	GetTodayWithdrawalCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetTodayWithdrawalTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetUserCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error)
}

// WithdrawalSecurityMiddleware enforces withdrawal rate limits and daily caps
func WithdrawalSecurityMiddleware(store WithdrawalSecurityStore, cfg WithdrawalSecurityConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to POST /withdrawals
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		userID, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "User not authenticated",
				},
			})
			c.Abort()
			return
		}

		uid, ok := userID.(uuid.UUID)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "Invalid user ID",
				},
			})
			c.Abort()
			return
		}

		ctx := c.Request.Context()

		// Check rate limit: max requests per day
		count, err := store.GetTodayWithdrawalCount(ctx, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_ERROR",
					"message": "Failed to check withdrawal limits",
				},
			})
			c.Abort()
			return
		}

		if count >= cfg.MaxRequestsPerDay {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": fmt.Sprintf("Maximum %d withdrawal requests per day exceeded", cfg.MaxRequestsPerDay),
				},
			})
			c.Abort()
			return
		}

		// Store config in context for handler to use for amount validation
		c.Set("withdrawal_security_config", cfg)
		c.Set("withdrawal_security_store", store)

		c.Next()
	}
}

// ValidateWithdrawalAmount validates withdrawal amount against daily limits
// Call this from the handler after parsing the request body
func ValidateWithdrawalAmount(ctx context.Context, store WithdrawalSecurityStore, cfg WithdrawalSecurityConfig, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user account age
	createdAt, err := store.GetUserCreatedAt(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to check account age: %w", err)
	}

	// Determine daily max based on account age
	accountAge := time.Since(createdAt)
	var dailyMax decimal.Decimal
	if accountAge < time.Duration(cfg.NewAccountAgeDays)*24*time.Hour {
		dailyMax = cfg.NewAccountDailyMax
	} else {
		dailyMax = cfg.EstablishedDailyMax
	}

	// Get today's total withdrawals
	todayTotal, err := store.GetTodayWithdrawalTotal(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to check daily total: %w", err)
	}

	// Check if this withdrawal would exceed daily limit
	newTotal := todayTotal.Add(amount)
	if newTotal.GreaterThan(dailyMax) {
		remaining := dailyMax.Sub(todayTotal)
		if remaining.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("daily withdrawal limit of $%s reached", dailyMax.StringFixed(2))
		}
		return fmt.Errorf("withdrawal would exceed daily limit ($%s remaining of $%s daily max)",
			remaining.StringFixed(2), dailyMax.StringFixed(2))
	}

	return nil
}
