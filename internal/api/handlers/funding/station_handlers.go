package funding

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"go.uber.org/zap"
)

// SystemStatus represents the current system state for the Station display
type SystemStatus string

const (
	SystemStatusAllocating SystemStatus = "allocating"
	SystemStatusActive     SystemStatus = "active"
	SystemStatusPaused     SystemStatus = "paused"
)

// StationResponse represents the home screen data
// Per PRD: "Total balance, Spend balance, Invest balance, System status"
type StationResponse struct {
	TotalBalance             string              `json:"total_balance"`
	SpendBalance             string              `json:"spend_balance"`
	InvestBalance            string              `json:"invest_balance"`
	Currency                 string              `json:"currency"`
	PendingAmount            string              `json:"pending_amount"`
	PendingTransactionsCount int                 `json:"pending_transactions_count"`
	SystemStatus             SystemStatus        `json:"system_status"`
	SystemStatusMessage      string              `json:"system_status_message,omitempty"`
	LastActivityAt           string              `json:"last_activity_at,omitempty"`
	AllocationMode           *AllocationModeInfo `json:"allocation_mode,omitempty"`
}

// AllocationModeInfo provides allocation mode details
type AllocationModeInfo struct {
	Active        bool   `json:"active"`
	SpendingRatio string `json:"spending_ratio"`
	StashRatio    string `json:"stash_ratio"`
}

// StationService interface for station data retrieval
type StationService interface {
	GetUserBalances(ctx context.Context, userID uuid.UUID) (*station.Balances, error)
	GetAllocationMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	HasPendingDeposits(ctx context.Context, userID uuid.UUID) (bool, error)
	GetPendingInfo(ctx context.Context, userID uuid.UUID) (int, error)
}

// StationHandlers handles station/home screen endpoints
type StationHandlers struct {
	stationService StationService
	logger         *zap.Logger
}

// NewStationHandlers creates new station handlers
func NewStationHandlers(stationService StationService, logger *zap.Logger) *StationHandlers {
	return &StationHandlers{
		stationService: stationService,
		logger:         logger,
	}
}

// GetStation handles GET /api/v1/account/station
// @Summary Get home screen data (Station)
// @Description Returns total balance, spend/invest split, and system status for the home screen
// @Tags account
// @Produce json
// @Success 200 {object} StationResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/account/station [get]
func (h *StationHandlers) GetStation(c *gin.Context) {
	ctx := c.Request.Context()

	// Get user ID from context (set by auth middleware)
	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	userID, ok := userIDVal.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "INTERNAL_ERROR",
			Message: "Invalid user context",
		})
		return
	}

	// Get user balances
	balances, err := h.stationService.GetUserBalances(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to get user balances", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "BALANCE_ERROR",
			Message: "Failed to retrieve balances",
		})
		return
	}

	// Get allocation mode
	mode, err := h.stationService.GetAllocationMode(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get allocation mode", zap.Error(err), zap.String("user_id", userID.String()))
	}

	// Get pending info
	pendingCount, err := h.stationService.GetPendingInfo(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to get pending info", zap.Error(err))
		pendingCount = 0
	}

	// Determine system status
	systemStatus, statusMessage := h.determineSystemStatus(ctx, userID, mode)

	// Build response
	response := StationResponse{
		TotalBalance:             balances.TotalBalance.StringFixed(2),
		SpendBalance:             balances.SpendingBalance.StringFixed(2),
		InvestBalance:            balances.StashBalance.StringFixed(2),
		Currency:                 "USD",
		PendingAmount:            balances.PendingAmount.StringFixed(2),
		PendingTransactionsCount: pendingCount,
		SystemStatus:             systemStatus,
		SystemStatusMessage:      statusMessage,
	}

	// Add allocation mode info if available
	if mode != nil {
		response.AllocationMode = &AllocationModeInfo{
			Active:        mode.Active,
			SpendingRatio: mode.RatioSpending.StringFixed(2),
			StashRatio:    mode.RatioStash.StringFixed(2),
		}
	}

	c.JSON(http.StatusOK, response)
}

// determineSystemStatus determines the current system status and message
func (h *StationHandlers) determineSystemStatus(ctx context.Context, userID uuid.UUID, mode *entities.SmartAllocationMode) (SystemStatus, string) {
	// Check if allocation mode is paused
	if mode != nil && !mode.Active {
		return SystemStatusPaused, "Allocation mode is paused"
	}

	// Check for pending deposits (allocating state)
	hasPending, err := h.stationService.HasPendingDeposits(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to check pending deposits", zap.Error(err))
		return SystemStatusActive, "All systems operational"
	}

	if hasPending {
		return SystemStatusAllocating, "Processing pending transactions"
	}

	return SystemStatusActive, "All systems operational"
}
