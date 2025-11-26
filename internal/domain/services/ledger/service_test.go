package ledger

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockLedgerRepository is a mock implementation of LedgerRepository
type MockLedgerRepository struct {
	mock.Mock
}

func (m *MockLedgerRepository) CreateAccount(ctx context.Context, account *entities.LedgerAccount) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockLedgerRepository) GetAccountByID(ctx context.Context, id uuid.UUID) (*entities.LedgerAccount, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerAccount), args.Error(1)
}

func (m *MockLedgerRepository) GetAccountByUserAndType(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	args := m.Called(ctx, userID, accountType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerAccount), args.Error(1)
}

func (m *MockLedgerRepository) GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	args := m.Called(ctx, accountType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerAccount), args.Error(1)
}

func (m *MockLedgerRepository) GetUserAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.LedgerAccount), args.Error(1)
}

func (m *MockLedgerRepository) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	args := m.Called(ctx, userID, accountType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerAccount), args.Error(1)
}

func (m *MockLedgerRepository) UpdateAccountBalance(ctx context.Context, accountID uuid.UUID, newBalance decimal.Decimal) error {
	args := m.Called(ctx, accountID, newBalance)
	return args.Error(0)
}

func (m *MockLedgerRepository) CreateTransaction(ctx context.Context, tx *entities.LedgerTransaction) error {
	args := m.Called(ctx, tx)
	return args.Error(0)
}

func (m *MockLedgerRepository) GetTransactionByID(ctx context.Context, id uuid.UUID) (*entities.LedgerTransaction, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerTransaction), args.Error(1)
}

func (m *MockLedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*entities.LedgerTransaction, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.LedgerTransaction), args.Error(1)
}

func (m *MockLedgerRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status entities.TransactionStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockLedgerRepository) CreateEntry(ctx context.Context, entry *entities.LedgerEntry) error {
	args := m.Called(ctx, entry)
	return args.Error(0)
}

func (m *MockLedgerRepository) GetEntriesByTransactionID(ctx context.Context, txID uuid.UUID) ([]*entities.LedgerEntry, error) {
	args := m.Called(ctx, txID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.LedgerEntry), args.Error(1)
}

func (m *MockLedgerRepository) GetEntriesByAccountID(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]*entities.LedgerEntry, error) {
	args := m.Called(ctx, accountID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*entities.LedgerEntry), args.Error(1)
}

func (m *MockLedgerRepository) GetAccountBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	args := m.Called(ctx, accountID)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

func (m *MockLedgerRepository) GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.UserBalances), args.Error(1)
}

func (m *MockLedgerRepository) GetSystemBuffers(ctx context.Context) (*entities.SystemBuffers, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.SystemBuffers), args.Error(1)
}

// MockDB is a mock implementation of sqlx.DB for transaction testing
type MockDB struct {
	*sqlx.DB
}

func TestCreateTransaction_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil, // Not needed for this test
		logger: log,
	}

	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromFloat(100.00)

	// Mock GetOrCreateUserAccount
	userAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypeUSDCBalance,
		Balance:     decimal.Zero,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypeUSDCBalance).Return(userAccount, nil)

	// Mock GetSystemAccount
	systemAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		AccountType: entities.AccountTypeSystemBufferUSDC,
		Balance:     decimal.NewFromFloat(1000.00),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetSystemAccount", ctx, entities.AccountTypeSystemBufferUSDC).Return(systemAccount, nil)

	// Mock GetTransactionByIdempotencyKey (no existing transaction)
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, mock.Anything).Return(nil, sql.ErrNoRows)

	// Mock CreateTransaction
	mockRepo.On("CreateTransaction", ctx, mock.AnythingOfType("*entities.LedgerTransaction")).Return(nil)

	// Mock CreateEntry (will be called twice: debit and credit)
	mockRepo.On("CreateEntry", ctx, mock.AnythingOfType("*entities.LedgerEntry")).Return(nil).Times(2)

	// Mock UpdateAccountBalance (will be called twice: for user and system accounts)
	mockRepo.On("UpdateAccountBalance", ctx, userAccount.ID, amount).Return(nil)
	mockRepo.On("UpdateAccountBalance", ctx, systemAccount.ID, systemAccount.Balance.Sub(amount)).Return(nil)

	// Create transaction request
	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeDeposit,
		Description:     "Test deposit",
		IdempotencyKey:  "test-deposit-123",
		Entries: []entities.CreateEntryRequest{
			{
				AccountID: userAccount.ID,
				EntryType: entities.EntryTypeCredit,
				Amount:    amount,
			},
			{
				AccountID: systemAccount.ID,
				EntryType: entities.EntryTypeDebit,
				Amount:    amount,
			},
		},
	}

	// Execute
	result, err := service.CreateTransaction(ctx, req)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, entities.TransactionTypeDeposit, result.TransactionType)
	assert.Equal(t, entities.TransactionStatusCompleted, result.Status)
	mockRepo.AssertExpectations(t)
}

