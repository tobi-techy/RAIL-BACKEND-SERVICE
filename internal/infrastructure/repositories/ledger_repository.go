package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// LedgerRepository handles ledger data persistence
type LedgerRepository struct {
	db *sqlx.DB
}

// NewLedgerRepository creates a new ledger repository
func NewLedgerRepository(db *sqlx.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// BeginTx starts a new database transaction
func (r *LedgerRepository) BeginTx(ctx context.Context) (context.Context, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return context.WithValue(ctx, "db_tx", tx), nil
}

// CommitTx commits the transaction in the context
func (r *LedgerRepository) CommitTx(ctx context.Context) error {
	tx := txFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("no transaction in context")
	}
	return tx.Commit()
}

// RollbackTx rolls back the transaction in the context
func (r *LedgerRepository) RollbackTx(ctx context.Context) error {
	tx := txFromContext(ctx)
	if tx == nil {
		return nil // No transaction to rollback
	}
	return tx.Rollback()
}

// GetAccountByUserAndTypeForUpdate retrieves a ledger account with row-level lock for atomic updates
func (r *LedgerRepository) GetAccountByUserAndTypeForUpdate(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, balance, currency, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id = $1 AND account_type = $2
		FOR UPDATE
	`
	var account entities.LedgerAccount
	err := r.queryRowxContext(ctx, query, userID, accountType).Scan(
		&account.ID,
		&account.UserID,
		&account.AccountType,
		&account.Balance,
		&account.Currency,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("account not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get account for update: %w", err)
	}
	return &account, nil
}

// txFromContext extracts a sqlx transaction from context when present.
func txFromContext(ctx context.Context) *sqlx.Tx {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value("db_tx").(*sqlx.Tx)
	return tx
}

func (r *LedgerRepository) queryRowxContext(ctx context.Context, query string, args ...interface{}) *sqlx.Row {
	if tx := txFromContext(ctx); tx != nil {
		return tx.QueryRowxContext(ctx, query, args...)
	}
	return r.db.QueryRowxContext(ctx, query, args...)
}

func (r *LedgerRepository) execContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if tx := txFromContext(ctx); tx != nil {
		return tx.ExecContext(ctx, query, args...)
	}
	return r.db.ExecContext(ctx, query, args...)
}

func (r *LedgerRepository) getContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	if tx := txFromContext(ctx); tx != nil {
		return tx.GetContext(ctx, dest, query, args...)
	}
	return r.db.GetContext(ctx, dest, query, args...)
}

func (r *LedgerRepository) selectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	if tx := txFromContext(ctx); tx != nil {
		return tx.SelectContext(ctx, dest, query, args...)
	}
	return r.db.SelectContext(ctx, dest, query, args...)
}

func (r *LedgerRepository) queryxContext(ctx context.Context, query string, args ...interface{}) (*sqlx.Rows, error) {
	if tx := txFromContext(ctx); tx != nil {
		return tx.QueryxContext(ctx, query, args...)
	}
	return r.db.QueryxContext(ctx, query, args...)
}

// ===== Account Operations =====

// CreateAccount creates a new ledger account
func (r *LedgerRepository) CreateAccount(ctx context.Context, account *entities.LedgerAccount) error {
	if err := account.Validate(); err != nil {
		return fmt.Errorf("validate account: %w", err)
	}

	query := `
		INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`

	now := time.Now()
	account.CreatedAt = now
	account.UpdatedAt = now

	err := r.queryRowxContext(
		ctx,
		query,
		account.ID,
		account.UserID,
		account.AccountType,
		account.Currency,
		account.Balance,
		account.CreatedAt,
		account.UpdatedAt,
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code == "23505" { // unique_violation
				return fmt.Errorf("account already exists: %w", err)
			}
		}
		return fmt.Errorf("create account: %w", err)
	}

	return nil
}

// GetAccountByID retrieves an account by ID
func (r *LedgerRepository) GetAccountByID(ctx context.Context, accountID uuid.UUID) (*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE id = $1
	`

	var account entities.LedgerAccount
	err := r.getContext(ctx, &account, query, accountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("account not found: %w", err)
		}
		return nil, fmt.Errorf("get account: %w", err)
	}

	return &account, nil
}

