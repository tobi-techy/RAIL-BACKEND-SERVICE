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

// SpendingCommitmentRepository persists self-imposed daily spending commitments
// and their per-day usage counters.
type SpendingCommitmentRepository struct {
	db *sqlx.DB
}

func NewSpendingCommitmentRepository(db *sqlx.DB) *SpendingCommitmentRepository {
	return &SpendingCommitmentRepository{db: db}
}

// GetCommitment returns the user's commitment, or nil if none exists.
func (r *SpendingCommitmentRepository) GetCommitment(ctx context.Context, userID uuid.UUID) (*entities.SpendingCommitment, error) {
	var c entities.SpendingCommitment
	err := r.db.GetContext(ctx, &c, `SELECT * FROM spending_commitments WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get spending commitment: %w", err)
	}
	return &c, nil
}

// UpsertCommitment inserts or updates a user's commitment.
func (r *SpendingCommitmentRepository) UpsertCommitment(ctx context.Context, c *entities.SpendingCommitment) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO spending_commitments (
			user_id, daily_limit_cents, currency, is_active, increase_count, last_increased_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			daily_limit_cents = EXCLUDED.daily_limit_cents,
			currency = EXCLUDED.currency,
			is_active = EXCLUDED.is_active,
			increase_count = EXCLUDED.increase_count,
			last_increased_at = EXCLUDED.last_increased_at,
			updated_at = NOW()`,
		c.UserID, c.DailyLimitCents, c.Currency, c.IsActive, c.IncreaseCount, c.LastIncreasedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert spending commitment: %w", err)
	}
	return nil
}

// DeactivateCommitment marks a user's commitment inactive.
func (r *SpendingCommitmentRepository) DeactivateCommitment(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE spending_commitments SET is_active = false, updated_at = NOW() WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("deactivate spending commitment: %w", err)
	}
	return nil
}

// GetOrCreateUsage returns the user's daily usage row, creating it if absent.
func (r *SpendingCommitmentRepository) GetOrCreateUsage(ctx context.Context, userID uuid.UUID, resetAt time.Time) (*entities.SpendingCommitmentUsage, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO spending_commitment_daily_usage (user_id, used_cents, reset_at, created_at, updated_at)
		VALUES ($1, 0, $2, NOW(), NOW())
		ON CONFLICT (user_id) DO NOTHING`, userID, resetAt)
	if err != nil {
		return nil, fmt.Errorf("create spending commitment usage: %w", err)
	}
	var u entities.SpendingCommitmentUsage
	if err := r.db.GetContext(ctx, &u,
		`SELECT * FROM spending_commitment_daily_usage WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("get spending commitment usage: %w", err)
	}
	return &u, nil
}

// ResetExpiredUsage zeroes the counter and advances reset_at when the day rolled over.
func (r *SpendingCommitmentRepository) ResetExpiredUsage(ctx context.Context, userID uuid.UUID, now, nextReset time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE spending_commitment_daily_usage
		SET used_cents = CASE WHEN reset_at <= $2 THEN 0 ELSE used_cents END,
			reset_at = CASE WHEN reset_at <= $2 THEN $3 ELSE reset_at END,
			updated_at = NOW()
		WHERE user_id = $1`, userID, now, nextReset)
	if err != nil {
		return fmt.Errorf("reset spending commitment usage: %w", err)
	}
	return nil
}

// AtomicIncrementUsage adds cents to the counter only if it stays within limitCents.
// Returns false if the increment would breach the cap.
func (r *SpendingCommitmentRepository) AtomicIncrementUsage(ctx context.Context, userID uuid.UUID, cents, limitCents int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE spending_commitment_daily_usage
		SET used_cents = used_cents + $2, updated_at = NOW()
		WHERE user_id = $1 AND used_cents + $2 <= $3`, userID, cents, limitCents)
	if err != nil {
		return false, fmt.Errorf("increment spending commitment usage: %w", err)
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

// DecrementUsage lowers the counter (used to release usage on a reversed outflow),
// clamping at zero.
func (r *SpendingCommitmentRepository) DecrementUsage(ctx context.Context, userID uuid.UUID, cents int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE spending_commitment_daily_usage
		SET used_cents = GREATEST(used_cents - $2, 0), updated_at = NOW()
		WHERE user_id = $1`, userID, cents)
	if err != nil {
		return fmt.Errorf("decrement spending commitment usage: %w", err)
	}
	return nil
}
