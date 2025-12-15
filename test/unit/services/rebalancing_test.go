package services_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"go.uber.org/zap"
)

type mockRebalancingConfigRepo struct {
	configs []*entities.RebalancingConfig
}

func (m *mockRebalancingConfigRepo) Create(ctx context.Context, c *entities.RebalancingConfig) error {
	m.configs = append(m.configs, c)
	return nil
}

func (m *mockRebalancingConfigRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.RebalancingConfig, error) {
	for _, c := range m.configs {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, nil
}

func (m *mockRebalancingConfigRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.RebalancingConfig, error) {
	var result []*entities.RebalancingConfig
	for _, c := range m.configs {
		if c.UserID == userID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *mockRebalancingConfigRepo) Update(ctx context.Context, c *entities.RebalancingConfig) error {
	for i, existing := range m.configs {
		if existing.ID == c.ID {
			m.configs[i] = c
			return nil
		}
	}
	return nil
}

func (m *mockRebalancingConfigRepo) Delete(ctx context.Context, id uuid.UUID) error {
	for i, c := range m.configs {
		if c.ID == id {
			m.configs = append(m.configs[:i], m.configs[i+1:]...)
			return nil
		}
	}
	return nil
}

type mockRebalancingPositionProvider struct {
	positions []*entities.InvestmentPosition
}

func (m *mockRebalancingPositionProvider) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error) {
	var result []*entities.InvestmentPosition
	for _, p := range m.positions {
		if p.UserID == userID {
			result = append(result, p)
		}
	}
	return result, nil
}

type mockQuoteProvider struct {
	quotes map[string]*entities.MarketQuote
}

func (m *mockQuoteProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	result := make(map[string]*entities.MarketQuote)
	for _, sym := range symbols {
		if q, ok := m.quotes[sym]; ok {
			result[sym] = q
		}
	}
	return result, nil
}

func TestRebalancingService_CreateConfig(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	configRepo := &mockRebalancingConfigRepo{}
	positionRepo := &mockRebalancingPositionProvider{}
	quoteProvider := &mockQuoteProvider{}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewRebalancingService(configRepo, positionRepo, quoteProvider, orderPlacer, logger)

	allocations := map[string]decimal.Decimal{
		"AAPL":  decimal.NewFromInt(40),
		"GOOGL": decimal.NewFromInt(30),
		"VTI":   decimal.NewFromInt(30),
	}

	config, err := svc.CreateRebalancingConfig(context.Background(), userID, "My Portfolio", allocations, decimal.NewFromInt(5), nil)
	require.NoError(t, err)
	require.NotNil(t, config)

	assert.Equal(t, userID, config.UserID)
	assert.Equal(t, "My Portfolio", config.Name)
	assert.Len(t, config.TargetAllocations, 3)
}

func TestRebalancingService_CreateConfig_InvalidAllocations(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	configRepo := &mockRebalancingConfigRepo{}
	positionRepo := &mockRebalancingPositionProvider{}
	quoteProvider := &mockQuoteProvider{}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewRebalancingService(configRepo, positionRepo, quoteProvider, orderPlacer, logger)

	// Allocations don't sum to 100
	allocations := map[string]decimal.Decimal{
		"AAPL":  decimal.NewFromInt(40),
		"GOOGL": decimal.NewFromInt(30),
	}

	_, err := svc.CreateRebalancingConfig(context.Background(), userID, "Bad Portfolio", allocations, decimal.NewFromInt(5), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "100%")
}

func TestRebalancingService_GeneratePlan(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	configID := uuid.New()
	config := &entities.RebalancingConfig{
		ID:     configID,
		UserID: userID,
		Name:   "Test Portfolio",
		TargetAllocations: map[string]decimal.Decimal{
			"AAPL":  decimal.NewFromInt(50),
			"GOOGL": decimal.NewFromInt(50),
		},
		ThresholdPct: decimal.NewFromInt(5),
		Status:       "active",
	}

	configRepo := &mockRebalancingConfigRepo{configs: []*entities.RebalancingConfig{config}}
	positionRepo := &mockRebalancingPositionProvider{
		positions: []*entities.InvestmentPosition{
			{UserID: userID, Symbol: "AAPL", MarketValue: decimal.NewFromInt(7000)},  // 70%
			{UserID: userID, Symbol: "GOOGL", MarketValue: decimal.NewFromInt(3000)}, // 30%
		},
	}
	quoteProvider := &mockQuoteProvider{
		quotes: map[string]*entities.MarketQuote{
			"AAPL":  {Symbol: "AAPL", Price: decimal.NewFromInt(150)},
			"GOOGL": {Symbol: "GOOGL", Price: decimal.NewFromInt(140)},
		},
	}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewRebalancingService(configRepo, positionRepo, quoteProvider, orderPlacer, logger)

	plan, err := svc.GenerateRebalancingPlan(context.Background(), userID, configID)
	require.NoError(t, err)
	require.NotNil(t, plan)

	// Should have trades to rebalance from 70/30 to 50/50
	assert.True(t, len(plan.Trades) > 0)
}

func TestRebalancingService_CheckDrift(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	configID := uuid.New()
	config := &entities.RebalancingConfig{
		ID:     configID,
		UserID: userID,
		TargetAllocations: map[string]decimal.Decimal{
			"AAPL": decimal.NewFromInt(100),
		},
		ThresholdPct: decimal.NewFromInt(5),
		Status:       "active",
	}

	configRepo := &mockRebalancingConfigRepo{configs: []*entities.RebalancingConfig{config}}
	positionRepo := &mockRebalancingPositionProvider{
		positions: []*entities.InvestmentPosition{
			{UserID: userID, Symbol: "AAPL", MarketValue: decimal.NewFromInt(10000)},
		},
	}
	quoteProvider := &mockQuoteProvider{}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewRebalancingService(configRepo, positionRepo, quoteProvider, orderPlacer, logger)

	needsRebalance, maxDrift, err := svc.CheckDrift(context.Background(), userID, configID)
	require.NoError(t, err)
	assert.False(t, needsRebalance) // Already at 100% AAPL
	assert.True(t, maxDrift.IsZero() || maxDrift.LessThanOrEqual(decimal.NewFromInt(5)))
}
