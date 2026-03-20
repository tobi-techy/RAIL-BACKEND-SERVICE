package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type StashLockRepository struct {
	db *sqlx.DB
}

func NewStashLockRepository(db *sqlx.DB) *StashLockRepository {
	return &StashLockRepository{db: db}
}

func (r *StashLockRepository) Create(ctx context.Context, c *entities.StashLockCycle) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO stash_lock_cycles
		 (id, user_id, deposit_id, amount, lock_start, lock_end, window_end, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.UserID, c.DepositID, c.Amount, c.LockStart, c.LockEnd, c.WindowEnd, c.Status)
	return err
}

func (r *StashLockRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.StashLockCycle, error) {
	var rows []*entities.StashLockCycle
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, user_id, deposit_id, amount, lock_start, lock_end, window_end, status, created_at, updated_at
		 FROM stash_lock_cycles WHERE user_id = $1 ORDER BY lock_start DESC`,
		userID)
	return rows, err
}

func (r *StashLockRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE stash_lock_cycles SET status=$1, updated_at=NOW() WHERE id=$2`,
		status, id)
	return err
}

// GetExpiredWindows returns window_open cycles whose window_end has passed.
func (r *StashLockRepository) GetExpiredWindows(ctx context.Context, now time.Time, limit int) ([]*entities.StashLockCycle, error) {
	var rows []*entities.StashLockCycle
	err := r.db.SelectContext(ctx, &rows,
		`SELECT id, user_id, deposit_id, amount, lock_start, lock_end, window_end, status, created_at, updated_at
		 FROM stash_lock_cycles
		 WHERE status = 'window_open' AND window_end < $1
		 LIMIT $2`,
		now, limit)
	return rows, err
}
