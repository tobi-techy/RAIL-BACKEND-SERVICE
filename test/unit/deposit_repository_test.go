package unit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockDepositRepository struct {
	mock.Mock
}

func (m *MockDepositRepository) Create(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

func (m *MockDepositRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

func (m *MockDepositRepository) GetByOffRampTxID(ctx context.Context, txID string) (*entities.Deposit, error) {
	args := m.Called(ctx, txID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

func (m *MockDepositRepository) Update(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

func (m *MockDepositRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Deposit, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.Deposit), args.Error(1)
}

func TestDepositRepository_Create(t *testing.T) {
	repo := new(MockDepositRepository)
	ctx := context.Background()

	deposit := &entities.Deposit{
		ID:       uuid.New(),
		UserID:   uuid.New(),
		Amount:   decimal.NewFromFloat(100.0),
		Currency: "USDC",
		Status:   "pending",
	}

	repo.On("Create", ctx, deposit).Return(nil)

	err := repo.Create(ctx, deposit)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestDepositRepository_GetByOffRampTxID(t *testing.T) {
	repo := new(MockDepositRepository)
	ctx := context.Background()
	txID := "transfer_123"

	expectedDeposit := &entities.Deposit{
		ID:          uuid.New(),
		UserID:      uuid.New(),
		Amount:      decimal.NewFromFloat(100.0),
		Currency:    "USDC",
		Status:      "off_ramp_completed",
		OffRampTxID: &txID,
	}

	repo.On("GetByOffRampTxID", ctx, txID).Return(expectedDeposit, nil)

	deposit, err := repo.GetByOffRampTxID(ctx, txID)
	assert.NoError(t, err)
	assert.NotNil(t, deposit)
	assert.Equal(t, txID, *deposit.OffRampTxID)
	repo.AssertExpectations(t)
}

func TestDepositRepository_Update(t *testing.T) {
	repo := new(MockDepositRepository)
	ctx := context.Background()

	deposit := &entities.Deposit{
		ID:       uuid.New(),
		UserID:   uuid.New(),
		Amount:   decimal.NewFromFloat(100.0),
		Currency: "USDC",
		Status:   "broker_funded",
	}

	repo.On("Update", ctx, deposit).Return(nil)

	err := repo.Update(ctx, deposit)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestDepositRepository_ListByUserID(t *testing.T) {
	repo := new(MockDepositRepository)
	ctx := context.Background()
	userID := uuid.New()

	expectedDeposits := []*entities.Deposit{
		{
			ID:       uuid.New(),
			UserID:   userID,
			Amount:   decimal.NewFromFloat(100.0),
			Currency: "USDC",
			Status:   "completed",
		},
		{
			ID:       uuid.New(),
			UserID:   userID,
			Amount:   decimal.NewFromFloat(50.0),
			Currency: "USDC",
			Status:   "pending",
		},
	}

	repo.On("ListByUserID", ctx, userID).Return(expectedDeposits, nil)

	deposits, err := repo.ListByUserID(ctx, userID)
	assert.NoError(t, err)
	assert.Len(t, deposits, 2)
	repo.AssertExpectations(t)
}
