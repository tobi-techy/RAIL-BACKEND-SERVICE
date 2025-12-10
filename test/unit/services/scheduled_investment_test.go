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
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"go.uber.org/zap"
)

type mockScheduledInvestmentRepo struct {
	investments []*entities.ScheduledInvestment
	executions  []*entities.ScheduledInvestmentExecution
}

func (m *mockScheduledInvestmentRepo) Create(ctx context.Context, si *entities.ScheduledInvestment) error {
	m.investments = append(m.investments, si)
	return nil
}

func (m *mockScheduledInvestmentRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.ScheduledInvestment, error) {
	for _, si := range m.investments {
		if si.ID == id {
			return si, nil
		}
	}
	return nil, nil
}

func (m *mockScheduledInvestmentRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ScheduledInvestment, error) {
	var result []*entities.ScheduledInvestment
	for _, si := range m.investments {
		if si.UserID == userID {
			result = append(result, si)
		}
	}
	return result, nil
}

func (m *mockScheduledInvestmentRepo) GetDueForExecution(ctx context.Context, before time.Time) ([]*entities.ScheduledInvestment, error) {
	var result []*entities.ScheduledInvestment
	for _, si := range m.investments {
		if si.Status == "active" && si.NextExecutionAt.Before(before) {
			result = append(result, si)
		}
	}
	return result, nil
}

func (m *mockScheduledInvestmentRepo) Update(ctx context.Context, si *entities.ScheduledInvestment) error {
	for i, existing := range m.investments {
		if existing.ID == si.ID {
			m.investments[i] = si
			return nil
		}
	}
	return nil
}

func (m *mockScheduledInvestmentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	for _, si := range m.investments {
		if si.ID == id {
			si.Status = status
			return nil
		}
	}
	return nil
}

func (m *mockScheduledInvestmentRepo) CreateExecution(ctx context.Context, exec *entities.ScheduledInvestmentExecution) error {
	m.executions = append(m.executions, exec)
	return nil
}

func (m *mockScheduledInvestmentRepo) GetExecutions(ctx context.Context, scheduleID uuid.UUID, limit int) ([]*entities.ScheduledInvestmentExecution, error) {
	var result []*entities.ScheduledInvestmentExecution
	for _, e := range m.executions {
		if e.ScheduledInvestmentID == scheduleID {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

type mockOrderPlacer struct {
	orders []*entities.InvestmentOrder
}

func (m *mockOrderPlacer) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error) {
	order := &entities.InvestmentOrder{
		ID:     uuid.New(),
		UserID: userID,
		Symbol: symbol,
	}
	m.orders = append(m.orders, order)
	return order, nil
}

func TestScheduledInvestmentService_Create(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()
	repo := &mockScheduledInvestmentRepo{}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewScheduledInvestmentService(repo, orderPlacer, logger)

	symbol := "AAPL"
	req := &investing.CreateScheduledInvestmentRequest{
		UserID:    userID,
		Symbol:    &symbol,
		Amount:    decimal.NewFromInt(100),
		Frequency: "weekly",
	}

	si, err := svc.CreateScheduledInvestment(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, si)

	assert.Equal(t, userID, si.UserID)
	assert.Equal(t, "AAPL", *si.Symbol)
	assert.Equal(t, "weekly", si.Frequency)
	assert.Equal(t, "active", si.Status)
	assert.True(t, si.NextExecutionAt.After(time.Now()))
}

func TestScheduledInvestmentService_Pause(t *testing.T) {
	userID := uuid.New()
	logger := zap.NewNop()
	
	si := &entities.ScheduledInvestment{
		ID:     uuid.New(),
		UserID: userID,
		Status: "active",
	}
	repo := &mockScheduledInvestmentRepo{investments: []*entities.ScheduledInvestment{si}}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewScheduledInvestmentService(repo, orderPlacer, logger)

	err := svc.PauseScheduledInvestment(context.Background(), si.ID)
	require.NoError(t, err)
	assert.Equal(t, "paused", si.Status)
}

func TestScheduledInvestmentService_GetUserScheduledInvestments(t *testing.T) {
	userID := uuid.New()
	otherUserID := uuid.New()
	logger := zap.NewNop()

	repo := &mockScheduledInvestmentRepo{
		investments: []*entities.ScheduledInvestment{
			{ID: uuid.New(), UserID: userID, Status: "active"},
			{ID: uuid.New(), UserID: userID, Status: "paused"},
			{ID: uuid.New(), UserID: otherUserID, Status: "active"},
		},
	}
	orderPlacer := &mockOrderPlacer{}

	svc := investing.NewScheduledInvestmentService(repo, orderPlacer, logger)

	investments, err := svc.GetUserScheduledInvestments(context.Background(), userID)
	require.NoError(t, err)
	assert.Len(t, investments, 2)
}
