package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/statement"
	"github.com/shopspring/decimal"
)

type BankStatementRepository struct {
	db *sqlx.DB
}

func NewBankStatementRepository(db *sqlx.DB) *BankStatementRepository {
	return &BankStatementRepository{db: db}
}

// Available reports whether the repo has a live DB. The DI layer wires the
// repo unconditionally (typed-nil when sqlxDB is nil); callers must check
// Available before use so correct_statement_category degrades to
// "unavailable" instead of panicking on a nil *sqlx.DB.
func (r *BankStatementRepository) Available() bool {
	return r != nil && r.db != nil
}

func (r *BankStatementRepository) Create(ctx context.Context, upload *entities.BankStatementUpload) error {
	if upload.ID == uuid.Nil {
		upload.ID = uuid.New()
	}
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
	err := r.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM bank_statement_uploads WHERE user_id = $1 AND file_hash = $2 AND status = 'completed')`, userID, hash)
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

func (r *BankStatementRepository) ResetToPending(ctx context.Context, uploadID uuid.UUID) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE bank_statement_uploads SET status = 'pending', updated_at = NOW() WHERE id = $1 AND status = 'processing'`, uploadID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
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
		INSERT INTO bank_statement_transactions (id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
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
		if t.Recurrence == "" {
			t.Recurrence = entities.StatementRecurrenceOneOff
		}
		_, err = stmt.ExecContext(ctx, t.ID, t.UploadID, t.UserID, t.TransactionDate, t.Description, t.Amount, t.Currency, t.Type, t.Category, t.Counterparty, t.IsEssential, t.CategoryConfidence, t.Recurrence, t.BalanceAfter, t.RawLine, t.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert transaction: %w", err)
		}
	}
	return tx.Commit()
}

func (r *BankStatementRepository) GetTransactionsByUploadID(ctx context.Context, uploadID uuid.UUID) ([]*entities.BankStatementTransaction, error) {
	var txns []*entities.BankStatementTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at
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
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at
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
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at
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
		  AND COALESCE(category_confidence, 0.700) >= 0.5
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

// GetPreviousUploadSummary returns spending-by-category from the user's most recent
// completed upload that was created before the given date.
func (r *BankStatementRepository) GetPreviousUploadSummary(ctx context.Context, userID uuid.UUID, beforeDate time.Time) (map[string]decimal.Decimal, error) {
	// Find the most recent completed upload before this one
	var prevUploadID uuid.UUID
	err := r.db.GetContext(ctx, &prevUploadID, `
		SELECT id FROM bank_statement_uploads
		WHERE user_id = $1 AND status = 'completed' AND created_at < $2
		ORDER BY created_at DESC LIMIT 1 OFFSET 1`, userID, beforeDate)
	if err != nil {
		return nil, err // sql.ErrNoRows if no previous upload
	}

	type row struct {
		Category string          `db:"category"`
		Total    decimal.Decimal `db:"total"`
	}
	var rows []row
	err = r.db.SelectContext(ctx, &rows, `
		SELECT category, SUM(amount) as total FROM bank_statement_transactions
		WHERE upload_id = $1 AND type = 'debit'
		GROUP BY category`, prevUploadID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]decimal.Decimal, len(rows))
	for _, r := range rows {
		result[r.Category] = r.Total
	}
	return result, nil
}

// GetTopRecurringRecipients finds debit transaction descriptions that appear 3+ times.
func (r *BankStatementRepository) GetTopRecurringRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]string, []int, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	type row struct {
		Description string `db:"description"`
		Count       int    `db:"cnt"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, `
		SELECT label AS description, COUNT(*) as cnt FROM (
			SELECT CASE
				WHEN recurrence IN ('bill', 'subscription') AND counterparty <> '' AND counterparty <> 'Unknown' THEN counterparty
				WHEN recurrence = 'one_off' AND counterparty = '' AND COALESCE(category_confidence, 0.700) >= 0.7 THEN description
				ELSE NULL
			END AS label
			FROM bank_statement_transactions
			WHERE user_id = $1 AND type = 'debit'
		) labeled
		WHERE label IS NOT NULL
		GROUP BY label HAVING COUNT(*) >= 3
		ORDER BY cnt DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, len(rows))
	counts := make([]int, len(rows))
	for i, r := range rows {
		names[i] = r.Description
		counts[i] = r.Count
	}
	return names, counts, nil
}

