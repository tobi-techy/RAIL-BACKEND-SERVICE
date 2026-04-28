package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// GetSpendingByCategory returns spending grouped by merchant_category for a user in a date range.
func (r *CardRepository) GetSpendingByCategory(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByCategory, error) {
	query := `
		SELECT COALESCE(merchant_category, 'Uncategorized') AS merchant_category,
		       SUM(amount) AS total, COUNT(*) AS count
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		GROUP BY merchant_category
		ORDER BY total DESC`

	var results []entities.SpendingByCategory
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

// GetSpendingByMerchant returns top merchants by spend for a user in a date range.
func (r *CardRepository) GetSpendingByMerchant(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingByMerchant, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `
		SELECT COALESCE(merchant_name, 'Unknown') AS merchant_name,
		       SUM(amount) AS total, COUNT(*) AS count
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		GROUP BY merchant_name
		ORDER BY total DESC
		LIMIT $4`

	var results []entities.SpendingByMerchant
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end, limit); err != nil {
		return nil, err
	}
	return results, nil
}

// GetSpendingByDay returns daily spending totals for a user in a date range.
func (r *CardRepository) GetSpendingByDay(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]entities.SpendingByPeriod, error) {
	query := `
		SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS period,
		       SUM(amount) AS total, COUNT(*) AS count
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		GROUP BY period
		ORDER BY period`

	var results []entities.SpendingByPeriod
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end); err != nil {
		return nil, err
	}
	return results, nil
}

// GetSpendingTotal returns total spending for a user in a date range.
func (r *CardRepository) GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error) {
	var total decimal.Decimal
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0) AS total, COUNT(*) AS count
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3`,
		userID, start, end).Scan(&total, &count)
	return total, count, err
}

// GetRecentOutflows returns individual card transactions (card-only, no withdrawals/P2P).
func (r *CardRepository) GetRecentOutflows(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]entities.SpendingTransaction, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	query := `
		SELECT TO_CHAR(created_at, 'YYYY-MM-DD') AS date, amount,
		       COALESCE(merchant_category, 'Card Payment') AS category,
		       COALESCE(merchant_name, 'Card Payment') AS source
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC LIMIT $4`

	var results []entities.SpendingTransaction
	if err := r.db.SelectContext(ctx, &results, query, userID, start, end, limit); err != nil {
		return nil, err
	}
	return results, nil
}

// GetMoneyFlow returns card-only money flow (no withdrawals/P2P). Stub for interface parity.
func (r *CardRepository) GetMoneyFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	s := &entities.MoneyFlowSummary{}
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0), COUNT(*)
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3`,
		userID, start, end).Scan(&s.TotalCardSpend, &s.CardSpendCount)
	return s, err
}
