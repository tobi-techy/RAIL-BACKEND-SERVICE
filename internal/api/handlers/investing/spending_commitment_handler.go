package investing

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	spendingcommitment "github.com/rail-service/rail_service/internal/domain/services/spendingcommitment"
)

type SpendingCommitmentHandler struct {
	service *spendingcommitment.Service
	logger  *zap.Logger
}

func NewSpendingCommitmentHandler(service *spendingcommitment.Service, logger *zap.Logger) *SpendingCommitmentHandler {
	return &SpendingCommitmentHandler{service: service, logger: logger}
}

// GetStatus returns the user's daily commitment and today's usage.
// GET /api/v1/spending-commitment
func (h *SpendingCommitmentHandler) GetStatus(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	status, err := h.service.GetStatus(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("get spending commitment failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to get spending commitment")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

// Set sets or updates the daily commitment.
// PUT /api/v1/spending-commitment
func (h *SpendingCommitmentHandler) Set(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	var req entities.SetCommitmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.SendBadRequest(c, common.ErrCodeInvalidRequest, err.Error())
		return
	}
	status, err := h.service.SetCommitment(c.Request.Context(), userID, req)
	if err != nil {
		h.respondSetError(c, userID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

// Deactivate removes the daily commitment (charges the flat increase fee).
// DELETE /api/v1/spending-commitment
func (h *SpendingCommitmentHandler) Deactivate(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		common.SendUnauthorized(c, "unauthorized")
		return
	}
	confirm := c.Query("confirm_fee") == "true"
	status, err := h.service.Deactivate(c.Request.Context(), userID, confirm)
	if err != nil {
		h.respondSetError(c, userID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": status})
}

func (h *SpendingCommitmentHandler) respondSetError(c *gin.Context, userID uuid.UUID, err error) {
	switch {
	case errors.Is(err, entities.ErrIncreaseFeeUnconfirmed):
		// Surface the fee so the client can prompt for confirmation, then retry with confirm_fee=true.
		var feeCents int64
		if status, gErr := h.service.GetStatus(c.Request.Context(), userID); gErr == nil {
			feeCents = status.IncreaseFeeCents
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":              "FEE_CONFIRMATION_REQUIRED",
			"message":            "Raising your daily limit requires confirming a fee",
			"increase_fee_cents": feeCents,
		})
	case errors.Is(err, entities.ErrInsufficientForFee):
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":   "INSUFFICIENT_FUNDS",
			"message": "Insufficient spend balance to cover the increase fee",
		})
	case errors.Is(err, entities.ErrInvalidCommitmentLimit):
		common.SendBadRequest(c, common.ErrCodeValidationError, "daily limit must be a positive amount")
	default:
		h.logger.Error("set spending commitment failed", zap.Error(err), zap.String("user_id", userID.String()))
		common.SendInternalError(c, common.ErrCodeInternalError, "failed to update spending commitment")
	}
}
