package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/shopspring/decimal"
)

// RecurringExpenseRepository detects recurring expenses from all spending sources.
type RecurringExpenseRepository struct {
	db *sqlx.DB
}

func NewRecurringExpenseRepository(db *sqlx.DB) *RecurringExpenseRepository {
	return &RecurringExpenseRepository{db: db}
}

// recurringMerchantRow is the raw result from the detection query.
type recurringMerchantRow struct {
	Name      string          `db:"name"`
	Count     int             `db:"visit_count"`
	AvgAmount decimal.Decimal `db:"avg_amount"`
	Total     decimal.Decimal `db:"total"`
	FirstSeen time.Time       `db:"first_seen"`
	LastSeen  time.Time       `db:"last_seen"`
}

func (r *RecurringExpenseRepository) DetectRecurring(ctx context.Context, userID uuid.UUID) ([]ai.RecurringExpense, error) {
	since := time.Now().AddDate(0, -6, 0)

	query := `
		WITH merchant_visits AS (
			SELECT merchant AS name, amount, created_at FROM receipt_scans WHERE user_id = $1 AND created_at >= $2
			UNION ALL
			SELECT merchant_name AS name, amount, created_at FROM card_transactions WHERE user_id = $1 AND type = 'capture' AND status = 'completed' AND created_at >= $2
			UNION ALL
			SELECT recipient_identifier AS name, amount, created_at FROM p2p_transfers WHERE sender_id = $1 AND status = 'completed' AND created_at >= $2
			UNION ALL
			SELECT CASE WHEN destination_type = 'bank_account' THEN COALESCE(currency, 'USD') || ' to bank'
			            ELSE COALESCE(currency, 'USDC') || ' to ' || COALESCE(NULLIF(LEFT(destination_address, 8), ''), 'wallet') END AS name,
				amount, created_at FROM withdrawals WHERE user_id = $1 AND status = 'completed' AND created_at >= $2
		)
		SELECT name, COUNT(*) as visit_count, AVG(amount) as avg_amount, SUM(amount) as total, MIN(created_at) as first_seen, MAX(created_at) as last_seen
		FROM merchant_visits WHERE name IS NOT NULL AND name != ''
		GROUP BY name HAVING COUNT(*) >= 3
		ORDER BY total DESC
		LIMIT 50`

	var rows []recurringMerchantRow
	if err := r.db.SelectContext(ctx, &rows, query, userID, since); err != nil {
		return nil, fmt.Errorf("detect recurring expenses: %w", err)
	}

	var results []ai.RecurringExpense
	for _, row := range rows {
		days := row.LastSeen.Sub(row.FirstSeen).Hours() / 24
		if days < 7 {
			continue
		}
		avgInterval := days / float64(row.Count-1)
		frequency := "monthly"
		if avgInterval <= 10 {
			frequency = "weekly"
		}
		results = append(results, ai.RecurringExpense{
			Merchant:  row.Name,
			Frequency: frequency,
			AvgAmount: row.AvgAmount,
			Total:     row.Total,
			FirstSeen: row.FirstSeen.Format("2006-01-02"),
			LastSeen:  row.LastSeen.Format("2006-01-02"),
			Count:     row.Count,
		})
	}
	return results, nil
}
