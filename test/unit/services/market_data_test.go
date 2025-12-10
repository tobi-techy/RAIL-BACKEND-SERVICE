package services_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/market"
	"go.uber.org/zap"
)

type mockAlertRepo struct {
	alerts []*entities.MarketAlert
}

func (m *mockAlertRepo) Create(ctx context.Context, alert *entities.MarketAlert) error {
	m.alerts = append(m.alerts, alert)
	return nil
}

func (m *mockAlertRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.MarketAlert, error) {
	var result []*entities.MarketAlert
	for _, a := range m.alerts {
		if a.UserID == userID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockAlertRepo) GetActiveBySymbol(ctx context.Context, symbol string) ([]*entities.MarketAlert, error) {
	var result []*entities.MarketAlert
	for _, a := range m.alerts {
		if a.Symbol == symbol && a.Status == "active" {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockAlertRepo) GetAllActive(ctx context.Context) ([]*entities.MarketAlert, error) {
	var result []*entities.MarketAlert
	for _, a := range m.alerts {
		if a.Status == "active" {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockAlertRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.MarketAlert, error) {
	for _, a := range m.alerts {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, nil
}

func (m *mockAlertRepo) MarkTriggered(ctx context.Context, id uuid.UUID, currentPrice decimal.Decimal) error {
	for _, a := range m.alerts {
		if a.ID == id {
			a.Triggered = true
			a.Status = "triggered"
			a.CurrentPrice = &currentPrice
			return nil
		}
	}
	return nil
}

func (m *mockAlertRepo) Delete(ctx context.Context, id uuid.UUID) error {
	for i, a := range m.alerts {
		if a.ID == id {
			m.alerts = append(m.alerts[:i], m.alerts[i+1:]...)
			return nil
		}
	}
	return nil
}

type mockNotifier struct {
	notifications []string
}

func (m *mockNotifier) SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	m.notifications = append(m.notifications, title+": "+message)
	return nil
}

func TestMarketDataService_CreateAlert(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	alertRepo := &mockAlertRepo{}
	notifier := &mockNotifier{}

	svc := market.NewMarketDataService(nil, alertRepo, notifier, logger)

	alert, err := svc.CreateAlert(context.Background(), userID, "AAPL", "price_above", decimal.NewFromInt(200))
	require.NoError(t, err)
	require.NotNil(t, alert)

	assert.Equal(t, userID, alert.UserID)
	assert.Equal(t, "AAPL", alert.Symbol)
	assert.Equal(t, "price_above", alert.AlertType)
	assert.Equal(t, decimal.NewFromInt(200), alert.ConditionValue)
	assert.Equal(t, "active", alert.Status)
}

func TestMarketDataService_CreateAlert_InvalidType(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()

	alertRepo := &mockAlertRepo{}
	notifier := &mockNotifier{}

	svc := market.NewMarketDataService(nil, alertRepo, notifier, logger)

	_, err := svc.CreateAlert(context.Background(), userID, "AAPL", "invalid_type", decimal.NewFromInt(200))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid alert type")
}

func TestMarketDataService_GetUserAlerts(t *testing.T) {
	userID := uuid.New()
	otherUserID := uuid.New()
	logger := zap.NewNop()

	alertRepo := &mockAlertRepo{
		alerts: []*entities.MarketAlert{
			{ID: uuid.New(), UserID: userID, Symbol: "AAPL", Status: "active"},
			{ID: uuid.New(), UserID: userID, Symbol: "GOOGL", Status: "triggered"},
			{ID: uuid.New(), UserID: otherUserID, Symbol: "MSFT", Status: "active"},
		},
	}
	notifier := &mockNotifier{}

	svc := market.NewMarketDataService(nil, alertRepo, notifier, logger)

	alerts, err := svc.GetUserAlerts(context.Background(), userID)
	require.NoError(t, err)
	assert.Len(t, alerts, 2)
}

func TestMarketDataService_DeleteAlert(t *testing.T) {
	userID := uuid.New()
	alertID := uuid.New()
	logger := zap.NewNop()

	alertRepo := &mockAlertRepo{
		alerts: []*entities.MarketAlert{
			{ID: alertID, UserID: userID, Symbol: "AAPL", Status: "active"},
		},
	}
	notifier := &mockNotifier{}

	svc := market.NewMarketDataService(nil, alertRepo, notifier, logger)

	err := svc.DeleteAlert(context.Background(), userID, alertID)
	require.NoError(t, err)
	assert.Len(t, alertRepo.alerts, 0)
}
