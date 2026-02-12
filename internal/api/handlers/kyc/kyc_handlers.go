package kyc

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/kyc"
	"github.com/rail-service/rail_service/pkg/logger"
)

type Handler struct {
	kycService *kyc.Service
	logger     *logger.Logger
}

func NewHandler(kycService *kyc.Service, log *logger.Logger) *Handler {
	return &Handler{
		kycService: kycService,
		logger:     log,
	}
}

// SubmitKYC handles POST /api/v1/kyc/submit
// @Summary Submit KYC information
// @Description Submit tax ID, identity documents, and regulatory disclosures for KYC verification
// @Tags KYC
// @Accept json
// @Produce json
// @Param request body entities.KYCSubmitRequest true "KYC submission data"
// @Success 200 {object} entities.KYCSubmitResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 429 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Security BearerAuth
// @Router /kyc/submit [post]
func (h *Handler) SubmitKYC(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from auth context
	userIDStr, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	userID, err := uuid.Parse(userIDStr.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Parse request
	var req entities.KYCSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid KYC request",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request format",
		})
		return
	}

	// Set context fields
	req.UserID = userID
	req.IPAddress = c.ClientIP()

	// Submit KYC
	response, err := h.kycService.SubmitKYC(ctx, &req)
	if err != nil {
		h.logger.Error("KYC submission failed",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)

		// Handle specific errors
		switch err {
		case kyc.ErrInvalidSSN:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid SSN format"})
		case kyc.ErrInvalidImage:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid image format"})
		case kyc.ErrImageTooLarge:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Image exceeds 10MB limit"})
		case kyc.ErrKYCAlreadyApproved:
			c.JSON(http.StatusBadRequest, gin.H{"error": "KYC already approved"})
		case kyc.ErrNoBridgeCustomer:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Complete signup first"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit KYC"})
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetKYCStatus handles GET /api/v1/kyc/status
// @Summary Get KYC status
// @Description Get current KYC verification status and capabilities
// @Tags KYC
// @Produce json
// @Success 200 {object} entities.KYCStatusResponse
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Security BearerAuth
// @Router /kyc/status [get]
func (h *Handler) GetKYCStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from auth context
	userIDStr, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	userID, err := uuid.Parse(userIDStr.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	status, err := h.kycService.GetKYCStatus(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get KYC status",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get KYC status"})
		return
	}

	c.JSON(http.StatusOK, status)
}