func TestCreateTransaction_IdempotencyKeyExists(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	existingTx := &entities.LedgerTransaction{
		ID:              uuid.New(),
		TransactionType: entities.TransactionTypeDeposit,
		IdempotencyKey:  "existing-key-123",
		Status:          entities.TransactionStatusCompleted,
		CreatedAt:       time.Now(),
	}

	// Mock GetTransactionByIdempotencyKey (existing transaction found)
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, "existing-key-123").Return(existingTx, nil)

	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeDeposit,
		Description:     "Duplicate request",
		IdempotencyKey:  "existing-key-123",
		Entries: []entities.CreateEntryRequest{
			{
				AccountID: uuid.New(),
				EntryType: entities.EntryTypeCredit,
				Amount:    decimal.NewFromFloat(100),
			},
		},
	}

	// Execute
	result, err := service.CreateTransaction(ctx, req)

	// Assert - should return existing transaction
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, existingTx.ID, result.ID)
	mockRepo.AssertExpectations(t)
}

func TestCreateTransaction_UnbalancedEntries(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()

	// Mock GetTransactionByIdempotencyKey (no existing transaction)
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, mock.Anything).Return(nil, sql.ErrNoRows)

	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeDeposit,
		Description:     "Unbalanced transaction",
		IdempotencyKey:  "unbalanced-123",
		Entries: []entities.CreateEntryRequest{
			{
				AccountID: uuid.New(),
				EntryType: entities.EntryTypeCredit,
				Amount:    decimal.NewFromFloat(100),
			},
			{
				AccountID: uuid.New(),
				EntryType: entities.EntryTypeDebit,
				Amount:    decimal.NewFromFloat(50), // Unbalanced!
			},
		},
	}

	// Execute
	result, err := service.CreateTransaction(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "debits and credits must balance")
}

func TestReserveForInvestment_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromFloat(500.00)

	// Mock GetOrCreateUserAccount for fiat_exposure
	fiatAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypeFiatExposure,
		Balance:     decimal.NewFromFloat(1000.00),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypeFiatExposure).Return(fiatAccount, nil)

	// Mock GetOrCreateUserAccount for pending_investment
	pendingAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypePendingInvestment,
		Balance:     decimal.Zero,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypePendingInvestment).Return(pendingAccount, nil)

	// Mock idempotency check
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, mock.Anything).Return(nil, sql.ErrNoRows)

	// Mock CreateTransaction
	mockRepo.On("CreateTransaction", ctx, mock.AnythingOfType("*entities.LedgerTransaction")).Return(nil)

	// Mock CreateEntry (2 times)
	mockRepo.On("CreateEntry", ctx, mock.AnythingOfType("*entities.LedgerEntry")).Return(nil).Times(2)

	// Mock UpdateAccountBalance (2 times)
	mockRepo.On("UpdateAccountBalance", ctx, fiatAccount.ID, fiatAccount.Balance.Sub(amount)).Return(nil)
	mockRepo.On("UpdateAccountBalance", ctx, pendingAccount.ID, amount).Return(nil)

	// Execute
	err := service.ReserveForInvestment(ctx, userID, amount, "order-123")

	// Assert
	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestReserveForInvestment_InsufficientFunds(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromFloat(500.00)

	// Mock GetOrCreateUserAccount - account has insufficient balance
	fiatAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypeFiatExposure,
		Balance:     decimal.NewFromFloat(100.00), // Less than requested amount
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypeFiatExposure).Return(fiatAccount, nil)

	// Execute
	err := service.ReserveForInvestment(ctx, userID, amount, "order-123")

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient")
	mockRepo.AssertExpectations(t)
}

