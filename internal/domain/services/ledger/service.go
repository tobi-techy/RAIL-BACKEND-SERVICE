package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Service handles ledger operations using double-entry bookkeeping
type Service struct {
	ledgerRepo *repositories.LedgerRepository
	db         *sqlx.DB
	logger     *logger.Logger
}

// NewService creates a new ledger service
func NewService(
	ledgerRepo *repositories.LedgerRepository,
	db *sqlx.DB,
	logger *logger.Logger,
) *Service {
	return &Service{
		ledgerRepo: ledgerRepo,
		db:         db,
		logger:     logger,
	}
}

// CreateTransaction creates a new ledger transaction with entries atomically
// This is the core operation that ensures double-entry bookkeeping integrity
func (s *Service) CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate request: %w", err)
	}

	// Check for idempotency
	existing, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("check idempotency: %w", err)
	}
	if existing != nil {
		s.logger.Info("Transaction already exists (idempotent)",
			"idempotency_key", req.IdempotencyKey,
			"transaction_id", existing.ID)
		return existing, nil
	}

	// Begin database transaction
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Create ledger transaction record
	now := time.Now()
	ledgerTx := &entities.LedgerTransaction{
		ID:              uuid.New(),
		UserID:          req.UserID,
		TransactionType: req.TransactionType,
		ReferenceID:     req.ReferenceID,
		ReferenceType:   req.ReferenceType,
		Status:          entities.TransactionStatusPending,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
		Metadata:        req.Metadata,
		CreatedAt:       now,
	}

	// Use transaction context for all operations
	txCtx := context.WithValue(ctx, "db_tx", tx)

	if err := s.ledgerRepo.CreateTransaction(txCtx, ledgerTx); err != nil {
		return nil, fmt.Errorf("create transaction: %w", err)
	}

	// Create entries and update account balances
	for _, entryReq := range req.Entries {
		entry := &entities.LedgerEntry{
			ID:            uuid.New(),
			TransactionID: ledgerTx.ID,
			AccountID:     entryReq.AccountID,
			EntryType:     entryReq.EntryType,
			Amount:        entryReq.Amount,
			Currency:      entryReq.Currency,
			Description:   entryReq.Description,
			Metadata:      entryReq.Metadata,
			CreatedAt:     now,
		}

		if err := s.ledgerRepo.CreateEntry(txCtx, entry); err != nil {
			return nil, fmt.Errorf("create entry: %w", err)
		}

		// Update account balance
		if err := s.updateAccountBalanceInTx(txCtx, entryReq.AccountID, entryReq.EntryType, entryReq.Amount); err != nil {
			return nil, fmt.Errorf("update account balance: %w", err)
		}
	}

	// Mark transaction as completed
	ledgerTx.MarkCompleted()
	if err := s.ledgerRepo.UpdateTransactionStatus(txCtx, ledgerTx.ID, entities.TransactionStatusCompleted); err != nil {
		return nil, fmt.Errorf("update transaction status: %w", err)
	}

	// Commit database transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	s.logger.Info("Ledger transaction created successfully",
		"transaction_id", ledgerTx.ID,
		"type", ledgerTx.TransactionType,
		"user_id", ledgerTx.UserID)

	return ledgerTx, nil
}

// updateAccountBalanceInTx updates an account balance within a database transaction
func (s *Service) updateAccountBalanceInTx(ctx context.Context, accountID uuid.UUID, entryType entities.EntryType, amount decimal.Decimal) error {
	// Get current balance
	currentBalance, err := s.ledgerRepo.GetAccountBalance(ctx, accountID)
	if err != nil {
		return fmt.Errorf("get account balance: %w", err)
	}

	// Calculate new balance
	var newBalance decimal.Decimal
	switch entryType {
	case entities.EntryTypeDebit:
		// Debit increases asset accounts
		newBalance = currentBalance.Add(amount)
	case entities.EntryTypeCredit:
		// Credit decreases asset accounts
		newBalance = currentBalance.Sub(amount)
	}

	// Ensure balance doesn't go negative
	if newBalance.IsNegative() {
		return fmt.Errorf("insufficient balance: current=%s, adjustment=%s %s",
			currentBalance.String(), amount.String(), entryType)
	}

	// Update balance
	if err := s.ledgerRepo.UpdateAccountBalance(ctx, accountID, newBalance); err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	return nil
}

// GetAccountBalance retrieves the current balance for an account
func (s *Service) GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	account, err := s.ledgerRepo.GetAccountByUserAndType(ctx, userID, accountType)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get account: %w", err)
	}

	return account.Balance, nil
}

