package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// WithdrawalRepository handles withdrawal persistence
type WithdrawalRepository struct {
	db *sqlx.DB
}

// NewWithdrawalRepository creates a new withdrawal repository
func NewWithdrawalRepository(db *sqlx.DB) *WithdrawalRepository {
	return &WithdrawalRepository{db: db}
}

// Create creates a new withdrawal record
func (r *WithdrawalRepository) Create(ctx context.Context, withdrawal *entities.Withdrawal) error {
	query := `
		INSERT INTO withdrawals (
			id, user_id, alpaca_account_id, amount, destination_chain, destination_address,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.ExecContext(ctx, query,
		withdrawal.ID,
		withdrawal.UserID,
		withdrawal.AlpacaAccountID,
		withdrawal.Amount,
		withdrawal.DestinationChain,
		withdrawal.DestinationAddress,
		withdrawal.Status,
		withdrawal.CreatedAt,
		withdrawal.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create withdrawal: %w", err)
	}

	return nil
}

// GetByID retrieves a withdrawal by ID
func (r *WithdrawalRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error) {
	query := `
		SELECT id, user_id, alpaca_account_id, amount, destination_chain, destination_address,
			status, alpaca_journal_id, due_transfer_id, due_recipient_id, tx_hash, error_message,
			created_at, updated_at, completed_at
		FROM withdrawals
		WHERE id = $1
	`

	var withdrawal entities.Withdrawal
	err := r.db.GetContext(ctx, &withdrawal, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("withdrawal not found")
		}
		return nil, fmt.Errorf("failed to get withdrawal: %w", err)
	}

	return &withdrawal, nil
}

// GetByUserID retrieves withdrawals for a user
func (r *WithdrawalRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error) {
	query := `
		SELECT id, user_id, alpaca_account_id, amount, destination_chain, destination_address,
			status, alpaca_journal_id, due_transfer_id, due_recipient_id, tx_hash, error_message,
			created_at, updated_at, completed_at
		FROM withdrawals
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	var withdrawals []*entities.Withdrawal
	err := r.db.SelectContext(ctx, &withdrawals, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get withdrawals: %w", err)
	}

	return withdrawals, nil
}

// UpdateStatus updates the withdrawal status
func (r *WithdrawalRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error {
	query := `
		UPDATE withdrawals
		SET status = $1, updated_at = $2
		WHERE id = $3
	`

	_, err := r.db.ExecContext(ctx, query, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}

	return nil
}

// UpdateAlpacaJournal updates the Alpaca journal ID
func (r *WithdrawalRepository) UpdateAlpacaJournal(ctx context.Context, id uuid.UUID, journalID string) error {
	query := `
		UPDATE withdrawals
		SET alpaca_journal_id = $1, status = $2, updated_at = $3
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, journalID, entities.WithdrawalStatusAlpacaDebited, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update alpaca journal: %w", err)
	}

	return nil
}

// UpdateDueTransfer updates the Due transfer details
func (r *WithdrawalRepository) UpdateDueTransfer(ctx context.Context, id uuid.UUID, transferID, recipientID string) error {
	query := `
		UPDATE withdrawals
		SET due_transfer_id = $1, due_recipient_id = $2, status = $3, updated_at = $4
		WHERE id = $5
	`

	_, err := r.db.ExecContext(ctx, query, transferID, recipientID, entities.WithdrawalStatusDueProcessing, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update due transfer: %w", err)
	}

	return nil
}

// UpdateTxHash updates the transaction hash
func (r *WithdrawalRepository) UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error {
	query := `
		UPDATE withdrawals
		SET tx_hash = $1, status = $2, updated_at = $3
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, txHash, entities.WithdrawalStatusOnChainTransfer, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update tx hash: %w", err)
	}

	return nil
}

// MarkCompleted marks the withdrawal as completed
func (r *WithdrawalRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	query := `
		UPDATE withdrawals
		SET status = $1, completed_at = $2, updated_at = $3
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, entities.WithdrawalStatusCompleted, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to mark withdrawal completed: %w", err)
	}

	return nil
}

// MarkFailed marks the withdrawal as failed
func (r *WithdrawalRepository) MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error {
	query := `
		UPDATE withdrawals
		SET status = $1, error_message = $2, updated_at = $3
		WHERE id = $4
	`

	_, err := r.db.ExecContext(ctx, query, entities.WithdrawalStatusFailed, errorMsg, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to mark withdrawal failed: %w", err)
	}

	return nil
}

// GetTotalCompletedWithdrawals returns the total amount of all completed withdrawals
// Used by reconciliation service to verify withdrawal totals
func (r *WithdrawalRepository) GetTotalCompletedWithdrawals(ctx context.Context) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0) as total
		FROM withdrawals
		WHERE status = $1
	`
	
	var total decimal.Decimal
	err := r.db.QueryRowContext(ctx, query, entities.WithdrawalStatusCompleted).Scan(&total)
	if err != nil {
		if err == sql.ErrNoRows {
			return decimal.Zero, nil
		}
		return decimal.Zero, fmt.Errorf("failed to get total completed withdrawals: %w", err)
	}
	
	return total, nil
}

// GetStuckWithdrawals returns withdrawals stuck in non-terminal states beyond the SLA threshold
// Used for status enquiry reconciliation when webhooks fail
func (r *WithdrawalRepository) GetStuckWithdrawals(ctx context.Context, slaThreshold time.Duration) ([]*entities.Withdrawal, error) {
	cutoff := time.Now().Add(-slaThreshold)
	query := `
		SELECT id, user_id, alpaca_account_id, amount, destination_chain, destination_address,
			status, alpaca_journal_id, due_transfer_id, due_recipient_id, tx_hash, error_message,
			created_at, updated_at, completed_at
		FROM withdrawals
		WHERE status NOT IN ($1, $2, $3)
		  AND updated_at < $4
		ORDER BY updated_at ASC
		LIMIT 100
	`

	var withdrawals []*entities.Withdrawal
	err := r.db.SelectContext(ctx, &withdrawals, query,
		entities.WithdrawalStatusCompleted,
		entities.WithdrawalStatusFailed,
		entities.WithdrawalStatusReversed,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get stuck withdrawals: %w", err)
	}

	return withdrawals, nil
}

// MarkTimeout marks a withdrawal as timed out (no response within SLA)
func (r *WithdrawalRepository) MarkTimeout(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE withdrawals
		SET status = $1, updated_at = $2
		WHERE id = $3
	`

	_, err := r.db.ExecContext(ctx, query, entities.WithdrawalStatusTimeout, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to mark withdrawal timeout: %w", err)
	}

	return nil
}


// GetPendingWithdrawalsTotal returns the total amount of pending withdrawals for a user
// Used to prevent race conditions by accounting for in-flight withdrawals
func (r *WithdrawalRepository) GetPendingWithdrawalsTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0) as total
		FROM withdrawals
		WHERE user_id = $1
		  AND status NOT IN ($2, $3, $4)
	`

	var total decimal.Decimal
	err := r.db.QueryRowContext(ctx, query, userID,
		entities.WithdrawalStatusCompleted,
		entities.WithdrawalStatusFailed,
		entities.WithdrawalStatusReversed,
	).Scan(&total)
	if err != nil {
		if err == sql.ErrNoRows {
			return decimal.Zero, nil
		}
		return decimal.Zero, fmt.Errorf("failed to get pending withdrawals total: %w", err)
	}

	return total, nil
}
