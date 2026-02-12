package investing

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AllocationHandlers handles smart allocation mode endpoints
type AllocationHandlers struct {
	allocationService *allocation.Service
	validator         *validator.Validate
	logger            *logger.Logger
}

// NewAllocationHandlers creates a new allocation handlers instance
func NewAllocationHandlers(
	allocationService *allocation.Service,
	logger *logger.Logger,
) *AllocationHandlers {
	return &AllocationHandlers{
		allocationService: allocationService,
		validator:         validator.New(),
		logger:            logger,
	}
}

// Request/Response models

// EnableAllocationModeRequest represents the request to enable 70/30 allocation mode
type EnableAllocationModeRequest struct {
	SpendingRatio float64 `json:"spending_ratio" validate:"required,gte=0,lte=1"`
	StashRatio    float64 `json:"stash_ratio" validate:"required,gte=0,lte=1"`
}

// AllocationModeResponse represents the allocation mode status response
type AllocationModeResponse struct {
	Message string                       `json:"message"`
	Mode    *entities.SmartAllocationMode `json:"mode,omitempty"`
}

// AllocationBalancesResponse represents the balances response
type AllocationBalancesResponse struct {
	SpendingBalance   string `json:"spending_balance"`
	StashBalance      string `json:"stash_balance"`
	SpendingUsed      string `json:"spending_used"`
	SpendingRemaining string `json:"spending_remaining"`
	TotalBalance      string `json:"total_balance"`
	ModeActive        bool   `json:"mode_active"`
}

// EnableAllocationMode handles POST /api/v1/allocation/enable
// @Summary Enable smart allocation mode
// @Description Enables 70/30 allocation mode with custom ratios (default: 70% spending, 30% stash)
// @Tags allocation
// @Accept json
// @Produce json
// @Param request body EnableAllocationModeRequest true "Allocation ratios"
// @Success 200 {object} AllocationModeResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/allocation/enable [post]
func (h *AllocationHandlers) EnableAllocationMode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	var req EnableAllocationModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_REQUEST", Message: "Invalid request body"})
		return
	}

	if err := h.validator.Struct(req); err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "VALIDATION_FAILED", Message: err.Error()})
		return
	}

	sum := req.SpendingRatio + req.StashRatio
	if sum < 0.999 || sum > 1.001 {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{Code: "INVALID_RATIOS", Message: "Spending and stash ratios must sum to 1.0"})
		return
	}

	ratios := entities.AllocationRatios{
		SpendingRatio: decimal.NewFromFloat(req.SpendingRatio),
		StashRatio:    decimal.NewFromFloat(req.StashRatio),
	}

	if err := h.allocationService.EnableMode(ctx, userID, ratios); err != nil {
		h.logger.Error("Failed to enable allocation mode", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "ENABLE_FAILED", Message: "Failed to enable allocation mode"})
		return
	}

	mode, _ := h.allocationService.GetMode(ctx, userID)

	c.JSON(http.StatusOK, AllocationModeResponse{
		Message: "Smart Allocation Mode enabled",
		Mode:    mode,
	})
}

// DisableAllocationMode handles POST /api/v1/allocation/disable
// @Summary Disable smart allocation mode
// @Description Disables 70/30 allocation mode - all future funds go to spending balance
// @Tags allocation
// @Produce json
// @Success 200 {object} AllocationModeResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/allocation/disable [post]
func (h *AllocationHandlers) DisableAllocationMode(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	if err := h.allocationService.DisableMode(ctx, userID); err != nil {
		h.logger.Error("Failed to disable allocation mode", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DISABLE_FAILED", Message: "Failed to disable allocation mode"})
		return
	}

	c.JSON(http.StatusOK, AllocationModeResponse{
		Message: "Smart Allocation Mode disabled - all funds will go to spending balance",
	})
}

// GetAllocationBalances handles GET /api/v1/allocation/balances
// @Summary Get allocation balances
// @Description Returns detailed balance breakdown for allocation mode
// @Tags allocation
// @Produce json
// @Success 200 {object} AllocationBalancesResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/allocation/balances [get]
func (h *AllocationHandlers) GetAllocationBalances(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	balances, err := h.allocationService.GetBalances(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation balances", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "GET_BALANCES_FAILED", Message: "Failed to retrieve allocation balances"})
		return
	}

	c.JSON(http.StatusOK, AllocationBalancesResponse{
		SpendingBalance:   balances.SpendingBalance.String(),
		StashBalance:      balances.StashBalance.String(),
		SpendingUsed:      balances.SpendingUsed.String(),
		SpendingRemaining: balances.SpendingRemaining.String(),
		TotalBalance:      balances.TotalBalance.String(),
		ModeActive:        balances.ModeActive,
	})
}
