package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WeekdaySpending represents spending grouped by day of week.
type WeekdaySpending struct {
	DayOfWeek int             `db:"dow" json:"day_of_week"` // 0=Sun, 6=Sat
	DayName   string          `json:"day_name"`
	Total     decimal.Decimal `db:"total" json:"total"`
	Count     int             `db:"count" json:"count"`
	AvgPerTx  decimal.Decimal `json:"avg_per_transaction"`
}

// GetSpendingByDayOfWeek returns spending grouped by day of week.
func (r *CardRepository) GetSpendingByDayOfWeek(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]WeekdaySpending, error) {
	query := `
		SELECT EXTRACT(DOW FROM created_at)::int AS dow,
		       SUM(amount) AS total, COUNT(*) AS count
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		GROUP BY dow ORDER BY dow`

	type row struct {
		DOW   int             `db:"dow"`
		Total decimal.Decimal `db:"total"`
		Count int             `db:"count"`
	}
	var rows []row
	if err := r.db.SelectContext(ctx, &rows, query, userID, start, end); err != nil {
		return nil, err
	}

	dayNames := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	results := make([]WeekdaySpending, len(rows))
	for i, r := range rows {
		avg := decimal.Zero
		if r.Count > 0 {
			avg = r.Total.Div(decimal.NewFromInt(int64(r.Count)))
		}
		results[i] = WeekdaySpending{
			DayOfWeek: r.DOW,
			DayName:   dayNames[r.DOW],
			Total:     r.Total,
			Count:     r.Count,
			AvgPerTx:  avg,
		}
	}
	return results, nil
}

// LargeTransaction represents a notable transaction.
type LargeTransaction struct {
	Amount       decimal.Decimal `db:"amount" json:"amount"`
	MerchantName *string         `db:"merchant_name" json:"merchant_name"`
	CreatedAt    time.Time       `db:"created_at" json:"date"`
}

// GetLargestTransactions returns the N largest transactions in a period.
func (r *CardRepository) GetLargestTransactions(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]LargeTransaction, error) {
	if limit <= 0 {
		limit = 5
	}
	var results []LargeTransaction
	err := r.db.SelectContext(ctx, &results, `
		SELECT amount, merchant_name, created_at
		FROM card_transactions
		WHERE user_id = $1 AND status = 'completed' AND created_at >= $2 AND created_at < $3
		ORDER BY amount DESC LIMIT $4`,
		userID, start, end, limit)
	return results, err
}