// GetUserBalances retrieves all balances for a user
func (s *Service) GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error) {
	balances, err := s.ledgerRepo.GetUserBalances(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user balances: %w", err)
	}

	return balances, nil
}

// GetSystemBuffers retrieves system buffer balances
func (s *Service) GetSystemBuffers(ctx context.Context) (*entities.SystemBuffers, error) {
	buffers, err := s.ledgerRepo.GetSystemBuffers(ctx)
	if err != nil {
		return nil, fmt.Errorf("get system buffers: %w", err)
	}

	return buffers, nil
}

// GetOrCreateUserAccount ensures a user account exists
func (s *Service) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	// Determine currency based on account type
	currency := "USDC"
	if accountType == entities.AccountTypeFiatExposure {
		currency = "USD"
	}

	account, err := s.ledgerRepo.GetOrCreateUserAccount(ctx, userID, accountType, currency)
	if err != nil {
		return nil, fmt.Errorf("get or create user account: %w", err)
	}

	return account, nil
}

// GetSystemAccount retrieves a system-level account
func (s *Service) GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	account, err := s.ledgerRepo.GetSystemAccount(ctx, accountType)
	if err != nil {
		return nil, fmt.Errorf("get system account: %w", err)
	}

	return account, nil
}

// GetAccountByID retrieves an account by its ID
func (s *Service) GetAccountByID(ctx context.Context, accountID uuid.UUID) (*entities.LedgerAccount, error) {
	account, err := s.ledgerRepo.GetAccountByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account by id: %w", err)
	}
	return account, nil
}

// ReserveForInvestment reserves funds for an investment by moving from usdc_balance to pending_investment
func (s *Service) ReserveForInvestment(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user accounts
	usdcAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get usdc account: %w", err)
	}

	pendingAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypePendingInvestment)
	if err != nil {
		return fmt.Errorf("get pending account: %w", err)
	}

	// Check sufficient balance
	if usdcAccount.Balance.LessThan(amount) {
		return fmt.Errorf("insufficient USDC balance: have %s, need %s",
			usdcAccount.Balance.String(), amount.String())
	}

	// Create reservation transaction
	idempotencyKey := fmt.Sprintf("reserve-%s-%s-%d", userID.String(), amount.String(), time.Now().UnixNano())
	desc := "Reserve funds for investment"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   usdcAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   pendingAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create reservation transaction: %w", err)
	}

	s.logger.Info("Funds reserved for investment",
		"user_id", userID,
		"amount", amount.String())

	return nil
}

// ReleaseReservation releases reserved funds back to usdc_balance (e.g., on trade cancellation)
func (s *Service) ReleaseReservation(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Get user accounts
	usdcAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get usdc account: %w", err)
	}

	pendingAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypePendingInvestment)
	if err != nil {
		return fmt.Errorf("get pending account: %w", err)
	}

	// Check sufficient pending balance
	if pendingAccount.Balance.LessThan(amount) {
		return fmt.Errorf("insufficient pending balance: have %s, need %s",
			pendingAccount.Balance.String(), amount.String())
	}

	// Create release transaction
	idempotencyKey := fmt.Sprintf("release-%s-%s-%d", userID.String(), amount.String(), time.Now().UnixNano())
	desc := "Release reserved funds"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   pendingAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
			{
				AccountID:   usdcAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USDC",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create release transaction: %w", err)
	}

	s.logger.Info("Reserved funds released",
		"user_id", userID,
		"amount", amount.String())

	return nil
}

