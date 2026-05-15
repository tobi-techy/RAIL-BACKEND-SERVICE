package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type YieldRepository struct {
	db *sqlx.DB
}

func NewYieldRepository(db *sqlx.DB) *YieldRepository {
	return &YieldRepository{db: db}
}

// RecordSnapshot writes a new balance snapshot for a user.
func (r *YieldRepository) RecordSnapshot(ctx context.Context, userID uuid.UUID, balance decimal.Decimal) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO yield_balance_snapshots (user_id, balance) VALUES ($1, $2)`,
		userID, balance)
	return err
}

// GetSnapshotsInWindow returns all snapshots for a user within [from, to].
func (r *YieldRepository) GetSnapshotsInWindow(ctx context.Context, userID uuid.UUID, from, to time.Time) ([]*entities.YieldBalanceSnapshot, error) {
	var rows []*entities.YieldBalanceSnapshot
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, user_id, balance, recorded_at
		 FROM yield_balance_snapshots
		 WHERE user_id = $1 AND recorded_at >= $2 AND recorded_at <= $3
		 ORDER BY recorded_at ASC`,
		userID, from, to)
	return rows, err
}

// GetLastSnapshotBefore returns the most recent snapshot before a given time.
func (r *YieldRepository) GetLastSnapshotBefore(ctx context.Context, userID uuid.UUID, before time.Time) (*entities.YieldBalanceSnapshot, error) {
	var s entities.YieldBalanceSnapshot
	err := r.db.GetContext(ctx, &s,
		`SELECT id, user_id, balance, recorded_at
		 FROM yield_balance_snapshots
		 WHERE user_id = $1 AND recorded_at < $2
		 ORDER BY recorded_at DESC LIMIT 1`,
		userID, before)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// GetAllUsersWithSnapshotsInWindow returns distinct user IDs that had a positive stash balance
// at any point up to the end of the window. The computeTWB function handles zero-balance periods.
func (r *YieldRepository) GetAllUsersWithSnapshotsInWindow(ctx context.Context, from, to time.Time) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.SelectContext(ctx, &ids,
		`SELECT DISTINCT user_id FROM yield_balance_snapshots
		 WHERE recorded_at <= $2 AND balance > 0`,
		from, to)
	return ids, err
}

// CreateDistribution inserts a new distribution record.
func (r *YieldRepository) CreateDistribution(ctx context.Context, d *entities.YieldDistribution) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO yield_distributions
		 (id, period_start, period_end, total_reward, total_twb, total_distributed, remainder, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		d.ID, d.PeriodStart, d.PeriodEnd, d.TotalReward, d.TotalTWB,
		d.TotalDistributed, d.Remainder, d.Status)
	return err
}

// UpdateDistribution updates totals and status on a distribution.
func (r *YieldRepository) UpdateDistribution(ctx context.Context, d *entities.YieldDistribution) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE yield_distributions
		 SET total_twb=$1, total_distributed=$2, remainder=$3, status=$4
		 WHERE id=$5`,
		d.TotalTWB, d.TotalDistributed, d.Remainder, d.Status, d.ID)
	return err
}

// UpsertDistributionUser inserts a per-user reward row (idempotent via ON CONFLICT DO NOTHING).
func (r *YieldRepository) UpsertDistributionUser(ctx context.Context, u *entities.YieldDistributionUser) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO yield_distribution_users
		 (id, distribution_id, user_id, twb, share_pct, reward_amount, credited_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (distribution_id, user_id) DO NOTHING`,
		u.ID, u.DistributionID, u.UserID, u.TWB, u.SharePct, u.RewardAmount, u.CreditedAt)
	return err
}

// GetDistributionByPeriod returns an existing distribution for a period if one exists.
func (r *YieldRepository) GetDistributionByPeriod(ctx context.Context, start, end time.Time) (*entities.YieldDistribution, error) {
	var d entities.YieldDistribution
	err := r.db.GetContext(ctx, &d,
		`SELECT id, period_start, period_end, total_reward, total_twb, total_distributed, remainder, status, created_at
		 FROM yield_distributions WHERE period_start=$1 AND period_end=$2`,
		start, end)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &d, err
}

// GetTotalDistributedYield returns the sum of all total_distributed across completed distributions.
func (r *YieldRepository) GetTotalDistributedYield(ctx context.Context) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.QueryRowxContext(ctx,
		`SELECT COALESCE(SUM(total_distributed), 0) FROM yield_distributions WHERE status = 'completed'`,
	).Scan(&total)
	return total, err
}
