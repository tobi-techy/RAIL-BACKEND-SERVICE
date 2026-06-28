package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// SessionValidator interface for session validation
type SessionValidator interface {
	ValidateSession(ctx context.Context, token string) (*SessionInfo, error)
}

// APIKeyValidator interface for API key validation
type APIKeyValidator interface {
	ValidateAPIKey(ctx context.Context, key string) (*APIKeyInfo, error)
}

// SessionInfo represents session information
type SessionInfo struct {
	ID     uuid.UUID
	UserID uuid.UUID
}

// APIKeyInfo represents API key information
type APIKeyInfo struct {
	ID     uuid.UUID
	UserID *uuid.UUID
	Scopes []string
}

const (
	MaxRequestSize = 25 << 20 // 25MiB (matches max file upload; per-handler checks prevent abuse)
)

// RequestID adds a unique request ID to each request
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		} else if _, err := uuid.Parse(requestID); err != nil {
			requestID = uuid.New().String()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// RequestSizeLimit limits the size of incoming requests
func RequestSizeLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxRequestSize)
		c.Next()
	}
}

// LargeBodyLimit overrides the global RequestSizeLimit for routes that accept large payloads (e.g. image uploads).
// It replaces the MaxBytesReader set by the global middleware with a larger one.
func LargeBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The global RequestSizeLimit already wrapped the body in a 1MB MaxBytesReader.
		// We need to unwrap and re-wrap with the larger limit.
		// http.MaxBytesReader wraps the underlying reader, so wrapping again with
		// a larger limit on top doesn't help — the inner 1MB reader still fires.
		// Solution: set the limit on the request directly via Gin's built-in.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Set("_bodyLimitOverride", maxBytes)
		c.Next()
	}
}