// ReverseTransaction creates compensating entries to reverse a transaction
func (s *Service) ReverseTransaction(ctx context.Context, originalTxID uuid.UUID, reason string) error {
	// Get original transaction
	originalTx, err := s.ledgerRepo.GetTransactionByID(ctx, originalTxID)
	if err != nil {
		return fmt.Errorf("get original transaction: %w", err)
	}

	if originalTx.Status == entities.TransactionStatusReversed {
		return fmt.Errorf("transaction already reversed")
	}

	// Get original entries
	entries, err := s.ledgerRepo.GetEntriesByTransactionID(ctx, originalTxID)
	if err != nil {
		return fmt.Errorf("get original entries: %w", err)
	}

	// Begin database transaction for atomicity
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	txCtx := context.WithValue(ctx, "db_tx", tx)

	// Mark original transaction as reversed first
	if err := s.ledgerRepo.UpdateTransactionStatus(txCtx, originalTxID, entities.TransactionStatusReversed); err != nil {
		return fmt.Errorf("update original transaction status: %w", err)
	}

	// Create reversal entries (flip debit/credit)
	reversalEntries := make([]entities.CreateEntryRequest, len(entries))
	for i, entry := range entries {
		var reversalType entities.EntryType
		if entry.EntryType == entities.EntryTypeDebit {
			reversalType = entities.EntryTypeCredit
		} else {
			reversalType = entities.EntryTypeDebit
		}

		desc := fmt.Sprintf("Reversal of transaction %s: %s", originalTxID.String(), reason)
		reversalEntries[i] = entities.CreateEntryRequest{
			AccountID:   entry.AccountID,
			EntryType:   reversalType,
			Amount:      entry.Amount,
			Currency:    entry.Currency,
			Description: &desc,
		}
	}

	// Create reversal transaction within same db transaction
	idempotencyKey := fmt.Sprintf("reversal-%s", originalTxID.String())
	desc := fmt.Sprintf("Reversal: %s", reason)

	req := &entities.CreateTransactionRequest{
		UserID:          originalTx.UserID,
		TransactionType: entities.TransactionTypeReversal,
		ReferenceID:     &originalTxID,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries:         reversalEntries,
	}

	// Note: CreateTransaction will use the existing tx from context
	_, err = s.CreateTransaction(txCtx, req)
	if err != nil {
		return fmt.Errorf("create reversal transaction: %w", err)
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reversal: %w", err)
	}

	s.logger.Info("Transaction reversed",
		"original_tx_id", originalTxID,
		"reason", reason)

	return nil
}

// GetTransactionHistory retrieves transaction history for a user
func (s *Service) GetTransactionHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.LedgerEntry, error) {
	// Get all user accounts
	accounts, err := s.ledgerRepo.GetUserAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user accounts: %w", err)
	}

	// Aggregate entries from all accounts
	var allEntries []*entities.LedgerEntry
	for _, account := range accounts {
		entries, err := s.ledgerRepo.GetEntriesByAccountID(ctx, account.ID, limit, offset)
		if err != nil {
			return nil, fmt.Errorf("get entries for account %s: %w", account.ID, err)
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

// GetSystemBufferBalance retrieves the balance for a system buffer account
func (s *Service) GetSystemBufferBalance(ctx context.Context, accountType string) (decimal.Decimal, error) {
	// Convert string to AccountType enum
	var accountTypeEnum entities.AccountType
	switch accountType {
	case "system_buffer_usdc", "liquidity_buffer":
		accountTypeEnum = entities.AccountTypeSystemBufferUSDC
	case "system_buffer_fiat", "fee_revenue":
		accountTypeEnum = entities.AccountTypeSystemBufferFiat
	case "broker_operational":
		accountTypeEnum = entities.AccountTypeBrokerOperational
	default:
		return decimal.Zero, fmt.Errorf("unknown account type: %s", accountType)
	}

	account, err := s.GetSystemAccount(ctx, accountTypeEnum)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get system account: %w", err)
	}

	return account.Balance, nil
}

// GetTotalUserFiatExposure calculates total USD exposure across all users
func (s *Service) GetTotalUserFiatExposure(ctx context.Context) (decimal.Decimal, error) {
	// Query sum of all user fiat exposure accounts from database
	query := `
		SELECT COALESCE(SUM(balance), 0) as total
		FROM ledger_accounts
		WHERE account_type = $1 AND user_id IS NOT NULL
	`
	
	var total decimal.Decimal
	err := s.db.QueryRowContext(ctx, query, entities.AccountTypeFiatExposure).Scan(&total)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get total fiat exposure: %w", err)
	}

	return total, nil
}

// RecordCardTransaction records a card transaction by debiting the spend balance
func (s *Service) RecordCardTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	// Get user's spend balance account (spending_balance for Smart Allocation Mode)
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spend account: %w", err)
	}

	// Check sufficient balance
	if spendAccount.Balance.LessThan(amount) {
		return fmt.Errorf("insufficient spend balance: have %s, need %s",
			spendAccount.Balance.String(), amount.String())
	}

	// Get system card settlement account (or create one)
	settlementAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeSystemBufferFiat)
	if err != nil {
		return fmt.Errorf("get settlement account: %w", err)
	}

	// Create card transaction
	idempotencyKey := fmt.Sprintf("card-tx-%s-%s-%d", userID.String(), reference, time.Now().UnixNano())
	desc := fmt.Sprintf("Card transaction: %s", reference)
	refType := "card_transaction"

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeCardPayment,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeCredit, // Debit from user's perspective
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   settlementAccount.ID,
				EntryType:   entities.EntryTypeDebit, // Credit to settlement
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("create card transaction: %w", err)
	}

	s.logger.Info("Card transaction recorded",
		"user_id", userID,
		"amount", amount.String(),
		"reference", reference)

	return nil
}

func stringPtr(s string) *string {
	return &s
}
