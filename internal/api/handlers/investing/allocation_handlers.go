package investing

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/pkg/logger"
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
	Message string                         `json:"message"`
	Mode    *entities.SmartAllocationMode  `json:"mode"`
}

// AllocationStatusResponse represents the allocation status response
type AllocationStatusResponse struct {
	Active        bool    `json:"active"`
	SpendingRatio string  `json:"spending_ratio"`
	StashRatio    string  `json:"stash_ratio"`
	SpendingBalance string `json:"spending_balance"`
	StashBalance    string `json:"stash_balance"`
	SpendingUsed    string `json:"spending_used"`
	SpendingRemaining string `json:"spending_remaining"`
	TotalBalance    string `json:"total_balance"`
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

// EnableAllocationMode handles POST /api/v1/user/{id}/allocation/enable
// @Summary Enable smart allocation mode
// @Description Enables 70/30 allocation mode with custom ratios
// @Tags allocation
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Param request body EnableAllocationModeRequest true "Allocation ratios"
// @Success 200 {object} AllocationModeResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/user/{id}/allocation/enable [post]
func (h *AllocationHandlers) EnableAllocationMode(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from path parameter
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": userIDStr},
		})
		return
	}

	// Validate authenticated user matches path parameter
	authenticatedUserID, err := common.GetUserID(c)
	if err != nil || authenticatedUserID != userID {
		h.logger.Warn("Unauthorized allocation mode access",
			zap.String("authenticated_user", authenticatedUserID.String()),
			zap.String("requested_user", userID.String()))
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "FORBIDDEN",
			Message: "Cannot modify allocation mode for another user",
		})
		return
	}

	// Parse request body
	var req EnableAllocationModeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("Invalid request body", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_REQUEST",
			Message: "Invalid request body",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate request
	if err := h.validator.Struct(req); err != nil {
		h.logger.Warn("Validation failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "VALIDATION_FAILED",
			Message: "Request validation failed",
			Details: map[string]interface{}{"error": err.Error()},
		})
		return
	}

	// Validate ratios sum to 1.0
	sum := req.SpendingRatio + req.StashRatio
	if sum < 0.999 || sum > 1.001 {
		h.logger.Warn("Invalid ratio sum",
			zap.Float64("spending", req.SpendingRatio),
			zap.Float64("stash", req.StashRatio),
			zap.Float64("sum", sum))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_RATIOS",
			Message: "Spending and stash ratios must sum to 1.0",
			Details: map[string]interface{}{
				"spending_ratio": req.SpendingRatio,
				"stash_ratio":    req.StashRatio,
				"sum":            sum,
			},
		})
		return
	}

	// Convert to allocation ratios
	ratios := entities.AllocationRatios{
		SpendingRatio: decimal.NewFromFloat(req.SpendingRatio),
		StashRatio:    decimal.NewFromFloat(req.StashRatio),
	}

	h.logger.Info("Enabling allocation mode",
		zap.String("user_id", userID.String()),
		zap.Float64("spending_ratio", req.SpendingRatio),
		zap.Float64("stash_ratio", req.StashRatio))

	// Enable allocation mode
	if err := h.allocationService.EnableMode(ctx, userID, ratios); err != nil {
		h.logger.Error("Failed to enable allocation mode",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ENABLE_FAILED",
			Message: "Failed to enable allocation mode",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Get updated mode
	mode, err := h.allocationService.GetMode(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation mode after enabling",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		// Mode was enabled but we can't retrieve it - still return success
		c.JSON(http.StatusOK, AllocationModeResponse{
			Message: "Smart Allocation Mode enabled successfully",
			Mode:    nil,
		})
		return
	}

	h.logger.Info("Allocation mode enabled successfully", zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, AllocationModeResponse{
		Message: "Smart Allocation Mode enabled",
		Mode:    mode,
	})
}

// PauseAllocationMode handles POST /api/v1/user/{id}/allocation/pause
// @Summary Pause smart allocation mode
// @Description Temporarily pauses the allocation mode without deleting settings
// @Tags allocation
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} AllocationModeResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/user/{id}/allocation/pause [post]
func (h *AllocationHandlers) PauseAllocationMode(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from path parameter
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": userIDStr},
		})
		return
	}

	// Validate authenticated user matches path parameter
	authenticatedUserID, err := common.GetUserID(c)
	if err != nil || authenticatedUserID != userID {
		h.logger.Warn("Unauthorized allocation mode access",
			zap.String("authenticated_user", authenticatedUserID.String()),
			zap.String("requested_user", userID.String()))
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "FORBIDDEN",
			Message: "Cannot modify allocation mode for another user",
		})
		return
	}

	h.logger.Info("Pausing allocation mode", zap.String("user_id", userID.String()))

	// Pause allocation mode
	if err := h.allocationService.PauseMode(ctx, userID); err != nil {
		h.logger.Error("Failed to pause allocation mode",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "PAUSE_FAILED",
			Message: "Failed to pause allocation mode",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Get updated mode
	mode, err := h.allocationService.GetMode(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation mode after pausing",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, AllocationModeResponse{
			Message: "Smart Allocation Mode paused",
			Mode:    nil,
		})
		return
	}

	h.logger.Info("Allocation mode paused successfully", zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, AllocationModeResponse{
		Message: "Smart Allocation Mode paused",
		Mode:    mode,
	})
}