// GetDailySpendingPace computes current month spend vs historical daily average from statement data.
func (r *BankStatementRepository) GetDailySpendingPace(ctx context.Context, userID uuid.UUID) (float64, float64, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var currentSpend float64
	err := r.db.GetContext(ctx, &currentSpend, `
		SELECT COALESCE(SUM(amount), 0) FROM bank_statement_transactions
		WHERE user_id = $1 AND type = 'debit' AND transaction_date >= $2 AND transaction_date < $3`,
		userID, monthStart, now)
	if err != nil {
		return 0, 0, err
	}

	var historicalTotal float64
	var historicalDays int
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0), GREATEST(EXTRACT(DAY FROM (MAX(transaction_date) - MIN(transaction_date)))::int, 1)
		FROM bank_statement_transactions
		WHERE user_id = $1 AND type = 'debit' AND transaction_date < $2`,
		userID, monthStart).Scan(&historicalTotal, &historicalDays)
	if err != nil {
		return 0, 0, err
	}

	var dailyAvg float64
	if historicalDays > 0 {
		dailyAvg = historicalTotal / float64(historicalDays)
	}
	return currentSpend, dailyAvg, nil
}

// GetCategoryMonthlyAverages computes average monthly spending per category
// across all uploaded statements for a user. Only debit transactions are included.
func (r *BankStatementRepository) GetCategoryMonthlyAverages(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error) {
	type row struct {
		Category string          `db:"category"`
		Total    decimal.Decimal `db:"total"`
		Months   int             `db:"months"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, `
		SELECT category, SUM(amount) as total,
			GREATEST(1, EXTRACT(MONTH FROM AGE(MAX(transaction_date), MIN(transaction_date)))::int + 1) as months
		FROM bank_statement_transactions
		WHERE user_id = $1 AND type = 'debit'
		GROUP BY category`, userID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]decimal.Decimal, len(rows))
	for _, r := range rows {
		result[r.Category] = r.Total.Div(decimal.NewFromInt(int64(r.Months)))
	}
	return result, nil
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

