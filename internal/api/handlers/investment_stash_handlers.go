package handlers

import (
	"context"
	"net/http"
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

// InvestmentStashHandlers handles the investment stash screen endpoint
type InvestmentStashHandlers struct {
	allocationService *allocation.Service
	investingService  *investing.Service
	logger            *zap.Logger
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

// GetInvestmentStash handles GET /api/v1/account/investment-stash
// @Summary Get investment stash screen data
// @Description Returns comprehensive investment data for the investment stash screen
// @Tags account
// @Produce json
// @Param page query int false "Page number" default(1)
// @Param page_size query int false "Page size" default(20)
// @Success 200 {object} InvestmentStashResponse
// @Failure 401 {object} entities.ErrorResponse
// @Failure 500 {object} entities.ErrorResponse
// @Security BearerAuth
// @Router /api/v1/account/investment-stash [get]
func (h *InvestmentStashHandlers) GetInvestmentStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
			Code:    "UNAUTHORIZED",
			Message: "User not authenticated",
		})
		return
	}

	// Parse pagination params
	page := 1
	pageSize := 20
	_ = page // For future pagination implementation
	_ = pageSize

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	// Data containers
	var (
		balances       *entities.AllocationBalances
		portfolio      *entities.Portfolio
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
			return
		}
		mu.Lock()
		portfolio = p
		mu.Unlock()
	}()

	wg.Wait()

	// Build response
	response := h.buildResponse(userID, balances, portfolio, allocationMode)

	c.JSON(http.StatusOK, response)
}

// buildResponse constructs the InvestmentStashResponse from fetched data
func (h *InvestmentStashHandlers) buildResponse(
	userID uuid.UUID,
	balances *entities.AllocationBalances,
	portfolio *entities.Portfolio,
	allocationMode *entities.SmartAllocationMode,
) *InvestmentStashResponse {
	response := &InvestmentStashResponse{
		TotalInvestmentBalance: "0.00",
		TotalCostBasis:         "0.00",
		TotalGain:              "0.00",
		TotalGainPercent:       "0.00",
		Positions: &PositionList{
			Items:      []PositionSummary{},
			TotalCount: 0,
			Page:       1,
			PageSize:   20,
		},
		AllocationInfo: InvestmentAllocationInfo{Active: false, StashRatio: "0.30"},
		Stats: InvestmentStats{
			TotalDeposits:    "0.00",
			TotalWithdrawals: "0.00",
		},
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
			weight := float64(0)
			if !totalValue.IsZero() {
				weight, _ = marketValue.Div(totalValue).Mul(decimal.NewFromInt(100)).Float64()
			}

			positions = append(positions, PositionSummary{
				Symbol:            pos.BasketID.String(),
				Quantity:          pos.Quantity,
				MarketValue:       pos.MarketValue,
				CostBasis:         costBasis.StringFixed(2),
				AvgCost:           pos.AvgPrice,
				UnrealizedGain:    gain.StringFixed(2),
				UnrealizedGainPct: gainPct.StringFixed(2),
				PortfolioWeight:   weight,
			})
		}
		response.Positions.Items = positions
		response.Positions.TotalCount = len(positions)
		response.Stats.PositionCount = len(positions)

		// Calculate totals
		totalCostBasis := decimal.Zero
		totalGain := decimal.Zero
		for _, pos := range positions {
			cost, _ := decimal.NewFromString(pos.CostBasis)
			gain, _ := decimal.NewFromString(pos.UnrealizedGain)
			totalCostBasis = totalCostBasis.Add(cost)
			totalGain = totalGain.Add(gain)
		}
		response.TotalCostBasis = totalCostBasis.StringFixed(2)
		response.TotalGain = totalGain.StringFixed(2)
		if !totalCostBasis.IsZero() {
			totalGainPct := totalGain.Div(totalCostBasis).Mul(decimal.NewFromInt(100))
			response.TotalGainPercent = totalGainPct.StringFixed(2)
		}

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

	return response
}
