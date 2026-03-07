package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/allocation"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

var errInvestmentDependencyUnavailable = errors.New("investment dependency unavailable")

const (
	defaultInvestmentPageSize = 20
	maxInvestmentPageSize     = 50
	defaultTradesLimit        = 20
	maxTradesLimit            = 100
)

var validPerformancePeriods = map[string]struct{}{
	"1D":  {},
	"1W":  {},
	"1M":  {},
	"3M":  {},
	"6M":  {},
	"1Y":  {},
	"ALL": {},
}

// AutoInvestRepository interface for auto-invest settings
type AutoInvestRepository interface {
	GetUserSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error)
}

// InvestmentPositionsRepository provides position access for the investment dashboard.
type InvestmentPositionsRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error)
}

// InvestmentOrdersRepository provides order access for trade-focused transaction feeds.
type InvestmentOrdersRepository interface {
	GetByUserIDFiltered(
		ctx context.Context,
		userID uuid.UUID,
		limit, offset int,
		side *entities.AlpacaOrderSide,
		status *entities.AlpacaOrderStatus,
	) ([]*entities.InvestmentOrder, error)
	CountByUserIDFiltered(
		ctx context.Context,
		userID uuid.UUID,
		side *entities.AlpacaOrderSide,
		status *entities.AlpacaOrderStatus,
	) (int, error)
}

// PortfolioAnalyticsProvider provides portfolio history for performance views.
type PortfolioAnalyticsProvider interface {
	GetPortfolioHistory(ctx context.Context, userID uuid.UUID, period string) (*entities.PortfolioHistory, error)
}

// StrategyProvider resolves the investment strategy for a user.
type StrategyProvider interface {
	GetStrategy(ctx context.Context, userID uuid.UUID) (*strategy.StrategyResult, error)
}

// PortfolioSyncer triggers a live sync of positions from Alpaca.
type PortfolioSyncer interface {
	SyncPositions(ctx context.Context, userID uuid.UUID) error
}

// InvestmentStashHandlers handles investment stash endpoints.
type InvestmentStashHandlers struct {
	allocationService *allocation.Service
	positionsRepo     InvestmentPositionsRepository
	ordersRepo        InvestmentOrdersRepository
	analyticsService  PortfolioAnalyticsProvider
	autoInvestRepo    AutoInvestRepository
	strategyProvider  StrategyProvider
	portfolioSyncer   PortfolioSyncer
	logger            *zap.Logger
}

// NewInvestmentStashHandlers creates new investment stash handlers.
func NewInvestmentStashHandlers(
	allocationService *allocation.Service,
	positionsRepo InvestmentPositionsRepository,
	ordersRepo InvestmentOrdersRepository,
	analyticsService PortfolioAnalyticsProvider,
	logger *zap.Logger,
) *InvestmentStashHandlers {
	return &InvestmentStashHandlers{
		allocationService: allocationService,
		positionsRepo:     positionsRepo,
		ordersRepo:        ordersRepo,
		analyticsService:  analyticsService,
		logger:            logger,
	}
}

// SetAutoInvestRepository sets the auto-invest repository.
func (h *InvestmentStashHandlers) SetAutoInvestRepository(repo AutoInvestRepository) {
	h.autoInvestRepo = repo
}

// SetStrategyProvider sets the strategy provider for investment rule display.
func (h *InvestmentStashHandlers) SetStrategyProvider(p StrategyProvider) {
	h.strategyProvider = p
}

// SetPortfolioSyncer sets the portfolio syncer for on-demand position refresh.
func (h *InvestmentStashHandlers) SetPortfolioSyncer(s PortfolioSyncer) {
	h.portfolioSyncer = s
}

