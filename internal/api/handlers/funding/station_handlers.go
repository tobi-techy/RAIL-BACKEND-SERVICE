package funding

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// SystemStatus represents the current system state for the Station display
type SystemStatus string

const (
	SystemStatusAllocating SystemStatus = "allocating"
	SystemStatusActive     SystemStatus = "active"
	SystemStatusPaused     SystemStatus = "paused"
)

// BalanceTrendResponse represents percentage changes for a balance type
type BalanceTrendResponse struct {
	DayChange   string `json:"day_change"`
	WeekChange  string `json:"week_change"`
	MonthChange string `json:"month_change"`
}

// BalanceTrendsResponse contains trends for spend and invest
type BalanceTrendsResponse struct {
	Spend  BalanceTrendResponse `json:"spend"`
	Invest BalanceTrendResponse `json:"invest"`
}

// ActivityItemResponse represents a recent transaction preview
type ActivityItemResponse struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// StationResponse represents the home screen data
type StationResponse struct {
	TotalBalance             string                  `json:"total_balance"`
	SpendBalance             string                  `json:"spend_balance"`
	InvestBalance            string                  `json:"invest_balance"`
	Currency                 string                  `json:"currency"`
	CurrencyLocale           string                  `json:"currency_locale"`
	PendingAmount            string                  `json:"pending_amount"`
	PendingTransactionsCount int                     `json:"pending_transactions_count"`
	SystemStatus             SystemStatus            `json:"system_status"`
	AccountNickname          *string                 `json:"account_nickname,omitempty"`
	BalanceTrends            *BalanceTrendsResponse  `json:"balance_trends,omitempty"`
	RecentActivity           []ActivityItemResponse  `json:"recent_activity"`
	UnreadAlertCount         int                     `json:"unread_alert_count"`
}

// StationService interface for station data retrieval
type StationService interface {
	GetUserBalances(ctx context.Context, userID uuid.UUID) (*station.Balances, error)
	GetAllocationMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	HasPendingDeposits(ctx context.Context, userID uuid.UUID) (bool, error)
	GetPendingInfo(ctx context.Context, userID uuid.UUID) (int, error)
	GetBalanceTrends(ctx context.Context, userID uuid.UUID, currentSpend, currentInvest decimal.Decimal) (*station.BalanceTrends, error)
	GetUserSettings(ctx context.Context, userID uuid.UUID) (*station.UserSettings, error)
	GetUnreadNotificationCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetRecentActivity(ctx context.Context, userID uuid.UUID, limit int) ([]*station.ActivityItem, error)
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
// @Description Returns total balance, spend/invest split, trends, recent activity, and alerts
// @Tags account
// @Produce json
// @Success 200 {object} StationResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/account/station [get]
func (h *StationHandlers) GetStation(c *gin.Context) {
	ctx := c.Request.Context()

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

	// Get user settings
	settings, _ := h.stationService.GetUserSettings(ctx, userID)

	// Get pending info
	pendingCount, _ := h.stationService.GetPendingInfo(ctx, userID)

	// Determine system status
	systemStatus := h.determineSystemStatus(ctx, userID)

	// Build response
	response := StationResponse{
		TotalBalance:             balances.TotalBalance.StringFixed(2),
		SpendBalance:             balances.SpendingBalance.StringFixed(2),
		InvestBalance:            balances.StashBalance.StringFixed(2),
		Currency:                 "USD",
		CurrencyLocale:           settings.CurrencyLocale,
		PendingAmount:            balances.PendingAmount.StringFixed(2),
		PendingTransactionsCount: pendingCount,
		SystemStatus:             systemStatus,
		AccountNickname:          settings.Nickname,
		RecentActivity:           []ActivityItemResponse{},
	}

	// Get balance trends
	if trends, err := h.stationService.GetBalanceTrends(ctx, userID, balances.SpendingBalance, balances.StashBalance); err == nil && trends != nil {
		response.BalanceTrends = &BalanceTrendsResponse{
			Spend: BalanceTrendResponse{
				DayChange:   trends.Spend.DayChange.StringFixed(2),
				WeekChange:  trends.Spend.WeekChange.StringFixed(2),
				MonthChange: trends.Spend.MonthChange.StringFixed(2),
			},
			Invest: BalanceTrendResponse{
				DayChange:   trends.Invest.DayChange.StringFixed(2),
				WeekChange:  trends.Invest.WeekChange.StringFixed(2),
				MonthChange: trends.Invest.MonthChange.StringFixed(2),
			},
		}
	}

	// Get recent activity (last 5 transactions)
	if activity, err := h.stationService.GetRecentActivity(ctx, userID, 5); err == nil {
		for _, item := range activity {
			response.RecentActivity = append(response.RecentActivity, ActivityItemResponse{
				ID:          item.ID.String(),
				Type:        item.Type,
				Amount:      item.Amount.StringFixed(2),
				Description: item.Description,
				CreatedAt:   item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
	}

	// Get unread alert count
	response.UnreadAlertCount, _ = h.stationService.GetUnreadNotificationCount(ctx, userID)

	c.JSON(http.StatusOK, response)
}

// determineSystemStatus determines the current system status
func (h *StationHandlers) determineSystemStatus(ctx context.Context, userID uuid.UUID) SystemStatus {
	// Check for pending deposits (allocating state)
	hasPending, err := h.stationService.HasPendingDeposits(ctx, userID)
	if err != nil {
		h.logger.Warn("Failed to check pending deposits", zap.Error(err))
		return SystemStatusActive
	}

	if hasPending {
		return SystemStatusAllocating
	}

	return SystemStatusActive
}
