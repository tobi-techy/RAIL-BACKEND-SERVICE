package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type BankStatementRepository struct {
	db *sqlx.DB
}

func NewBankStatementRepository(db *sqlx.DB) *BankStatementRepository {
	return &BankStatementRepository{db: db}
}

func (r *BankStatementRepository) Create(ctx context.Context, upload *entities.BankStatementUpload) error {
	upload.ID = uuid.New()
	upload.CreatedAt = time.Now().UTC()
	upload.UpdatedAt = upload.CreatedAt
	if upload.Status == "" {
		upload.Status = entities.StatementStatusPending
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO bank_statement_uploads (id, user_id, bank_name, file_hash, file_size_bytes, file_data, page_count, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		upload.ID, upload.UserID, upload.BankName, upload.FileHash, upload.FileSizeBytes, upload.FileData, upload.PageCount, upload.Status, upload.CreatedAt, upload.UpdatedAt)
	return err
}

// GetByID returns a lightweight upload record (no file_data bytes) for auth checks
// and metadata lookups that don't need the PDF binary.
func (r *BankStatementRepository) GetByID(ctx context.Context, userID, uploadID uuid.UUID) (*entities.BankStatementUpload, error) {
	var u entities.BankStatementUpload
	err := r.db.GetContext(ctx, &u, `
		SELECT id, user_id, bank_name, file_hash, file_size_bytes, page_count, status, error_message, summary, period_start, period_end, transaction_count, created_at, updated_at
		FROM bank_statement_uploads WHERE id = $1 AND user_id = $2`, uploadID, userID)
	if err != nil {
		return nil, fmt.Errorf("get statement upload: %w", err)
	}
	return &u, nil
}

// GetByIDWithData returns the full upload record including the PDF file_data bytes.
// Use this only when the worker needs the binary to process the statement.
func (r *BankStatementRepository) GetByIDWithData(ctx context.Context, userID, uploadID uuid.UUID) (*entities.BankStatementUpload, error) {
	var u entities.BankStatementUpload
	err := r.db.GetContext(ctx, &u, `SELECT * FROM bank_statement_uploads WHERE id = $1 AND user_id = $2`, uploadID, userID)
	if err != nil {
		return nil, fmt.Errorf("get statement upload: %w", err)
	}
	return &u, nil
}

func (r *BankStatementRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]*entities.BankStatementUpload, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var uploads []*entities.BankStatementUpload
	err := r.db.SelectContext(ctx, &uploads, `
		SELECT id, user_id, bank_name, file_hash, file_size_bytes, page_count, status, error_message, summary, period_start, period_end, transaction_count, created_at, updated_at
		FROM bank_statement_uploads WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	return uploads, err
}

func (r *BankStatementRepository) ExistsByHash(ctx context.Context, userID uuid.UUID, hash string) (bool, error) {
	var exists bool
	err := r.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM bank_statement_uploads WHERE user_id = $1 AND file_hash = $2)`, userID, hash)
	return exists, err
}

func (r *BankStatementRepository) UpdateStatus(ctx context.Context, uploadID uuid.UUID, status string, errMsg *string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE bank_statement_uploads SET status = $1, error_message = $2, updated_at = NOW() WHERE id = $3`,
		status, errMsg, uploadID)
	return err
}

// AtomicClaim atomically claims a pending upload for processing.
// Returns true if the row was updated (claim succeeded), false if already claimed/completed.
func (r *BankStatementRepository) AtomicClaim(ctx context.Context, uploadID uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE bank_statement_uploads SET status = 'processing', updated_at = NOW() WHERE id = $1 AND status = 'pending'`,
		uploadID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *BankStatementRepository) CountUploadsSince(ctx context.Context, userID uuid.UUID, since time.Duration) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM bank_statement_uploads WHERE user_id = $1 AND created_at > NOW() - $2::interval`,
		userID, fmt.Sprintf("%.0f seconds", since.Seconds()))
	return count, err
}

func (r *BankStatementRepository) ResetToPending(ctx context.Context, uploadID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE bank_statement_uploads SET status = 'pending', updated_at = NOW() WHERE id = $1 AND status = 'processing'`, uploadID)
	return err
}

func (r *BankStatementRepository) UpdateBankName(ctx context.Context, uploadID uuid.UUID, bankName string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE bank_statement_uploads SET bank_name = $1, updated_at = NOW() WHERE id = $2`, bankName, uploadID)
	return err
}

func (r *BankStatementRepository) UpdateCompleted(ctx context.Context, uploadID uuid.UUID, txnCount int, periodStart, periodEnd *time.Time, pageCount *int, summary *string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE bank_statement_uploads SET status = 'completed', transaction_count = $1, period_start = $2, period_end = $3, page_count = $4, summary = $5, updated_at = NOW() WHERE id = $6`,
		txnCount, periodStart, periodEnd, pageCount, summary, uploadID)
	return err
}

