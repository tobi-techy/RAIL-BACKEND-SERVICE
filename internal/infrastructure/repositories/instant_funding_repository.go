package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
)

// InstantFundingRepository handles instant funding persistence
type InstantFundingRepository struct {
	db *sqlx.DB
}

// NewInstantFundingRepository creates a new instant funding repository
func NewInstantFundingRepository(db *sqlx.DB) *InstantFundingRepository {
	return &InstantFundingRepository{db: db}
}

// Create inserts a new instant funding record
func (r *InstantFundingRepository) Create(ctx context.Context, f *funding.InstantFunding) error {
	query := `
		INSERT INTO instant_fundings (id, user_id, alpaca_account_id, amount, journal_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.ExecContext(ctx, query,
		f.ID, f.UserID, f.AlpacaAccountID, f.Amount, f.JournalID, f.Status, f.CreatedAt)
	return err
}

// GetActiveByUserID returns all active instant fundings for a user
func (r *InstantFundingRepository) GetActiveByUserID(ctx context.Context, userID uuid.UUID) ([]*funding.InstantFunding, error) {
	query := `
		SELECT id, user_id, alpaca_account_id, amount, journal_id, status, created_at, settled_at
		FROM instant_fundings
		WHERE user_id = $1 AND status = 'active'
		ORDER BY created_at DESC`
	
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fundings []*funding.InstantFunding
	for rows.Next() {
		f := &funding.InstantFunding{}
		if err := rows.Scan(&f.ID, &f.UserID, &f.AlpacaAccountID, &f.Amount, &f.JournalID, &f.Status, &f.CreatedAt, &f.SettledAt); err != nil {
			return nil, err
		}
		fundings = append(fundings, f)
	}
	return fundings, rows.Err()
}

// GetTotalActiveAmount returns the sum of all active instant funding amounts
func (r *InstantFundingRepository) GetTotalActiveAmount(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `SELECT COALESCE(SUM(amount), 0) FROM instant_fundings WHERE user_id = $1 AND status = 'active'`
	var total decimal.Decimal
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&total)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	return total, err
}

// UpdateStatus updates the status of an instant funding
func (r *InstantFundingRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status funding.InstantFundingStatus) error {
	query := `UPDATE instant_fundings SET status = $1 WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, status, id)
	return err
}

// MarkSettled marks an instant funding as settled
func (r *InstantFundingRepository) MarkSettled(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE instant_fundings SET status = 'settled', settled_at = $1 WHERE id = $2`
	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// CountTodayRequests counts instant funding requests made today by a user
func (r *InstantFundingRepository) CountTodayRequests(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*) FROM instant_fundings 
		WHERE user_id = $1 AND created_at >= CURRENT_DATE`
	var count int
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&count)
	return count, err
}

// UserAccountRepository provides user account info for instant funding limits
type UserAccountRepository struct {
	db *sqlx.DB
}

// NewUserAccountRepository creates a new user account repository
func NewUserAccountRepository(db *sqlx.DB) *UserAccountRepository {
	return &UserAccountRepository{db: db}
}

// GetCreatedAt returns when the user account was created
func (r *UserAccountRepository) GetCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	query := `SELECT created_at FROM users WHERE id = $1`
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, query, userID).Scan(&createdAt)
	return createdAt, err
}
