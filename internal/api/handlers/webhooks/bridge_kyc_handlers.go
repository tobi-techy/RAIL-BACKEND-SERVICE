package webhooks

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// BridgeKYCHandlers handles Bridge KYC operations
type BridgeKYCHandlers struct {
	bridgeClient bridge.BridgeClient
	userRepo     repositories.UserRepository
	logger       *zap.Logger
}

// NewBridgeKYCHandlers creates a new Bridge KYC handlers instance
func NewBridgeKYCHandlers(
	bridgeClient bridge.BridgeClient,
	userRepo repositories.UserRepository,
	logger *zap.Logger,
) *BridgeKYCHandlers {
	return &BridgeKYCHandlers{
		bridgeClient: bridgeClient,
		userRepo:     userRepo,
		logger:       logger,
	}
}

// GetBridgeKYCLink handles GET /kyc/bridge/link
// @Summary Get Bridge KYC verification link
// @Description Returns a Bridge KYC link for fast identity verification (< 2 minutes)
// @Tags kyc
// @Produce json
// @Success 200 {object} map[string]interface{} "KYC link response"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse "Bridge customer not found"
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/kyc/bridge/link [get]
func (h *BridgeKYCHandlers) GetBridgeKYCLink(c *gin.Context) {
	ctx := c.Request.Context()

	userIDStr, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userID, ok := userIDStr.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
		})
		return
	}

	// Get user to find Bridge customer ID
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "USER_FETCH_ERROR",
			Message: "Failed to retrieve user information",
		})
		return
	}

	if user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		h.logger.Warn("User has no Bridge customer ID", zap.String("user_id", userID.String()))
		c.JSON(http.StatusNotFound, entities.ErrorResponse{
			Code:    "BRIDGE_CUSTOMER_NOT_FOUND",
			Message: "Bridge customer not set up. Please complete onboarding first.",
		})
		return
	}

	// Get KYC link from Bridge
	kycResp, err := h.bridgeClient.GetKYCLink(ctx, *user.BridgeCustomerID)
	if err != nil {
		h.logger.Error("Failed to get Bridge KYC link",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("bridge_customer_id", *user.BridgeCustomerID))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "KYC_LINK_ERROR",
			Message: "Failed to generate KYC verification link",
		})
		return
	}

	h.logger.Info("Generated Bridge KYC link",
		zap.String("user_id", userID.String()),
		zap.String("bridge_customer_id", *user.BridgeCustomerID))

	c.JSON(http.StatusOK, gin.H{
		"kyc_link":            kycResp.KYCLink,
		"expires_at":          kycResp.ExpiresAt,
		"provider":            "bridge",
		"estimated_time":      "2 minutes",
		"message":             "Complete your identity verification using this link",
		"instructions": []string{
			"Click the link to open Bridge's secure verification portal",
			"Have your government-issued ID ready",
			"Take a clear selfie when prompted",
			"Verification typically completes in under 2 minutes",
		},
	})
}

// GetBridgeKYCStatus handles GET /kyc/bridge/status
// @Summary Get Bridge KYC status
// @Description Returns the current KYC verification status from Bridge
// @Tags kyc
// @Produce json
// @Success 200 {object} map[string]interface{} "KYC status response"
// @Failure 400 {object} entities.ErrorResponse
// @Failure 404 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/kyc/bridge/status [get]
func (h *BridgeKYCHandlers) GetBridgeKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	userIDStr, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userID, ok := userIDStr.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
		})
		return
	}

	// Get user to find Bridge customer ID
	user, err := h.userRepo.GetUserEntityByID(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "USER_FETCH_ERROR",
			Message: "Failed to retrieve user information",
		})
		return
	}

	if user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		c.JSON(http.StatusOK, gin.H{
			"status":       "not_started",
			"verified":     false,
			"provider":     "bridge",
			"message":      "KYC verification not started",
			"next_step":    "Get KYC link to begin verification",
		})
		return
	}

	// Get customer details from Bridge to check KYC status
	customer, err := h.bridgeClient.GetCustomer(ctx, *user.BridgeCustomerID)
	if err != nil {
		h.logger.Error("Failed to get Bridge customer",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("bridge_customer_id", *user.BridgeCustomerID))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "CUSTOMER_FETCH_ERROR",
			Message: "Failed to retrieve KYC status",
		})
		return
	}

	// Map Bridge customer status to KYC status
	// Bridge status: active, pending, rejected, etc.
	verified := customer.Status == "active"
	status := string(customer.Status)

	response := gin.H{
		"status":              status,
		"verified":            verified,
		"provider":            "bridge",
		"bridge_customer_id":  *user.BridgeCustomerID,
	}

	if verified {
		response["message"] = "Identity verification complete"
		response["kyc_approved_at"] = customer.UpdatedAt
	} else if status == "pending" || status == "processing" {
		response["message"] = "Verification in progress"
		response["estimated_time"] = "Usually completes within 2 minutes"
	} else if status == "rejected" {
		response["message"] = "Verification failed"
		response["next_step"] = "Please contact support or try again"
		if len(customer.RequirementsDue) > 0 {
			response["requirements_due"] = customer.RequirementsDue
		}
		if len(customer.RejectionReasons) > 0 {
			response["rejection_reasons"] = customer.RejectionReasons
		}
	} else {
		response["message"] = "Please complete identity verification"
		response["next_step"] = "Get KYC link to begin verification"
	}

	c.JSON(http.StatusOK, response)
}
