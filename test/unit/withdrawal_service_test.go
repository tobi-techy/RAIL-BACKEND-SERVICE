package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockWithdrawalRepository struct {
	mock.Mock
}

func (m *MockWithdrawalRepository) Create(ctx context.Context, withdrawal *entities.Withdrawal) error {
	args := m.Called(ctx, withdrawal)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Withdrawal), args.Error(1)
}

func (m *MockWithdrawalRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.Withdrawal), args.Error(1)
}

func (m *MockWithdrawalRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) UpdateAlpacaJournal(ctx context.Context, id uuid.UUID, journalID string) error {
	args := m.Called(ctx, id, journalID)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) UpdateDueTransfer(ctx context.Context, id uuid.UUID, transferID, recipientID string) error {
	args := m.Called(ctx, id, transferID, recipientID)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error {
	args := m.Called(ctx, id, txHash)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error {
	args := m.Called(ctx, id, errorMsg)
	return args.Error(0)
}

type MockAlpacaAdapter struct {
	mock.Mock
}

func (m *MockAlpacaAdapter) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlpacaAccountResponse), args.Error(1)
}

func (m *MockAlpacaAdapter) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlpacaJournalResponse), args.Error(1)
}

type MockDueAdapter struct {
	mock.Mock
}

type MockQueuePublisher struct {
	mock.Mock
}

func (m *MockDueAdapter) ProcessWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*services.ProcessWithdrawalResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.ProcessWithdrawalResponse), args.Error(1)
}

func (m *MockDueAdapter) GetTransferStatus(ctx context.Context, transferID string) (*services.OnRampTransferResponse, error) {
	args := m.Called(ctx, transferID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*services.OnRampTransferResponse), args.Error(1)
}

func (m *MockQueuePublisher) Publish(ctx context.Context, queueName string, message interface{}) error {
	args := m.Called(ctx, queueName, message)
	return args.Error(0)
}

func TestInitiateWithdrawal_Success(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	userID := uuid.New()
	alpacaAccountID := "test-account"
	amount := decimal.NewFromFloat(100.00)

	req := &entities.InitiateWithdrawalRequest{
		UserID:             userID,
		AlpacaAccountID:    alpacaAccountID,
		Amount:             amount,
		DestinationChain:   "ethereum",
		DestinationAddress: "0x1234567890123456789012345678901234567890",
	}

	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:          alpacaAccountID,
		Status:      entities.AlpacaAccountStatusActive,
		BuyingPower: decimal.NewFromFloat(500.00),
	}

	mockAlpaca.On("GetAccount", mock.Anything, alpacaAccountID).Return(alpacaAccount, nil)
	mockRepo.On("Create", mock.Anything, mock.AnythingOfType("*entities.Withdrawal")).Return(nil)
	mockQueue.On("Publish", mock.Anything, "withdrawal-processing", mock.Anything).Return(nil)

	resp, err := service.InitiateWithdrawal(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEqual(t, uuid.Nil, resp.WithdrawalID)
	assert.Equal(t, entities.WithdrawalStatusPending, resp.Status)
	mockAlpaca.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestInitiateWithdrawal_InsufficientFunds(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	userID := uuid.New()
	alpacaAccountID := "test-account"
	amount := decimal.NewFromFloat(1000.00)

	req := &entities.InitiateWithdrawalRequest{
		UserID:             userID,
		AlpacaAccountID:    alpacaAccountID,
		Amount:             amount,
		DestinationChain:   "ethereum",
		DestinationAddress: "0x1234567890123456789012345678901234567890",
	}

	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:          alpacaAccountID,
		Status:      entities.AlpacaAccountStatusActive,
		BuyingPower: decimal.NewFromFloat(100.00),
	}

	mockAlpaca.On("GetAccount", mock.Anything, alpacaAccountID).Return(alpacaAccount, nil)

	resp, err := service.InitiateWithdrawal(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "insufficient buying power")
	mockAlpaca.AssertExpectations(t)
}

func TestInitiateWithdrawal_InactiveAccount(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	userID := uuid.New()
	alpacaAccountID := "test-account"
	amount := decimal.NewFromFloat(100.00)

	req := &entities.InitiateWithdrawalRequest{
		UserID:             userID,
		AlpacaAccountID:    alpacaAccountID,
		Amount:             amount,
		DestinationChain:   "ethereum",
		DestinationAddress: "0x1234567890123456789012345678901234567890",
	}

	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:          alpacaAccountID,
		Status:      entities.AlpacaAccountStatusDisabled,
		BuyingPower: decimal.NewFromFloat(500.00),
	}

	mockAlpaca.On("GetAccount", mock.Anything, alpacaAccountID).Return(alpacaAccount, nil)

	resp, err := service.InitiateWithdrawal(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not active")
	mockAlpaca.AssertExpectations(t)
}

func TestGetWithdrawal_Success(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	withdrawalID := uuid.New()
	expectedWithdrawal := &entities.Withdrawal{
		ID:                 withdrawalID,
		UserID:             uuid.New(),
		AlpacaAccountID:    "test-account",
		Amount:             decimal.NewFromFloat(100.00),
		DestinationChain:   "ethereum",
		DestinationAddress: "0x1234567890123456789012345678901234567890",
		Status:             entities.WithdrawalStatusCompleted,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	mockRepo.On("GetByID", mock.Anything, withdrawalID).Return(expectedWithdrawal, nil)

	withdrawal, err := service.GetWithdrawal(context.Background(), withdrawalID)

	assert.NoError(t, err)
	assert.NotNil(t, withdrawal)
	assert.Equal(t, withdrawalID, withdrawal.ID)
	assert.Equal(t, entities.WithdrawalStatusCompleted, withdrawal.Status)
	mockRepo.AssertExpectations(t)
}

func TestGetWithdrawal_NotFound(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	withdrawalID := uuid.New()

	mockRepo.On("GetByID", mock.Anything, withdrawalID).Return(nil, errors.New("withdrawal not found"))

	withdrawal, err := service.GetWithdrawal(context.Background(), withdrawalID)

	assert.Error(t, err)
	assert.Nil(t, withdrawal)
	mockRepo.AssertExpectations(t)
}

func TestGetUserWithdrawals_Success(t *testing.T) {
	mockRepo := new(MockWithdrawalRepository)
	mockAlpaca := new(MockAlpacaAdapter)
	mockDue := new(MockDueAdapter)
	log := logger.New("debug", "test")
	mockQueue := &MockQueuePublisher{}

	service := services.NewWithdrawalService(mockRepo, mockAlpaca, mockDue, log, mockQueue)

	userID := uuid.New()
	expectedWithdrawals := []*entities.Withdrawal{
		{
			ID:                 uuid.New(),
			UserID:             userID,
			AlpacaAccountID:    "test-account",
			Amount:             decimal.NewFromFloat(100.00),
			DestinationChain:   "ethereum",
			DestinationAddress: "0x1234567890123456789012345678901234567890",
			Status:             entities.WithdrawalStatusCompleted,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		},
	}

	mockRepo.On("GetByUserID", mock.Anything, userID, 20, 0).Return(expectedWithdrawals, nil)

	withdrawals, err := service.GetUserWithdrawals(context.Background(), userID, 20, 0)

	assert.NoError(t, err)
	assert.NotNil(t, withdrawals)
	assert.Len(t, withdrawals, 1)
	assert.Equal(t, userID, withdrawals[0].UserID)
	mockRepo.AssertExpectations(t)
}