// GetInvestmentStash handles GET /api/v1/account/investment-stash.
func (h *InvestmentStashHandlers) GetInvestmentStash(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	page, pageSize := parsePageParams(c, defaultInvestmentPageSize, maxInvestmentPageSize)
	locale := localeFromRequest(c)

	var (
		wg sync.WaitGroup

		balances        *entities.AllocationBalances
		autoInvest      *entities.AutoInvestSettings
		positions       []*entities.InvestmentPosition
		strategyResult  *strategy.StrategyResult

		balancesErr   error
		autoInvestErr error
		positionsErr  error
	)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if h.allocationService == nil {
			balancesErr = errInvestmentDependencyUnavailable
			return
		}
		balances, balancesErr = h.allocationService.GetBalances(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		positions, positionsErr = h.getPositions(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		if h.autoInvestRepo == nil {
			return
		}
		autoInvest, autoInvestErr = h.autoInvestRepo.GetUserSettings(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		if h.strategyProvider == nil {
			return
		}
		strategyResult, _ = h.strategyProvider.GetStrategy(ctx, userID)
	}()
	wg.Wait()

	if errors.Is(balancesErr, errInvestmentDependencyUnavailable) || errors.Is(positionsErr, errInvestmentDependencyUnavailable) {
		h.logger.Error("Investment stash dependencies unavailable", zap.String("user_id", userID.String()))
		c.JSON(http.StatusServiceUnavailable, entities.ErrorResponse{
			Code:    "SERVICE_UNAVAILABLE",
			Message: "Investment service temporarily unavailable",
		})
		return
	}

	if balancesErr != nil {
		h.logger.Warn("Failed to load investment balances", zap.String("user_id", userID.String()), zap.Error(balancesErr))
	}
	if autoInvestErr != nil {
		h.logger.Warn("Failed to load auto-invest settings", zap.String("user_id", userID.String()), zap.Error(autoInvestErr))
	}
	if positionsErr != nil {
		h.logger.Warn("Failed to load investment positions", zap.String("user_id", userID.String()), zap.Error(positionsErr))
		positions = []*entities.InvestmentPosition{}
	}

	response := h.buildOverviewResponse(
		balances,
		autoInvest,
		positions,
		strategyResult,
		page,
		pageSize,
		locale,
		balancesErr,
		positionsErr,
	)

	c.JSON(http.StatusOK, response)
}

// GetInvestmentPositions handles GET /api/v1/account/investment-stash/positions.
func (h *InvestmentStashHandlers) GetInvestmentPositions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	page, pageSize := parsePageParams(c, defaultInvestmentPageSize, maxInvestmentPageSize)
	locale := localeFromRequest(c)

	positions, err := h.getPositions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to load investment positions", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "POSITIONS_ERROR", Message: "Failed to retrieve investment positions"})
		return
	}

	details, _, _, _ := buildPositionViews(positions, locale)
	paged, totalCount, hasMore := paginatePositions(details, page, pageSize)

	c.JSON(http.StatusOK, InvestmentPositionsResponse{
		Page:       page,
		PageSize:   pageSize,
		TotalCount: totalCount,
		HasMore:    hasMore,
		Items:      paged,
	})
}

// GetInvestmentDistribution handles GET /api/v1/account/investment-stash/distribution.
func (h *InvestmentStashHandlers) GetInvestmentDistribution(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	limit := common.ParseIntParam(c, "limit", 10)
	if limit < 1 {
		limit = 10
	}
	if limit > maxInvestmentPageSize {
		limit = maxInvestmentPageSize
	}
	locale := localeFromRequest(c)

	positions, err := h.getPositions(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to load investment distribution", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "DISTRIBUTION_ERROR", Message: "Failed to retrieve investment distribution"})
		return
	}

	details, _, _, _ := buildPositionViews(positions, locale)
	allItems := buildDistributionItems(details)
	top1, top3, hhi := calculateDistributionMetrics(allItems)

	items := allItems
	if len(allItems) > limit {
		items = allItems[:limit]
	}

	c.JSON(http.StatusOK, InvestmentDistributionResponse{
		Items:             items,
		Top1WeightPercent: top1,
		Top3WeightPercent: top3,
		HHI:               hhi,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
	})
}

