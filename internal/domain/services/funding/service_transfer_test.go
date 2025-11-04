package funding

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stack-service/stack_service/internal/adapters/due"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// MockDueAdapter is a mock implementation of the DueAdapter interface
type MockDueAdapter struct {
	mock.Mock
}

func (m *MockDueAdapter) CreateVirtualAccount(ctx context.Context, userID string, destination string, schemaIn string, currencyIn string, railOut string, currencyOut string) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, userID, destination, schemaIn, currencyIn, railOut, currencyOut)
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func (m *MockDueAdapter) ListVirtualAccounts(ctx context.Context, filters map[string]string) ([]due.VirtualAccountSummary, error) {
	args := m.Called(ctx, filters)
	return args.Get(0).([]due.VirtualAccountSummary), args.Error(1)
}

func (m *MockDueAdapter) GetVirtualAccount(ctx context.Context, reference string) (*due.GetVirtualAccountResponse, error) {
	args := m.Called(ctx, reference)
	return args.Get(0).(*due.GetVirtualAccountResponse), args.Error(1)
}

func (m *MockDueAdapter) CreateQuote(ctx context.Context, req due.CreateQuoteRequest) (*due.CreateQuoteResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*due.CreateQuoteResponse), args.Error(1)
}

func (m *MockDueAdapter) CreateTransfer(ctx context.Context, req due.CreateTransferRequest) (*due.CreateTransferResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(*due.CreateTransferResponse), args.Error(1)
}

func (m *MockDueAdapter) CreateTransferIntent(ctx context.Context, transferID string) (*due.CreateTransferIntentResponse, error) {
	args := m.Called(ctx, transferID)
	return args.Get(0).(*due.CreateTransferIntentResponse), args.Error(1)
}

func (m *MockDueAdapter) SubmitTransferIntent(ctx context.Context, req due.SubmitTransferIntentRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockDueAdapter) CreateFundingAddress(ctx context.Context, transferID string) (*due.CreateFundingAddressResponse, error) {
	args := m.Called(ctx, transferID)
	return args.Get(0).(*due.CreateFundingAddressResponse), args.Error(1)
}

func (m *MockDueAdapter) GetTransfer(ctx context.Context, transferID string) (*due.GetTransferResponse, error) {
	args := m.Called(ctx, transferID)
	return args.Get(0).(*due.GetTransferResponse), args.Error(1)
}

// Mock repositories
type MockDepositRepository struct {
	mock.Mock
}

func (m *MockDepositRepository) Create(ctx context.Context, deposit *entities.Deposit) error {
	args := m.Called(ctx, deposit)
	return args.Error(0)
}

func (m *MockDepositRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]*entities.Deposit), args.Error(1)
}

