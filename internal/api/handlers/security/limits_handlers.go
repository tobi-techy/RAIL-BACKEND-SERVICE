package security

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// LimitsService interface for limits operations
type LimitsService interface {
	ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
	ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
	GetUserLimits(ctx context.Context, userID uuid.UUID) (*entities.UserLimitsResponse, error)
}

// LimitsHandler handles limit-related API requests
type LimitsHandler struct {
	limitsService LimitsService
	logger        *logger.Logger
}

// NewLimitsHandler creates a new limits handler
func NewLimitsHandler(limitsService LimitsService, logger *logger.Logger) *LimitsHandler {
	return &LimitsHandler{
		limitsService: limitsService,
		logger:        logger,
	}
}

// GetUserLimits returns the user's current transaction limits and usage
// GET /api/v1/limits
func (h *LimitsHandler) GetUserLimits() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := common.GetUserID(c)
		if err != nil {
			common.RespondUnauthorized(c, "Invalid user session")
			return
		}

		limits, err := h.limitsService.GetUserLimits(c.Request.Context(), userID)
		if err != nil {
			h.logger.Error("Failed to get user limits", "error", err, "user_id", userID.String())
			common.RespondInternalError(c, "Failed to retrieve limits")
			return
		}

		c.JSON(http.StatusOK, limits)
	}
}

// ValidateDepositRequest represents a deposit validation request
type ValidateDepositRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// ValidateDeposit checks if a deposit amount is within limits
// POST /api/v1/limits/validate/deposit
func (h *LimitsHandler) ValidateDeposit() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := common.GetUserID(c)
		if err != nil {
			common.RespondUnauthorized(c, "Invalid user session")
			return
		}

		var req ValidateDepositRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			common.RespondBadRequest(c, "Invalid request body", nil)
			return
		}

		amount, err := decimal.NewFromString(req.Amount)
		if err != nil {
			common.RespondBadRequest(c, "Invalid amount format", nil)
			return
		}

		result, err := h.limitsService.ValidateDeposit(c.Request.Context(), userID, amount)
		if err != nil {
			// Return limit exceeded errors with details
			if errors.Is(err, entities.ErrBelowMinimumDeposit) ||
				errors.Is(err, entities.ErrDailyDepositExceeded) ||
				errors.Is(err, entities.ErrMonthlyDepositExceeded) {
				c.JSON(http.StatusOK, result)
				return
			}
			h.logger.Error("Failed to validate deposit", "error", err, "user_id", userID.String())
			common.RespondInternalError(c, "Failed to validate deposit")
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

// ValidateWithdrawalRequest represents a withdrawal validation request
type ValidateWithdrawalRequest struct {
	Amount string `json:"amount" binding:"required"`
}

// ValidateWithdrawal checks if a withdrawal amount is within limits
// POST /api/v1/limits/validate/withdrawal
func (h *LimitsHandler) ValidateWithdrawal() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := common.GetUserID(c)
		if err != nil {
			common.RespondUnauthorized(c, "Invalid user session")
			return
		}

		var req ValidateWithdrawalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			common.RespondBadRequest(c, "Invalid request body", nil)
			return
		}

		amount, err := decimal.NewFromString(req.Amount)
		if err != nil {
			common.RespondBadRequest(c, "Invalid amount format", nil)
			return
		}

		result, err := h.limitsService.ValidateWithdrawal(c.Request.Context(), userID, amount)
		if err != nil {
			if errors.Is(err, entities.ErrBelowMinimumWithdrawal) ||
				errors.Is(err, entities.ErrDailyWithdrawalExceeded) ||
				errors.Is(err, entities.ErrMonthlyWithdrawalExceeded) {
				c.JSON(http.StatusOK, result)
				return
			}
			h.logger.Error("Failed to validate withdrawal", "error", err, "user_id", userID.String())
			common.RespondInternalError(c, "Failed to validate withdrawal")
			return
		}

		c.JSON(http.StatusOK, result)
	}
}