// GetInvestmentTransactions handles GET /api/v1/account/investment-stash/transactions.
func (h *InvestmentStashHandlers) GetInvestmentTransactions(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	limit := common.ParseIntParam(c, "limit", defaultTradesLimit)
	if limit < 1 {
		limit = defaultTradesLimit
	}
	if limit > maxTradesLimit {
		limit = maxTradesLimit
	}

	offset := common.ParseIntParam(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}

	sideParam := strings.ToLower(strings.TrimSpace(c.DefaultQuery("side", "all")))
	statusParam := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", "filled")))

	sideFilter, err := parseSideFilter(sideParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_SIDE",
			Message: "Invalid side filter. Valid values: all, buy, sell",
		})
		return
	}

	statusFilter, err := parseStatusFilter(statusParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_STATUS",
			Message: "Invalid status filter. Valid values: filled, all",
		})
		return
	}

	locale := localeFromRequest(c)
	response, err := h.fetchTradeTransactions(ctx, userID, limit, offset, sideFilter, statusFilter, locale)
	if err != nil {
		h.logger.Error("Failed to load investment transactions", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "TRANSACTIONS_ERROR", Message: "Failed to retrieve investment transactions"})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetInvestmentPerformance handles GET /api/v1/account/investment-stash/performance.
