package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AutoInvestRepository interface for auto-invest settings
type AutoInvestRepository interface {
	GetUserSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error)
}

// InvestmentStashHandlers handles the investment stash screen endpoint
type InvestmentStashHandlers struct {
	allocationService  *allocation.Service
	investingService   *investing.Service
	autoInvestRepo     AutoInvestRepository
	logger             *zap.Logger
}

// NewInvestmentStashHandlers creates new investment stash handlers
func NewInvestmentStashHandlers(
	allocationService *allocation.Service,
	investingService *investing.Service,
	logger *zap.Logger,
) *InvestmentStashHandlers {
	return &InvestmentStashHandlers{
		allocationService: allocationService,
		investingService:  investingService,
		logger:            logger,
	}
}

// SetAutoInvestRepository sets the auto-invest repository
func (h *InvestmentStashHandlers) SetAutoInvestRepository(repo AutoInvestRepository) {
	h.autoInvestRepo = repo
}

// GetInvestmentStash handles GET /api/v1/account/investment-stash
func (h *InvestmentStashHandlers) GetInvestmentStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize > 50 {
		pageSize = 50
	}

	var wg sync.WaitGroup
	var (
		balances       *entities.AllocationBalances
		portfolio      *entities.Portfolio
		allocationMode *entities.SmartAllocationMode
		autoInvest     *entities.AutoInvestSettings
	)

	wg.Add(4)
	go func() { defer wg.Done(); balances, _ = h.allocationService.GetBalances(ctx, userID) }()
	go func() { defer wg.Done(); allocationMode, _ = h.allocationService.GetMode(ctx, userID) }()
	go func() { defer wg.Done(); portfolio, _ = h.investingService.GetPortfolio(ctx, userID) }()
	go func() {
		defer wg.Done()
		if h.autoInvestRepo != nil {
			autoInvest, _ = h.autoInvestRepo.GetUserSettings(ctx, userID)
		}
	}()
	wg.Wait()

	c.JSON(http.StatusOK, h.buildResponse(balances, portfolio, allocationMode, autoInvest, page, pageSize))
}