// GetAccountByUserAndType retrieves an account by user ID and account type
func (r *LedgerRepository) GetAccountByUserAndType(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id = $1 AND account_type = $2
	`

	var account entities.LedgerAccount
	err := r.getContext(ctx, &account, query, userID, accountType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("account not found: %w", err)
		}
		return nil, fmt.Errorf("get account: %w", err)
	}

	return &account, nil
}

// GetSystemAccount retrieves a system-level account by type
func (r *LedgerRepository) GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id IS NULL AND account_type = $1
	`

	var account entities.LedgerAccount
	err := r.getContext(ctx, &account, query, accountType)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("system account not found: %w", err)
		}
		return nil, fmt.Errorf("get system account: %w", err)
	}

	return &account, nil
}

// GetUserAccounts retrieves all accounts for a user
func (r *LedgerRepository) GetUserAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id = $1
		ORDER BY account_type
	`

	var accounts []*entities.LedgerAccount
	err := r.selectContext(ctx, &accounts, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get user accounts: %w", err)
	}

	return accounts, nil
}

// GetOrCreateUserAccount retrieves or creates a user account
func (r *LedgerRepository) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, currency string) (*entities.LedgerAccount, error) {
	// Try to get existing account
	account, err := r.GetAccountByUserAndType(ctx, userID, accountType)
	if err == nil {
		return account, nil
	}

	// Create new account if not found
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get account: %w", err)
	}

	account = &entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: accountType,
		Currency:    currency,
		Balance:     decimal.Zero,
	}

	if err := r.CreateAccount(ctx, account); err != nil {
		// Handle concurrent create race: another request may have created it.
		existing, getErr := r.GetAccountByUserAndType(ctx, userID, accountType)
		if getErr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("create account: %w", err)
	}

	return account, nil
}

// UpdateAccountBalance updates an account balance
// This should only be called within a transaction by the ledger service
func (r *LedgerRepository) UpdateAccountBalance(ctx context.Context, accountID uuid.UUID, newBalance decimal.Decimal) error {
	query := `
		UPDATE ledger_accounts
		SET balance = $1, updated_at = $2
		WHERE id = $3
	`

	result, err := r.execContext(ctx, query, newBalance, time.Now(), accountID)
	if err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("account not found")
	}

	return nil
}

// UpdateAccountBalanceByUserAndType updates balance for a specific user and account type
func (r *LedgerRepository) UpdateAccountBalanceByUserAndType(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, newBalance decimal.Decimal) error {
	query := `
		UPDATE ledger_accounts
		SET balance = $1, updated_at = $2
		WHERE user_id = $3 AND account_type = $4
	`

	result, err := r.execContext(ctx, query, newBalance, time.Now(), userID, accountType)
	if err != nil {
		return fmt.Errorf("update account balance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("account not found for user %s type %s", userID, accountType)
	}

	return nil
}

// ===== Transaction Operations =====

// CreateTransaction creates a new ledger transaction
func (r *LedgerRepository) CreateTransaction(ctx context.Context, tx *entities.LedgerTransaction) error {
	if err := tx.Validate(); err != nil {
		return fmt.Errorf("validate transaction: %w", err)
	}

	metadataJSON, err := json.Marshal(tx.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	query := `
		INSERT INTO ledger_transactions (
			id, user_id, transaction_type, reference_id, reference_type,
			status, idempotency_key, description, metadata, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at
	`

	err = r.queryRowxContext(
		ctx,
		query,
		tx.ID,
		tx.UserID,
		tx.TransactionType,
		tx.ReferenceID,
		tx.ReferenceType,
		tx.Status,
		tx.IdempotencyKey,
		tx.Description,
		metadataJSON,
		tx.CreatedAt,
	).Scan(&tx.CreatedAt)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code == "23505" { // unique_violation on idempotency_key
				return fmt.Errorf("transaction with idempotency key already exists: %w", err)
			}
		}
		return fmt.Errorf("create transaction: %w", err)
	}

	return nil
}

// GetTransactionByID retrieves a transaction by ID
func (r *LedgerRepository) GetTransactionByID(ctx context.Context, txID uuid.UUID) (*entities.LedgerTransaction, error) {
	query := `
		SELECT id, user_id, transaction_type, reference_id, reference_type,
		       status, idempotency_key, description, metadata, created_at, completed_at
		FROM ledger_transactions
		WHERE id = $1
	`

	var tx entities.LedgerTransaction
	var metadataJSON []byte

	err := r.queryRowxContext(ctx, query, txID).Scan(
		&tx.ID,
		&tx.UserID,
		&tx.TransactionType,
		&tx.ReferenceID,
		&tx.ReferenceType,
		&tx.Status,
		&tx.IdempotencyKey,
		&tx.Description,
		&metadataJSON,
		&tx.CreatedAt,
		&tx.CompletedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("transaction not found: %w", err)
		}
		return nil, fmt.Errorf("get transaction: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &tx.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &tx, nil
}

// GetTransactionByIdempotencyKey retrieves a transaction by idempotency key
func (r *LedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*entities.LedgerTransaction, error) {
	query := `
		SELECT id, user_id, transaction_type, reference_id, reference_type,
		       status, idempotency_key, description, metadata, created_at, completed_at
		FROM ledger_transactions
		WHERE idempotency_key = $1
	`

	var tx entities.LedgerTransaction
	var metadataJSON []byte

	err := r.queryRowxContext(ctx, query, key).Scan(
		&tx.ID,
		&tx.UserID,
		&tx.TransactionType,
		&tx.ReferenceID,
		&tx.ReferenceType,
		&tx.Status,
		&tx.IdempotencyKey,
		&tx.Description,
		&metadataJSON,
		&tx.CreatedAt,
		&tx.CompletedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found is valid for idempotency check
		}
		return nil, fmt.Errorf("get transaction by idempotency key: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &tx.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return &tx, nil
}

// UpdateTransactionStatus updates a transaction status
func (r *LedgerRepository) UpdateTransactionStatus(ctx context.Context, txID uuid.UUID, status entities.TransactionStatus) error {
	var completedAt *time.Time
	if status == entities.TransactionStatusCompleted {
		now := time.Now()
		completedAt = &now
	}

	query := `
		UPDATE ledger_transactions
		SET status = $1, completed_at = $2
		WHERE id = $3
	`

	result, err := r.execContext(ctx, query, status, completedAt, txID)
	if err != nil {
		return fmt.Errorf("update transaction status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("transaction not found")
	}

	return nil
}

// ===== Entry Operations =====

// CreateEntry creates a new ledger entry
func (r *LedgerRepository) CreateEntry(ctx context.Context, entry *entities.LedgerEntry) error {
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("validate entry: %w", err)
	}

	metadataJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	query := `
		INSERT INTO ledger_entries (
			id, transaction_id, account_id, entry_type, amount, currency,
			description, metadata, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at
	`

	err = r.queryRowxContext(
		ctx,
		query,
		entry.ID,
		entry.TransactionID,
		entry.AccountID,
		entry.EntryType,
		entry.Amount,
		entry.Currency,
		entry.Description,
		metadataJSON,
		entry.CreatedAt,
	).Scan(&entry.CreatedAt)

	if err != nil {
		return fmt.Errorf("create entry: %w", err)
	}

	return nil
}

// GetEntriesByTransactionID retrieves all entries for a transaction
func (r *LedgerRepository) GetEntriesByTransactionID(ctx context.Context, txID uuid.UUID) ([]*entities.LedgerEntry, error) {
	query := `
		SELECT id, transaction_id, account_id, entry_type, amount, currency,
		       description, metadata, created_at
		FROM ledger_entries
		WHERE transaction_id = $1
		ORDER BY created_at
	`

	rows, err := r.queryxContext(ctx, query, txID)
	if err != nil {
		return nil, fmt.Errorf("query entries: %w", err)
	}
	defer rows.Close()

	var entries []*entities.LedgerEntry
	for rows.Next() {
		var entry entities.LedgerEntry
		var metadataJSON []byte

		err := rows.Scan(
			&entry.ID,
			&entry.TransactionID,
			&entry.AccountID,
			&entry.EntryType,
			&entry.Amount,
			&entry.Currency,
			&entry.Description,
			&metadataJSON,
			&entry.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		entries = append(entries, &entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return entries, nil
}

// GetEntriesByAccountID retrieves all entries for an account
func (r *LedgerRepository) GetEntriesByAccountID(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]*entities.LedgerEntry, error) {
	query := `
		SELECT id, transaction_id, account_id, entry_type, amount, currency,
		       description, metadata, created_at
		FROM ledger_entries
		WHERE account_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.queryxContext(ctx, query, accountID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query entries: %w", err)
	}
	defer rows.Close()

	var entries []*entities.LedgerEntry
	for rows.Next() {
		var entry entities.LedgerEntry
		var metadataJSON []byte

		err := rows.Scan(
			&entry.ID,
			&entry.TransactionID,
			&entry.AccountID,
			&entry.EntryType,
			&entry.Amount,
			&entry.Currency,
			&entry.Description,
			&metadataJSON,
			&entry.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}

		entries = append(entries, &entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return entries, nil
}

// ===== Balance Queries =====

// GetAccountBalance retrieves the current balance for an account
func (r *LedgerRepository) GetAccountBalance(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	query := `SELECT balance FROM ledger_accounts WHERE id = $1`

	var balance decimal.Decimal
	err := r.queryRowxContext(ctx, query, accountID).Scan(&balance)
	if err != nil {
		if err == sql.ErrNoRows {
			return decimal.Zero, fmt.Errorf("account not found")
		}
		return decimal.Zero, fmt.Errorf("get account balance: %w", err)
	}

	return balance, nil
}

// GetUserBalances retrieves all balances for a user
func (r *LedgerRepository) GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error) {
	query := `
		SELECT account_type, balance, updated_at
		FROM ledger_accounts
		WHERE user_id = $1
	`

	rows, err := r.queryxContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query user balances: %w", err)
	}
	defer rows.Close()

	balances := &entities.UserBalances{
		UserID:            userID,
		USDCBalance:       decimal.Zero,
		SpendingBalance:   decimal.Zero,
		StashBalance:      decimal.Zero,
		FiatExposure:      decimal.Zero,
		PendingInvestment: decimal.Zero,
	}

	var latestUpdate time.Time
	for rows.Next() {
		var accountType entities.AccountType
		var balance decimal.Decimal
		var updatedAt time.Time

		if err := rows.Scan(&accountType, &balance, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}

		switch accountType {
		case entities.AccountTypeUSDCBalance:
			balances.USDCBalance = balance
		case entities.AccountTypeSpendingBalance:
			balances.SpendingBalance = balance
		case entities.AccountTypeStashBalance:
			balances.StashBalance = balance
		case entities.AccountTypeFiatExposure:
			balances.FiatExposure = balance
		case entities.AccountTypePendingInvestment:
			balances.PendingInvestment = balance
		}

		if updatedAt.After(latestUpdate) {
			latestUpdate = updatedAt
		}
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	balances.UpdatedAt = latestUpdate
	balances.TotalUSDEquivalent = balances.CalculateTotalUSD()

	return balances, nil
}

// GetSystemBuffers retrieves all system buffer balances
func (r *LedgerRepository) GetSystemBuffers(ctx context.Context) (*entities.SystemBuffers, error) {
	query := `
		SELECT account_type, balance, updated_at
		FROM ledger_accounts
		WHERE user_id IS NULL 
		  AND account_type IN ('system_buffer_usdc', 'system_buffer_fiat', 'broker_operational')
	`

	rows, err := r.queryxContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query system buffers: %w", err)
	}
	defer rows.Close()

	buffers := &entities.SystemBuffers{
		BufferUSDC:        decimal.Zero,
		BufferFiat:        decimal.Zero,
		BrokerOperational: decimal.Zero,
	}

	var latestUpdate time.Time
	for rows.Next() {
		var accountType entities.AccountType
		var balance decimal.Decimal
		var updatedAt time.Time

		if err := rows.Scan(&accountType, &balance, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan buffer: %w", err)
		}

		switch accountType {
		case entities.AccountTypeSystemBufferUSDC:
			buffers.BufferUSDC = balance
		case entities.AccountTypeSystemBufferFiat:
			buffers.BufferFiat = balance
		case entities.AccountTypeBrokerOperational:
			buffers.BrokerOperational = balance
		}

		if updatedAt.After(latestUpdate) {
			latestUpdate = updatedAt
		}
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	buffers.UpdatedAt = latestUpdate

	return buffers, nil
}

// ===== Reconciliation Methods =====

// GetTotalDebitsAndCredits returns the sum of all debits and credits in the ledger
func (r *LedgerRepository) GetTotalDebitsAndCredits(ctx context.Context) (totalDebits, totalCredits decimal.Decimal, err error) {
	query := `
		SELECT 
			COALESCE(SUM(CASE WHEN entry_type = 'debit' THEN amount ELSE 0 END), 0) as total_debits,
			COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE 0 END), 0) as total_credits
		FROM ledger_entries
	`

	var debitsStr, creditsStr string
	err = r.queryRowxContext(ctx, query).Scan(&debitsStr, &creditsStr)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("get total debits and credits: %w", err)
	}

	totalDebits, err = decimal.NewFromString(debitsStr)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("parse debits: %w", err)
	}

	totalCredits, err = decimal.NewFromString(creditsStr)
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("parse credits: %w", err)
	}

	return totalDebits, totalCredits, nil
}