func (h *InvestmentStashHandlers) GetInvestmentPerformance(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, entities.ErrorResponse{Code: "UNAUTHORIZED", Message: "User not authenticated"})
		return
	}

	period := strings.ToUpper(strings.TrimSpace(c.DefaultQuery("period", "1W")))
	if _, ok := validPerformancePeriods[period]; !ok {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_PERIOD",
			Message: "Invalid period. Valid values: 1D, 1W, 1M, 3M, 6M, 1Y, ALL",
		})
		return
	}

	locale := localeFromRequest(c)
	resp, err := h.buildPerformanceResponse(ctx, userID, period, locale)
	if err != nil {
		h.logger.Error("Failed to load investment performance", zap.String("user_id", userID.String()), zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{Code: "PERFORMANCE_ERROR", Message: "Failed to retrieve investment performance"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *InvestmentStashHandlers) buildOverviewResponse(
	balances *entities.AllocationBalances,
	autoInvest *entities.AutoInvestSettings,
	positions []*entities.InvestmentPosition,
	strategyResult *strategy.StrategyResult,
	page, pageSize int,
	locale language.Tag,
	balancesErr error,
	positionsErr error,
) *InvestmentStashResponse {
	now := time.Now().UTC()

	resp := &InvestmentStashResponse{
		Balance: InvestmentBalanceInfo{
			Total:             "0.00",
			Stash:             "0.00",
			Invested:          "0.00",
			PendingAllocation: "0.00",
			LeftToInvest:      "0.00",
			NetPnL:            "0.00",
			NetPnLPercent:     0,
			Currency:          "USD",
			LastUpdated:       now,
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
		HoldingsPreview:           []InvestmentPositionDetail{},
		TopPerformersPreview:      []InvestmentPositionDetail{},
		DistributionPreview:       []InvestmentDistributionItem{},
		RecentTransactionsPreview: []InvestmentTradeTransaction{},
		DataHealth: InvestmentDataHealth{
			Positions:    healthStatusFromError(positionsErr),
			Distribution: healthStatusFromError(positionsErr),
			Transactions: "ok",
			Performance:  "ok",
		},
		Links: InvestmentLinks{
			Self:           "/api/v1/account/investment-stash",
			Positions:      "/api/v1/account/investment-stash/positions",
			Distribution:   "/api/v1/account/investment-stash/distribution",
			Transactions:   "/api/v1/account/investment-stash/transactions",
			Baskets:        "/api/v1/baskets",
			Performance:    "/api/v1/account/investment-stash/performance",
			Withdraw:       "/api/v1/withdrawals",
			EditAllocation: "/api/v1/allocation",
			EditAutoInvest: "/api/v1/auto-invest",
		},
	}

	if balances != nil {
		resp.Balance.Stash = balances.StashBalance.StringFixed(2)
		resp.Balance.LastUpdated = balances.UpdatedAt
	}

	details, legacyPositions, totalValue, totalCostBasis := buildPositionViews(positions, locale)
	paged, totalCount, hasMore := paginateLegacyPositions(legacyPositions, page, pageSize)

	resp.Positions.Items = paged
	resp.Positions.TotalCount = totalCount
	resp.Positions.HasMore = hasMore
	resp.Stats.PositionCount = totalCount
	resp.Balance.Invested = totalValue.StringFixed(2)

	totalGain := totalValue.Sub(totalCostBasis)
	resp.Performance.TotalGain = totalGain.StringFixed(2)
	resp.Balance.NetPnL = totalGain.StringFixed(2)
	if !totalCostBasis.IsZero() {
		resp.Performance.TotalGainPercent, _ = totalGain.Div(totalCostBasis).Mul(decimal.NewFromInt(100)).Float64()
		resp.Balance.NetPnLPercent = resp.Performance.TotalGainPercent
	}

	buyingPower := decimal.Zero
	if balances != nil {
		buyingPower = balances.FiatExposure
	}
	leftToInvest := buyingPower
	investmentTotal := totalValue.Add(leftToInvest)
	resp.Balance.LeftToInvest = leftToInvest.StringFixed(2)
	resp.Balance.Total = investmentTotal.StringFixed(2)

	if len(details) > 3 {
		resp.HoldingsPreview = details[:3]
	} else {
		resp.HoldingsPreview = details
	}
	resp.TopPerformersPreview = buildTopPerformerPreview(details, 3)

	distribution := buildDistributionItems(details)
	if len(distribution) > 3 {
		resp.DistributionPreview = distribution[:3]
	} else {
		resp.DistributionPreview = distribution
	}

	resp.Summary = &InvestmentSummary{
		TotalBalance:      moneyValue(investmentTotal, locale),
		InvestedValue:     moneyValue(totalValue, locale),
		BuyingPower:       moneyValue(buyingPower, locale),
		DayChange:         moneyValue(decimal.Zero, locale),
		DayChangePercent:  0,
		WeekChange:        moneyValue(decimal.Zero, locale),
		WeekChangePercent: 0,
		Currency:          "USD",
		LastUpdated:       now.Format(time.RFC3339),
	}

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

	if balancesErr != nil {
		resp.Summary.BuyingPower = moneyValue(decimal.Zero, locale)
	}

	if strategyResult != nil {
		stockPct, _ := strategyResult.StockAllocation.Float64()
		bondPct, _ := strategyResult.BondAllocation.Float64()
		resp.InvestmentRule = &InvestmentRule{
			StrategyName:    strategyResult.StrategyName,
			Description:     buildStrategyDescription(strategyResult),
			StockAllocation: stockPct,
			BondAllocation:  bondPct,
			RiskLevel:       deriveRiskLevel(stockPct),
			RiskLabel:       deriveRiskLabel(stockPct),
			AgeUsed:         strategyResult.AgeUsed,
		}
	}

	return resp
}

func (h *InvestmentStashHandlers) getPositions(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error) {
	if h.positionsRepo == nil {
		return nil, errInvestmentDependencyUnavailable
	}

	positions, err := h.positionsRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// If no local positions, trigger a live sync from Alpaca then re-fetch.
	if len(positions) == 0 && h.portfolioSyncer != nil {
		if syncErr := h.portfolioSyncer.SyncPositions(ctx, userID); syncErr != nil {
			h.logger.Warn("Failed to sync positions from Alpaca", zap.String("user_id", userID.String()), zap.Error(syncErr))
		} else {
			positions, err = h.positionsRepo.GetByUserID(ctx, userID)
			if err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(positions, func(i, j int) bool {
		return positions[i].MarketValue.GreaterThan(positions[j].MarketValue)
	})

	return positions, nil
}

func (h *InvestmentStashHandlers) fetchTradeTransactions(
	ctx context.Context,
	userID uuid.UUID,
	limit, offset int,
	side *entities.AlpacaOrderSide,
	status *entities.AlpacaOrderStatus,
	locale language.Tag,
) (*InvestmentTransactionsResponse, error) {
	if h.ordersRepo == nil {
		return nil, errInvestmentDependencyUnavailable
	}

	total, err := h.ordersRepo.CountByUserIDFiltered(ctx, userID, side, status)
	if err != nil {
		return nil, err
	}

	orders, err := h.ordersRepo.GetByUserIDFiltered(ctx, userID, limit, offset, side, status)
	if err != nil {
		return nil, err
	}

	items := make([]InvestmentTradeTransaction, 0, len(orders))
	for _, order := range orders {
		items = append(items, mapOrderToTradeTransaction(order, locale))
	}

	hasMore := offset+len(items) < total
	var nextOffset *int
	if hasMore {
		n := offset + len(items)
		nextOffset = &n
	}

	return &InvestmentTransactionsResponse{
		Items:      items,
		Limit:      limit,
		Offset:     offset,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

func (h *InvestmentStashHandlers) buildPerformanceResponse(
	ctx context.Context,
	userID uuid.UUID,
	period string,
	locale language.Tag,
) (*InvestmentPerformanceResponse, error) {
	if h.analyticsService == nil {
		return nil, errInvestmentDependencyUnavailable
	}

	history, err := h.analyticsService.GetPortfolioHistory(ctx, userID, period)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return &InvestmentPerformanceResponse{
			Period:        period,
			Return:        moneyValue(decimal.Zero, locale),
			ReturnPercent: 0,
			Points:        []InvestmentPerformancePoint{},
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	points := make([]InvestmentPerformancePoint, 0, len(history.DataPoints))
	for _, p := range history.DataPoints {
		points = append(points, InvestmentPerformancePoint{
			Date:  p.Date.UTC().Format(time.RFC3339),
			Value: moneyValue(p.Value, locale),
		})
	}

	returnPercent, _ := history.ChangePct.Float64()
	return &InvestmentPerformanceResponse{
		Period:        history.Period,
		Return:        moneyValue(history.Change, locale),
		ReturnPercent: returnPercent,
		Points:        points,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func mapOrderToTradeTransaction(order *entities.InvestmentOrder, locale language.Tag) InvestmentTradeTransaction {
	amount := deriveOrderAmount(order)

	var qty string
	switch {
	case order.FilledQty.GreaterThan(decimal.Zero):
		qty = order.FilledQty.String()
	case order.Qty != nil:
		qty = order.Qty.String()
	default:
		qty = "0"
	}

	var price *MoneyValue
	if order.FilledAvgPrice != nil {
		p := moneyValue(*order.FilledAvgPrice, locale)
		price = &p
	} else if order.LimitPrice != nil {
		p := moneyValue(*order.LimitPrice, locale)
		price = &p
	}

	occurredAt := order.CreatedAt
	if order.FilledAt != nil {
		occurredAt = *order.FilledAt
	} else if order.SubmittedAt != nil {
		occurredAt = *order.SubmittedAt
	}

	return InvestmentTradeTransaction{
		ID:         order.ID.String(),
		Type:       "trade",
		Side:       string(order.Side),
		Status:     string(order.Status),
		Symbol:     order.Symbol,
		Quantity:   qty,
		Price:      price,
		Amount:     moneyValue(amount, locale),
		OccurredAt: occurredAt.UTC().Format(time.RFC3339),
	}
}

func deriveOrderAmount(order *entities.InvestmentOrder) decimal.Decimal {
	if order == nil {
		return decimal.Zero
	}
	if order.Notional != nil && !order.Notional.IsZero() {
		return order.Notional.Abs()
	}
	if order.FilledAvgPrice != nil && order.FilledQty.GreaterThan(decimal.Zero) {
		return order.FilledQty.Mul(*order.FilledAvgPrice).Abs()
	}
	if order.Qty != nil && order.LimitPrice != nil {
		return order.Qty.Mul(*order.LimitPrice).Abs()
	}
	return decimal.Zero
}

func deriveDayChange(points []InvestmentPerformancePoint) (decimal.Decimal, float64) {
	if len(points) < 2 {
		return decimal.Zero, 0
	}

	latest, err := decimal.NewFromString(points[len(points)-1].Value.Raw)
	if err != nil {
		return decimal.Zero, 0
	}
	prior, err := decimal.NewFromString(points[len(points)-2].Value.Raw)
	if err != nil {
		return decimal.Zero, 0
	}
	change := latest.Sub(prior)
	if prior.IsZero() {
		return change, 0
	}

	pct, _ := change.Div(prior).Mul(decimal.NewFromInt(100)).Float64()
	return change, pct
}

func buildPositionViews(
	positions []*entities.InvestmentPosition,
	locale language.Tag,
) ([]InvestmentPositionDetail, []PositionSummary, decimal.Decimal, decimal.Decimal) {
	if len(positions) == 0 {
		return []InvestmentPositionDetail{}, []PositionSummary{}, decimal.Zero, decimal.Zero
	}

	totalValue := decimal.Zero
	totalCostBasis := decimal.Zero
	for _, pos := range positions {
		totalValue = totalValue.Add(pos.MarketValue)
		totalCostBasis = totalCostBasis.Add(pos.CostBasis)
	}

	details := make([]InvestmentPositionDetail, 0, len(positions))
	legacy := make([]PositionSummary, 0, len(positions))

	for _, pos := range positions {
		weight := 0.0
		if totalValue.GreaterThan(decimal.Zero) {
			weight, _ = pos.MarketValue.Div(totalValue).Mul(decimal.NewFromInt(100)).Float64()
		}

		unrealizedPct, _ := pos.UnrealizedPLPC.Float64()
		dayChange := pos.CurrentPrice.Sub(pos.LastdayPrice).Mul(pos.Qty)
		dayChangePct, _ := pos.ChangeToday.Mul(decimal.NewFromInt(100)).Float64()

		details = append(details, InvestmentPositionDetail{
			ID:                   pos.ID.String(),
			Symbol:               pos.Symbol,
			Name:                 pos.Symbol,
			Quantity:             pos.Qty.String(),
			AvgEntryPrice:        moneyValue(pos.AvgEntryPrice, locale),
			CurrentPrice:         moneyValue(pos.CurrentPrice, locale),
			MarketValue:          moneyValue(pos.MarketValue, locale),
			CostBasis:            moneyValue(pos.CostBasis, locale),
			UnrealizedPnL:        moneyValue(pos.UnrealizedPL, locale),
			UnrealizedPnLPercent: unrealizedPct,
			PortfolioWeight:      weight,
		})

		legacy = append(legacy, PositionSummary{
			ID:                pos.ID.String(),
			Symbol:            pos.Symbol,
			Name:              pos.Symbol,
			Type:              "asset",
			Quantity:          pos.Qty.String(),
			CurrentPrice:      pos.CurrentPrice.String(),
			MarketValue:       pos.MarketValue.String(),
			CostBasis:         pos.CostBasis.String(),
			AvgCost:           pos.AvgEntryPrice.String(),
			UnrealizedGain:    pos.UnrealizedPL.String(),
			UnrealizedGainPct: unrealizedPct,
			DayChange:         dayChange.StringFixed(2),
			DayChangePct:      dayChangePct,
			PortfolioWeight:   weight,
		})
	}

	return details, legacy, totalValue, totalCostBasis
}

func buildDistributionItems(details []InvestmentPositionDetail) []InvestmentDistributionItem {
	items := make([]InvestmentDistributionItem, 0, len(details))
	for _, p := range details {
		items = append(items, InvestmentDistributionItem{
			Symbol:        p.Symbol,
			Name:          p.Name,
			WeightPercent: p.PortfolioWeight,
			Value:         p.MarketValue,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].WeightPercent > items[j].WeightPercent
	})

	return items
}

func buildTopPerformerPreview(details []InvestmentPositionDetail, limit int) []InvestmentPositionDetail {
	if len(details) == 0 || limit <= 0 {
		return []InvestmentPositionDetail{}
	}

	cloned := append([]InvestmentPositionDetail(nil), details...)
	sort.Slice(cloned, func(i, j int) bool {
		if cloned[i].UnrealizedPnLPercent == cloned[j].UnrealizedPnLPercent {
			iv, _ := decimal.NewFromString(cloned[i].MarketValue.Raw)
			jv, _ := decimal.NewFromString(cloned[j].MarketValue.Raw)
			return iv.GreaterThan(jv)
		}
		return cloned[i].UnrealizedPnLPercent > cloned[j].UnrealizedPnLPercent
	})

	if len(cloned) > limit {
		return cloned[:limit]
	}
	return cloned
}

func calculateDistributionMetrics(items []InvestmentDistributionItem) (float64, float64, float64) {
	if len(items) == 0 {
		return 0, 0, 0
	}

	top1 := items[0].WeightPercent
	top3 := 0.0
	hhi := 0.0

	for idx, item := range items {
		hhi += math.Pow(item.WeightPercent, 2)
		if idx < 3 {
			top3 += item.WeightPercent
		}
	}

	return top1, top3, hhi
}

func paginatePositions(items []InvestmentPositionDetail, page, pageSize int) ([]InvestmentPositionDetail, int, bool) {
	totalCount := len(items)
	offset := (page - 1) * pageSize
	if offset > totalCount {
		offset = totalCount
	}
	end := offset + pageSize
	if end > totalCount {
		end = totalCount
	}

	return items[offset:end], totalCount, end < totalCount
}

func paginateLegacyPositions(items []PositionSummary, page, pageSize int) ([]PositionSummary, int, bool) {
	totalCount := len(items)
	offset := (page - 1) * pageSize
	if offset > totalCount {
		offset = totalCount
	}
	end := offset + pageSize
	if end > totalCount {
		end = totalCount
	}

	return items[offset:end], totalCount, end < totalCount
}

func parsePageParams(c *gin.Context, defaultPageSize, maxPageSize int) (int, int) {
	page := common.ParseIntParam(c, "page", 1)
	if page < 1 {
		page = 1
	}

	pageSize := common.ParseIntParam(c, "page_size", defaultPageSize)
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	return page, pageSize
}

func parseSideFilter(side string) (*entities.AlpacaOrderSide, error) {
	switch side {
	case "", "all":
		return nil, nil
	case "buy":
		v := entities.AlpacaOrderSideBuy
		return &v, nil
	case "sell":
		v := entities.AlpacaOrderSideSell
		return &v, nil
	default:
		return nil, errors.New("invalid side")
	}
}

func parseStatusFilter(status string) (*entities.AlpacaOrderStatus, error) {
	switch status {
	case "", "filled":
		v := entities.AlpacaOrderStatusFilled
		return &v, nil
	case "all":
		return nil, nil
	default:
		return nil, errors.New("invalid status")
	}
}

func buildStrategyDescription(r *strategy.StrategyResult) string {
	stock, _ := r.StockAllocation.Float64()
	bond, _ := r.BondAllocation.Float64()
	return fmt.Sprintf(
		"Your portfolio targets %.0f%% equities and %.0f%% bonds, automatically rebalanced based on your age and risk profile using the 120-age formula.",
		stock, bond,
	)
}

func deriveRiskLevel(stockPct float64) int {
	switch {
	case stockPct >= 85:
		return 5
	case stockPct >= 70:
		return 4
	case stockPct >= 55:
		return 3
	case stockPct >= 40:
		return 2
	default:
		return 1
	}
}

func deriveRiskLabel(stockPct float64) string {
	switch {
	case stockPct >= 85:
		return "High Growth"
	case stockPct >= 70:
		return "Growth"
	case stockPct >= 55:
		return "Balanced"
	case stockPct >= 40:
		return "Conservative"
	default:
		return "Capital Preservation"
	}
}

func healthStatusFromError(err error) string {
	if err == nil {
		return "ok"
	}
	return "degraded"
}

func localeFromRequest(c *gin.Context) language.Tag {
	acceptLanguage := strings.TrimSpace(c.GetHeader("Accept-Language"))
	if acceptLanguage == "" {
		return language.AmericanEnglish
	}

	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return language.AmericanEnglish
	}

	return tags[0]
}

func moneyValue(amount decimal.Decimal, locale language.Tag) MoneyValue {
	return MoneyValue{
		Raw:       amount.String(),
		Formatted: formatUSDByLocale(amount, locale),
	}
}

func formatUSDByLocale(amount decimal.Decimal, locale language.Tag) string {
	printer := message.NewPrinter(locale)
	abs := amount.Abs().InexactFloat64()
	formattedNumber := printer.Sprintf(
		"%v",
		number.Decimal(abs, number.MinFractionDigits(2), number.MaxFractionDigits(2)),
	)

	if amount.IsNegative() {
		return "-$" + formattedNumber
	}
	return "$" + formattedNumber
}
