package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/drip"
	"github.com/shopspring/decimal"
)

// DividendRepository handles dividend event persistence
type DividendRepository struct {
	db *sqlx.DB
}

// NewDividendRepository creates a new dividend repository
func NewDividendRepository(db *sqlx.DB) *DividendRepository {
	return &DividendRepository{db: db}
}

// Create creates a new dividend event
func (r *DividendRepository) Create(ctx context.Context, event *drip.DividendEvent) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO dividend_events (id, user_id, symbol, amount, shares_held, ex_date, pay_date, 
		 received_at, reinvested, reinvested_at, reinvest_order_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		event.ID, event.UserID, event.Symbol, event.Amount, event.SharesHeld,
		event.ExDate, event.PayDate, event.ReceivedAt, event.Reinvested,
		event.ReinvestedAt, event.ReinvestOrder)
	return err
}

// GetByUserID retrieves dividend events for a user
func (r *DividendRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*drip.DividendEvent, error) {
	var events []*drip.DividendEvent
	err := r.db.SelectContext(ctx, &events,
		`SELECT * FROM dividend_events WHERE user_id = $1 ORDER BY received_at DESC LIMIT $2`,
		userID, limit)
	return events, err
}

// GetPendingReinvestment retrieves dividends that haven't been reinvested
func (r *DividendRepository) GetPendingReinvestment(ctx context.Context) ([]*drip.DividendEvent, error) {
	var events []*drip.DividendEvent
	err := r.db.SelectContext(ctx, &events,
		`SELECT * FROM dividend_events WHERE reinvested = false ORDER BY received_at LIMIT 500 FOR UPDATE SKIP LOCKED`)
	return events, err
}

// MarkReinvested marks a dividend as reinvested
func (r *DividendRepository) MarkReinvested(ctx context.Context, id uuid.UUID, orderID uuid.UUID) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx,
		`UPDATE dividend_events SET reinvested = true, reinvested_at = $1, reinvest_order_id = $2 WHERE id = $3`,
		now, orderID, id)
	return err
}

// GetTotalReinvested returns total reinvested amount for a user
func (r *DividendRepository) GetTotalReinvested(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total,
		`SELECT COALESCE(SUM(amount), 0) FROM dividend_events WHERE user_id = $1 AND reinvested = true`, userID)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	return total, err
}