// CountOrphanedEntries returns the count of ledger entries without matching transactions
func (r *LedgerRepository) CountOrphanedEntries(ctx context.Context) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM ledger_entries
		WHERE transaction_id IS NULL
	`

	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count orphaned entries: %w", err)
	}

	return count, nil
}

// CountInvalidTransactions returns the count of transactions without exactly 2 entries
func (r *LedgerRepository) CountInvalidTransactions(ctx context.Context) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM (
			SELECT transaction_id
			FROM ledger_entries
			WHERE transaction_id IS NOT NULL
			GROUP BY transaction_id
			HAVING COUNT(*) != 2
		) as invalid_txs
	`

	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count invalid transactions: %w", err)
	}

	return count, nil
}

// GetTotalDepositEntries returns the sum of all deposit-related ledger entries
func (r *LedgerRepository) GetTotalDepositEntries(ctx context.Context) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries le
		JOIN ledger_transactions lt ON le.transaction_id = lt.id
		WHERE lt.transaction_type = 'deposit'
		  AND le.entry_type = 'credit'
	`

	var totalStr string
	err := r.queryRowxContext(ctx, query).Scan(&totalStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get total deposit entries: %w", err)
	}

	total, err := decimal.NewFromString(totalStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse total: %w", err)
	}

	return total, nil
}

// GetTotalWithdrawalEntries returns the sum of all withdrawal-related ledger entries
func (r *LedgerRepository) GetTotalWithdrawalEntries(ctx context.Context) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries le
		JOIN ledger_transactions lt ON le.transaction_id = lt.id
		WHERE lt.transaction_type = 'withdrawal'
		  AND le.entry_type = 'debit'
	`

	var totalStr string
	err := r.queryRowxContext(ctx, query).Scan(&totalStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get total withdrawal entries: %w", err)
	}

	total, err := decimal.NewFromString(totalStr)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse total: %w", err)
	}

	return total, nil
}
