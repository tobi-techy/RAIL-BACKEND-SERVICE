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

type contextKey string

const txContextKey contextKey = "db_tx"

// NewLedgerRepository creates a new ledger repository
func NewLedgerRepository(db *sqlx.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// BeginTx starts a new database transaction
func (r *LedgerRepository) BeginTx(ctx context.Context) (context.Context, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: false})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return context.WithValue(ctx, txContextKey, tx), nil
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

// HasTx reports whether the context already carries an active ledger transaction.
// Callers use this to decide whether to open a new transaction or reuse the
// existing one (preventing accidental nested/second connections).
func HasTx(ctx context.Context) bool {
	return txFromContext(ctx) != nil
}

// IsSerializationFailure reports whether err is a Postgres serialization
// failure (40001) or deadlock (40P01). Both are safe to retry: the whole
// transaction was rolled back by the server, so re-running the unit of work
// from scratch is correct and will not double-apply balances.
func IsSerializationFailure(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		// 40001 = serialization_failure, 40P01 = deadlock_detected
		return pqErr.Code == "40001" || pqErr.Code == "40P01"
	}
	return false
}

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation (23505), optionally scoped to a specific constraint name.
// Pass an empty constraint to match any unique violation.
func IsUniqueViolation(err error, constraint string) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		if pqErr.Code != "23505" {
			return false
		}
		return constraint == "" || pqErr.Constraint == constraint
	}
	return false
}

// txFromContext extracts a sqlx transaction from context when present.
func txFromContext(ctx context.Context) *sqlx.Tx {
	if ctx == nil {
		return nil
	}
	tx, _ := ctx.Value(txContextKey).(*sqlx.Tx)
	return tx
}

