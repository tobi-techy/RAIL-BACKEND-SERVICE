package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/pkg/auth"
)

// DeviceBoundValidator interface for device-bound token validation
type DeviceBoundValidator interface {
	ValidateDeviceBoundToken(tokenString string) (*auth.DeviceBoundClaims, error)
	ValidateDeviceBinding(ctx context.Context, claims *auth.DeviceBoundClaims, currentFingerprint string) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
}

// DeviceValidationConfig holds configuration for device validation middleware
type DeviceValidationConfig struct {
	Enabled          bool
	StrictValidation bool
	LogMismatches    bool
}

// GenerateDeviceFingerprint creates a fingerprint from request headers
func GenerateDeviceFingerprint(userAgent, acceptLanguage, secChUA, secChUAMobile string) string {
	data := fmt.Sprintf("%s|%s|%s|%s", userAgent, acceptLanguage, secChUA, secChUAMobile)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// DeviceBoundValidation middleware validates device-bound JWT tokens
func DeviceBoundValidation(validator DeviceBoundValidator, config DeviceValidationConfig, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		tokenString := extractBearerToken(c)
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "MISSING_TOKEN",
				"message":    "Authorization token required",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Validate token and extract claims
		claims, err := validator.ValidateDeviceBoundToken(tokenString)
		if err != nil {
			logger.Debug("Token validation failed", zap.Error(err))
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "INVALID_TOKEN",
				"message":    "Token validation failed",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Generate current device fingerprint
		currentFingerprint := GenerateDeviceFingerprint(
			c.GetHeader("User-Agent"),
			c.GetHeader("Accept-Language"),
			c.GetHeader("Sec-CH-UA"),
			c.GetHeader("Sec-CH-UA-Mobile"),
		)

		// Validate device binding
		if err := validator.ValidateDeviceBinding(c.Request.Context(), claims, currentFingerprint); err != nil {
			if config.LogMismatches {
				logger.Warn("Device binding validation failed",
					zap.String("user_id", claims.UserID.String()),
					zap.String("session_id", claims.SessionID),
					zap.String("expected_device", claims.DeviceFingerprint),
					zap.String("actual_device", currentFingerprint),
					zap.Error(err))
			}

			// On device mismatch with strict validation, revoke all sessions
			if config.StrictValidation && strings.Contains(err.Error(), "device mismatch") {
				_ = validator.RevokeAllUserSessions(c.Request.Context(), claims.UserID)
			}

			c.JSON(http.StatusUnauthorized, gin.H{
				"error":      "DEVICE_VALIDATION_FAILED",
				"message":    "Device binding validation failed",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Set context values for downstream handlers
		c.Set("user_id", claims.UserID)
		c.Set("session_id", claims.SessionID)
		c.Set("device_id", claims.DeviceID)
		c.Set("binding_hash", claims.BindingHash)
		c.Set("device_fingerprint", currentFingerprint)
		c.Set("email", claims.Email)
		c.Set("role", claims.Role)

		c.Next()
	}
}

// extractBearerToken extracts the JWT token from Authorization header
func extractBearerToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}

	return strings.TrimSpace(parts[1])
}

// OptionalDeviceBoundValidation validates device binding if token is present, but doesn't require it
func OptionalDeviceBoundValidation(validator DeviceBoundValidator, config DeviceValidationConfig, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.Enabled {
			c.Next()
			return
		}

		tokenString := extractBearerToken(c)
		if tokenString == "" {
			c.Next()
			return
		}

		claims, err := validator.ValidateDeviceBoundToken(tokenString)
		if err != nil {
			c.Next()
			return
		}

		currentFingerprint := GenerateDeviceFingerprint(
			c.GetHeader("User-Agent"),
			c.GetHeader("Accept-Language"),
			c.GetHeader("Sec-CH-UA"),
			c.GetHeader("Sec-CH-UA-Mobile"),
		)

		if err := validator.ValidateDeviceBinding(c.Request.Context(), claims, currentFingerprint); err != nil {
			if config.LogMismatches {
				logger.Warn("Optional device binding validation failed",
					zap.String("user_id", claims.UserID.String()),
					zap.Error(err))
			}
			c.Next()
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("session_id", claims.SessionID)
		c.Set("device_id", claims.DeviceID)
		c.Set("authenticated", true)

		c.Next()
	}
}

// GetDeviceInfo extracts device information from context
func GetDeviceInfo(c *gin.Context) (userID, sessionID, deviceID string, ok bool) {
	uid, exists := c.Get("user_id")
	if !exists {
		return "", "", "", false
	}

	sid, _ := c.Get("session_id")
	did, _ := c.Get("device_id")

	return fmt.Sprintf("%v", uid), fmt.Sprintf("%v", sid), fmt.Sprintf("%v", did), true
}