// ResumeAllocationMode handles POST /api/v1/user/{id}/allocation/resume
// @Summary Resume smart allocation mode
// @Description Resumes a previously paused allocation mode
// @Tags allocation
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} AllocationModeResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/user/{id}/allocation/resume [post]
func (h *AllocationHandlers) ResumeAllocationMode(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from path parameter
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": userIDStr},
		})
		return
	}

	// Validate authenticated user matches path parameter
	authenticatedUserID, err := common.GetUserID(c)
	if err != nil || authenticatedUserID != userID {
		h.logger.Warn("Unauthorized allocation mode access",
			zap.String("authenticated_user", authenticatedUserID.String()),
			zap.String("requested_user", userID.String()))
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "FORBIDDEN",
			Message: "Cannot modify allocation mode for another user",
		})
		return
	}

	h.logger.Info("Resuming allocation mode", zap.String("user_id", userID.String()))

	// Resume allocation mode
	if err := h.allocationService.ResumeMode(ctx, userID); err != nil {
		h.logger.Error("Failed to resume allocation mode",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "RESUME_FAILED",
			Message: "Failed to resume allocation mode",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Get updated mode
	mode, err := h.allocationService.GetMode(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation mode after resuming",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, AllocationModeResponse{
			Message: "Smart Allocation Mode resumed",
			Mode:    nil,
		})
		return
	}

	h.logger.Info("Allocation mode resumed successfully", zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, AllocationModeResponse{
		Message: "Smart Allocation Mode resumed",
		Mode:    mode,
	})
}

// GetAllocationStatus handles GET /api/v1/user/{id}/allocation/status
// @Summary Get allocation mode status
// @Description Returns the current allocation mode status and balances
// @Tags allocation
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} AllocationStatusResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/user/{id}/allocation/status [get]
func (h *AllocationHandlers) GetAllocationStatus(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from path parameter
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": userIDStr},
		})
		return
	}

	// Validate authenticated user matches path parameter
	authenticatedUserID, err := common.GetUserID(c)
	if err != nil || authenticatedUserID != userID {
		h.logger.Warn("Unauthorized allocation mode access",
			zap.String("authenticated_user", authenticatedUserID.String()),
			zap.String("requested_user", userID.String()))
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "FORBIDDEN",
			Message: "Cannot access allocation status for another user",
		})
		return
	}

	// Get allocation mode
	mode, err := h.allocationService.GetMode(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation mode",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "GET_STATUS_FAILED",
			Message: "Failed to retrieve allocation status",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Get balances
	balances, err := h.allocationService.GetBalances(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation balances",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "GET_BALANCES_FAILED",
			Message: "Failed to retrieve allocation balances",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Build response
	response := AllocationStatusResponse{
		Active:            false,
		SpendingRatio:     "0.70",
		StashRatio:        "0.30",
		SpendingBalance:   balances.SpendingBalance.String(),
		StashBalance:      balances.StashBalance.String(),
		SpendingUsed:      balances.SpendingUsed.String(),
		SpendingRemaining: balances.SpendingRemaining.String(),
		TotalBalance:      balances.TotalBalance.String(),
	}

	if mode != nil {
		response.Active = mode.Active
		response.SpendingRatio = mode.RatioSpending.String()
		response.StashRatio = mode.RatioStash.String()
	}

	c.JSON(http.StatusOK, response)
}

// GetAllocationBalances handles GET /api/v1/user/{id}/allocation/balances
// @Summary Get allocation balances
// @Description Returns detailed balance breakdown for allocation mode
// @Tags allocation
// @Produce json
// @Param id path string true "User ID"
// @Success 200 {object} AllocationBalancesResponse
// @Failure 400 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/user/{id}/allocation/balances [get]
func (h *AllocationHandlers) GetAllocationBalances(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from path parameter
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Warn("Invalid user ID", zap.String("user_id", userIDStr), zap.Error(err))
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_USER_ID",
			Message: "Invalid user ID format",
			Details: map[string]interface{}{"user_id": userIDStr},
		})
		return
	}

	// Validate authenticated user matches path parameter
	authenticatedUserID, err := common.GetUserID(c)
	if err != nil || authenticatedUserID != userID {
		h.logger.Warn("Unauthorized allocation balance access",
			zap.String("authenticated_user", authenticatedUserID.String()),
			zap.String("requested_user", userID.String()))
		c.JSON(http.StatusForbidden, entities.ErrorResponse{
			Code:    "FORBIDDEN",
			Message: "Cannot access allocation balances for another user",
		})
		return
	}

	// Get balances
	balances, err := h.allocationService.GetBalances(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get allocation balances",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "GET_BALANCES_FAILED",
			Message: "Failed to retrieve allocation balances",
			Details: map[string]interface{}{"error": "Internal server error"},
		})
		return
	}

	// Build response
	response := AllocationBalancesResponse{
		SpendingBalance:   balances.SpendingBalance.String(),
		StashBalance:      balances.StashBalance.String(),
		SpendingUsed:      balances.SpendingUsed.String(),
		SpendingRemaining: balances.SpendingRemaining.String(),
		TotalBalance:      balances.TotalBalance.String(),
		ModeActive:        balances.ModeActive,
	}

	c.JSON(http.StatusOK, response)
}