func (h *InvestmentStashHandlers) buildResponse(
	balances *entities.AllocationBalances,
	portfolio *entities.Portfolio,
	allocationMode *entities.SmartAllocationMode,
	autoInvest *entities.AutoInvestSettings,
	page, pageSize int,
) *InvestmentStashResponse {
	now := time.Now().UTC()

	resp := &InvestmentStashResponse{
		Balance: InvestmentBalanceInfo{
			Total:             "0.00",
			Stash:             "0.00",
			Invested:          "0.00",
			PendingAllocation: "0.00",
			Currency:          "USD",
			LastUpdated:       now,
		},
		Allocation: InvestmentAllocationInfo{
			Active:         false,
			SpendingRatio:  0.70,
			StashRatio:     0.30,
			TotalAllocated: "0.00",
		},
		Performance: PerformanceInfo{
			TotalGain: "0.00",
		},
		Positions: PositionListResponse{
			Page:     page,
			PageSize: pageSize,
			Items:    []PositionSummary{},
		},
		Stats: InvestmentStats{
			TotalDeposits:    "0.00",
			TotalWithdrawals: "0.00",
		},
		Links: InvestmentLinks{
			Self:           "/api/v1/account/investment-stash",
			Positions:      "/api/v1/investing/positions",
			Baskets:        "/api/v1/baskets",
			Performance:    "/api/v1/investing/performance",
			Withdraw:       "/api/v1/investing/withdraw",
			EditAllocation: "/api/v1/allocation",
			EditAutoInvest: "/api/v1/auto-invest",
		},
	}

	// Balance
	stashBalance := decimal.Zero
	if balances != nil {
		stashBalance = balances.StashBalance
		resp.Balance.Stash = stashBalance.StringFixed(2)
		resp.Balance.LastUpdated = balances.UpdatedAt
		resp.Allocation.Active = balances.ModeActive
	}

	// Allocation mode
	if allocationMode != nil {
		resp.Allocation.Active = allocationMode.Active
		spendRatio, _ := allocationMode.RatioSpending.Float64()
		stashRatio, _ := allocationMode.RatioStash.Float64()
		resp.Allocation.SpendingRatio = spendRatio
		resp.Allocation.StashRatio = stashRatio
		if allocationMode.ResumedAt != nil {
			ts := allocationMode.ResumedAt.Format(time.RFC3339)
			resp.Allocation.LastAllocationAt = &ts
		}
	}

	// Portfolio & positions
	if portfolio != nil && len(portfolio.Positions) > 0 {
		totalValue := decimal.Zero
		totalCostBasis := decimal.Zero
		positions := make([]PositionSummary, 0, len(portfolio.Positions))

		for _, pos := range portfolio.Positions {
			marketValue, _ := decimal.NewFromString(pos.MarketValue)
			avgCost, _ := decimal.NewFromString(pos.AvgPrice)
			qty, _ := decimal.NewFromString(pos.Quantity)
			costBasis := avgCost.Mul(qty)
			gain := marketValue.Sub(costBasis)
			gainPct := float64(0)
			if !costBasis.IsZero() {
				gainPct, _ = gain.Div(costBasis).Mul(decimal.NewFromInt(100)).Float64()
			}
			totalValue = totalValue.Add(marketValue)
			totalCostBasis = totalCostBasis.Add(costBasis)

			positions = append(positions, PositionSummary{
				ID:                pos.BasketID.String(),
				Symbol:            pos.BasketID.String()[:8],
				Name:              "Investment Position",
				Type:              "basket",
				Quantity:          pos.Quantity,
				CurrentPrice:      pos.AvgPrice,
				MarketValue:       pos.MarketValue,
				CostBasis:         costBasis.StringFixed(2),
				AvgCost:           pos.AvgPrice,
				UnrealizedGain:    gain.StringFixed(2),
				UnrealizedGainPct: gainPct,
			})
		}

		// Calculate portfolio weights
		for i := range positions {
			mv, _ := decimal.NewFromString(positions[i].MarketValue)
			if !totalValue.IsZero() {
				positions[i].PortfolioWeight, _ = mv.Div(totalValue).Mul(decimal.NewFromInt(100)).Float64()
			}
		}

		// Pagination
		totalCount := len(positions)
		offset := (page - 1) * pageSize
		end := offset + pageSize
		if offset > totalCount {
			offset = totalCount
		}
		if end > totalCount {
			end = totalCount
		}

		resp.Positions.Items = positions[offset:end]
		resp.Positions.TotalCount = totalCount
		resp.Positions.HasMore = end < totalCount
		resp.Stats.PositionCount = totalCount

		// Performance
		totalGain := totalValue.Sub(totalCostBasis)
		resp.Performance.TotalGain = totalGain.StringFixed(2)
		if !totalCostBasis.IsZero() {
			resp.Performance.TotalGainPercent, _ = totalGain.Div(totalCostBasis).Mul(decimal.NewFromInt(100)).Float64()
		}

		// Balance totals
		resp.Balance.Invested = totalValue.StringFixed(2)
		resp.Balance.Total = stashBalance.Add(totalValue).StringFixed(2)
	} else {
		resp.Balance.Total = stashBalance.StringFixed(2)
	}

	// Auto-invest
	if autoInvest != nil && autoInvest.Enabled {
		resp.AutoInvest = &AutoInvestInfo{
			IsEnabled:        true,
			TriggerThreshold: autoInvest.Threshold.StringFixed(2),
			Strategy:         "diversified",
		}
		if !autoInvest.UpdatedAt.IsZero() {
			ts := autoInvest.UpdatedAt.Format(time.RFC3339)
			resp.AutoInvest.LastTriggeredAt = &ts
		}
	}

	return resp
}
