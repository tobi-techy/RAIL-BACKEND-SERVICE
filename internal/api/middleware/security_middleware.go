package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"go.uber.org/zap"
)

// RequireIPWhitelist enforces IP whitelist for sensitive operations
func RequireIPWhitelist(ipService *security.IPWhitelistService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			c.Next()
			return
		}

		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.Next()
			return
		}

		// Check if user has whitelist enabled
		hasWhitelist, err := ipService.HasWhitelistEnabled(c.Request.Context(), userID)
		if err != nil || !hasWhitelist {
			c.Next()
			return
		}

		// Verify IP is whitelisted
		clientIP := c.ClientIP()
		isWhitelisted, err := ipService.IsIPWhitelisted(c.Request.Context(), userID, clientIP)
		if err != nil {
			logger.Error("Failed to check IP whitelist", zap.Error(err))
			c.Next()
			return
		}

		if !isWhitelisted {
			logger.Warn("Blocked request from non-whitelisted IP",
				zap.String("user_id", userIDStr),
				zap.String("ip", clientIP),
				zap.String("path", c.Request.URL.Path))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "IP_NOT_WHITELISTED",
				"message":    "This operation requires a whitelisted IP address",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireMFA enforces MFA verification for sensitive operations
func RequireMFA(twoFAService MFAValidator, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			c.Next()
			return
		}

		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.Next()
			return
		}

		// Check if user has 2FA enabled
		status, err := twoFAService.GetStatus(c.Request.Context(), userID)
		if err != nil || !status.IsEnabled {
			c.Next()
			return
		}

		// Check for MFA token in header
		mfaToken := c.GetHeader("X-MFA-Token")
		if mfaToken == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "MFA_REQUIRED",
				"message":    "This operation requires MFA verification",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Verify MFA token
		valid, err := twoFAService.Verify(c.Request.Context(), userID, mfaToken)
		if err != nil || !valid {
			logger.Warn("MFA verification failed",
				zap.String("user_id", userIDStr),
				zap.String("path", c.Request.URL.Path))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "MFA_INVALID",
				"message":    "Invalid MFA token",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// MFAValidator interface for MFA verification
type MFAValidator interface {
	GetStatus(ctx interface{}, userID uuid.UUID) (*MFAStatus, error)
	Verify(ctx interface{}, userID uuid.UUID, code string) (bool, error)
}

// MFAStatus represents MFA status
type MFAStatus struct {
	IsEnabled bool
}

// DeviceVerification checks device fingerprint and flags new devices
func DeviceVerification(deviceService *security.DeviceTrackingService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			c.Next()
			return
		}

		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.Next()
			return
		}

		// Get device fingerprint from header
		fingerprint := c.GetHeader("X-Device-Fingerprint")
		if fingerprint == "" {
			// Generate basic fingerprint from available data
			fingerprint = security.GenerateFingerprint(
				c.GetHeader("User-Agent"),
				c.GetHeader("Accept-Language"),
				c.GetHeader("X-Screen-Resolution"),
				c.GetHeader("X-Timezone"),
			)
		}

		clientIP := c.ClientIP()
		result, err := deviceService.CheckDevice(c.Request.Context(), userID, fingerprint, clientIP)
		if err != nil {
			logger.Error("Device check failed", zap.Error(err))
			c.Next()
			return
		}

		// Store device check result in context
		c.Set("device_check", result)
		c.Set("device_fingerprint", fingerprint)

		// If new device, register it
		if !result.IsKnownDevice {
			deviceName := c.GetHeader("X-Device-Name")
			if deviceName == "" {
				deviceName = "Unknown Device"
			}
			deviceService.RegisterDevice(c.Request.Context(), userID, fingerprint, deviceName, clientIP, "")
		}

		// Flag high-risk requests
		if result.RiskScore > 0.7 {
			c.Set("high_risk_device", true)
			logger.Warn("High risk device detected",
				zap.String("user_id", userIDStr),
				zap.Float64("risk_score", result.RiskScore),
				zap.Strings("risk_factors", result.RiskFactors))
		}

		c.Next()
	}
}

// LoginProtection applies login attempt tracking
func LoginProtection(loginService *security.LoginProtectionService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to login endpoints
		if c.Request.URL.Path != "/api/v1/auth/login" {
			c.Next()
			return
		}

		// Get identifier (email/phone) from request
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

		// Check if login is allowed
		result, err := loginService.CheckLoginAllowed(c.Request.Context(), identifier)
		if err != nil {
			logger.Error("Login protection check failed", zap.Error(err))
			c.Next()
			return
		}

		if !result.Allowed {
			logger.Warn("Login blocked - account locked",
				zap.String("email", identifier),
				zap.String("ip", c.ClientIP()))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":        "ACCOUNT_LOCKED",
				"message":      result.Reason,
				"locked_until": result.LockedUntil,
				"request_id":   c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Store service in context for post-login handling
		c.Set("login_protection", loginService)
		c.Set("login_identifier", identifier)
		if req.Email != nil && strings.TrimSpace(*req.Email) != "" {
			c.Set("login_email", strings.TrimSpace(*req.Email))
		}

		c.Next()
	}
}

// WithdrawalSecurity applies security checks for withdrawal operations
func WithdrawalSecurity(withdrawalSecurity *security.WithdrawalSecurityService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to withdrawal endpoints
		if c.Request.URL.Path != "/api/v1/withdrawals" || c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		c.Set("withdrawal_security", withdrawalSecurity)
		c.Next()
	}
}
