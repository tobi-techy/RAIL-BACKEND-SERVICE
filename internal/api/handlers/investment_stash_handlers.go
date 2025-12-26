package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"go.uber.org/zap"
)

// InvestmentStashHandlers handles the investment stash screen endpoint
type InvestmentStashHandlers struct {
	allocationService  *allocation.Service
	investingService   *investing.Service
	copyTradingService *copytrading.Service
	logger             *zap.Logger
}

// NewInvestmentStashHandlers creates new investment stash handlers
func NewInvestmentStashHandlers(
	allocationService *allocation.Service,
	investingService *investing.Service,
	copyTradingService *copytrading.Service,
	logger *zap.Logger,
) *InvestmentStashHandlers {
	return &InvestmentStashHandlers{
		allocationService:  allocationService,
		investingService:   investingService,
		copyTradingService: copyTradingService,
		logger:             logger,
	}
}

// GetInvestmentStash handles GET /api/v1/account/investment-stash
// @Summary Get investment stash screen data
// @Description Returns comprehensive investment data for the investment stash screen
// @Tags account
// @Produce json
// @Success 200 {object} InvestmentStashResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/account/investment-stash [get]
func (h *InvestmentStashHandlers) GetInvestmentStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		warnings []string
	)

	// Data containers
	var (
		balances       *entities.AllocationBalances
		portfolio      *entities.Portfolio
		drafts         []*entities.DraftSummary
		conductorApp   *entities.ConductorApplication
		allocationMode *entities.SmartAllocationMode
	)

	// Parallel fetch - allocation balances
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			return
		}
		b, err := h.allocationService.GetBalances(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get allocation balances", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "balances_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		balances = b
		mu.Unlock()
	}()

	// Parallel fetch - allocation mode
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			return
		}
		m, err := h.allocationService.GetMode(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get allocation mode", zap.Error(err))
			return
		}
		mu.Lock()
		allocationMode = m
		mu.Unlock()
	}()

	// Parallel fetch - portfolio/positions
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.investingService == nil {
			return
		}
		p, err := h.investingService.GetPortfolio(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get portfolio", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "positions_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		portfolio = p
		mu.Unlock()
	}()

	// Parallel fetch - copy trading drafts (following)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.copyTradingService == nil {
			return
		}
		d, err := h.copyTradingService.GetUserDrafts(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get user drafts", zap.Error(err), zap.String("user_id", userID.String()))
			mu.Lock()
			warnings = append(warnings, "following_unavailable")
			mu.Unlock()
			return
		}
		mu.Lock()
		drafts = d
		mu.Unlock()
	}()

	// Parallel fetch - conductor application status
	wg.Add(1)
	go func() {
		defer wg.Done()
		if h.copyTradingService == nil {
			return
		}
		app, err := h.copyTradingService.GetMyApplication(ctx, userID)
		if err != nil {
			h.logger.Warn("Failed to get conductor application", zap.Error(err))
			return
		}
		mu.Lock()
		conductorApp = app
		mu.Unlock()
	}()

	wg.Wait()

	// Build response
	response := h.buildResponse(userID, balances, portfolio, drafts, conductorApp, allocationMode, warnings)

	c.JSON(http.StatusOK, response)
}

