package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// LedgerSpendingRepository queries ALL outflows from the ledger (card, withdrawal, p2p).
type LedgerSpendingRepository struct {
	db *sqlx.DB
}

func NewLedgerSpendingRepository(db *sqlx.DB) *LedgerSpendingRepository {
	return &LedgerSpendingRepository{db: db}
}

// outflowTypes are the transaction types that count as "spending".
const outflowWhere = `
	t.type IN ('card_payment', 'withdrawal', 'p2p_transfer')
	AND t.status = 'completed'
	AND e.entry_type = 'debit'
	AND a.user_id = $1
	AND e.created_at >= $2 AND e.created_at < $3`

const outflowJoin = `
	FROM ledger_entries e
	JOIN ledger_transactions t ON t.id = e.transaction_id
	JOIN ledger_accounts a ON a.id = e.account_id
	WHERE ` + outflowWhere

func (r *LedgerSpendingRepository) GetSpendingByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error) {
	query := `
		SELECT COALESCE(t.type, 'Uncategorized') AS merchant_category,
		       SUM(e.amount) AS total, COUNT(*) AS count
		` + outflowJoin + `
		GROUP BY t.type
		ORDER BY total DESC`

	var results []entities.SpendingByCategory
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingByMerchant(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingByMerchant, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT COALESCE(e.description, t.type) AS merchant_name,
		       SUM(e.amount) AS total, COUNT(*) AS count
		` + outflowJoin + `
		GROUP BY merchant_name
		ORDER BY total DESC
		LIMIT $4`

	var results []entities.SpendingByMerchant
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end, limit); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingByDay(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByPeriod, error) {
	query := `
		SELECT TO_CHAR(e.created_at, 'YYYY-MM-DD') AS period,
		       SUM(e.amount) AS total, COUNT(*) AS count
		` + outflowJoin + `
		GROUP BY period
		ORDER BY period`

	var results []entities.SpendingByPeriod
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *LedgerSpendingRepository) GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error) {
	var total decimal.Decimal
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(e.amount), 0) AS total, COUNT(*) AS count
		`+outflowJoin, userID, start, end).Scan(&total, &count)
	return total, count, err
}
