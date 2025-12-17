package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/analytics"
	"go.uber.org/zap"
)

// Mock repositories
type mockSnapshotRepo struct {
	snapshots []*entities.PortfolioSnapshot
}

func (m *mockSnapshotRepo) Create(ctx context.Context, s *entities.PortfolioSnapshot) error {
	m.snapshots = append(m.snapshots, s)
	return nil
}

func (m *mockSnapshotRepo) GetByUserIDAndDateRange(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]*entities.PortfolioSnapshot, error) {
	var result []*entities.PortfolioSnapshot
	for _, s := range m.snapshots {
		if s.UserID == userID && !s.SnapshotDate.Before(start) && !s.SnapshotDate.After(end) {
			result = append(result, s)
		}
	}
	return result, nil
}

func (m *mockSnapshotRepo) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.PortfolioSnapshot, error) {
	var latest *entities.PortfolioSnapshot
	for _, s := range m.snapshots {
		if s.UserID == userID && (latest == nil || s.SnapshotDate.After(latest.SnapshotDate)) {
			latest = s
		}
	}
	return latest, nil
}

func (m *mockSnapshotRepo) GetByDate(ctx context.Context, userID uuid.UUID, date time.Time) (*entities.PortfolioSnapshot, error) {
	for _, s := range m.snapshots {
		if s.UserID == userID && s.SnapshotDate.Equal(date) {
			return s, nil
		}
	}
	return nil, nil
}

type mockPositionProvider struct {
	positions []*entities.InvestmentPosition
}

func (m *mockPositionProvider) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentPosition, error) {
	var result []*entities.InvestmentPosition
	for _, p := range m.positions {
		if p.UserID == userID {
			result = append(result, p)
		}
	}
	return result, nil
}

type mockAccountProvider struct {
	account *entities.AlpacaAccount
}

func (m *mockAccountProvider) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error) {
	if m.account != nil && m.account.UserID == userID {
		return m.account, nil
	}
	return nil, nil
}

func TestPortfolioAnalyticsService_TakeSnapshot(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	snapshotRepo := &mockSnapshotRepo{}
	positionRepo := &mockPositionProvider{
		positions: []*entities.InvestmentPosition{
			{UserID: userID, Symbol: "AAPL", MarketValue: decimal.NewFromInt(1000), CostBasis: decimal.NewFromInt(800), LastdayPrice: decimal.NewFromInt(95), Qty: decimal.NewFromInt(10)},
			{UserID: userID, Symbol: "GOOGL", MarketValue: decimal.NewFromInt(2000), CostBasis: decimal.NewFromInt(1800), LastdayPrice: decimal.NewFromInt(190), Qty: decimal.NewFromInt(10)},
		},
	}
	accountRepo := &mockAccountProvider{
		account: &entities.AlpacaAccount{UserID: userID, Cash: decimal.NewFromInt(500)},
	}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	err := svc.TakeSnapshot(context.Background(), userID)
	require.NoError(t, err)
	require.Len(t, snapshotRepo.snapshots, 1)

	snapshot := snapshotRepo.snapshots[0]
	assert.Equal(t, userID, snapshot.UserID)
	assert.Equal(t, decimal.NewFromInt(3500), snapshot.TotalValue) // 1000 + 2000 + 500
	assert.Equal(t, decimal.NewFromInt(500), snapshot.CashValue)
	assert.Equal(t, decimal.NewFromInt(3000), snapshot.InvestedValue)
}

func TestPortfolioAnalyticsService_GetDiversificationAnalysis(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	snapshotRepo := &mockSnapshotRepo{}
	positionRepo := &mockPositionProvider{
		positions: []*entities.InvestmentPosition{
			{UserID: userID, Symbol: "AAPL", MarketValue: decimal.NewFromInt(5000)},
			{UserID: userID, Symbol: "GOOGL", MarketValue: decimal.NewFromInt(3000)},
			{UserID: userID, Symbol: "VTI", MarketValue: decimal.NewFromInt(2000)},
		},
	}
	accountRepo := &mockAccountProvider{}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	analysis, err := svc.GetDiversificationAnalysis(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, analysis)

	assert.Len(t, analysis.TopHoldings, 3)
	assert.Equal(t, "AAPL", analysis.TopHoldings[0].Symbol)
	assert.True(t, analysis.DiversificationScore > 0)
}

func TestPortfolioAnalyticsService_GetPerformanceMetrics_NoData(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	snapshotRepo := &mockSnapshotRepo{}
	positionRepo := &mockPositionProvider{}
	accountRepo := &mockAccountProvider{}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	metrics, err := svc.GetPerformanceMetrics(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, metrics)
	assert.True(t, metrics.TotalReturn.IsZero())
}

