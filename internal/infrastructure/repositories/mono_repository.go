package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// MonoRepository persists Mono-linked accounts, imported transactions, and
// DirectPay payment records. It also provides analysis queries for spending
// breakdowns used by Miriam's coaching context.
type MonoRepository struct {
	db *sqlx.DB
}

func NewMonoRepository(db *sqlx.DB) *MonoRepository {
	return &MonoRepository{db: db}
}

// --- Linked Accounts ---

func (r *MonoRepository) CreateLinkedAccount(ctx context.Context, acct *entities.MonoLinkedAccount) error {
	if acct.ID == uuid.Nil {
		acct.ID = uuid.New()
	}
	now := time.Now().UTC()
	acct.CreatedAt = now
	acct.UpdatedAt = now
	if acct.Status == "" {
		acct.Status = entities.MonoAccountStatusLinked
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO mono_linked_accounts (id, user_id, mono_account_id, institution, account_name, account_number, account_type, currency, balance, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		acct.ID, acct.UserID, acct.MonoAccountID, acct.Institution, acct.AccountName,
		acct.AccountNumber, acct.AccountType, acct.Currency, acct.Balance, acct.Status,
		acct.CreatedAt, acct.UpdatedAt)
	return err
}

func (r *MonoRepository) GetLinkedAccountByID(ctx context.Context, userID, accountID uuid.UUID) (*entities.MonoLinkedAccount, error) {
	var acct entities.MonoLinkedAccount
	err := r.db.GetContext(ctx, &acct, `
		SELECT * FROM mono_linked_accounts WHERE id = $1 AND user_id = $2`, accountID, userID)
	if err != nil {
		return nil, fmt.Errorf("get mono linked account: %w", err)
	}
	return &acct, nil
}

func (r *MonoRepository) GetLinkedAccountByMonoID(ctx context.Context, monoAccountID string) (*entities.MonoLinkedAccount, error) {
	var acct entities.MonoLinkedAccount
	err := r.db.GetContext(ctx, &acct, `
		SELECT * FROM mono_linked_accounts WHERE mono_account_id = $1`, monoAccountID)
	if err != nil {
		return nil, fmt.Errorf("get mono linked account by mono id: %w", err)
	}
	return &acct, nil
}

func (r *MonoRepository) ListLinkedAccounts(ctx context.Context, userID uuid.UUID) ([]*entities.MonoLinkedAccount, error) {
	var accounts []*entities.MonoLinkedAccount
	err := r.db.SelectContext(ctx, &accounts, `
		SELECT * FROM mono_linked_accounts WHERE user_id = $1 AND status != 'unlinked' ORDER BY created_at DESC`, userID)
	return accounts, err
}

func (r *MonoRepository) UpdateLinkedAccountStatus(ctx context.Context, accountID uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE mono_linked_accounts SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now().UTC(), accountID)
	return err
}

func (r *MonoRepository) UpdateLinkedAccountBalance(ctx context.Context, accountID uuid.UUID, balance int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE mono_linked_accounts SET balance = $1, last_synced_at = $2, updated_at = $3 WHERE id = $4`,
		balance, time.Now().UTC(), time.Now().UTC(), accountID)
	return err
}

// --- Imported Transactions ---

func (r *MonoRepository) ImportTransactions(ctx context.Context, txns []*entities.MonoImportedTransaction) (int, error) {
	if len(txns) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	inserted := 0
	for _, txn := range txns {
		if txn.ID == uuid.Nil {
			txn.ID = uuid.New()
		}
		txn.CreatedAt = time.Now().UTC()

		// Insert with ON CONFLICT to deduplicate on (account_id, mono_txn_id).
		var lastInsertID uuid.UUID
		err := tx.QueryRowxContext(ctx, `
			INSERT INTO mono_imported_transactions (id, user_id, account_id, mono_txn_id, amount, type, description, category, sub_category, transaction_date, reference, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (account_id, mono_txn_id) DO NOTHING
			RETURNING id`,
			txn.ID, txn.UserID, txn.AccountID, txn.MonoTxnID, txn.Amount, txn.Type,
			txn.Description, txn.Category, txn.SubCategory, txn.TransactionDate,
			txn.Reference, txn.CreatedAt).Scan(&lastInsertID)

		if err != nil {
			if err == sql.ErrNoRows {
				// Conflict — transaction already exists, skip.
				continue
			}
			return inserted, fmt.Errorf("insert transaction: %w", err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return inserted, fmt.Errorf("commit tx: %w", err)
	}
	return inserted, nil
}

func (r *MonoRepository) GetTransactions(ctx context.Context, userID, accountID uuid.UUID, limit, offset int) ([]*entities.MonoImportedTransaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var txns []*entities.MonoImportedTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT * FROM mono_imported_transactions
		WHERE user_id = $1 AND account_id = $2
		ORDER BY transaction_date DESC LIMIT $3 OFFSET $4`,
		userID, accountID, limit, offset)
	return txns, err
}

