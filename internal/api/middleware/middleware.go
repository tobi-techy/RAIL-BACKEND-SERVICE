package middleware

import (
	"context"
	"database/sql"
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
	MaxRequestSize = 10 << 20 // 10MB
)

// RequestID adds a unique request ID to each request
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
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
func CORS(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range allowedOrigins {
			if allowedOrigin == "*" || allowedOrigin == origin {
				allowed = true
				break
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "3600")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// RateLimiter stores rate limiters for different IPs
type RateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	rate     int
	burst    int
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rate:     requestsPerMinute,
		burst:    requestsPerMinute, // Allow burst equal to rate
	}
}

// GetLimiter returns the rate limiter for a specific IP
func (rl *RateLimiter) GetLimiter(ip string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[ip]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		limiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.rate)), rl.burst)
		rl.limiters[ip] = limiter
		rl.mu.Unlock()
	}

	return limiter
}

// RateLimit applies rate limiting per IP
func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	limiter := NewRateLimiter(requestsPerMinute)

	return func(c *gin.Context) {
		ip := c.ClientIP()
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
func Authentication(cfg *config.Config, log *logger.Logger, sessionService SessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
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

// AdminAuth checks if user has admin role
func AdminAuth(db *sql.DB, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole := c.GetString("user_role")
		if userRole != "admin" && userRole != "super_admin" {
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
