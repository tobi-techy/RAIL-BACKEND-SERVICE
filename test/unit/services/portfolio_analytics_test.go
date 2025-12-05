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
