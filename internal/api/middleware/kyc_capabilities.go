package middleware

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// UserEntityReader exposes the minimum user lookup needed for capability checks.
type UserEntityReader interface {
	GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
}

func extractUserID(c *gin.Context) (uuid.UUID, error) {
	userIDValue, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, fmt.Errorf("user_id not found in context")
	}

	switch v := userIDValue.(type) {
	case uuid.UUID:
		return v, nil
	case string:
		return uuid.Parse(v)
	default:
		return uuid.Nil, fmt.Errorf("unsupported user_id type %T", userIDValue)
	}
}

// RequireBridgeCapability enforces Bridge KYC eligibility for Bridge-dependent features.
func RequireBridgeCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to load user for Bridge capability check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "KYC_STATUS_ERROR",
				"message": "Unable to verify Bridge capability at this time",
			})
			return
		}

		bridgeActive := user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active"
		legacyApproved := user.KYCStatus == "approved"
		if !bridgeActive && !legacyApproved {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "BRIDGE_KYC_REQUIRED",
				"message": "Bridge identity verification is required to access this feature",
			})
			return
		}

		c.Next()
	}
}

// RequireAlpacaCapability enforces Alpaca-related KYC eligibility for investing features.
func RequireAlpacaCapability(userReader UserEntityReader, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := extractUserID(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Authentication required",
			})
			return
		}

		user, err := userReader.GetUserEntityByID(c.Request.Context(), userID)
		if err != nil {
			log.Error("Failed to load user for Alpaca capability check",
				zap.Error(err),
				zap.String("user_id", userID.String()),
				zap.String("request_id", c.GetString("request_id")))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"code":    "KYC_STATUS_ERROR",
				"message": "Unable to verify investing eligibility at this time",
			})
			return
		}

		if user.KYCStatus != "approved" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "ALPACA_KYC_REQUIRED",
				"message": "Complete identity verification to access investing features",
			})
			return
		}

		c.Next()
	}
}
