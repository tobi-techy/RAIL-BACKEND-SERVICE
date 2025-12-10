package funding_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/workers/funding_webhook"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock repositories and adapters
type MockDepositRepo struct {
	mock.Mock
}

func (m *MockDepositRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

func (m *MockDepositRepo) Update(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

type MockBalanceRepo struct {
	mock.Mock
}

func (m *MockBalanceRepo) UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

type MockVirtualAccountRepo struct {
	mock.Mock
}

func (m *MockVirtualAccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
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

func (m *MockAlpacaAdapter) InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlpacaInstantFundingResponse), args.Error(1)
}

func (m *MockAlpacaAdapter) GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.AlpacaAccountResponse), args.Error(1)
}

type MockNotificationService struct {
	mock.Mock
}

func (m *MockNotificationService) NotifyFundingSuccess(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func (m *MockNotificationService) NotifyFundingFailure(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, reason string) error {
	args := m.Called(ctx, userID, depositID, reason)
	return args.Error(0)
}

func TestProcessOffRampCompletion_Success(t *testing.T) {
	// Setup
	ctx := context.Background()
	depositID := uuid.New()
	userID := uuid.New()
	virtualAccountID := uuid.New()
	alpacaAccountID := "test-alpaca-account"
	amount := decimal.NewFromFloat(100.00)

	mockDepositRepo := new(MockDepositRepo)
	mockBalanceRepo := new(MockBalanceRepo)
	mockVirtualAccountRepo := new(MockVirtualAccountRepo)
	mockAlpacaAdapter := new(MockAlpacaAdapter)
	mockNotificationSvc := new(MockNotificationService)

	orchestrator := funding_webhook.NewAlpacaFundingOrchestrator(
		mockDepositRepo,
		mockBalanceRepo,
		mockVirtualAccountRepo,
		mockAlpacaAdapter,
		mockNotificationSvc,
		logger.New("info", "test"),
	)

	// Mock deposit
	deposit := &entities.Deposit{
		ID:               depositID,
		UserID:           userID,
		Amount:           amount,
		Status:           "off_ramp_completed",
		VirtualAccountID: &virtualAccountID,
	}

	// Mock virtual account
	virtualAccount := &entities.VirtualAccount{
		ID:               virtualAccountID,
		UserID:           userID,
		AlpacaAccountID:  alpacaAccountID,
	}

	// Mock Alpaca account
	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:            alpacaAccountID,
		AccountNumber: "ACC123",
		Status:        entities.AlpacaAccountStatusActive,
		BuyingPower:   decimal.NewFromFloat(1000.00),
	}

	// Mock instant funding response
	fundingResp := &entities.AlpacaInstantFundingResponse{
		ID:     "funding-123",
		Status: "PENDING",
	}

	// Setup expectations
	mockDepositRepo.On("GetByID", ctx, depositID).Return(deposit, nil)
	mockVirtualAccountRepo.On("GetByID", ctx, virtualAccountID).Return(virtualAccount, nil)
	mockAlpacaAdapter.On("GetAccount", ctx, alpacaAccountID).Return(alpacaAccount, nil)
	mockAlpacaAdapter.On("InitiateInstantFunding", ctx, mock.AnythingOfType("*entities.AlpacaInstantFundingRequest")).Return(fundingResp, nil)
	mockDepositRepo.On("Update", ctx, mock.AnythingOfType("*entities.Deposit")).Return(nil)
	mockBalanceRepo.On("UpdateBuyingPower", ctx, userID, amount).Return(nil)
	mockNotificationSvc.On("NotifyFundingSuccess", ctx, userID, amount).Return(nil)

	// Execute
	err := orchestrator.ProcessOffRampCompletion(ctx, depositID)

	// Assert
	assert.NoError(t, err)
	mockDepositRepo.AssertExpectations(t)
	mockVirtualAccountRepo.AssertExpectations(t)
	mockAlpacaAdapter.AssertExpectations(t)
	mockBalanceRepo.AssertExpectations(t)
	mockNotificationSvc.AssertExpectations(t)
}

