package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/ratelimit"
)

// EnhancedAuthConfig holds configuration for enhanced authentication
type EnhancedAuthConfig struct {
	JWTSecret        string
	TokenBlacklist   *auth.TokenBlacklist
	SessionValidator SessionValidator
	Logger           *logger.Logger
}

// EnhancedAuthentication validates JWT tokens with blacklist checking
func EnhancedAuthentication(cfg *config.Config, blacklist *auth.TokenBlacklist, log *logger.Logger, sessionService SessionValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "UNAUTHORIZED",
				"message":    "Authorization header required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		tokenParts := strings.Split(authHeader, " ")
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "INVALID_AUTH_FORMAT",
				"message":    "Invalid authorization format",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		tokenString := tokenParts[1]

		// Validate token signature and claims
		claims, err := auth.ValidateToken(tokenString, cfg.JWT.Secret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "INVALID_TOKEN",
				"message":    "Invalid or expired token",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Check token blacklist
		if blacklist != nil {
			tokenHash := hashToken(tokenString)

			// Check if specific token is blacklisted
			isBlacklisted, err := blacklist.IsBlacklisted(c.Request.Context(), tokenHash)
			if err != nil {
				log.Errorw("Failed to check token blacklist", "error", err)
			} else if isBlacklisted {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":      "TOKEN_REVOKED",
					"message":    "Token has been revoked",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}

			// Check if all user tokens are blacklisted (global logout)
			if claims.IssuedAt != nil {
				isUserBlacklisted, err := blacklist.IsUserBlacklisted(c.Request.Context(), claims.UserID.String(), claims.IssuedAt.Time)
				if err != nil {
					log.Errorw("Failed to check user blacklist", "error", err)
				} else if isUserBlacklisted {
					c.JSON(http.StatusUnauthorized, gin.H{
						"error":      "SESSION_INVALIDATED",
						"message":    "All sessions have been invalidated",
						"request_id": c.GetString("request_id"),
					})
					c.Abort()
					return
				}
			}
		}

		// Validate session if service is provided
		if sessionService != nil {
			session, err := sessionService.ValidateSession(c.Request.Context(), tokenString)
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{
					"error":      "SESSION_INVALID",
					"message":    "Session invalid or expired",
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
		c.Set("token_hash", hashToken(tokenString))

		c.Next()
	}
}

// LoginRateLimiting applies rate limiting and CAPTCHA requirements for login
func LoginRateLimiting(tracker *ratelimit.LoginAttemptTracker, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to login endpoint
		if c.Request.URL.Path != "/api/v1/auth/login" {
			c.Next()
			return
		}

		// Get identifier from request body
		var req struct {
			Email *string `json:"email"`
			Phone *string `json:"phone"`
		}
		if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
			c.Next()
			return
		}

		identifier := ""
		if req.Email != nil && strings.TrimSpace(*req.Email) != "" {
			identifier = strings.TrimSpace(*req.Email)
		} else if req.Phone != nil && strings.TrimSpace(*req.Phone) != "" {
			identifier = strings.TrimSpace(*req.Phone)
		}

		if identifier == "" {
			c.Next()
			return
		}

		// Re-bind for downstream handlers
		c.Set("login_identifier", identifier)
		if req.Email != nil && strings.TrimSpace(*req.Email) != "" {
			c.Set("login_email", strings.TrimSpace(*req.Email))
		}

		// Check if login is allowed
		result, err := tracker.CheckLoginAllowed(c.Request.Context(), identifier)
		if err != nil {
			log.Errorw("Failed to check login attempts", "error", err)
			c.Next()
			return
		}

		if !result.Allowed {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":        "ACCOUNT_LOCKED",
				"message":      "Too many failed login attempts. Please try again later.",
				"locked_until": result.LockedUntil,
				"retry_after":  int(result.RetryAfter.Seconds()),
				"request_id":   c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Check if CAPTCHA is required
		if result.RequireCaptcha {
			captchaToken := c.GetHeader("X-Captcha-Token")
			if captchaToken == "" {
				c.JSON(http.StatusForbidden, gin.H{
					"error":           "CAPTCHA_REQUIRED",
					"message":         "CAPTCHA verification required",
					"failed_attempts": result.FailedAttempts,
					"request_id":      c.GetString("request_id"),
				})
				c.Abort()
				return
			}
			// Verify CAPTCHA token - verification is done via CaptchaVerifier injected at route level
			// For now, mark as verified if token is present (actual verification happens in handler)
			c.Set("captcha_token", captchaToken)
			c.Set("captcha_verified", false) // Will be verified by handler with CaptchaVerifier
		}

		// Store tracker in context for post-login handling
		c.Set("login_tracker", tracker)

		c.Next()
	}
}

// TieredRateLimiting applies multi-tier rate limiting
func TieredRateLimiting(limiter *ratelimit.TieredLimiter, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		userID := c.GetString("user_id")
		endpoint := c.Request.Method + " " + c.FullPath()

		result, err := limiter.Check(c.Request.Context(), ip, userID, endpoint)
		if err != nil {
			log.Errorw("Rate limit check failed", "error", err)
			c.Next()
			return
		}

		if !result.Allowed {
			c.Header("X-RateLimit-Remaining", "0")
			c.Header("X-RateLimit-Reset", result.ResetAt.Format(time.RFC3339))
			c.Header("Retry-After", string(rune(int(result.RetryAfter.Seconds()))))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "RATE_LIMITED",
				"message":     "Rate limit exceeded",
				"limited_by":  result.LimitedBy,
				"retry_after": int(result.RetryAfter.Seconds()),
				"request_id":  c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		if result.Remaining >= 0 {
			c.Header("X-RateLimit-Remaining", string(rune(result.Remaining)))
		}

		c.Next()
	}
}

// PasswordExpirationCheck checks if user's password has expired
func PasswordExpirationCheck(passwordChecker PasswordExpirationChecker, log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			c.Next()
			return
		}

		// Skip for password change endpoint
		if c.Request.URL.Path == "/api/v1/auth/change-password" {
			c.Next()
			return
		}

		expired, err := passwordChecker.IsPasswordExpired(c.Request.Context(), userIDStr)
		if err != nil {
			log.Errorw("Failed to check password expiration", "error", err)
			c.Next()
			return
		}

		if expired {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "PASSWORD_EXPIRED",
				"message":    "Your password has expired. Please change your password.",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// PasswordExpirationChecker interface for password expiration checking
type PasswordExpirationChecker interface {
	IsPasswordExpired(ctx interface{}, userID string) (bool, error)
}

func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
