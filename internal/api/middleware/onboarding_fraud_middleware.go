package middleware

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/security"
	"go.uber.org/zap"
)

// validFingerprint matches a 64-char lowercase hex SHA-256 hash.
var validFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

// OnboardingFraudMiddleware runs cross-account fraud detection on the
// POST /onboarding/complete route. Captures device fingerprint from
// X-Device-Fingerprint header and assesses onboarding risk.
func OnboardingFraudMiddleware(fraudService *security.OnboardingFraudService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only assess on POST (state-changing) requests.
		if c.Request.Method != http.MethodPost {
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

		fingerprint := sanitizeFingerprint(c)
		ip := c.ClientIP()

		assessment, err := fraudService.AssessOnboarding(c.Request.Context(), userID, fingerprint, ip, c.GetHeader("User-Agent"))
		if err != nil {
			logger.Error("onboarding fraud assessment failed", zap.Error(err))
			c.Next()
			return
		}

		setFraudContext(c, assessment, fingerprint)

		if assessment.Action == entities.FraudActionBlock {
			logger.Warn("onboarding blocked by fraud detection",
				zap.String("user_id", userIDStr),
				zap.Float64("score", assessment.RiskScore))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "RISK_ASSESSMENT_FAILED",
				"message":    "Unable to complete this request. Please contact support.",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// DepositFraudMiddleware runs cross-account fraud detection on
// POST /deposits. Uses AssessFirstDeposit which includes deposit-specific
// signals (account age, deposit amount).
func DepositFraudMiddleware(fraudService *security.OnboardingFraudService, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
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

		fingerprint := sanitizeFingerprint(c)
		ip := c.ClientIP()

		// For deposits, we use AssessOnboarding as a baseline check.
		// The full AssessFirstDeposit (with amount/account age) is called
		// from the deposit handler itself, which has access to those values.
		assessment, err := fraudService.AssessOnboarding(c.Request.Context(), userID, fingerprint, ip, c.GetHeader("User-Agent"))
		if err != nil {
			logger.Error("deposit fraud assessment failed", zap.Error(err))
			c.Next()
			return
		}

		setFraudContext(c, assessment, fingerprint)

		if assessment.Action == entities.FraudActionBlock {
			logger.Warn("deposit blocked by fraud detection",
				zap.String("user_id", userIDStr),
				zap.Float64("score", assessment.RiskScore))

			c.JSON(http.StatusForbidden, gin.H{
				"error":      "RISK_ASSESSMENT_FAILED",
				"message":    "Unable to complete this request. Please contact support.",
				"request_id": c.GetString("request_id"),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// sanitizeFingerprint extracts and validates the device fingerprint.
func sanitizeFingerprint(c *gin.Context) string {
	fp := c.GetHeader("X-Device-Fingerprint")
	if fp != "" && validFingerprint.MatchString(fp) {
		return fp
	}

	// Fall back to context value from DeviceBoundValidation middleware.
	fp = c.GetString("device_fingerprint")
	if fp != "" && validFingerprint.MatchString(fp) {
		return fp
	}

	// Last resort: compute from headers (weak but better than nothing).
	return GenerateDeviceFingerprint(
		c.GetHeader("User-Agent"),
		c.GetHeader("Accept-Language"),
		c.GetHeader("Sec-CH-UA"),
		c.GetHeader("Sec-CH-UA-Mobile"),
	)
}

func setFraudContext(c *gin.Context, assessment *entities.OnboardingRiskAssessment, fingerprint string) {
	c.Set("fraud_assessment", assessment)
	c.Set("fraud_risk_score", assessment.RiskScore)
	c.Set("fraud_risk_action", string(assessment.Action))
	c.Set("device_fingerprint", fingerprint)

	switch assessment.Action {
	case entities.FraudActionManualReview:
		c.Set("requires_manual_review", true)
	case entities.FraudActionDelayFunding:
		c.Set("delay_funding", true)
	}
}
