package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/pkg/logger"
)

type kycEligibilityUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

type KYCMiddleware struct {
	userRepo kycEligibilityUserRepository
	logger   *logger.Logger
}

func NewKYCMiddleware(userRepo kycEligibilityUserRepository, log *logger.Logger) *KYCMiddleware {
	return &KYCMiddleware{
		userRepo: userRepo,
		logger:   log,
	}
}

// RequireKYCEligibility ensures user is eligible to submit KYC.
func (m *KYCMiddleware) RequireKYCEligibility() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		userID, err := extractUserID(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// Get user
		user, err := m.userRepo.GetByID(ctx, userID)
		if err != nil {
			m.logger.Error("Failed to get user for KYC eligibility check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
			)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
			c.Abort()
			return
		}

		// Must have bridge_customer_id from signup
		if user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "Complete signup first",
				"code":  "SIGNUP_REQUIRED",
			})
			c.Abort()
			return
		}

		// Can't resubmit if already approved
		if user.KYCStatus == "approved" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "KYC already approved",
				"code":  "KYC_ALREADY_APPROVED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