func TestProcessOffRampCompletion_InvalidStatus(t *testing.T) {
	// Setup
	ctx := context.Background()
	depositID := uuid.New()
	userID := uuid.New()

	mockDepositRepo := new(MockDepositRepo)
	mockBalanceRepo := new(MockBalanceRepo)
	mockVirtualAccountRepo := new(MockVirtualAccountRepo)
	mockAlpacaAdapter := new(MockAlpacaAdapter)
	mockNotificationSvc := new(MockNotificationService)

	orchestrator := funding_webhook.NewAlpacaFundingOrchestrator(
		mockDepositRepo,
		mockBalanceRepo,
		mockVirtualAccountRepo,
		mockAlpacaAdapter,
		mockNotificationSvc,
		logger.New("info", "test"),
	)

	// Mock deposit with wrong status
	deposit := &entities.Deposit{
		ID:     depositID,
		UserID: userID,
		Status: "pending",
	}

	mockDepositRepo.On("GetByID", ctx, depositID).Return(deposit, nil)

	// Execute
	err := orchestrator.ProcessOffRampCompletion(ctx, depositID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected off_ramp_completed")
	mockDepositRepo.AssertExpectations(t)
}

func TestProcessOffRampCompletion_AlpacaFundingFailure(t *testing.T) {
	// Setup
	ctx := context.Background()
	depositID := uuid.New()
	userID := uuid.New()
	virtualAccountID := uuid.New()
	alpacaAccountID := "test-alpaca-account"
	amount := decimal.NewFromFloat(100.00)

	mockDepositRepo := new(MockDepositRepo)
	mockBalanceRepo := new(MockBalanceRepo)
	mockVirtualAccountRepo := new(MockVirtualAccountRepo)
	mockAlpacaAdapter := new(MockAlpacaAdapter)
	mockNotificationSvc := new(MockNotificationService)

	orchestrator := funding_webhook.NewAlpacaFundingOrchestrator(
		mockDepositRepo,
		mockBalanceRepo,
		mockVirtualAccountRepo,
		mockAlpacaAdapter,
		mockNotificationSvc,
		logger.New("info", "test"),
	)

	deposit := &entities.Deposit{
		ID:               depositID,
		UserID:           userID,
		Amount:           amount,
		Status:           "off_ramp_completed",
		VirtualAccountID: &virtualAccountID,
	}

	virtualAccount := &entities.VirtualAccount{
		ID:              virtualAccountID,
		UserID:          userID,
		AlpacaAccountID: alpacaAccountID,
	}

	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:            alpacaAccountID,
		AccountNumber: "ACC123",
		Status:        entities.AlpacaAccountStatusActive,
	}

	fundingError := errors.New("insufficient funds")

	mockDepositRepo.On("GetByID", ctx, depositID).Return(deposit, nil)
	mockVirtualAccountRepo.On("GetByID", ctx, virtualAccountID).Return(virtualAccount, nil)
	mockAlpacaAdapter.On("GetAccount", ctx, alpacaAccountID).Return(alpacaAccount, nil)
	mockAlpacaAdapter.On("InitiateInstantFunding", ctx, mock.AnythingOfType("*entities.AlpacaInstantFundingRequest")).Return(nil, fundingError)
	mockNotificationSvc.On("NotifyFundingFailure", ctx, userID, depositID, mock.AnythingOfType("string")).Return(nil)

	// Execute
	err := orchestrator.ProcessOffRampCompletion(ctx, depositID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initiate Alpaca funding")
	mockDepositRepo.AssertExpectations(t)
	mockVirtualAccountRepo.AssertExpectations(t)
	mockAlpacaAdapter.AssertExpectations(t)
	mockNotificationSvc.AssertExpectations(t)
}

func TestProcessOffRampCompletion_InactiveAlpacaAccount(t *testing.T) {
	// Setup
	ctx := context.Background()
	depositID := uuid.New()
	userID := uuid.New()
	virtualAccountID := uuid.New()
	alpacaAccountID := "test-alpaca-account"
	amount := decimal.NewFromFloat(100.00)

	mockDepositRepo := new(MockDepositRepo)
	mockBalanceRepo := new(MockBalanceRepo)
	mockVirtualAccountRepo := new(MockVirtualAccountRepo)
	mockAlpacaAdapter := new(MockAlpacaAdapter)
	mockNotificationSvc := new(MockNotificationService)

	orchestrator := funding_webhook.NewAlpacaFundingOrchestrator(
		mockDepositRepo,
		mockBalanceRepo,
		mockVirtualAccountRepo,
		mockAlpacaAdapter,
		mockNotificationSvc,
		logger.New("info", "test"),
	)

	deposit := &entities.Deposit{
		ID:               depositID,
		UserID:           userID,
		Amount:           amount,
		Status:           "off_ramp_completed",
		VirtualAccountID: &virtualAccountID,
	}

	virtualAccount := &entities.VirtualAccount{
		ID:              virtualAccountID,
		UserID:          userID,
		AlpacaAccountID: alpacaAccountID,
	}

	// Alpaca account is not active
	alpacaAccount := &entities.AlpacaAccountResponse{
		ID:            alpacaAccountID,
		AccountNumber: "ACC123",
		Status:        entities.AlpacaAccountStatusDisabled,
	}

	mockDepositRepo.On("GetByID", ctx, depositID).Return(deposit, nil)
	mockVirtualAccountRepo.On("GetByID", ctx, virtualAccountID).Return(virtualAccount, nil)
	mockAlpacaAdapter.On("GetAccount", ctx, alpacaAccountID).Return(alpacaAccount, nil)
	mockNotificationSvc.On("NotifyFundingFailure", ctx, userID, depositID, mock.AnythingOfType("string")).Return(nil)

	// Execute
	err := orchestrator.ProcessOffRampCompletion(ctx, depositID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Alpaca account not active")
	mockDepositRepo.AssertExpectations(t)
	mockVirtualAccountRepo.AssertExpectations(t)
	mockAlpacaAdapter.AssertExpectations(t)
	mockNotificationSvc.AssertExpectations(t)
}
