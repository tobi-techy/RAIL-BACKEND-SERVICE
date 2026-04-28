package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
)

// UserStatsRepository provides user stats for achievement evaluation
type UserStatsRepository struct {
	db *sqlx.DB
}

func NewUserStatsRepository(db *sqlx.DB) *UserStatsRepository {
	return &UserStatsRepository{db: db}
}

func (r *UserStatsRepository) GetDepositCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM deposits WHERE user_id = $1 AND status = 'completed'`, userID)
	return count, err
}

func (r *UserStatsRepository) GetTotalBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, `SELECT COALESCE(SUM(balance), 0) FROM ledger_accounts WHERE user_id = $1 AND account_type IN ('spending_balance', 'stash_balance')`, userID)
	return total, err
}

func (r *UserStatsRepository) GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	var bal decimal.Decimal
	err := r.db.GetContext(ctx, &bal, `SELECT COALESCE(balance, 0) FROM ledger_accounts WHERE user_id = $1 AND account_type = 'stash_balance'`, userID)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	return bal, err
}

func (r *UserStatsRepository) GetRoundupCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM roundup_transactions WHERE user_id = $1 AND status = 'collected'`, userID)
	return count, err
}

func (r *UserStatsRepository) GetAccountAgeDays(ctx context.Context, userID uuid.UUID) (int, error) {
	var days int
	err := r.db.GetContext(ctx, &days, `SELECT EXTRACT(DAY FROM NOW() - created_at)::int FROM users WHERE id = $1`, userID)
	return days, err
}

func (r *UserStatsRepository) GetReferralCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM users WHERE referred_by = $1`, userID)
	if err != nil {
		return 0, nil // referral column may not exist yet
	}
	return count, nil
}

func (r *UserStatsRepository) GetDaysSinceLastStashWithdrawal(ctx context.Context, userID uuid.UUID) (int, error) {
	var days int
	err := r.db.GetContext(ctx, &days, `
		SELECT COALESCE(EXTRACT(DAY FROM NOW() - MAX(created_at))::int, 9999)
		FROM stash_transfers WHERE user_id = $1 AND direction = 'stash_to_spending'`, userID)
	if err == sql.ErrNoRows {
		return 9999, nil // never withdrawn
	}
	return days, err
}

func (r *UserStatsRepository) GetUserRank(ctx context.Context, userID uuid.UUID) (int, error) {
	var rank int
	err := r.db.GetContext(ctx, &rank, `
		SELECT COUNT(*) + 1 FROM users WHERE created_at < (SELECT created_at FROM users WHERE id = $1) AND is_active = true`, userID)
	return rank, err
}

// Ensure it's used
var _ time.Time
