package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// Service handles ledger operations using double-entry bookkeeping
type Service struct {
	ledgerRepo *repositories.LedgerRepository
	db         *sqlx.DB
	logger     *logger.Logger
	stashLock  StashLockChecker
}

// StashLockChecker enforces the 90-day lock / 7-day window rule.
type StashLockChecker interface {
	CanWithdraw(ctx context.Context, userID uuid.UUID) (bool, time.Time, error)
}

// SetStashLockChecker wires stash lock enforcement into the ledger.
func (s *Service) SetStashLockChecker(c StashLockChecker) {
	s.stashLock = c
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
	txCtx := repositories.WithTx(ctx, tx)

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
	// Acquire row-level lock and get current balance atomically
	currentBalance, err := s.ledgerRepo.GetAccountBalanceForUpdate(ctx, accountID)
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

	// Ensure balance doesn't go negative (skip for system accounts — they track external reserves)
	if newBalance.IsNegative() {
		account, accountErr := s.ledgerRepo.GetAccountByID(ctx, accountID)
		if accountErr != nil || !account.AccountType.IsSystemAccountType() {
			return fmt.Errorf("insufficient balance: current=%s, adjustment=%s %s",
				currentBalance.String(), amount.String(), entryType)
		}
		// SECURITY: Solvency guard — system accounts cannot go below -$100,000.
		// This prevents unbounded liability from bugs in yield distribution or reconciliation.
		maxDeficit := decimal.NewFromInt(-100000)
		if newBalance.LessThan(maxDeficit) {
			return fmt.Errorf("system account solvency limit breached: balance=%s exceeds max deficit=%s",
				newBalance.String(), maxDeficit.String())
		}
	}

	// Update balance
	if err := s.ledgerRepo.UpdateAccountBalance(ctx, accountID, newBalance); err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	return nil
}

// GetAccountBalance retrieves the current balance for an account
func (s *Service) GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	if s == nil || s.ledgerRepo == nil {
		return decimal.Zero, fmt.Errorf("ledger repository not configured")
	}

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

// ReconcileBalance directly sets a user's account balance (for admin reconciliation)
// Uses SELECT FOR UPDATE to prevent TOCTOU race conditions
func (s *Service) ReconcileBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, newBalance decimal.Decimal) error {
	// Validate accountType is valid (spending or savings)
	if accountType != entities.AccountTypeSpendingBalance && accountType != entities.AccountTypeStashBalance {
		return fmt.Errorf("invalid account type: %s (must be spending_balance or stash_balance)", accountType)
	}

	// Validate newBalance is not negative
	if newBalance.IsNegative() {
		return fmt.Errorf("reconciliation cannot set negative balance: %s", newBalance.String())
	}

	// Begin transaction for atomic read-modify-write
	txCtx, err := s.ledgerRepo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer s.ledgerRepo.RollbackTx(txCtx)

	// Use SELECT FOR UPDATE to lock the row and prevent concurrent modifications
	account, err := s.ledgerRepo.GetAccountByUserAndTypeForUpdate(txCtx, userID, accountType)
	if err != nil {
		return fmt.Errorf("get account for update: %w", err)
	}

	// Get old balance for audit trail
	oldBalance := account.Balance

	// Log with Warn level since this is an admin override action
	diff := newBalance.Sub(oldBalance)
	s.logger.Warn("Reconciling balance (admin override)",
		"user_id", userID.String(),
		"account_type", string(accountType),
		"old_balance", oldBalance.String(),
		"new_balance", newBalance.String(),
		"difference", diff.String())

	// Update balance within the transaction
	if err := s.ledgerRepo.UpdateAccountBalance(txCtx, account.ID, newBalance); err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	// Commit transaction
	if err := s.ledgerRepo.CommitTx(txCtx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
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

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

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

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

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

	txCtx := repositories.WithTx(ctx, tx)

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

	// Balance check removed: CreateTransaction uses SELECT FOR UPDATE internally,
	// which atomically checks and prevents overdraft. Pre-flight check was a TOCTOU race.

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

// TransferSpendingToStash moves funds from spending_balance to stash_balance.
// Used for roundup collection: the spare change is already in spending and should move to stash.
func (s *Service) TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	desc := fmt.Sprintf("Roundup collection: %s", amount.String())
	refType := "roundup_collection"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeCredit, // debit spending
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeDebit, // credit stash
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, req)
	return err
}

// TransferStashToSpending moves funds from stash to spending.
func (s *Service) TransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}

	// Enforce stash lock: transfers from stash only allowed during the 7-day window.
	if s.stashLock != nil {
		canWithdraw, _, err := s.stashLock.CanWithdraw(ctx, userID)
		if err != nil {
			return fmt.Errorf("stash lock check failed: %w", err)
		}
		if !canWithdraw {
			return fmt.Errorf("stash funds are locked: no active withdrawal window (funds lock for 90 days, then a 7-day window opens)")
		}
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	desc := fmt.Sprintf("Transfer stash to spending: %s", amount.String())
	refType := "miriam_transfer"
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit, // debit stash
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeDebit, // credit spending
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	_, err = s.CreateTransaction(ctx, txReq)
	return err
}

// AdminTransferStashToSpending moves funds from stash to spending, bypassing the stash lock.
// This is an admin-only operation with a distinct reference type for audit trail.
func (s *Service) AdminTransferStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey, adminReason string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid transfer amount: %s", amount.String())
	}
	// SECURITY: Enforce cap at service level — defense in depth regardless of caller.
	maxAdminTransfer := decimal.NewFromInt(500)
	if amount.GreaterThan(maxAdminTransfer) {
		return fmt.Errorf("admin transfer amount %s exceeds maximum of $500", amount.String())
	}

	spendAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return fmt.Errorf("get spending account: %w", err)
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}

	reason := adminReason
	if len(reason) > 255 {
		reason = reason[:255]
	}
	desc := fmt.Sprintf("Admin stash-to-spend transfer: %s (reason: %s)", amount.String(), reason)
	refType := "admin_stash_transfer"
	txReq := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{
				AccountID:   stashAccount.ID,
				EntryType:   entities.EntryTypeCredit,
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
			{
				AccountID:   spendAccount.ID,
				EntryType:   entities.EntryTypeDebit,
				Amount:      amount,
				Currency:    "USD",
				Description: &desc,
			},
		},
	}

	s.logger.Warn("Admin stash-to-spend transfer (bypasses stash lock)",
		"user_id", userID.String(),
		"amount", amount.String(),
		"reason", adminReason,
	)

	_, err = s.CreateTransaction(ctx, txReq)
	return err
}

// CreditStash credits a user's stash_balance from the system USDC buffer.
// Used for yield distribution payouts.
func (s *Service) CreditStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if amount.IsZero() || amount.IsNegative() {
		return fmt.Errorf("invalid credit amount: %s (must be positive)", amount.String())
	}
	stashAccount, err := s.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return fmt.Errorf("get stash account: %w", err)
	}
	systemAccount, err := s.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system buffer account: %w", err)
	}

	refType := "yield_distribution"
	// Include userID + description (which contains distributionID) for per-distribution uniqueness.
	idempotencyKey := fmt.Sprintf("yield-credit-%s-%s", userID, description)
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		ReferenceType:   &refType,
		IdempotencyKey:  idempotencyKey,
		Description:     &description,
		Entries: []entities.CreateEntryRequest{
			{AccountID: stashAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &description},
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &description},
		},
	}
	_, err = s.CreateTransaction(ctx, req)
	return err
}