func (r *BankStatementRepository) CreateTransactions(ctx context.Context, txns []*entities.BankStatementTransaction) error {
	if len(txns) == 0 {
		return nil
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bank_statement_transactions (id, upload_id, user_id, transaction_date, description, amount, currency, type, category, balance_after, raw_line, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT ON CONSTRAINT uq_bank_stmt_txns_dedup DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, t := range txns {
		if t.ID == uuid.Nil {
			t.ID = uuid.New()
		}
		t.CreatedAt = now
		_, err = stmt.ExecContext(ctx, t.ID, t.UploadID, t.UserID, t.TransactionDate, t.Description, t.Amount, t.Currency, t.Type, t.Category, t.BalanceAfter, t.RawLine, t.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert transaction: %w", err)
		}
	}
	return tx.Commit()
}

func (r *BankStatementRepository) GetTransactionsByUploadID(ctx context.Context, uploadID uuid.UUID) ([]*entities.BankStatementTransaction, error) {
	var txns []*entities.BankStatementTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, balance_after, raw_line, created_at
		FROM bank_statement_transactions WHERE upload_id = $1 ORDER BY transaction_date DESC`, uploadID)
	return txns, err
}

func (r *BankStatementRepository) GetTransactionsByUploadIDPaginated(ctx context.Context, uploadID uuid.UUID, limit, offset int) ([]*entities.BankStatementTransaction, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var txns []*entities.BankStatementTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, balance_after, raw_line, created_at
		FROM bank_statement_transactions WHERE upload_id = $1 ORDER BY transaction_date DESC LIMIT $2 OFFSET $3`,
		uploadID, limit, offset)
	return txns, err
}

func (r *BankStatementRepository) GetTransactionsByUser(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]*entities.BankStatementTransaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var txns []*entities.BankStatementTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, balance_after, raw_line, created_at
		FROM bank_statement_transactions WHERE user_id = $1 AND transaction_date >= $2 AND transaction_date < $3
		ORDER BY transaction_date DESC LIMIT $4`, userID, start, end, limit)
	return txns, err
}

func (r *BankStatementRepository) GetSpendingSummaryByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) (map[string]float64, error) {
	type row struct {
		Category string  `db:"category"`
		Total    float64 `db:"total"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, `
		SELECT category, SUM(amount) as total FROM bank_statement_transactions
		WHERE user_id = $1 AND type = 'debit' AND transaction_date >= $2 AND transaction_date < $3
		GROUP BY category ORDER BY total DESC`, userID, start, end)
	if err != nil {
		return nil, err
	}
	result := make(map[string]float64, len(rows))
	for _, r := range rows {
		result[r.Category] = r.Total
	}
	return result, nil
}

func (r *BankStatementRepository) CountTransactionsByUploadID(ctx context.Context, uploadID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM bank_statement_transactions WHERE upload_id = $1`, uploadID)
	return count, err
}

func (r *BankStatementRepository) Delete(ctx context.Context, userID, uploadID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM bank_statement_uploads WHERE id = $1 AND user_id = $2`, uploadID, userID)
	return err
}

func (r *BankStatementRepository) GetPendingOlderThan(ctx context.Context, since time.Duration) ([]*entities.BankStatementUpload, error) {
	var uploads []*entities.BankStatementUpload
	err := r.db.SelectContext(ctx, &uploads, `
		SELECT id, user_id, bank_name, file_hash, file_size_bytes, page_count, status, error_message, summary, period_start, period_end, transaction_count, created_at, updated_at
		FROM bank_statement_uploads WHERE status IN ('pending', 'processing') AND created_at < NOW() - $1::interval ORDER BY created_at ASC`,
		fmt.Sprintf("%.0f seconds", since.Seconds()))
	return uploads, err
}

func (r *BankStatementRepository) GetCompletedUploadSummary(ctx context.Context, userID uuid.UUID) (int, []string, error) {
	type row struct {
		BankName string `db:"bank_name"`
		TxnCount int    `db:"txn_count"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, `
		SELECT bank_name, transaction_count as txn_count FROM bank_statement_uploads
		WHERE user_id = $1 AND status = 'completed' ORDER BY created_at DESC LIMIT 10`, userID)
	if err != nil {
		return 0, nil, err
	}
	var total int
	var banks []string
	seen := make(map[string]bool)
	for _, row := range rows {
		total += row.TxnCount
		if !seen[row.BankName] {
			banks = append(banks, row.BankName)
			seen[row.BankName] = true
		}
	}
	return total, banks, nil
}