// buildResponse constructs the InvestmentStashResponse from fetched data
func (h *InvestmentStashHandlers) buildResponse(
	userID uuid.UUID,
	balances *entities.AllocationBalances,
	portfolio *entities.Portfolio,
	drafts []*entities.DraftSummary,
	conductorApp *entities.ConductorApplication,
	allocationMode *entities.SmartAllocationMode,
	warnings []string,
) *InvestmentStashResponse {
	response := &InvestmentStashResponse{
		TotalInvestmentBalance: "0.00",
		TotalCostBasis:         "0.00",
		TotalGain:              "0.00",
		TotalGainPercent:       "0.00%",
		Performance:            PerformanceMetrics{},
		Positions:              []PositionSummary{},
		Baskets:                []BasketInvestmentSummary{},
		Following:              []FollowedConductorSummary{},
		AllocationInfo:         InvestmentAllocationInfo{Active: false, StashRatio: "0.30"},
		Stats: InvestmentStats{
			TotalDeposits:    "0.00",
			TotalWithdrawals: "0.00",
		},
		Warnings: warnings,
	}

	// Populate balance summary from allocation balances
	if balances != nil {
		response.TotalInvestmentBalance = balances.StashBalance.StringFixed(2)
		response.AllocationInfo.Active = balances.ModeActive
	}

	// Populate allocation mode info
	if allocationMode != nil {
		response.AllocationInfo.Active = allocationMode.Active
		response.AllocationInfo.StashRatio = allocationMode.RatioStash.StringFixed(2)
		if allocationMode.ResumedAt != nil {
			response.AllocationInfo.LastAllocatedAt = allocationMode.ResumedAt.Format(time.RFC3339)
		}
	}

	// Populate positions from portfolio
	if portfolio != nil && len(portfolio.Positions) > 0 {
		totalValue := decimal.Zero
		for _, pos := range portfolio.Positions {
			marketValue, err := decimal.NewFromString(pos.MarketValue)
			if err != nil {
				h.logger.Error("Failed to parse market value for total calculation", zap.Error(err), zap.String("value", pos.MarketValue))
				continue
			}
			totalValue = totalValue.Add(marketValue)
		}

		positions := make([]PositionSummary, 0, len(portfolio.Positions))
		for _, pos := range portfolio.Positions {
			marketValue, err := decimal.NewFromString(pos.MarketValue)
			if err != nil {
				h.logger.Error("Failed to parse market value", zap.Error(err), zap.String("value", pos.MarketValue))
				continue
			}
			avgCost, err := decimal.NewFromString(pos.AvgPrice)
			if err != nil {
				h.logger.Error("Failed to parse avg price", zap.Error(err), zap.String("value", pos.AvgPrice))
				continue
			}
			qty, err := decimal.NewFromString(pos.Quantity)
			if err != nil {
				h.logger.Error("Failed to parse quantity", zap.Error(err), zap.String("value", pos.Quantity))
				continue
			}
			costBasis := avgCost.Mul(qty)
			gain := marketValue.Sub(costBasis)
			gainPct := decimal.Zero
			if !costBasis.IsZero() {
				gainPct = gain.Div(costBasis).Mul(decimal.NewFromInt(100))
			}
			weight := decimal.Zero
			if !totalValue.IsZero() {
				weight = marketValue.Div(totalValue).Mul(decimal.NewFromInt(100))
			}

			positions = append(positions, PositionSummary{
				Symbol:            pos.BasketID.String(), // Using basket ID as symbol for now
				Quantity:          pos.Quantity,
				MarketValue:       pos.MarketValue,
				CostBasis:         costBasis.StringFixed(2),
				AvgCost:           pos.AvgPrice,
				UnrealizedGain:    gain.StringFixed(2),
				UnrealizedGainPct: gainPct.StringFixed(2) + "%",
				PortfolioWeight:   weight.StringFixed(2) + "%",
			})
		}
		response.Positions = positions
		response.Stats.PositionCount = len(positions)

		// Update total investment balance with portfolio value
		portfolioValue, err := decimal.NewFromString(portfolio.TotalValue)
		if err != nil {
			h.logger.Error("Failed to parse portfolio total value", zap.Error(err), zap.String("value", portfolio.TotalValue))
		} else if balances != nil {
			response.TotalInvestmentBalance = balances.StashBalance.Add(portfolioValue).StringFixed(2)
		} else {
			response.TotalInvestmentBalance = portfolioValue.StringFixed(2)
		}
	}

	// Populate following (copy trading drafts)
	if drafts != nil && len(drafts) > 0 {
		following := make([]FollowedConductorSummary, 0, len(drafts))
		for _, draft := range drafts {
			if draft.Status == entities.DraftStatusActive || draft.Status == entities.DraftStatusPaused {
				gainPct := decimal.Zero
				if !draft.AllocatedCapital.IsZero() {
					gainPct = draft.TotalProfitLoss.Div(draft.AllocatedCapital).Mul(decimal.NewFromInt(100))
				}

				following = append(following, FollowedConductorSummary{
					FollowID:      draft.ID.String(),
					ConductorID:   draft.ConductorID.String(),
					ConductorName: draft.ConductorName,
					Allocated:     draft.AllocatedCapital.StringFixed(2),
					CurrentValue:  draft.CurrentAUM.StringFixed(2),
					Gain:          draft.TotalProfitLoss.StringFixed(2),
					GainPercent:   gainPct.StringFixed(2) + "%",
					FollowedAt:    draft.CreatedAt.Format(time.RFC3339),
				})
			}
		}
		response.Following = following
		response.Stats.FollowingCount = len(following)
	}

	// Populate conductor application status
	response.ConductorStatus = h.buildConductorStatus(conductorApp)

	// Set default performance metrics (placeholder - would need historical data)
	response.Performance = PerformanceMetrics{
		Day:        "0.00",
		DayPct:     "0.00%",
		Week:       "0.00",
		WeekPct:    "0.00%",
		Month:      "0.00",
		MonthPct:   "0.00%",
		YTD:        "0.00",
		YTDPct:     "0.00%",
		AllTime:    "0.00",
		AllTimePct: "0.00%",
	}

	return response
}

// buildConductorStatus builds the conductor application status
func (h *InvestmentStashHandlers) buildConductorStatus(app *entities.ConductorApplication) *ConductorApplicationStatus {
	status := &ConductorApplicationStatus{
		Status:   "none",
		CanApply: true,
	}

	if app != nil {
		switch app.Status {
		case entities.ConductorApplicationStatusPending:
			status.Status = "pending"
			status.CanApply = false
			status.AppliedAt = app.CreatedAt.Format(time.RFC3339)
		case entities.ConductorApplicationStatusApproved:
			status.Status = "approved"
			status.CanApply = false
			if app.ReviewedAt != nil {
				status.ApprovedAt = app.ReviewedAt.Format(time.RFC3339)
			}
		case entities.ConductorApplicationStatusRejected:
			status.Status = "rejected"
			status.CanApply = true // Can reapply after rejection
			if app.ReviewedAt != nil {
				status.RejectedAt = app.ReviewedAt.Format(time.RFC3339)
			}
			status.RejectionReason = app.RejectionReason
		}
	}

	return status
}
