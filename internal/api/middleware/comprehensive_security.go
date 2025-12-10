package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/services/security"
)

// ComprehensiveSecurityConfig holds all security service dependencies
type ComprehensiveSecurityConfig struct {
	MFAService            *security.MFAService
	GeoSecurityService    *security.GeoSecurityService
	FraudDetectionService *security.FraudDetectionService
	IncidentService       *security.IncidentResponseService
	Logger                *zap.Logger
}

// GeoSecurityMiddleware checks IP geolocation and blocks high-risk requests
func GeoSecurityMiddleware(geoService *security.GeoSecurityService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		
		// Get user ID if authenticated
		var userID uuid.UUID
		if userIDStr := c.GetString("user_id"); userIDStr != "" {
			userID, _ = uuid.Parse(userIDStr)
		}

		result, err := geoService.CheckIP(c.Request.Context(), userID, clientIP)
		if err != nil {
			logger.Error("Geo security check failed", zap.Error(err))
			c.Next()
			return
		}

		// Store geo info in context
		c.Set("geo_location", result.Location)
		c.Set("geo_risk_score", result.RiskScore)
		c.Set("geo_risk_factors", result.RiskFactors)

		if !result.Allowed {
			logger.Warn("Request blocked by geo security",
				zap.String("ip", clientIP),
				zap.String("reason", result.BlockReason),
				zap.Strings("risk_factors", result.RiskFactors))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "GEO_BLOCKED",
				"message":    "Access denied from your location",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Flag if MFA required due to geo risk
		if result.RequiresMFA {
			c.Set("geo_requires_mfa", true)
		}

		// Flag anomaly for logging
		if result.IsAnomaly {
			c.Set("geo_anomaly", true)
			logger.Warn("Location anomaly detected",
				zap.String("ip", clientIP),
				zap.String("user_id", userID.String()))
		}

		c.Next()
	}
}

// MFAEnforcementMiddleware enforces MFA for protected operations
func MFAEnforcementMiddleware(mfaService *security.MFAService, logger *zap.Logger) gin.HandlerFunc {
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

		// Check if MFA is required
		mfaResult, err := mfaService.RequiresMFA(c.Request.Context(), userID)
		if err != nil {
			logger.Error("MFA check failed", zap.Error(err))
			c.Next()
			return
		}

		// Also check if geo security flagged MFA requirement
		geoRequiresMFA := c.GetBool("geo_requires_mfa")

		if !mfaResult.RequiresMFA && !geoRequiresMFA {
			c.Next()
			return
		}

		// Check for grace period
		if mfaResult.GracePeriod {
			c.Set("mfa_grace_period", true)
			c.Next()
			return
		}

		// Check for MFA token in header
		mfaToken := c.GetHeader("X-MFA-Token")
		mfaMethod := c.GetHeader("X-MFA-Method")

		if mfaToken == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error":             "MFA_REQUIRED",
				"message":           "Multi-factor authentication required",
				"available_methods": mfaResult.AvailableMethods,
				"request_id":        c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		// Verify MFA token
		method := security.MFAMethod(mfaMethod)
		if method == "" {
			method = security.MFAMethodTOTP
		}

		verifyResult, err := mfaService.VerifyAny(c.Request.Context(), userID, mfaToken, method)
		if err != nil || !verifyResult.Valid {
			logger.Warn("MFA verification failed",
				zap.String("user_id", userIDStr),
				zap.String("method", string(method)))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "MFA_INVALID",
				"message":    "Invalid MFA code",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Set("mfa_verified", true)
		c.Next()
	}
}

