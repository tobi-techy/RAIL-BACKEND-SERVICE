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

// StashTransferRepository handles stash transfer persistence
type StashTransferRepository struct {
	db *sqlx.DB
}

// NewStashTransferRepository creates a new stash transfer repository
func NewStashTransferRepository(db *sqlx.DB) *StashTransferRepository {
	return &StashTransferRepository{db: db}
}

// Create creates a new stash transfer
func (r *StashTransferRepository) Create(ctx context.Context, transfer *entities.StashTransfer) error {
	query := `
		INSERT INTO stash_transfers (id, user_id, amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := r.db.ExecContext(ctx, query,
		transfer.ID,
		transfer.UserID,
		transfer.Amount,
		transfer.Status,
		transfer.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create stash transfer: %w", err)
	}

	return nil
}

// GetByID retrieves a stash transfer by ID
func (r *StashTransferRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.StashTransfer, error) {
	query := `
		SELECT id, user_id, amount, status, created_at, completed_at
		FROM stash_transfers
		WHERE id = $1
	`

	var transfer entities.StashTransfer
	err := r.db.GetContext(ctx, &transfer, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("stash transfer not found")
		}
		return nil, fmt.Errorf("failed to get stash transfer: %w", err)
	}

	return &transfer, nil
}

// GetByUserID retrieves stash transfers for a user
func (r *StashTransferRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.StashTransfer, error) {
	query := `
		SELECT id, user_id, amount, status, created_at, completed_at
		FROM stash_transfers
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	var transfers []*entities.StashTransfer
	err := r.db.SelectContext(ctx, &transfers, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get stash transfers: %w", err)
	}

	return transfers, nil
}

// UpdateStatus updates the stash transfer status
func (r *StashTransferRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.StashTransferStatus) error {
	query := `UPDATE stash_transfers SET status = $1 WHERE id = $2`

	_, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update stash transfer status: %w", err)
	}

	return nil
}

// MarkCompleted marks the stash transfer as completed
func (r *StashTransferRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	query := `UPDATE stash_transfers SET status = $1, completed_at = $2 WHERE id = $3`

	_, err := r.db.ExecContext(ctx, query, entities.StashTransferStatusCompleted, now, id)
	if err != nil {
		return fmt.Errorf("failed to mark stash transfer completed: %w", err)
	}

	return nil
}

// MarkFailed marks the stash transfer as failed
func (r *StashTransferRepository) MarkFailed(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE stash_transfers SET status = $1 WHERE id = $2`

	_, err := r.db.ExecContext(ctx, query, entities.StashTransferStatusFailed, id)
	if err != nil {
		return fmt.Errorf("failed to mark stash transfer failed: %w", err)
	}

	return nil
}
