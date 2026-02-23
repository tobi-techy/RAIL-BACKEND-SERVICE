package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// PasscodeSessionValidator validates and invalidates short-lived passcode sessions.
type PasscodeSessionValidator interface {
	ValidateSession(ctx context.Context, userID uuid.UUID, token string) (bool, error)
	InvalidateSession(ctx context.Context, userID uuid.UUID, token string) error
}

// RequirePasscodeSession enforces a valid passcode session token for sensitive routes.
// If invalidateOnSuccess is true, successful requests consume the token (single-use).
func RequirePasscodeSession(validator PasscodeSessionValidator, invalidateOnSuccess bool, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if validator == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":      "PASSCODE_SESSION_UNAVAILABLE",
				"message":    "Passcode session validation is currently unavailable",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		userID, ok := getUserIDForPasscodeSession(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "UNAUTHORIZED",
				"message":    "User context is missing",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		token := strings.TrimSpace(c.GetHeader("X-Passcode-Session"))
		if token == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "PASSCODE_SESSION_REQUIRED",
				"message":    "Passcode verification required for this operation",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		valid, err := validator.ValidateSession(c.Request.Context(), userID, token)
		if err != nil {
			if logger != nil {
				logger.Warn("Failed to validate passcode session",
					zap.Error(err),
					zap.String("user_id", userID.String()))
			}
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "PASSCODE_SESSION_INVALID",
				"message":    "Passcode session is invalid or expired",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}
		if !valid {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "PASSCODE_SESSION_INVALID",
				"message":    "Passcode session is invalid or expired",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Set("passcode_session_validated", true)
		c.Next()

		if !invalidateOnSuccess || c.Writer.Status() >= http.StatusBadRequest {
			return
		}
		if err := validator.InvalidateSession(c.Request.Context(), userID, token); err != nil && logger != nil {
			logger.Warn("Failed to invalidate passcode session after successful request",
				zap.Error(err),
				zap.String("user_id", userID.String()))
		}
	}
}

func getUserIDForPasscodeSession(c *gin.Context) (uuid.UUID, bool) {
	raw, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}

	switch v := raw.(type) {
	case uuid.UUID:
		return v, true
	case string:
		id, err := uuid.Parse(v)
		if err != nil {
			return uuid.Nil, false
		}
		return id, true
	default:
		return uuid.Nil, false
	}
}
