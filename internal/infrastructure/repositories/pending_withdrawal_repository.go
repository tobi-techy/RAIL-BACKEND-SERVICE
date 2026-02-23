package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// PendingWithdrawalRepository handles pending withdrawal persistence
type PendingWithdrawalRepository struct {
	db *sqlx.DB
}

// NewPendingWithdrawalRepository creates a new pending withdrawal repository
func NewPendingWithdrawalRepository(db *sqlx.DB) *PendingWithdrawalRepository {
	return &PendingWithdrawalRepository{db: db}
}

// Create creates a new pending withdrawal
func (r *PendingWithdrawalRepository) Create(ctx context.Context, pw *entities.PendingWithdrawal) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO pending_withdrawals (id, user_id, amount, requested_at, execute_after, status)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		pw.ID, pw.UserID, pw.Amount, pw.RequestedAt, pw.ExecuteAfter, pw.Status)
	return err
}

// GetByID retrieves a pending withdrawal by ID
func (r *PendingWithdrawalRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.PendingWithdrawal, error) {
	var pw entities.PendingWithdrawal
	err := r.db.GetContext(ctx, &pw, `SELECT * FROM pending_withdrawals WHERE id = $1`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pw, nil
}

// GetByUserID retrieves all pending withdrawals for a user
func (r *PendingWithdrawalRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.PendingWithdrawal, error) {
	var pws []*entities.PendingWithdrawal
	err := r.db.SelectContext(ctx, &pws,
		`SELECT * FROM pending_withdrawals WHERE user_id = $1 ORDER BY requested_at DESC`, userID)
	return pws, err
}

// GetPendingByUserID retrieves only pending status withdrawals for a user
func (r *PendingWithdrawalRepository) GetPendingByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.PendingWithdrawal, error) {
	var pws []*entities.PendingWithdrawal
	err := r.db.SelectContext(ctx, &pws,
		`SELECT * FROM pending_withdrawals WHERE user_id = $1 AND status = 'pending' ORDER BY requested_at`, userID)
	return pws, err
}

// GetReadyToExecute retrieves all withdrawals ready to execute
func (r *PendingWithdrawalRepository) GetReadyToExecute(ctx context.Context) ([]*entities.PendingWithdrawal, error) {
	var pws []*entities.PendingWithdrawal
	err := r.db.SelectContext(ctx, &pws,
		`SELECT * FROM pending_withdrawals WHERE status = 'pending' AND execute_after <= NOW()`)
	return pws, err
}

// UpdateStatus updates the status of a pending withdrawal
func (r *PendingWithdrawalRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.PendingWithdrawalStatus, timestamp *time.Time) error {
	query := `UPDATE pending_withdrawals SET status = $1`
	args := []interface{}{status}

	if status == entities.PendingWithdrawalStatusExecuted && timestamp != nil {
		query += `, executed_at = $2 WHERE id = $3`
		args = append(args, timestamp, id)
	} else if status == entities.PendingWithdrawalStatusCancelled && timestamp != nil {
		query += `, cancelled_at = $2 WHERE id = $3`
		args = append(args, timestamp, id)
	} else {
		query += ` WHERE id = $2`
		args = append(args, id)
	}

	_, err := r.db.ExecContext(ctx, query, args...)
	return err
}

// Cancel cancels a pending withdrawal
func (r *PendingWithdrawalRepository) Cancel(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	return r.UpdateStatus(ctx, id, entities.PendingWithdrawalStatusCancelled, &now)
}
