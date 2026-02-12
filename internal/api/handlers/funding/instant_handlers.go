package funding

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"go.uber.org/zap"
)

// InstantFundingHandlers handles instant funding HTTP requests
type InstantFundingHandlers struct {
	service *funding.InstantFundingService
	logger  *zap.Logger
}

// NewInstantFundingHandlers creates new instant funding handlers
func NewInstantFundingHandlers(service *funding.InstantFundingService, logger *zap.Logger) *InstantFundingHandlers {
	return &InstantFundingHandlers{
		service: service,
		logger:  logger,
	}
}

// RequestInstantFunding handles POST /api/v1/funding/instant
// @Summary Request instant buying power
// @Description Get instant buying power for trading. Wire settles in 1-2 business days.
// @Tags Funding
// @Accept json
// @Produce json
// @Param request body funding.InstantFundingRequest true "Instant funding request"
// @Success 200 {object} funding.InstantFundingResponse
// @Failure 400 {object} common.ErrorResponse
// @Failure 401 {object} common.ErrorResponse
// @Router /api/v1/funding/instant [post]
func (h *InstantFundingHandlers) RequestInstantFunding(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User not authenticated")
		return
	}

	uid, ok := userID.(uuid.UUID)
	if !ok {
		common.SendUnauthorized(c, "Invalid user ID")
		return
	}

	var req funding.InstantFundingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, "INVALID_REQUEST", "Invalid request format")
		return
	}

	resp, err := h.service.RequestInstantFunding(c.Request.Context(), uid, &req)
	if err != nil {
		h.logger.Warn("Instant funding request failed",
			zap.String("user_id", uid.String()),
			zap.Error(err))
		common.SendBadRequest(c, "FUNDING_ERROR", err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": resp,
	})
}

// GetInstantFundingStatus handles GET /api/v1/funding/instant/status
// @Summary Get instant funding status
// @Description Check current instant funding state and available limits
// @Tags Funding
// @Produce json
// @Success 200 {object} funding.InstantFundingStatusResponse
// @Failure 401 {object} common.ErrorResponse
// @Router /api/v1/funding/instant/status [get]
func (h *InstantFundingHandlers) GetInstantFundingStatus(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		common.SendUnauthorized(c, "User not authenticated")
		return
	}

	uid, ok := userID.(uuid.UUID)
	if !ok {
		common.SendUnauthorized(c, "Invalid user ID")
		return
	}

	resp, err := h.service.GetInstantFundingStatus(c.Request.Context(), uid)
	if err != nil {
		h.logger.Error("Failed to get instant funding status",
			zap.String("user_id", uid.String()),
			zap.Error(err))
		common.SendInternalError(c, "STATUS_ERROR", "Failed to get funding status")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": resp,
	})
}
