package repositories

import (
	"context"
	"fmt"
	"strings"
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
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO stash_lock_cycles
		 (id, user_id, deposit_id, amount, lock_start, lock_end, window_end, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.UserID, c.DepositID, c.Amount, c.LockStart, c.LockEnd, c.WindowEnd, c.Status)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return fmt.Errorf("stash lock cycle already exists for deposit_id %s", c.DepositID)
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("stash lock cycle insert had no effect for deposit_id %s", c.DepositID)
	}
	return nil
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

// RelockCycle atomically marks oldID as relocked and inserts newCycle in a single transaction.
func (r *StashLockRepository) RelockCycle(ctx context.Context, oldID uuid.UUID, newCycle *entities.StashLockCycle) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("stashlock: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		`UPDATE stash_lock_cycles SET status=$1, updated_at=NOW() WHERE id=$2`,
		entities.StashCycleStatusRelocked, oldID,
	); err != nil {
		return fmt.Errorf("stashlock: mark relocked: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO stash_lock_cycles
		 (id, user_id, deposit_id, amount, lock_start, lock_end, window_end, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		newCycle.ID, newCycle.UserID, newCycle.DepositID, newCycle.Amount,
		newCycle.LockStart, newCycle.LockEnd, newCycle.WindowEnd, newCycle.Status)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return fmt.Errorf("stash lock cycle already exists for deposit_id %s", newCycle.DepositID)
		}
		return fmt.Errorf("stashlock: insert new cycle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("stashlock: new cycle insert had no effect for deposit_id %s", newCycle.DepositID)
	}

	return tx.Commit()
}