// InputValidation validates common input patterns
func InputValidation() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Validate common headers
		userAgent := c.GetHeader("User-Agent")
		if len(userAgent) > 500 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":      "User-Agent header too long",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}
		c.Set("user_agent", userAgent)

		// Validate content type for POST/PUT requests
		if c.Request.Method == "POST" || c.Request.Method == "PUT" {
			contentType := c.GetHeader("Content-Type")
			if contentType != "" && !strings.Contains(contentType, "application/json") &&
				!strings.Contains(contentType, "multipart/form-data") &&
				!strings.Contains(contentType, "application/x-www-form-urlencoded") {
				c.JSON(http.StatusUnsupportedMediaType, gin.H{
					"error":      "Unsupported content type",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// Logger logs HTTP requests with structured logging
func Logger(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		if raw != "" {
			path = path + "?" + raw
		}

		requestID := c.GetString("request_id")
		requestLogger := log.ForRequest(requestID, c.Request.Method, path)

		c.Set("logger", requestLogger)

		// Process request
		c.Next()

		// Log after processing
		end := time.Now()
		latency := end.Sub(start)

		c.Header("Server-Timing", fmt.Sprintf("app;dur=%.1f", float64(latency.Microseconds())/1000.0))

		requestLogger.Infow("HTTP Request",
			"status_code", c.Writer.Status(),
			"latency", latency,
			"client_ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
			"response_size", c.Writer.Size(),
		)
	}
}

// Recovery handles panics and returns 500 errors
func Recovery(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				requestID := c.GetString("request_id")
				requestLogger := log.ForRequest(requestID, c.Request.Method, c.Request.URL.Path)

				requestLogger.Errorw("Panic recovered",
					"error", err,
					"stack", string(debug.Stack()),
				)

				c.JSON(http.StatusInternalServerError, gin.H{
					"error":      "Internal server error",
					"request_id": requestID,
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}

// CORS handles Cross-Origin Resource Sharing
func CORS(allowedOrigins []string, environment ...string) gin.HandlerFunc {
	// Reject wildcard origins in production/staging
	if len(environment) > 0 {
		env := strings.ToLower(environment[0])
		if env == "production" || env == "staging" {
			for _, o := range allowedOrigins {
				if o == "*" {
					log.Fatalf("CORS wildcard origin not allowed in %s", env)
				}
			}
		}
	}
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		isWildcard := false
		allowed := false
		for _, allowedOrigin := range allowedOrigins {
			if allowedOrigin == "*" {
				isWildcard = true
				allowed = true
				break
			}
			if allowedOrigin == origin {
				allowed = true
				break
			}
		}

		if allowed {
			if isWildcard {
				c.Header("Access-Control-Allow-Origin", "*")
			} else {
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Origin", origin)
			}
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID, X-CSRF-Token, X-Requested-With")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID, X-CSRF-Token")
		c.Header("Access-Control-Max-Age", "3600")

		if !isWildcard {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// RateLimiter stores rate limiters for different IPs
type RateLimiter struct {
	limiters map[string]*rateLimiterEntry
	mu       sync.RWMutex
	rate     int
	burst    int
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	rl := &RateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		rate:     requestsPerMinute,
		burst:    requestsPerMinute,
	}
	go rl.cleanup()
	return rl
}

// cleanup evicts entries not seen in the last 10 minutes to prevent unbounded growth
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, entry := range rl.limiters {
			if entry.lastSeen.Before(cutoff) {
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// GetLimiter returns the rate limiter for a specific IP
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.RLock()
	entry, exists := rl.limiters[ip]
	rl.mu.RUnlock()

	if exists {
		rl.mu.Lock()
		entry.lastSeen = time.Now()
		rl.mu.Unlock()
		return entry.limiter
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	// Double-check after acquiring write lock
	if entry, exists = rl.limiters[ip]; exists {
		entry.lastSeen = time.Now()
		return entry.limiter
	}
	limiter := rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.rate)), rl.burst)
	rl.limiters[ip] = &rateLimiterEntry{limiter: limiter, lastSeen: time.Now()}
	return limiter
}

// RateLimit applies rate limiting per IP
func RateLimit(requestsPerMinute int, isBehindCloudflare ...bool) gin.HandlerFunc {
	limiter := NewRateLimiter(requestsPerMinute)
	cfProxy := len(isBehindCloudflare) > 0 && isBehindCloudflare[0]

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if cfProxy {
			if cfIP := c.GetHeader("CF-Connecting-IP"); cfIP != "" {
				ip = cfIP
			}
		}
		if !limiter.GetLimiter(ip).Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":      "Rate limit exceeded",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// SecurityHeaders adds security headers to responses
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("X-Permitted-Cross-Domain-Policies", "none")
		c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		c.Next()
	}
}

// Authentication validates JWT tokens with session management
func Authentication(cfg *config.Config, log *logger.Logger, sessionService SessionValidator, blacklist ...*auth.TokenBlacklist) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		// Fallback: WebSocket clients pass token as query param (can't set headers)
		if authHeader == "" {
			if qToken := c.Query("token"); qToken != "" {
				authHeader = "Bearer " + qToken
			}
		}
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Authorization header required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Extract token from "Bearer <token>"
		tokenParts := strings.Split(authHeader, " ")
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Invalid authorization format",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		tokenString := tokenParts[1]
		claims, err := auth.ValidateToken(tokenString, cfg.JWT.Secret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Invalid token",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Check token blacklist if provided (fail-closed: reject on error)
		if len(blacklist) > 0 && blacklist[0] != nil {
			bl := blacklist[0]
			h := sha256.Sum256([]byte(tokenString))
			tokenHash := fmt.Sprintf("%x", h)
			revoked, err := bl.IsBlacklisted(c.Request.Context(), tokenHash)
			if err != nil {
				// Redis unreachable AND no recent local cache for this token.
				// Default is strict (deny). Set AUTH_BLACKLIST_FAIL_OPEN=true to
				// prioritize availability over revocation during a Redis outage —
				// active sessions ride through via the local negative cache either way.
				if cfg.Security.AuthBlacklistFailOpen {
					log.Warnw("Token blacklist check failed — failing open (Redis unavailable)",
						"error", err,
						"token_hash_prefix", func() string {
						if len(tokenHash) >= 8 {
							return tokenHash[:8]
						}
						return tokenHash
					}(),
						"security_mode", "degraded")
				} else {
					log.Errorw("Token blacklist check failed — rejecting request", "error", err)
					c.JSON(http.StatusServiceUnavailable, gin.H{
						"error":      "Security check unavailable",
						"request_id": c.GetString("request_id"),
					})
					c.Abort()
					return
				}
			}
			if revoked {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":      "Token has been revoked",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}
		}

		// Validate session if service is provided
		if sessionService != nil {
			session, err := sessionService.ValidateSession(c.Request.Context(), tokenString)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":      "Session invalid or expired",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}
			c.Set("session_id", session.ID)
		}

		// Add user info to context
		c.Set("user_id", claims.UserID)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.Email)

		c.Next()
	}
}

// AdminAuth checks if user has admin role (JWT fast-path + DB verification)
func AdminAuth(db *sql.DB, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Fast-path rejection from JWT claims
		userRole := c.GetString("user_role")
		if userRole != "admin" && userRole != "super_admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Admin access required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Verify role against database to prevent stale/forged JWT claims
		var dbRole string
		err := db.QueryRowContext(c.Request.Context(), "SELECT role FROM users WHERE id = $1", c.GetString("user_id")).Scan(&dbRole)
		if err != nil || (dbRole != "admin" && dbRole != "super_admin") {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "Admin access required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ValidateAPIKey validates API keys using the API key service
func ValidateAPIKey(apikeyService APIKeyValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "API key required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		keyInfo, err := apikeyService.ValidateAPIKey(c.Request.Context(), apiKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "Invalid API key",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Add API key info to context
		c.Set("api_key_id", keyInfo.ID)
		c.Set("api_key_scopes", keyInfo.Scopes)
		if keyInfo.UserID != nil {
			c.Set("user_id", *keyInfo.UserID)
		}

		c.Next()
	}
}

// PublicCache sets Cache-Control for public, cacheable responses (market data, asset lists, tracks)
func PublicCache(maxAge int) gin.HandlerFunc {
	value := fmt.Sprintf("public, max-age=%d", maxAge)
	return func(c *gin.Context) {
		c.Header("Cache-Control", value)
		c.Next()
	}
}

type ResponseCache struct {
	Data      string `json:"data"`
	ETag      string `json:"etag"`
	ExpiresAt int64  `json:"expires_at"`
}

func GenerateETag(data string) string {
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf(`"%x"`, hash[:8])
}

func ETagMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "GET" {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if !isCacheableByETag(path) {
			c.Next()
			return
		}

		c.Next()

		if c.IsAborted() || c.Writer.Status() >= 400 {
			return
		}

		body, exists := c.Get("response_body")
		if !exists {
			return
		}

		bodyStr, ok := body.(string)
		if !ok {
			return
		}

		etag := GenerateETag(bodyStr)
		c.Header("ETag", etag)

		ifNoneMatch := c.GetHeader("If-None-Match")
		if ifNoneMatch != "" && ifNoneMatch == etag {
			c.AbortWithStatus(http.StatusNotModified)
			return
		}
	}
}

func isCacheableByETag(path string) bool {
	cacheablePaths := []string{
		"/api/v1/portfolio/overview",
		"/api/v1/portfolio",
		"/api/v1/limits",
		"/api/v1/kyc/status",
		"/api/v1/balances",
		"/api/v1/assets",
		"/api/v1/account/station",
		"/api/v1/account/spending-stash",
		"/api/v1/account/investment-stash",
	}

	for _, p := range cacheablePaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// PrivateNoCache ensures user-specific responses are never cached at the edge
func PrivateNoCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Next()
	}
}

// InternalAPIKeyAuth validates the internal API key at the group level.
// This prevents new endpoints from accidentally being exposed without auth.
func InternalAPIKeyAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "internal API key not configured"})
			c.Abort()
			return
		}
		key := c.GetHeader("Authorization")
		if len(key) > 7 && key[:7] == "Bearer " {
			key = key[7:]
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}
