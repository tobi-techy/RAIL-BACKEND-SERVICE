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

// PriceTrackingRepository tracks item price changes from receipt scans.
type PriceTrackingRepository struct {
	db *sqlx.DB
}

func NewPriceTrackingRepository(db *sqlx.DB) *PriceTrackingRepository {
	return &PriceTrackingRepository{db: db}
}

type priceRow struct {
	ItemName    string          `db:"item_name"`
	Currency    string          `db:"currency"`
	Occurrences int             `db:"occurrences"`
	FirstPrice  decimal.Decimal `db:"first_price"`
	LatestPrice decimal.Decimal `db:"latest_price"`
	FirstSeen   time.Time       `db:"first_seen"`
	LastSeen    time.Time       `db:"last_seen"`
}

func (r *PriceTrackingRepository) GetPriceChanges(ctx context.Context, userID uuid.UUID, limit int) ([]ai.PriceChange, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	since := time.Now().AddDate(-1, 0, 0)

	query := `
		WITH item_prices AS (
			SELECT
				item->>'name' AS item_name,
				(item->>'price')::decimal AS price,
				currency,
				created_at
			FROM receipt_scans, jsonb_array_elements(items) AS item
			WHERE user_id = $1 AND created_at >= $2 AND jsonb_typeof(items) = 'array' AND jsonb_array_length(items) > 0
		)
		SELECT item_name, currency, COUNT(*) AS occurrences,
			(array_agg(price ORDER BY created_at ASC))[1] AS first_price,
			(array_agg(price ORDER BY created_at DESC))[1] AS latest_price,
			MIN(created_at) AS first_seen, MAX(created_at) AS last_seen
		FROM item_prices WHERE item_name IS NOT NULL AND item_name != ''
		GROUP BY item_name, currency HAVING COUNT(*) >= 2
		ORDER BY ABS((array_agg(price ORDER BY created_at DESC))[1] - (array_agg(price ORDER BY created_at ASC))[1]) DESC
		LIMIT $3`

	var rows []priceRow
	if err := r.db.SelectContext(ctx, &rows, query, userID, since, limit); err != nil {
		return nil, fmt.Errorf("price changes query: %w", err)
	}

	results := make([]ai.PriceChange, len(rows))
	for i, row := range rows {
		changePct := decimal.Zero
		if !row.FirstPrice.IsZero() {
			changePct = row.LatestPrice.Sub(row.FirstPrice).Div(row.FirstPrice).Mul(decimal.NewFromInt(100))
		}
		results[i] = ai.PriceChange{
			ItemName:      row.ItemName,
			PreviousPrice: row.FirstPrice.StringFixed(2),
			CurrentPrice:  row.LatestPrice.StringFixed(2),
			ChangePct:     changePct.StringFixed(1) + "%",
			Currency:      row.Currency,
			FirstSeen:     row.FirstSeen.Format("2006-01-02"),
			LastSeen:      row.LastSeen.Format("2006-01-02"),
			Occurrences:   row.Occurrences,
		}
	}
	return results, nil
}