// GetRecentTransactions returns transactions across all linked accounts for a user
// within the given date range. Used for spending analysis.
func (r *MonoRepository) GetRecentTransactions(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]*entities.MonoImportedTransaction, error) {
	var txns []*entities.MonoImportedTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT * FROM mono_imported_transactions
		WHERE user_id = $1 AND transaction_date >= $2 AND transaction_date <= $3
		ORDER BY transaction_date DESC`,
		userID, start, end)
	return txns, err
}

// --- Spending Analysis Queries ---

// GetSpendingSummary returns total credits, total debits, and transaction count
// for a user within the given date range.
func (r *MonoRepository) GetSpendingSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (totalCredits, totalDebits int64, txnCount int, err error) {
	type summaryRow struct {
		TotalCredits sql.NullInt64 `db:"total_credits"`
		TotalDebits  sql.NullInt64 `db:"total_debits"`
		TxnCount     int           `db:"txn_count"`
	}
	var row summaryRow
	err = r.db.GetContext(ctx, &row, `
		SELECT
			COALESCE(SUM(CASE WHEN type = 'credit' THEN amount ELSE 0 END), 0) AS total_credits,
			COALESCE(SUM(CASE WHEN type = 'debit' THEN amount ELSE 0 END), 0) AS total_debits,
			COUNT(*) AS txn_count
		FROM mono_imported_transactions
		WHERE user_id = $1 AND transaction_date >= $2 AND transaction_date <= $3`,
		userID, start, end)
	if err != nil {
		return 0, 0, 0, err
	}
	return row.TotalCredits.Int64, row.TotalDebits.Int64, row.TxnCount, nil
}

// GetCategoryBreakdown returns per-category spending totals for debits.
func (r *MonoRepository) GetCategoryBreakdown(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.MonoCategoryBreakdown, error) {
	type catRow struct {
		Category string       `db:"category"`
		Amount   sql.NullInt64 `db:"amount"`
		Count    int          `db:"count"`
	}
	var rows []catRow
	err := r.db.SelectContext(ctx, &rows, `
		SELECT category, SUM(amount) AS amount, COUNT(*) AS count
		FROM mono_imported_transactions
		WHERE user_id = $1 AND type = 'debit' AND transaction_date >= $2 AND transaction_date <= $3
		GROUP BY category ORDER BY amount DESC`,
		userID, start, end)
	if err != nil {
		return nil, err
	}

	result := make([]entities.MonoCategoryBreakdown, 0, len(rows))
	for _, row := range rows {
		cat := row.Category
		if cat == "" {
			cat = "Uncategorized"
		}
		result = append(result, entities.MonoCategoryBreakdown{
			Category: cat,
			Amount:   row.Amount.Int64,
			Count:    row.Count,
		})
	}
	return result, nil
}

// --- Payments ---

func (r *MonoRepository) CreatePayment(ctx context.Context, pmt *entities.MonoPayment) error {
	if pmt.ID == uuid.Nil {
		pmt.ID = uuid.New()
	}
	now := time.Now().UTC()
	pmt.CreatedAt = now
	pmt.UpdatedAt = now
	if pmt.Status == "" {
		pmt.Status = entities.MonoPaymentStatusPending
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO mono_payments (id, user_id, account_id, amount, reference, status, mono_ref, approval_url, description, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		pmt.ID, pmt.UserID, pmt.AccountID, pmt.Amount, pmt.Reference,
		pmt.Status, pmt.MonoRef, pmt.ApprovalURL, pmt.Description,
		pmt.CreatedAt, pmt.UpdatedAt)
	return err
}

func (r *MonoRepository) GetPaymentByReference(ctx context.Context, reference string) (*entities.MonoPayment, error) {
	var pmt entities.MonoPayment
	err := r.db.GetContext(ctx, &pmt, `
		SELECT * FROM mono_payments WHERE reference = $1`, reference)
	if err != nil {
		return nil, fmt.Errorf("get mono payment: %w", err)
	}
	return &pmt, nil
}

func (r *MonoRepository) GetPaymentByID(ctx context.Context, userID, paymentID uuid.UUID) (*entities.MonoPayment, error) {
	var pmt entities.MonoPayment
	err := r.db.GetContext(ctx, &pmt, `
		SELECT * FROM mono_payments WHERE id = $1 AND user_id = $2`, paymentID, userID)
	if err != nil {
		return nil, fmt.Errorf("get mono payment: %w", err)
	}
	return &pmt, nil
}

func (r *MonoRepository) UpdatePaymentStatus(ctx context.Context, paymentID uuid.UUID, status, monoRef string) error {
	now := time.Now().UTC()
	verifiedAt := (*time.Time)(nil)
	if status == entities.MonoPaymentStatusSuccessful || status == entities.MonoPaymentStatusFailed {
		verifiedAt = &now
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE mono_payments SET status = $1, mono_ref = $2, verified_at = $3, updated_at = $4 WHERE id = $5`,
		status, monoRef, verifiedAt, now, paymentID)
	return err
}

func (r *MonoRepository) ListPayments(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.MonoPayment, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var payments []*entities.MonoPayment
	err := r.db.SelectContext(ctx, &payments, `
		SELECT * FROM mono_payments WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	return payments, err
}

// GetPendingPayments returns payments in 'pending' status for verification polling.
func (r *MonoRepository) GetPendingPayments(ctx context.Context, before time.Time, limit int) ([]*entities.MonoPayment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var payments []*entities.MonoPayment
	err := r.db.SelectContext(ctx, &payments, `
		SELECT * FROM mono_payments WHERE status = 'pending' AND created_at < $1 ORDER BY created_at ASC LIMIT $2`,
		before, limit)
	return payments, err
}