// WithTx embeds a sqlx.Tx into the context using the correct typed key.
// Use this in the ledger service instead of context.WithValue with a plain string.
func WithTx(ctx context.Context, tx *sqlx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey, tx)
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
		INSERT INTO ledger_accounts (id, user_id, account_type, goal_id, currency, balance, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
		account.GoalID,
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

// UpdateAccountBalanceGuarded updates an account balance using an optimistic
// compare-and-set on the expected (previously read) balance. It is a
// defense-in-depth complement to the pessimistic SELECT ... FOR UPDATE path:
// the row lock already serializes writers, but this CAS guarantees that the
// balance has not changed between the locked read and the write even if a
// future code path forgets to take the lock. A zero-row update means the
// balance was modified concurrently and the caller should abort/retry.
func (r *LedgerRepository) UpdateAccountBalanceGuarded(ctx context.Context, accountID uuid.UUID, expectedBalance, newBalance decimal.Decimal) error {
	query := `
		UPDATE ledger_accounts
		SET balance = $1, updated_at = $2
		WHERE id = $3 AND balance = $4
	`

	result, err := r.execContext(ctx, query, newBalance, time.Now(), accountID, expectedBalance)
	if err != nil {
		return fmt.Errorf("update account balance (guarded): %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Either the account vanished or the balance changed under us. Under
		// FOR UPDATE this should be impossible; treat it as a concurrency
		// violation so the caller aborts rather than silently losing a write.
		return fmt.Errorf("concurrent balance modification detected for account %s (expected balance %s)", accountID, expectedBalance.String())
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
			status, idempotency_key, description, metadata,
			previous_transaction_hash, transaction_hash, initiated_by, reason,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
		tx.PreviousTransactionHash,
		tx.TransactionHash,
		tx.InitiatedBy,
		tx.Reason,
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
		       status, idempotency_key, description, metadata,
		       previous_transaction_hash, transaction_hash, initiated_by, reason,
		       created_at, completed_at
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
		&tx.PreviousTransactionHash,
		&tx.TransactionHash,
		&tx.InitiatedBy,
		&tx.Reason,
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
		       status, idempotency_key, description, metadata,
		       previous_transaction_hash, transaction_hash, initiated_by, reason,
		       created_at, completed_at
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
		&tx.PreviousTransactionHash,
		&tx.TransactionHash,
		&tx.InitiatedBy,
		&tx.Reason,
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

// GetAccountBalanceForUpdate retrieves the balance with a row-level lock (SELECT FOR UPDATE).
// Must be called within a database transaction to prevent concurrent balance modifications.
func (r *LedgerRepository) GetAccountBalanceForUpdate(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	if txFromContext(ctx) == nil {
		return decimal.Zero, fmt.Errorf("GetAccountBalanceForUpdate must be called within a transaction")
	}

	query := `SELECT balance FROM ledger_accounts WHERE id = $1 FOR UPDATE`

	var balance decimal.Decimal
	err := r.queryRowxContext(ctx, query, accountID).Scan(&balance)
	if err != nil {
		if err == sql.ErrNoRows {
			return decimal.Zero, fmt.Errorf("account not found")
		}
		return decimal.Zero, fmt.Errorf("get account balance for update: %w", err)
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
		GoalBalance:       decimal.Zero,
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
		case entities.AccountTypeGoalBalance:
			balances.GoalBalance = balances.GoalBalance.Add(balance)
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

// GetTotalStashBalance returns the sum of all users' stash_balance ledger accounts.
func (r *LedgerRepository) GetTotalStashBalance(ctx context.Context) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.QueryRowxContext(ctx,
		`SELECT COALESCE(SUM(balance), 0) FROM ledger_accounts WHERE account_type = 'stash_balance'`,
	).Scan(&total)
	return total, err
}

// GetOrCreateGoalAccount retrieves or creates a goal_balance account for a user+goal pair.
func (r *LedgerRepository) GetOrCreateGoalAccount(ctx context.Context, userID, goalID uuid.UUID) (*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, goal_id, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id = $1 AND account_type = 'goal_balance' AND goal_id = $2
	`
	var account entities.LedgerAccount
	err := r.getContext(ctx, &account, query, userID, goalID)
	if err == nil {
		return &account, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get goal account: %w", err)
	}

	account = entities.LedgerAccount{
		ID:          uuid.New(),
		UserID:      &userID,
		AccountType: entities.AccountTypeGoalBalance,
		GoalID:      &goalID,
		Currency:    "USDC",
		Balance:     decimal.Zero,
	}
	if err := r.CreateAccount(ctx, &account); err != nil {
		// Race condition: another request created it
		var existing entities.LedgerAccount
		if getErr := r.getContext(ctx, &existing, query, userID, goalID); getErr == nil {
			return &existing, nil
		}
		return nil, fmt.Errorf("create goal account: %w", err)
	}
	return &account, nil
}

// GetGoalAccounts returns all goal_balance accounts for a user.
func (r *LedgerRepository) GetGoalAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.LedgerAccount, error) {
	query := `
		SELECT id, user_id, account_type, goal_id, currency, balance, created_at, updated_at
		FROM ledger_accounts
		WHERE user_id = $1 AND account_type = 'goal_balance'
		ORDER BY created_at
	`
	var accounts []*entities.LedgerAccount
	err := r.selectContext(ctx, &accounts, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get goal accounts: %w", err)
	}
	return accounts, nil
}

// GetTotalGoalAllocated returns the total balance across all goal accounts for a user.
func (r *LedgerRepository) GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.queryRowxContext(ctx,
		`SELECT COALESCE(SUM(balance), 0) FROM ledger_accounts WHERE user_id = $1 AND account_type = 'goal_balance'`,
		userID,
	).Scan(&total)
	return total, err
}

// GetProtectedGoalAllocated returns the total balance of goal accounts whose shared_goal is marked as protected.
func (r *LedgerRepository) GetProtectedGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.queryRowxContext(ctx,
		`SELECT COALESCE(SUM(la.balance), 0)
		 FROM ledger_accounts la
		 JOIN shared_goals sg ON sg.id = la.goal_id
		 WHERE la.user_id = $1 AND la.account_type = 'goal_balance' AND sg.protected = true`,
		userID,
	).Scan(&total)
	return total, err
}

// OutboxRecord represents a row in the ledger_outbox table.
type OutboxRecord struct {
	ID            uuid.UUID       `db:"id"`
	EventType     string          `db:"event_type"`
	AggregateID   uuid.UUID       `db:"aggregate_id"`
	AggregateType string          `db:"aggregate_type"`
	Payload       json.RawMessage `db:"payload"`
	RetryCount    int             `db:"retry_count"`
	LastError     *string         `db:"last_error"`
	CreatedAt     time.Time       `db:"created_at"`
	PublishedAt   *time.Time      `db:"published_at"`
}

// ===== Outbox Operations =====

// InsertOutboxRecord inserts a record into the ledger_outbox table within the
// current transaction (must be called inside a ledger transaction).
func (r *LedgerRepository) InsertOutboxRecord(ctx context.Context, eventType string, aggregateID uuid.UUID, aggregateType string, payload json.RawMessage) error {
	query := `
		INSERT INTO ledger_outbox (id, event_type, aggregate_id, aggregate_type, payload, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW())
	`
	_, err := r.execContext(ctx, query, eventType, aggregateID, aggregateType, payload)
	if err != nil {
		return fmt.Errorf("insert outbox record: %w", err)
	}
	return nil
}

// GetUnpublishedOutboxEvents returns unpublished outbox records without claiming.
// Use for monitoring/debugging only. Production publishing must use
// ClaimUnpublishedOutbox to avoid TOCTOU races between workers.
func (r *LedgerRepository) GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxRecord, error) {
	query := `
		SELECT id, event_type, aggregate_id, aggregate_type, payload,
		       retry_count, last_error, created_at, published_at
		FROM ledger_outbox
		WHERE published_at IS NULL
		ORDER BY created_at
		LIMIT $1
	`
	rows, err := r.queryxContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("get unpublished outbox events: %w", err)
	}
	defer rows.Close()

	var records []OutboxRecord
	for rows.Next() {
		var rec OutboxRecord
		if err := rows.Scan(&rec.ID, &rec.EventType, &rec.AggregateID, &rec.AggregateType, &rec.Payload,
			&rec.RetryCount, &rec.LastError, &rec.CreatedAt, &rec.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan outbox record: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// ClaimUnpublishedOutbox atomically claims a batch of unpublished outbox records
// using FOR UPDATE SKIP LOCKED, so multiple concurrent publisher workers can
// coexist without double-publishing. Must be called within a transaction.
func (r *LedgerRepository) ClaimUnpublishedOutbox(ctx context.Context, batchSize int, maxRetries int) ([]OutboxRecord, error) {
	if txFromContext(ctx) == nil {
		return nil, fmt.Errorf("ClaimUnpublishedOutbox must be called within a transaction")
	}

	query := `
		UPDATE ledger_outbox
		SET published_at = NOW()
		WHERE id IN (
			SELECT id
			FROM ledger_outbox
			WHERE published_at IS NULL
			  AND retry_count < $2
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, event_type, aggregate_id, aggregate_type, payload,
		          retry_count, last_error, created_at, published_at
	`

	rows, err := r.queryxContext(ctx, query, batchSize, maxRetries)
	if err != nil {
		return nil, fmt.Errorf("claim unpublished outbox: %w", err)
	}
	defer rows.Close()

	var records []OutboxRecord
	for rows.Next() {
		var rec OutboxRecord
		if err := rows.Scan(&rec.ID, &rec.EventType, &rec.AggregateID, &rec.AggregateType, &rec.Payload,
			&rec.RetryCount, &rec.LastError, &rec.CreatedAt, &rec.PublishedAt); err != nil {
			return nil, fmt.Errorf("scan outbox record: %w", err)
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

// IncrementOutboxRetry increments the retry_count and sets last_error for a
// failed publish attempt, without touching published_at.
func (r *LedgerRepository) IncrementOutboxRetry(ctx context.Context, id uuid.UUID, lastErr string) error {
	query := `
		UPDATE ledger_outbox
		SET retry_count = retry_count + 1,
		    last_error = $2,
		    published_at = NULL
		WHERE id = $1
	`
	_, err := r.execContext(ctx, query, id, lastErr)
	if err != nil {
		return fmt.Errorf("increment outbox retry: %w", err)
	}
	return nil
}

// DeletePublishedOutboxBefore deletes outbox records that were published before
// the given cutoff. Returns the number of rows deleted.
func (r *LedgerRepository) DeletePublishedOutboxBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	query := `DELETE FROM ledger_outbox WHERE published_at IS NOT NULL AND published_at < $1`
	res, err := r.execContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete published outbox: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MarkOutboxPublished sets published_at for the given outbox records.
func (r *LedgerRepository) MarkOutboxPublished(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}

	// lib/pq cannot bind []uuid.UUID ("a slice of array" since uuid.UUID is
	// [16]byte), so send text and let Postgres cast back to uuid[].
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}

	query := `UPDATE ledger_outbox SET published_at = NOW() WHERE id = ANY($1::uuid[])`
	_, err := r.execContext(ctx, query, pq.Array(strIDs))
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	return nil
}

// CountUnpublishedOutbox returns the number of unpublished outbox records.
func (r *LedgerRepository) CountUnpublishedOutbox(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM ledger_outbox WHERE published_at IS NULL`
	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	return count, err
}

// GetOldestUnpublishedOutbox returns the oldest unpublished outbox event time.
func (r *LedgerRepository) GetOldestUnpublishedOutbox(ctx context.Context) (*time.Time, error) {
	query := `SELECT MIN(created_at) FROM ledger_outbox WHERE published_at IS NULL`
	var t *time.Time
	err := r.queryRowxContext(ctx, query).Scan(&t)
	return t, err
}

// ===== Snapshot Operations =====

// InsertBalanceSnapshot records a balance snapshot for an account at a given date.
// Returns true if a new row was inserted, false if a snapshot already existed.
func (r *LedgerRepository) InsertBalanceSnapshot(ctx context.Context, accountID uuid.UUID, balance decimal.Decimal, date time.Time) (bool, error) {
	query := `
		INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date)
		VALUES ($1, $2, $3)
		ON CONFLICT (account_id, snapshot_date) DO NOTHING
	`
	res, err := r.execContext(ctx, query, accountID, balance, date)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// GetBalanceSnapshot retrieves the balance for an account at a specific date.
func (r *LedgerRepository) GetBalanceSnapshot(ctx context.Context, accountID uuid.UUID, date time.Time) (*decimal.Decimal, error) {
	query := `SELECT balance FROM ledger_balance_snapshots WHERE account_id = $1 AND snapshot_date = $2`
	var balance decimal.Decimal
	err := r.queryRowxContext(ctx, query, accountID, date.Truncate(24*time.Hour)).Scan(&balance)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get balance snapshot: %w", err)
	}
	return &balance, nil
}

// GetLatestSnapshotDate returns the most recent date with balance snapshots.
func (r *LedgerRepository) GetLatestSnapshotDate(ctx context.Context) (*time.Time, error) {
	query := `SELECT MAX(snapshot_date) FROM ledger_balance_snapshots`
	var date *time.Time
	err := r.queryRowxContext(ctx, query).Scan(&date)
	return date, err
}

// GetAllAccountIDs returns the IDs of every ledger account.
func (r *LedgerRepository) GetAllAccountIDs(ctx context.Context) ([]uuid.UUID, error) {
	query := `SELECT id FROM ledger_accounts ORDER BY id`
	var ids []uuid.UUID
	err := r.selectContext(ctx, &ids, query)
	if err != nil {
		return nil, fmt.Errorf("get all account IDs: %w", err)
	}
	return ids, nil
}

// GetAccountIDsBatch returns a page of account IDs using keyset pagination.
// afterID should be uuid.Nil for the first page; subsequent calls pass the last
// ID from the previous page. Returns fewer than batchSize for the final page.
func (r *LedgerRepository) GetAccountIDsBatch(ctx context.Context, afterID uuid.UUID, batchSize int) ([]uuid.UUID, error) {
	query := `SELECT id FROM ledger_accounts WHERE id > $1 ORDER BY id LIMIT $2`
	var ids []uuid.UUID
	err := r.selectContext(ctx, &ids, query, afterID, batchSize)
	if err != nil {
		return nil, fmt.Errorf("get account IDs batch: %w", err)
	}
	return ids, nil
}

// GetTransactionDeltaByAccount returns the net balance change per account for
// transactions created within the given time range [start, end).
// Debit entries increase the balance (counted as positive), credit entries
// decrease the balance (counted as negative). Only completed transactions
// are included. Used by ReconcileDay to verify snapshot integrity.
func (r *LedgerRepository) GetTransactionDeltaByAccount(ctx context.Context, start, end time.Time) (map[uuid.UUID]decimal.Decimal, error) {
	query := `
		SELECT le.account_id,
		       SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE -le.amount END) AS net_change
		FROM ledger_entries le
		JOIN ledger_transactions lt ON le.transaction_id = lt.id
		WHERE lt.created_at >= $1 AND lt.created_at < $2
		  AND lt.status = 'completed'
		GROUP BY le.account_id
	`
	rows, err := r.queryxContext(ctx, query, start, end)
	if err != nil {
		return nil, fmt.Errorf("get transaction delta by account: %w", err)
	}
	defer rows.Close()

	deltas := make(map[uuid.UUID]decimal.Decimal)
	for rows.Next() {
		var accountID uuid.UUID
		var netChange decimal.Decimal
		if err := rows.Scan(&accountID, &netChange); err != nil {
			return nil, fmt.Errorf("scan delta row: %w", err)
		}
		deltas[accountID] = netChange
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delta rows: %w", err)
	}
	return deltas, nil
}

// ===== Integrity Check Operations =====

// CountNegativeBalanceAccounts returns the number of user accounts with negative balance.
func (r *LedgerRepository) CountNegativeBalanceAccounts(ctx context.Context) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM ledger_accounts
		WHERE user_id IS NOT NULL AND balance < 0
	`
	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	return count, err
}

// CountSystemAccountDeficits returns the number of system accounts whose balance
// is below the given max deficit threshold.
func (r *LedgerRepository) CountSystemAccountDeficits(ctx context.Context, maxDeficit decimal.Decimal) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM ledger_accounts
		WHERE user_id IS NULL AND balance < $1
	`
	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	return count, err
}

// --- Velocity Bucket Methods ---

// GetOrCreateVelocityBucket retrieves the velocity bucket for an account on a given date,
// creating it if it doesn't exist.
func (r *LedgerRepository) GetOrCreateVelocityBucket(ctx context.Context, accountID uuid.UUID, date time.Time) (*entities.LedgerVelocityBucket, error) {
	query := `
		INSERT INTO ledger_velocity_buckets (account_id, bucket_date, outflow_total, tx_count, updated_at)
		VALUES ($1, $2, 0, 0, NOW())
		ON CONFLICT (account_id, bucket_date) DO UPDATE SET updated_at = ledger_velocity_buckets.updated_at
		RETURNING id, account_id, bucket_date, outflow_total, tx_count, created_at, updated_at
	`
	// The ON CONFLICT DO UPDATE with a no-op ensures RETURNING always returns a row.

	var bucket entities.LedgerVelocityBucket
	err := r.queryRowxContext(ctx, query, accountID, date).Scan(
		&bucket.ID, &bucket.AccountID, &bucket.BucketDate,
		&bucket.OutflowTotal, &bucket.TxCount,
		&bucket.CreatedAt, &bucket.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create velocity bucket: %w", err)
	}
	return &bucket, nil
}

// IncrementVelocityBucket atomically increments the outflow total and tx count for a bucket row.
// Uses UPDATE within the caller's FOR UPDATE lock context (called in the same tx as balance updates).
func (r *LedgerRepository) IncrementVelocityBucket(ctx context.Context, accountID uuid.UUID, date time.Time, amount decimal.Decimal) error {
	query := `
		UPDATE ledger_velocity_buckets
		SET outflow_total = outflow_total + $3,
		    tx_count = tx_count + 1,
		    updated_at = NOW()
		WHERE account_id = $1 AND bucket_date = $2
	`
	_, err := r.execContext(ctx, query, accountID, date, amount)
	return err
}

// GetLatestTransactionHash returns the transaction_hash of the most recent
// completed transaction, or empty string if no transactions exist.
// Used for hash chain linking.
func (r *LedgerRepository) GetLatestTransactionHash(ctx context.Context) (string, error) {
	query := `SELECT COALESCE(transaction_hash, '') FROM ledger_transactions ORDER BY created_at DESC LIMIT 1`
	var hash string
	err := r.queryRowxContext(ctx, query).Scan(&hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get latest transaction hash: %w", err)
	}
	return hash, nil
}

// GetTransactionsForHashVerification returns a window of transactions ordered by
// created_at for hash chain verification. Used by CheckIntegrity.
// Returns minimal fields needed for hash recomputation (skips metadata to avoid
// JSONB scanning complexity with sqlx).
func (r *LedgerRepository) GetTransactionsForHashVerification(ctx context.Context, limit, offset int) ([]*entities.LedgerTransaction, error) {
	query := `
		SELECT id, user_id, transaction_type, idempotency_key,
		       previous_transaction_hash, transaction_hash, reason,
		       created_at
		FROM ledger_transactions
		ORDER BY created_at, id
		LIMIT $1 OFFSET $2
	`
	var txs []*entities.LedgerTransaction
	err := r.selectContext(ctx, &txs, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get transactions for hash verification: %w", err)
	}
	return txs, nil
}

// CountTransactions returns the total number of ledger transactions.
func (r *LedgerRepository) CountTransactions(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM ledger_transactions`
	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	return count, err
}

// CountTransactionsWithoutEntries returns the number of transactions that have
// no associated entries in the ledger_entries table.
func (r *LedgerRepository) CountTransactionsWithoutEntries(ctx context.Context) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM ledger_transactions lt
		LEFT JOIN ledger_entries le ON le.transaction_id = lt.id
		WHERE le.id IS NULL
	`
	var count int
	err := r.queryRowxContext(ctx, query).Scan(&count)
	return count, err
}
