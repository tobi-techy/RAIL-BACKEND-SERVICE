package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/text/language"
)

type mockInvestmentOrdersRepo struct {
	orders []*entities.InvestmentOrder
}

func (m *mockInvestmentOrdersRepo) GetByUserIDFiltered(
	_ context.Context,
	_ uuid.UUID,
	limit, offset int,
	side *entities.AlpacaOrderSide,
	status *entities.AlpacaOrderStatus,
) ([]*entities.InvestmentOrder, error) {
	filtered := make([]*entities.InvestmentOrder, 0, len(m.orders))
	for _, order := range m.orders {
		if side != nil && order.Side != *side {
			continue
		}
		if status != nil && order.Status != *status {
			continue
		}
		filtered = append(filtered, order)
	}

	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}

func (m *mockInvestmentOrdersRepo) CountByUserIDFiltered(
	_ context.Context,
	_ uuid.UUID,
	side *entities.AlpacaOrderSide,
	status *entities.AlpacaOrderStatus,
) (int, error) {
	count := 0
	for _, order := range m.orders {
		if side != nil && order.Side != *side {
			continue
		}
		if status != nil && order.Status != *status {
			continue
		}
		count++
	}
	return count, nil
}

func TestInvestmentTransactions_DefaultsToFilledTrades(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	now := time.Now().UTC()
	filledStatus := entities.AlpacaOrderStatusFilled
	filledPrice := decimal.NewFromFloat(10.25)

	repo := &mockInvestmentOrdersRepo{
		orders: []*entities.InvestmentOrder{
			{
				ID:             uuid.New(),
				UserID:         userID,
				Symbol:         "AAPL",
				Side:           entities.AlpacaOrderSideBuy,
				Status:         filledStatus,
				FilledQty:      decimal.NewFromInt(2),
				FilledAvgPrice: &filledPrice,
				CreatedAt:      now,
			},
			{
				ID:        uuid.New(),
				UserID:    userID,
				Symbol:    "MSFT",
				Side:      entities.AlpacaOrderSideSell,
				Status:    entities.AlpacaOrderStatusPendingNew,
				FilledQty: decimal.Zero,
				CreatedAt: now.Add(-time.Minute),
			},
		},
	}

	h := NewInvestmentStashHandlers(nil, nil, repo, nil, zap.NewNop())

	router := gin.New()
	router.GET("/api/v1/account/investment-stash/transactions", func(c *gin.Context) {
		c.Set("user_id", userID)
		h.GetInvestmentTransactions(c)
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp InvestmentTransactionsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "trade", resp.Items[0].Type)
	assert.Equal(t, string(entities.AlpacaOrderStatusFilled), resp.Items[0].Status)
}

func TestInvestmentTransactions_SideFilterWorks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	now := time.Now().UTC()
	allStatus := entities.AlpacaOrderStatusPendingNew

	repo := &mockInvestmentOrdersRepo{
		orders: []*entities.InvestmentOrder{
			{
				ID:        uuid.New(),
				UserID:    userID,
				Symbol:    "AAPL",
				Side:      entities.AlpacaOrderSideBuy,
				Status:    allStatus,
				FilledQty: decimal.Zero,
				CreatedAt: now,
			},
			{
				ID:        uuid.New(),
				UserID:    userID,
				Symbol:    "TSLA",
				Side:      entities.AlpacaOrderSideSell,
				Status:    allStatus,
				FilledQty: decimal.Zero,
				CreatedAt: now.Add(-time.Minute),
			},
		},
	}

	h := NewInvestmentStashHandlers(nil, nil, repo, nil, zap.NewNop())

	router := gin.New()
	router.GET("/api/v1/account/investment-stash/transactions", func(c *gin.Context) {
		c.Set("user_id", userID)
		h.GetInvestmentTransactions(c)
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions?side=sell&status=all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp InvestmentTransactionsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "sell", resp.Items[0].Side)
	assert.Equal(t, "trade", resp.Items[0].Type)

	reqBuy, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions?side=buy&status=all", nil)
	wBuy := httptest.NewRecorder()
	router.ServeHTTP(wBuy, reqBuy)

	require.Equal(t, http.StatusOK, wBuy.Code)

	var respBuy InvestmentTransactionsResponse
	require.NoError(t, json.Unmarshal(wBuy.Body.Bytes(), &respBuy))
	require.Len(t, respBuy.Items, 1)
	assert.Equal(t, "buy", respBuy.Items[0].Side)
	assert.Equal(t, "trade", respBuy.Items[0].Type)
}

func TestInvestmentTransactions_InvalidStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	h := NewInvestmentStashHandlers(nil, nil, &mockInvestmentOrdersRepo{}, nil, zap.NewNop())

	router := gin.New()
	router.GET("/api/v1/account/investment-stash/transactions", func(c *gin.Context) {
		c.Set("user_id", userID)
		h.GetInvestmentTransactions(c)
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions?status=processing", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBuildOverviewResponse_UsesTradePreviewOnly(t *testing.T) {
	h := &InvestmentStashHandlers{logger: zap.NewNop()}
	resp := h.buildOverviewResponse(
		nil,
		nil,
		[]*entities.InvestmentPosition{},
		nil,
		&InvestmentTransactionsResponse{
			Items: []InvestmentTradeTransaction{
				{ID: uuid.New().String(), Type: "trade", Symbol: "AAPL"},
			},
		},
		1,
		20,
		language.AmericanEnglish,
		nil,
		nil,
		nil,
		nil,
	)

	require.Len(t, resp.RecentTransactionsPreview, 1)
	assert.Equal(t, "trade", resp.RecentTransactionsPreview[0].Type)
}

func TestInvestmentTransactions_StatusAllIncludesPendingTrades(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	now := time.Now().UTC()
	filledStatus := entities.AlpacaOrderStatusFilled

	repo := &mockInvestmentOrdersRepo{
		orders: []*entities.InvestmentOrder{
			{
				ID:             uuid.New(),
				UserID:         userID,
				Symbol:         "AAPL",
				Side:           entities.AlpacaOrderSideBuy,
				Status:         filledStatus,
				FilledQty:      decimal.NewFromInt(1),
				FilledAvgPrice: ptrDecimal(decimal.NewFromFloat(120.5)),
				CreatedAt:      now,
			},
			{
				ID:        uuid.New(),
				UserID:    userID,
				Symbol:    "MSFT",
				Side:      entities.AlpacaOrderSideSell,
				Status:    entities.AlpacaOrderStatusPendingNew,
				FilledQty: decimal.Zero,
				CreatedAt: now.Add(-time.Minute),
			},
		},
	}

	h := NewInvestmentStashHandlers(nil, nil, repo, nil, zap.NewNop())

	router := gin.New()
	router.GET("/api/v1/account/investment-stash/transactions", func(c *gin.Context) {
		c.Set("user_id", userID)
		h.GetInvestmentTransactions(c)
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions?status=all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp InvestmentTransactionsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	assert.Equal(t, "trade", resp.Items[0].Type)
	assert.Equal(t, "trade", resp.Items[1].Type)
}

func TestInvestmentTransactions_EmptyDataset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()
	h := NewInvestmentStashHandlers(nil, nil, &mockInvestmentOrdersRepo{orders: []*entities.InvestmentOrder{}}, nil, zap.NewNop())

	router := gin.New()
	router.GET("/api/v1/account/investment-stash/transactions", func(c *gin.Context) {
		c.Set("user_id", userID)
		h.GetInvestmentTransactions(c)
	})

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/account/investment-stash/transactions?limit=20&offset=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp InvestmentTransactionsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(t, resp.Items)
	assert.Equal(t, 20, resp.Limit)
	assert.Equal(t, 0, resp.Offset)
	assert.False(t, resp.HasMore)
	assert.Nil(t, resp.NextOffset)
}

func TestBuildOverviewResponse_BackwardCompatibleFieldsRemain(t *testing.T) {
	h := &InvestmentStashHandlers{logger: zap.NewNop()}
	resp := h.buildOverviewResponse(
		nil,
		nil,
		[]*entities.InvestmentPosition{},
		nil,
		nil,
		1,
		20,
		language.AmericanEnglish,
		nil,
		nil,
		nil,
		nil,
	)

	payload, err := json.Marshal(resp)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(payload, &body))

	_, hasBalance := body["balance"]
	_, hasPerformance := body["performance"]
	_, hasPositions := body["positions"]
	_, hasStats := body["stats"]
	_, hasLinks := body["_links"]

	assert.True(t, hasBalance)
	assert.NotContains(t, body, "allocation")
	assert.True(t, hasPerformance)
	assert.True(t, hasPositions)
	assert.True(t, hasStats)
	assert.True(t, hasLinks)
}

func TestBuildOverviewResponse_BalanceIsInvestmentOnly(t *testing.T) {
	now := time.Now().UTC()

	h := &InvestmentStashHandlers{logger: zap.NewNop()}
	resp := h.buildOverviewResponse(
		&entities.AllocationBalances{
			StashBalance: decimal.NewFromInt(400),
			FiatExposure: decimal.NewFromInt(600),
			TotalBalance: decimal.NewFromInt(99999), // Includes non-investment balances; should be ignored.
			UpdatedAt:    now,
		},
		nil,
		[]*entities.InvestmentPosition{
			{
				ID:             uuid.New(),
				Symbol:         "AAPL",
				Qty:            decimal.NewFromInt(10),
				AvgEntryPrice:  decimal.NewFromInt(100),
				CurrentPrice:   decimal.NewFromInt(120),
				LastdayPrice:   decimal.NewFromInt(118),
				MarketValue:    decimal.NewFromInt(1200),
				CostBasis:      decimal.NewFromInt(1000),
				UnrealizedPL:   decimal.NewFromInt(200),
				UnrealizedPLPC: decimal.NewFromInt(20),
				ChangeToday:    decimal.RequireFromString("0.0169"),
			},
		},
		nil,
		nil,
		1,
		20,
		language.AmericanEnglish,
		nil,
		nil,
		nil,
		nil,
	)

	assert.Equal(t, "1200.00", resp.Balance.Invested)
	assert.Equal(t, "600.00", resp.Balance.LeftToInvest)
	assert.Equal(t, "1800.00", resp.Balance.Total)
	assert.Equal(t, "200.00", resp.Balance.NetPnL)
	assert.InDelta(t, 20.0, resp.Balance.NetPnLPercent, 0.0001)
	assert.NotEqual(t, "99999.00", resp.Balance.Total)
}

func TestBuildOverviewResponse_TopPerformersPreviewSortedByPerformance(t *testing.T) {
	h := &InvestmentStashHandlers{logger: zap.NewNop()}
	resp := h.buildOverviewResponse(
		nil,
		nil,
		[]*entities.InvestmentPosition{
			{
				ID:             uuid.New(),
				Symbol:         "AAA",
				Qty:            decimal.NewFromInt(1),
				AvgEntryPrice:  decimal.NewFromInt(100),
				CurrentPrice:   decimal.NewFromInt(103),
				LastdayPrice:   decimal.NewFromInt(101),
				MarketValue:    decimal.NewFromInt(103),
				CostBasis:      decimal.NewFromInt(100),
				UnrealizedPL:   decimal.NewFromInt(3),
				UnrealizedPLPC: decimal.RequireFromString("3"),
				ChangeToday:    decimal.RequireFromString("0.01"),
			},
			{
				ID:             uuid.New(),
				Symbol:         "BBB",
				Qty:            decimal.NewFromInt(1),
				AvgEntryPrice:  decimal.NewFromInt(100),
				CurrentPrice:   decimal.NewFromInt(112),
				LastdayPrice:   decimal.NewFromInt(111),
				MarketValue:    decimal.NewFromInt(112),
				CostBasis:      decimal.NewFromInt(100),
				UnrealizedPL:   decimal.NewFromInt(12),
				UnrealizedPLPC: decimal.RequireFromString("12"),
				ChangeToday:    decimal.RequireFromString("0.009"),
			},
			{
				ID:             uuid.New(),
				Symbol:         "CCC",
				Qty:            decimal.NewFromInt(1),
				AvgEntryPrice:  decimal.NewFromInt(100),
				CurrentPrice:   decimal.NewFromInt(98),
				LastdayPrice:   decimal.NewFromInt(99),
				MarketValue:    decimal.NewFromInt(98),
				CostBasis:      decimal.NewFromInt(100),
				UnrealizedPL:   decimal.NewFromInt(-2),
				UnrealizedPLPC: decimal.RequireFromString("-2"),
				ChangeToday:    decimal.RequireFromString("-0.01"),
			},
		},
		nil,
		nil,
		1,
		20,
		language.AmericanEnglish,
		nil,
		nil,
		nil,
		nil,
	)

	require.Len(t, resp.TopPerformersPreview, 3)
	assert.Equal(t, "BBB", resp.TopPerformersPreview[0].Symbol)
	assert.Equal(t, "AAA", resp.TopPerformersPreview[1].Symbol)
	assert.Equal(t, "CCC", resp.TopPerformersPreview[2].Symbol)
}

func ptrDecimal(v decimal.Decimal) *decimal.Decimal {
	return &v
}