func TestReleaseReservation_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromFloat(500.00)

	// Mock GetOrCreateUserAccount for pending_investment
	pendingAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypePendingInvestment,
		Balance:     amount, // Has reserved funds
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypePendingInvestment).Return(pendingAccount, nil)

	// Mock GetOrCreateUserAccount for fiat_exposure
	fiatAccount := &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypeFiatExposure,
		Balance:     decimal.NewFromFloat(500.00),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	mockRepo.On("GetOrCreateUserAccount", ctx, userID, entities.AccountTypeFiatExposure).Return(fiatAccount, nil)

	// Mock idempotency check
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, mock.Anything).Return(nil, sql.ErrNoRows)

	// Mock CreateTransaction
	mockRepo.On("CreateTransaction", ctx, mock.AnythingOfType("*entities.LedgerTransaction")).Return(nil)

	// Mock CreateEntry (2 times)
	mockRepo.On("CreateEntry", ctx, mock.AnythingOfType("*entities.LedgerEntry")).Return(nil).Times(2)

	// Mock UpdateAccountBalance (2 times)
	mockRepo.On("UpdateAccountBalance", ctx, pendingAccount.ID, decimal.Zero).Return(nil)
	mockRepo.On("UpdateAccountBalance", ctx, fiatAccount.ID, fiatAccount.Balance.Add(amount)).Return(nil)

	// Execute
	err := service.ReleaseReservation(ctx, userID, amount, "order-123")

	// Assert
	require.NoError(t, err)
	mockRepo.AssertExpectations(t)
}

func TestGetUserBalances_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	userID := uuid.New()

	expectedBalances := &entities.UserBalances{
		UserID:            userID,
		USDCBalance:       decimal.NewFromFloat(100.00),
		FiatExposure:      decimal.NewFromFloat(500.00),
		PendingInvestment: decimal.NewFromFloat(50.00),
		TotalValue:        decimal.NewFromFloat(650.00),
	}

	mockRepo.On("GetUserBalances", ctx, userID).Return(expectedBalances, nil)

	// Execute
	balances, err := service.GetUserBalances(ctx, userID)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, expectedBalances, balances)
	mockRepo.AssertExpectations(t)
}

func TestGetSystemBuffers_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()

	expectedBuffers := &entities.SystemBuffers{
		USDCBuffer:          decimal.NewFromFloat(10000.00),
		FiatBuffer:          decimal.NewFromFloat(5000.00),
		BrokerOperational:   decimal.NewFromFloat(50000.00),
		TotalSystemCapital:  decimal.NewFromFloat(65000.00),
	}

	mockRepo.On("GetSystemBuffers", ctx).Return(expectedBuffers, nil)

	// Execute
	buffers, err := service.GetSystemBuffers(ctx)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, expectedBuffers, buffers)
	mockRepo.AssertExpectations(t)
}