func TestPortfolioAnalyticsService_GetPortfolioHistory(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	now := time.Now().Truncate(24 * time.Hour)
	snapshotRepo := &mockSnapshotRepo{
		snapshots: []*entities.PortfolioSnapshot{
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -6), TotalValue: decimal.NewFromInt(10000), DayGainLoss: decimal.NewFromInt(100)},
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -5), TotalValue: decimal.NewFromInt(10200), DayGainLoss: decimal.NewFromInt(200)},
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -4), TotalValue: decimal.NewFromInt(10100), DayGainLoss: decimal.NewFromInt(-100)},
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -3), TotalValue: decimal.NewFromInt(10400), DayGainLoss: decimal.NewFromInt(300)},
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -2), TotalValue: decimal.NewFromInt(10500), DayGainLoss: decimal.NewFromInt(100)},
		},
	}
	positionRepo := &mockPositionProvider{}
	accountRepo := &mockAccountProvider{}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	history, err := svc.GetPortfolioHistory(context.Background(), userID, "1W")
	require.NoError(t, err)
	require.NotNil(t, history)

	assert.Equal(t, "1W", history.Period)
	assert.Len(t, history.DataPoints, 5)
	assert.Equal(t, decimal.NewFromInt(10000), history.StartValue)
	assert.Equal(t, decimal.NewFromInt(10500), history.EndValue)
	assert.Equal(t, decimal.NewFromInt(500), history.Change)
}

func TestPortfolioAnalyticsService_GetPortfolioHistory_EmptyData(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	snapshotRepo := &mockSnapshotRepo{}
	positionRepo := &mockPositionProvider{}
	accountRepo := &mockAccountProvider{}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	history, err := svc.GetPortfolioHistory(context.Background(), userID, "1M")
	require.NoError(t, err)
	require.NotNil(t, history)

	assert.Equal(t, "1M", history.Period)
	assert.Empty(t, history.DataPoints)
}

func TestPortfolioAnalyticsService_GetDashboard(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	now := time.Now().Truncate(24 * time.Hour)
	snapshotRepo := &mockSnapshotRepo{
		snapshots: []*entities.PortfolioSnapshot{
			{UserID: userID, SnapshotDate: now.AddDate(0, 0, -1), TotalValue: decimal.NewFromInt(10000)},
			{UserID: userID, SnapshotDate: now, TotalValue: decimal.NewFromInt(10500)},
		},
	}
	positionRepo := &mockPositionProvider{
		positions: []*entities.InvestmentPosition{
			{UserID: userID, Symbol: "AAPL", MarketValue: decimal.NewFromInt(5000), CostBasis: decimal.NewFromInt(4500), LastdayPrice: decimal.NewFromInt(95), Qty: decimal.NewFromInt(50)},
			{UserID: userID, Symbol: "GOOGL", MarketValue: decimal.NewFromInt(5000), CostBasis: decimal.NewFromInt(4800), LastdayPrice: decimal.NewFromInt(95), Qty: decimal.NewFromInt(50)},
		},
	}
	accountRepo := &mockAccountProvider{
		account: &entities.AlpacaAccount{UserID: userID, Cash: decimal.NewFromInt(500)},
	}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	dashboard, err := svc.GetDashboard(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, dashboard)

	// Check summary
	assert.Equal(t, decimal.NewFromInt(10500), dashboard.Summary.TotalValue)
	assert.Equal(t, decimal.NewFromInt(500), dashboard.Summary.CashBalance)
	assert.Equal(t, decimal.NewFromInt(10000), dashboard.Summary.InvestedValue)
	assert.Equal(t, 2, dashboard.Summary.PositionCount)

	// Check that other sections are populated
	assert.NotNil(t, dashboard.Performance)
	assert.NotNil(t, dashboard.Risk)
	assert.NotNil(t, dashboard.Diversification)
	assert.NotEmpty(t, dashboard.GeneratedAt)
}

func TestPortfolioAnalyticsService_GetRiskMetrics(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	now := time.Now().Truncate(24 * time.Hour)
	// Create snapshots with varying values to generate volatility
	snapshots := make([]*entities.PortfolioSnapshot, 30)
	baseValue := 10000.0
	for i := 0; i < 30; i++ {
		// Simulate some volatility
		value := baseValue + float64(i*50) + float64((i%3-1)*100)
		snapshots[i] = &entities.PortfolioSnapshot{
			UserID:       userID,
			SnapshotDate: now.AddDate(0, 0, -30+i),
			TotalValue:   decimal.NewFromFloat(value),
		}
	}

	snapshotRepo := &mockSnapshotRepo{snapshots: snapshots}
	positionRepo := &mockPositionProvider{}
	accountRepo := &mockAccountProvider{}

	svc := analytics.NewPortfolioAnalyticsService(snapshotRepo, positionRepo, accountRepo, logger)

	metrics, err := svc.GetRiskMetrics(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	// Should have calculated volatility
	assert.True(t, metrics.Volatility.GreaterThan(decimal.Zero))
	// Should have a risk level
	assert.NotEmpty(t, metrics.RiskLevel)
}