func (r *BankStatementRepository) FindMatchingTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, date time.Time, tolerance time.Duration) (*entities.BankStatementTransaction, error) {
	lowerAmt := amount.Mul(decimal.NewFromFloat(0.95))
	upperAmt := amount.Mul(decimal.NewFromFloat(1.05))
	startDate := date.Add(-tolerance)
	endDate := date.Add(tolerance)

	var txn entities.BankStatementTransaction
	err := r.db.GetContext(ctx, &txn, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at
		FROM bank_statement_transactions
		WHERE user_id = $1 AND amount >= $2 AND amount <= $3 AND transaction_date >= $4 AND transaction_date <= $5
		ORDER BY ABS(EXTRACT(EPOCH FROM (transaction_date - $6::timestamp))) ASC
		LIMIT 1`,
		userID, lowerAmt, upperAmt, startDate, endDate, date)
	if err != nil {
		return nil, err
	}
	return &txn, nil
}

// GetIncomeExpenseSummary returns earned income and consumption spend, plus the
// period covered by every stored statement line. Transfers, savings moves, and
// loan payments are omitted from both totals. Lines below the advice confidence
// floor are omitted from the money totals and still count toward the period.
func (r *BankStatementRepository) GetIncomeExpenseSummary(ctx context.Context, userID uuid.UUID) (totalIncome, totalExpense decimal.Decimal, periodStart, periodEnd *time.Time, err error) {
	type summary struct {
		MinDate *time.Time `db:"min_date"`
		MaxDate *time.Time `db:"max_date"`
	}
	var s summary
	err = r.db.GetContext(ctx, &s, `
		SELECT MIN(transaction_date) AS min_date, MAX(transaction_date) AS max_date
		FROM bank_statement_transactions WHERE user_id = $1`, userID)
	if err != nil {
		return decimal.Zero, decimal.Zero, nil, nil, err
	}
	periodStart = s.MinDate
	periodEnd = s.MaxDate

	type row struct {
		Type     string          `db:"type"`
		Category string          `db:"category"`
		Total    decimal.Decimal `db:"total"`
	}
	var rows []row
	err = r.db.SelectContext(ctx, &rows, `
		SELECT type, category, SUM(amount) AS total
		FROM bank_statement_transactions
		WHERE user_id = $1 AND COALESCE(category_confidence, 0.700) >= 0.5
		GROUP BY type, category`, userID)
	if err != nil {
		return decimal.Zero, decimal.Zero, nil, nil, err
	}
	lines := make([]statement.CashflowLine, len(rows))
	for i, row := range rows {
		lines[i] = statement.CashflowLine{Type: row.Type, Category: row.Category, Amount: row.Total}
	}
	totalIncome, totalExpense = statement.FoldCashflow(lines)
	return totalIncome, totalExpense, periodStart, periodEnd, nil
}

// ListCategoryRules returns the user's statement corrections, longest pattern first.
func (r *BankStatementRepository) ListCategoryRules(ctx context.Context, userID uuid.UUID) ([]entities.StatementCategoryRule, error) {
	var rules []entities.StatementCategoryRule
	err := r.db.SelectContext(ctx, &rules, `
		SELECT id, user_id, match_type, pattern, bucket, created_at
		FROM statement_category_rules
		WHERE user_id = $1
		ORDER BY length(pattern) DESC`, userID)
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// SaveCategoryRule upserts a correction and rewrites matching stored lines.
// updated is how many existing statement lines changed.
func (r *BankStatementRepository) SaveCategoryRule(ctx context.Context, rule *entities.StatementCategoryRule, essential bool) (int, error) {
	if !r.Available() {
		return 0, fmt.Errorf("statement category rules unavailable (no database)")
	}
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	matchType, pattern, bucket, vErr := statement.ValidateCategoryRule(rule.MatchType, rule.Pattern, rule.Bucket)
	if vErr != nil {
		return 0, vErr
	}
	rule.MatchType = matchType
	rule.Pattern = pattern
	rule.Bucket = bucket
	rule.CreatedAt = time.Now().UTC()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO statement_category_rules (id, user_id, match_type, pattern, bucket, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, match_type, pattern)
		DO UPDATE SET bucket = EXCLUDED.bucket, created_at = EXCLUDED.created_at`,
		rule.ID, rule.UserID, rule.MatchType, rule.Pattern, rule.Bucket, rule.CreatedAt)
	if err != nil {
		return 0, err
	}

	var result interface {
		RowsAffected() (int64, error)
	}
	if rule.MatchType == entities.StatementRuleExact {
		result, err = tx.ExecContext(ctx, `
			UPDATE bank_statement_transactions
			SET category = $1, is_essential = $2, category_confidence = 0.990
			WHERE user_id = $3
			  AND (lower(description) = lower($4) OR lower(counterparty) = lower($4))`,
			rule.Bucket, essential, rule.UserID, rule.Pattern)
	} else {
		escaped := escapeLike(rule.Pattern)
		result, err = tx.ExecContext(ctx, `
			UPDATE bank_statement_transactions
			SET category = $1, is_essential = $2, category_confidence = 0.990
			WHERE user_id = $3
			  AND (description ILIKE $4 ESCAPE '\' OR counterparty ILIKE $4 ESCAPE '\')`,
			rule.Bucket, essential, rule.UserID, "%"+escaped+"%")
	}
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Longest-pattern-wins: a broad contains-rule just overwrote rows that a
	// narrower rule owned. Re-apply every longer rule so narrow beats broad
	// in SQL exactly like ApplyCategoryRules does in memory.
	var longer []entities.StatementCategoryRule
	if err := tx.SelectContext(ctx, &longer, `
		SELECT id, user_id, match_type, pattern, bucket, created_at
		FROM statement_category_rules
		WHERE user_id = $1 AND length(pattern) > length($2)`,
		rule.UserID, rule.Pattern); err == nil {
		for _, lr := range longer {
			lbucket := statement.NormalizeStatementCategory(lr.Bucket, "", "debit")
			if lbucket == "" {
				continue
			}
			if lr.MatchType == entities.StatementRuleExact {
				_, _ = tx.ExecContext(ctx, `
					UPDATE bank_statement_transactions
					SET category = $1, category_confidence = 0.990
					WHERE user_id = $2
					  AND (lower(description) = lower($3) OR lower(counterparty) = lower($3))`,
					lbucket, rule.UserID, lr.Pattern)
			} else {
				lesc := escapeLike(lr.Pattern)
				_, _ = tx.ExecContext(ctx, `
					UPDATE bank_statement_transactions
					SET category = $1, category_confidence = 0.990
					WHERE user_id = $2
					  AND (description ILIKE $3 ESCAPE '\' OR counterparty ILIKE $3 ESCAPE '\')`,
					lbucket, rule.UserID, "%"+lesc+"%")
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// BackfillUnderstanding fills counterparty, category, essential, and confidence
// for lines stored before structured understanding existed. User corrections
// (confidence >= 0.99) are left alone. Recurrence is recomputed for each
// touched user. limit caps the batch; callers loop until it returns 0.
func (r *BankStatementRepository) BackfillUnderstanding(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	type row struct {
		ID          uuid.UUID `db:"id"`
		UserID      uuid.UUID `db:"user_id"`
		Description string    `db:"description"`
		Category    string    `db:"category"`
		Type        string    `db:"type"`
	}
	var rows []row
	err := r.db.SelectContext(ctx, &rows, `
		SELECT id, user_id, description, category, type
		FROM bank_statement_transactions
		WHERE (counterparty = '' OR counterparty = 'Unknown') AND COALESCE(category_confidence, 0) < 0.99
		ORDER BY created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	users := make(map[uuid.UUID]struct{})
	for _, row := range rows {
		understood := statement.UnderstandLine(row.Category, row.Description, row.Type)
		// Never persist "Unknown": an empty counterparty falls back to the
		// description for recurrence and keeps the one_off+'' branch alive.
		// Existing 'Unknown' rows are cleaned to '' here.
		_, err = r.db.ExecContext(ctx, `
			UPDATE bank_statement_transactions
			SET category = $1, counterparty = $2, is_essential = $3, category_confidence = $4
			WHERE id = $5 AND COALESCE(category_confidence, 0) < 0.99`,
			understood.Bucket, understood.Counterparty, understood.Essential, understood.Confidence, row.ID)
		if err != nil {
			return 0, err
		}
		users[row.UserID] = struct{}{}
	}
	for userID := range users {
		if err := r.recomputeRecurrence(ctx, userID); err != nil {
			return len(rows), err
		}
	}
	return len(rows), nil
}

// RecomputeUserRecurrence refreshes bill/subscription flags across ALL of a
// user's debit lines. Call after each upload so monthly cadence spanning
// multiple uploads is detected — AssignRecurrence on the parse batch alone
// only sees one statement and would leave every subscription as one_off.
func (r *BankStatementRepository) RecomputeUserRecurrence(ctx context.Context, userID uuid.UUID) error {
	return r.recomputeRecurrence(ctx, userID)
}

// recurrenceUpdateBatchSize caps one CASE update at 2000 ids (6000 bind
// params), far below the Postgres 65535-parameter limit. The select above
// stays full-history — cadence detection needs every debit line — but the
// writes are chunked so power users never fail the upload path.
const recurrenceUpdateBatchSize = 2000

func (r *BankStatementRepository) recomputeRecurrence(ctx context.Context, userID uuid.UUID) error {
	var txns []*entities.BankStatementTransaction
	err := r.db.SelectContext(ctx, &txns, `
		SELECT id, upload_id, user_id, transaction_date, description, amount, currency, type, category, counterparty, is_essential, category_confidence, recurrence, balance_after, raw_line, created_at
		FROM bank_statement_transactions
		WHERE user_id = $1 AND type = 'debit'
		ORDER BY transaction_date ASC`, userID)
	if err != nil {
		return err
	}
	if len(txns) == 0 {
		return nil
	}
	// Snapshot pre-recompute values: steady-state uploads change a handful
	// of rows, so only changed rows are rewritten.
	original := make(map[uuid.UUID]string, len(txns))
	for _, txn := range txns {
		original[txn.ID] = txn.Recurrence
	}
	statement.AssignRecurrence(txns)
	changed := txns[:0]
	for _, txn := range txns {
		rec := txn.Recurrence
		if rec == "" {
			rec = entities.StatementRecurrenceOneOff
			txn.Recurrence = rec
		}
		if rec != original[txn.ID] {
			changed = append(changed, txn)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// Batched CASE updates in one transaction: no per-row round trips,
	// no half-updated recurrence on failure, no parameter-limit cliff.
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Commit runs below; rollback only matters on failure paths.
	for start := 0; start < len(changed); start += recurrenceUpdateBatchSize {
		end := start + recurrenceUpdateBatchSize
		if end > len(changed) {
			end = len(changed)
		}
		batch := changed[start:end]
		args := make([]any, 0, len(batch)*3)
		var sb strings.Builder
		sb.WriteString("UPDATE bank_statement_transactions SET recurrence = CASE id ")
		for _, txn := range batch {
			args = append(args, txn.ID, txn.Recurrence)
			sb.WriteString(fmt.Sprintf("WHEN $%d THEN $%d ", len(args)-1, len(args)))
		}
		placeholders := make([]string, 0, len(batch))
		for _, txn := range batch {
			args = append(args, txn.ID)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		sb.WriteString("ELSE recurrence END WHERE id IN (" + strings.Join(placeholders, ",") + ")")
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