func TestReverseTransaction_Success(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	originalTxID := uuid.New()
	accountID1 := uuid.New()
	accountID2 := uuid.New()

	// Original transaction
	originalTx := &entities.LedgerTransaction{
		ID:              originalTxID,
		TransactionType: entities.TransactionTypeDeposit,
		IdempotencyKey:  "original-tx-123",
		Status:          entities.TransactionStatusCompleted,
		CreatedAt:       time.Now(),
	}

	// Original entries
	originalEntries := []*entities.LedgerEntry{
		{
			ID:            uuid.New(),
			TransactionID: originalTxID,
			AccountID:     accountID1,
			EntryType:     entities.EntryTypeCredit,
			Amount:        decimal.NewFromFloat(100.00),
			CreatedAt:     time.Now(),
		},
		{
			ID:            uuid.New(),
			TransactionID: originalTxID,
			AccountID:     accountID2,
			EntryType:     entities.EntryTypeDebit,
			Amount:        decimal.NewFromFloat(100.00),
			CreatedAt:     time.Now(),
		},
	}

	// Mock GetTransactionByID
	mockRepo.On("GetTransactionByID", ctx, originalTxID).Return(originalTx, nil)

	// Mock GetEntriesByTransactionID
	mockRepo.On("GetEntriesByTransactionID", ctx, originalTxID).Return(originalEntries, nil)

	// Mock idempotency check for reversal
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, "reversal-original-tx-123").Return(nil, sql.ErrNoRows)

	// Mock CreateTransaction for reversal
	mockRepo.On("CreateTransaction", ctx, mock.AnythingOfType("*entities.LedgerTransaction")).Return(nil)

	// Mock CreateEntry (2 reversed entries)
	mockRepo.On("CreateEntry", ctx, mock.AnythingOfType("*entities.LedgerEntry")).Return(nil).Times(2)

	// Mock UpdateAccountBalance (2 times - for both accounts)
	mockRepo.On("UpdateAccountBalance", ctx, accountID1, mock.AnythingOfType("decimal.Decimal")).Return(nil)
	mockRepo.On("UpdateAccountBalance", ctx, accountID2, mock.AnythingOfType("decimal.Decimal")).Return(nil)

	// Mock GetAccountByID for balance calculations
	mockRepo.On("GetAccountByID", ctx, accountID1).Return(&entities.LedgerAccount{
		ID: accountID1, Balance: decimal.NewFromFloat(100.00),
	}, nil)
	mockRepo.On("GetAccountByID", ctx, accountID2).Return(&entities.LedgerAccount{
		ID: accountID2, Balance: decimal.NewFromFloat(0),
	}, nil)

	// Execute
	reversalTx, err := service.ReverseTransaction(ctx, originalTxID, "test reversal")

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, reversalTx)
	assert.Equal(t, entities.TransactionTypeReversal, reversalTx.TransactionType)
	mockRepo.AssertExpectations(t)
}

func TestReverseTransaction_AlreadyReversed(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	originalTxID := uuid.New()

	originalTx := &entities.LedgerTransaction{
		ID:              originalTxID,
		TransactionType: entities.TransactionTypeDeposit,
		IdempotencyKey:  "original-tx-123",
		Status:          entities.TransactionStatusReversed, // Already reversed
		CreatedAt:       time.Now(),
	}

	mockRepo.On("GetTransactionByID", ctx, originalTxID).Return(originalTx, nil)

	// Execute
	reversalTx, err := service.ReverseTransaction(ctx, originalTxID, "test reversal")

	// Assert
	require.Error(t, err)
	assert.Nil(t, reversalTx)
	assert.Contains(t, err.Error(), "already reversed")
	mockRepo.AssertExpectations(t)
}

func TestReverseTransaction_NotFound(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	originalTxID := uuid.New()

	mockRepo.On("GetTransactionByID", ctx, originalTxID).Return(nil, sql.ErrNoRows)

	// Execute
	reversalTx, err := service.ReverseTransaction(ctx, originalTxID, "test reversal")

	// Assert
	require.Error(t, err)
	assert.Nil(t, reversalTx)
	mockRepo.AssertExpectations(t)
}

func TestValidateTransaction_NegativeBalance(t *testing.T) {
	mockRepo := new(MockLedgerRepository)
	log := logger.NewLogger()
	service := &Service{
		repo:   mockRepo,
		db:     nil,
		logger: log,
	}

	ctx := context.Background()
	accountID := uuid.New()

	account := &entities.LedgerAccount{
		ID:          accountID,
		AccountType: entities.AccountTypeUSDCBalance,
		Balance:     decimal.NewFromFloat(50.00),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	mockRepo.On("GetAccountByID", ctx, accountID).Return(account, nil)

	// Try to debit more than balance
	req := &entities.CreateTransactionRequest{
		TransactionType: entities.TransactionTypeWithdrawal,
		Description:     "Overdraft attempt",
		IdempotencyKey:  "overdraft-123",
		Entries: []entities.CreateEntryRequest{
			{
				AccountID: accountID,
				EntryType: entities.EntryTypeDebit,
				Amount:    decimal.NewFromFloat(100.00), // More than balance
			},
		},
	}

	// Mock idempotency check
	mockRepo.On("GetTransactionByIdempotencyKey", ctx, "overdraft-123").Return(nil, sql.ErrNoRows)

	// Execute
	result, err := service.CreateTransaction(ctx, req)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "insufficient")
}