func (m *MockDepositRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

func (m *MockDepositRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error {
	args := m.Called(ctx, id, status, confirmedAt)
	return args.Error(0)
}

func (m *MockDepositRepository) UpdateOffRampStatus(ctx context.Context, id uuid.UUID, status string, transferRef string) error {
	args := m.Called(ctx, id, status, transferRef)
	return args.Error(0)
}

func (m *MockDepositRepository) GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error) {
	args := m.Called(ctx, txHash)
	return args.Get(0).(*entities.Deposit), args.Error(1)
}

type MockVirtualAccountRepository struct {
	mock.Mock
}

func (m *MockVirtualAccountRepository) Create(ctx context.Context, account *entities.VirtualAccount) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockVirtualAccountRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, userID)
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func (m *MockVirtualAccountRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func (m *MockVirtualAccountRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.VirtualAccountStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockVirtualAccountRepository) GetByDueAccountID(ctx context.Context, dueAccountID string) (*entities.VirtualAccount, error) {
	args := m.Called(ctx, dueAccountID)
	return args.Get(0).(*entities.VirtualAccount), args.Error(1)
}

func TestInitiateDueTransfer_Success(t *testing.T) {
	// Setup mocks
	mockDepositRepo := &MockDepositRepository{}
	mockVirtualAccountRepo := &MockVirtualAccountRepository{}
	mockDueAdapter := &MockDueAdapter{}

	// Create service
	zapLogger := zaptest.NewLogger(t)
	testLogger := &logger.Logger{SugaredLogger: zapLogger.Sugar()}
	service := &Service{
		depositRepo:       mockDepositRepo,
		virtualAccountRepo: mockVirtualAccountRepo,
		dueAPI:            mockDueAdapter,
		logger:            testLogger,
	}

	// Setup test data
	depositID := uuid.New()
	virtualAccountID := uuid.New()
	userID := uuid.New()

	deposit := &entities.Deposit{
		ID:     depositID,
		UserID: userID,
		Status: "confirmed_on_chain",
		Amount: decimal.NewFromFloat(1000.00),
	}

	quoteResponse := &due.CreateQuoteResponse{
		Token: "quote_token_123",
		Source: due.QuoteLeg{
			Rail:     "ethereum",
			Currency: "USDC",
			Amount:   "1000.00",
			Fee:      "0.00",
		},
		Destination: due.QuoteLeg{
			Rail:     "ach",
			Currency: "USD",
			Amount:   "997.50",
			Fee:      "2.50",
		},
		FXRate:    1.0,
		FXMarkup:  0,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	transferResponse := &due.CreateTransferResponse{
		ID:      "transfer_789",
		OwnerID: "account_123",
		Status:  "awaiting_funds",
		Source: due.TransferLeg{
			Amount:   "1000.00",
			Currency: "USDC",
			Rail:     "ethereum",
		},
		Destination: due.TransferLeg{
			Amount:   "997.50",
			Currency: "USD",
			Rail:     "ach",
		},
		FXRate:    1.0,
		FXMarkup:  0,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	// Setup expectations
	mockDepositRepo.On("GetByID", mock.Anything, depositID).Return(deposit, nil)
	mockDueAdapter.On("CreateQuote", mock.Anything, mock.AnythingOfType("due.CreateQuoteRequest")).Return(quoteResponse, nil)
	mockDueAdapter.On("CreateTransfer", mock.Anything, mock.AnythingOfType("due.CreateTransferRequest")).Return(transferResponse, nil)
	mockDepositRepo.On("UpdateOffRampStatus", mock.Anything, depositID, "off_ramp_initiated", "transfer_789").Return(nil)

	// Execute test
	ctx := context.Background()
	transferID, err := service.InitiateDueTransfer(ctx, depositID, virtualAccountID)

	// Assertions
	require.NoError(t, err)
	assert.Equal(t, "transfer_789", transferID)

	// Verify all expectations were met
	mockDepositRepo.AssertExpectations(t)
	mockVirtualAccountRepo.AssertExpectations(t)
	mockDueAdapter.AssertExpectations(t)
}

func TestInitiateDueTransfer_InvalidDepositStatus(t *testing.T) {
	// Setup mocks
	mockDepositRepo := &MockDepositRepository{}

	// Create service
	zapLogger := zaptest.NewLogger(t)
	testLogger := &logger.Logger{SugaredLogger: zapLogger.Sugar()}
	service := &Service{
		depositRepo: mockDepositRepo,
		logger:      testLogger,
	}

	// Setup test data
	depositID := uuid.New()
	deposit := &entities.Deposit{
		ID:     depositID,
		Status: "pending", // Invalid status
		Amount: decimal.NewFromFloat(1000.00),
	}

	// Setup expectations
	mockDepositRepo.On("GetByID", mock.Anything, depositID).Return(deposit, nil)

	// Execute test
	ctx := context.Background()
	transferID, err := service.InitiateDueTransfer(ctx, depositID, uuid.New())

	// Assertions
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deposit status is pending, expected confirmed_on_chain")
	assert.Empty(t, transferID)

	// Verify expectations
	mockDepositRepo.AssertExpectations(t)
}

func TestInitiateDueTransfer_QuoteFailure(t *testing.T) {
	// Setup mocks
	mockDepositRepo := &MockDepositRepository{}
	mockVirtualAccountRepo := &MockVirtualAccountRepository{}
	mockDueAdapter := &MockDueAdapter{}

	// Create service
	zapLogger := zaptest.NewLogger(t)
	testLogger := &logger.Logger{SugaredLogger: zapLogger.Sugar()}
	service := &Service{
		depositRepo:       mockDepositRepo,
		virtualAccountRepo: mockVirtualAccountRepo,
		dueAPI:            mockDueAdapter,
		logger:            testLogger,
	}

	// Setup test data
	depositID := uuid.New()
	virtualAccountID := uuid.New()
	deposit := &entities.Deposit{
		ID:     depositID,
		Status: "confirmed_on_chain",
		Amount: decimal.NewFromFloat(1000.00),
	}

	// Setup expectations
	mockDepositRepo.On("GetByID", mock.Anything, depositID).Return(deposit, nil)
	mockDueAdapter.On("CreateQuote", mock.Anything, mock.AnythingOfType("due.CreateQuoteRequest")).Return((*due.CreateQuoteResponse)(nil), assert.AnError)

	// Execute test
	ctx := context.Background()
	transferID, err := service.InitiateDueTransfer(ctx, depositID, virtualAccountID)

	// Assertions
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create transfer quote")
	assert.Empty(t, transferID)

	// Verify expectations
	mockDepositRepo.AssertExpectations(t)
	mockVirtualAccountRepo.AssertExpectations(t)
	mockDueAdapter.AssertExpectations(t)
}