// EnhancedFraudDetectionMiddleware analyzes transactions for fraud (renamed to avoid conflict)
func EnhancedFraudDetectionMiddleware(fraudService *security.FraudDetectionService, incidentService *security.IncidentResponseService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only check POST/PUT requests that might be transactions
		if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodPut {
			c.Next()
			return
		}

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

		// Check if this is a transaction endpoint
		path := c.FullPath()
		txType := getTransactionType(path)
		if txType == "" {
			c.Next()
			return
		}

		// Get amount from request body (if available)
		var body map[string]interface{}
		if err := c.ShouldBindJSON(&body); err == nil {
			amount := decimal.Zero
			if amountVal, ok := body["amount"]; ok {
				switch v := amountVal.(type) {
				case float64:
					amount = decimal.NewFromFloat(v)
				case string:
					amount, _ = decimal.NewFromString(v)
				}
			}

			destination := ""
			if dest, ok := body["destination"].(string); ok {
				destination = dest
			}
			if dest, ok := body["address"].(string); ok {
				destination = dest
			}

			// Build transaction context
			txCtx := &security.TransactionContext{
				UserID:      userID,
				Amount:      amount,
				Type:        txType,
				Destination: destination,
				IPAddress:   c.ClientIP(),
				DeviceID:    c.GetString("device_fingerprint"),
				SessionID:   c.GetString("session_id"),
			}

			// Check for fraud
			result, err := fraudService.CheckTransaction(c.Request.Context(), txCtx)
			if err != nil {
				logger.Error("Fraud check failed", zap.Error(err))
				c.Next()
				return
			}

			// Store fraud result in context
			c.Set("fraud_score", result.Score)
			c.Set("fraud_signals", result.Signals)

			// Handle based on action
			switch result.Action {
			case security.FraudActionBlock:
				logger.Warn("Transaction blocked by fraud detection",
					zap.String("user_id", userIDStr),
					zap.Float64("score", result.Score))

				// Create incident
				if incidentService != nil {
					incidentService.DetectAccountTakeover(c.Request.Context(), userID, result.Signals)
				}

				c.JSON(http.StatusForbidden, gin.H{
					"error":      "TRANSACTION_BLOCKED",
					"message":    "Transaction flagged for security review",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return

			case security.FraudActionReview:
				c.Set("requires_manual_review", true)
				logger.Warn("Transaction flagged for review",
					zap.String("user_id", userIDStr),
					zap.Float64("score", result.Score))

			case security.FraudActionMFA:
				if !c.GetBool("mfa_verified") {
					c.JSON(http.StatusForbidden, gin.H{
						"error":      "MFA_REQUIRED",
						"message":    "Additional verification required for this transaction",
						"request_id": c.GetString("request_id"),
					})
					c.Abort()
					return
				}
			}
		}

		c.Next()
	}
}

// HighValueAccountMiddleware enforces additional security for high-value accounts
func HighValueAccountMiddleware(ipService *security.IPWhitelistService, mfaService *security.MFAService, logger *zap.Logger) gin.HandlerFunc {
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

		// Check if high-value account
		// This would typically check account balance/tier from database
		isHighValue := c.GetBool("is_high_value_account")
		if !isHighValue {
			c.Next()
			return
		}

		// Enforce IP whitelist for high-value accounts
		clientIP := c.ClientIP()
		hasWhitelist, _ := ipService.HasWhitelistEnabled(c.Request.Context(), userID)
		
		if hasWhitelist {
			isWhitelisted, err := ipService.IsIPWhitelisted(c.Request.Context(), userID, clientIP)
			if err != nil || !isWhitelisted {
				logger.Warn("High-value account access from non-whitelisted IP",
					zap.String("user_id", userIDStr),
					zap.String("ip", clientIP))

				c.JSON(http.StatusForbidden, gin.H{
					"error":      "IP_NOT_WHITELISTED",
					"message":    "High-value accounts require IP whitelisting",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
				return
			}
		}

		c.Next()
	}
}

// BlockedIPMiddleware checks if IP is blocked
func BlockedIPMiddleware(redis interface{ Get(ctx interface{}, key string) interface{ Result() (string, error) } }, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		key := "blocked_ip:" + clientIP

		if blocked, err := redis.Get(c.Request.Context(), key).Result(); err == nil && blocked == "1" {
			logger.Warn("Blocked IP attempted access", zap.String("ip", clientIP))
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "IP_BLOCKED",
				"message":    "Access denied",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// SecurityAuditMiddleware logs security-relevant events
func SecurityAuditMiddleware(eventLogger *security.SecurityEventLogger, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request
		c.Next()

		// Log security-relevant events after request
		userIDStr := c.GetString("user_id")
		if userIDStr == "" {
			return
		}

		userID, _ := uuid.Parse(userIDStr)

		// Log if fraud detected
		if fraudScore, exists := c.Get("fraud_score"); exists {
			if score, ok := fraudScore.(float64); ok && score > 0.5 {
				eventLogger.Log(c.Request.Context(), &security.SecurityEvent{
					UserID:            &userID,
					EventType:         "fraud_detected",
					Severity:          security.SeverityWarning,
					IPAddress:         c.ClientIP(),
					UserAgent:         c.GetHeader("User-Agent"),
					DeviceFingerprint: c.GetString("device_fingerprint"),
					Metadata: map[string]interface{}{
						"fraud_score": score,
						"path":        c.FullPath(),
						"method":      c.Request.Method,
					},
				})
			}
		}

		// Log if geo anomaly
		if c.GetBool("geo_anomaly") {
			eventLogger.Log(c.Request.Context(), &security.SecurityEvent{
				UserID:    &userID,
				EventType: "geo_anomaly",
				Severity:  security.SeverityWarning,
				IPAddress: c.ClientIP(),
				Metadata: map[string]interface{}{
					"path": c.FullPath(),
				},
			})
		}
	}
}

// getTransactionType determines transaction type from path
func getTransactionType(path string) string {
	switch {
	case contains(path, "withdraw"):
		return "withdrawal"
	case contains(path, "deposit"):
		return "deposit"
	case contains(path, "trade"), contains(path, "order"), contains(path, "invest"):
		return "trade"
	case contains(path, "transfer"):
		return "transfer"
	default:
		return ""
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
