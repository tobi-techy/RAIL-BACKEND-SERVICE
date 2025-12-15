package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// KYCStatusService exposes the minimal method needed to gate features on KYC
type KYCStatusService interface {
	GetKYCStatus(ctx context.Context, userID uuid.UUID) (*entities.KYCStatusResponse, error)
}

// RequireKYC enforces that the authenticated user has completed KYC before accessing the handler
func RequireKYC(onboardingService KYCStatusService, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDValue, exists := c.Get("user_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		userID, ok := userIDValue.(uuid.UUID)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "INTERNAL_ERROR",
				"message": "Unable to parse user identity",
			})
			return
		}

		status, err := onboardingService.GetKYCStatus(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to fetch KYC status for gating",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "KYC_STATUS_ERROR",
				"message": "Unable to verify KYC status at this time",
			})
			return
		}

		if status == nil || !status.Verified {
			requiredFor := []string{}
			nextSteps := []string{}
			response := gin.H{
				"code":        "KYC_REQUIRED",
				"message":     "Please complete KYC to access this feature",
				"status":      "",
				"requiredFor": requiredFor,
				"nextSteps":   nextSteps,
			}
			if status != nil {
				response["status"] = status.Status
				response["hasSubmitted"] = status.HasSubmitted
				if len(status.RequiredFor) > 0 {
					response["requiredFor"] = status.RequiredFor
				}
				if len(status.NextSteps) > 0 {
					response["nextSteps"] = status.NextSteps
				}
			}

			c.AbortWithStatusJSON(http.StatusForbidden, response)
			return
		}

		c.Next()
	}
}
