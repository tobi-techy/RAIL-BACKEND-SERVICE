package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
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
			id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, idempotency_key, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`

	_, err := r.db.ExecContext(ctx, query,
		withdrawal.ID,
		withdrawal.UserID,
		withdrawal.WithdrawalType,
		withdrawal.Currency,
		withdrawal.Amount,
		withdrawal.SourceAccount,
		withdrawal.CircleWalletID,
		withdrawal.DestinationType,
		withdrawal.DestinationChain,
		withdrawal.DestinationAddress,
		withdrawal.BankAccountID,
		withdrawal.FeeAmount,
		withdrawal.FeeCurrency,
		withdrawal.Status,
		withdrawal.IdempotencyKey,
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
		SELECT id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, bridge_transfer_id, tx_hash, error_message,
			idempotency_key, created_at, updated_at, completed_at
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
		SELECT id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, bridge_transfer_id, tx_hash, error_message,
			idempotency_key, created_at, updated_at, completed_at
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

// GetByIdempotencyKey retrieves a withdrawal by idempotency key
func (r *WithdrawalRepository) GetByIdempotencyKey(ctx context.Context, key string) (*entities.Withdrawal, error) {
	query := `
		SELECT id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, bridge_transfer_id, tx_hash, error_message,
			idempotency_key, created_at, updated_at, completed_at
		FROM withdrawals
		WHERE idempotency_key = $1
	`

	var withdrawal entities.Withdrawal
	err := r.db.GetContext(ctx, &withdrawal, query, key)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get withdrawal by idempotency key: %w", err)
	}

	return &withdrawal, nil
}

// UpdateStatus updates the withdrawal status
func (r *WithdrawalRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error {
	query := `UPDATE withdrawals SET status = $1, updated_at = $2 WHERE id = $3`

	_, err := r.db.ExecContext(ctx, query, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}

	return nil
}

// UpdateBridgeTransfer updates the Bridge transfer ID
func (r *WithdrawalRepository) UpdateBridgeTransfer(ctx context.Context, id uuid.UUID, transferID string) error {
	query := `UPDATE withdrawals SET bridge_transfer_id = $1, updated_at = $2 WHERE id = $3`

	_, err := r.db.ExecContext(ctx, query, transferID, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update bridge transfer: %w", err)
	}

	return nil
}

// UpdateTxHash updates the transaction hash
func (r *WithdrawalRepository) UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error {
	query := `
		UPDATE withdrawals
		SET
			tx_hash = $1,
			status = CASE
				WHEN status IN ($2, $3, $4) THEN status
				ELSE $5
			END,
			updated_at = $6
		WHERE id = $7
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		txHash,
		entities.WithdrawalStatusCompleted,
		entities.WithdrawalStatusFailed,
		entities.WithdrawalStatusReversed,
		entities.WithdrawalStatusOnChainTransfer,
		time.Now(),
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to update tx hash: %w", err)
	}

	return nil
}

// MarkCompleted marks the withdrawal as completed
func (r *WithdrawalRepository) MarkCompleted(ctx context.Context, id uuid.UUID) error {
	now := time.Now()
	query := `UPDATE withdrawals SET status = $1, completed_at = $2, updated_at = $3 WHERE id = $4`

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
		  AND status NOT IN ($5, $6)
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		entities.WithdrawalStatusFailed,
		errorMsg,
		time.Now(),
		id,
		entities.WithdrawalStatusCompleted,
		entities.WithdrawalStatusReversed,
	)
	if err != nil {
		return fmt.Errorf("failed to mark withdrawal failed: %w", err)
	}

	return nil
}

// GetPendingWithdrawalsTotal returns the total amount of pending withdrawals for a user
func (r *WithdrawalRepository) GetPendingWithdrawalsTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount + COALESCE(fee_amount, 0)), 0) as total
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

// GetTotalCompletedWithdrawals returns the total amount of all completed withdrawals
func (r *WithdrawalRepository) GetTotalCompletedWithdrawals(ctx context.Context) (decimal.Decimal, error) {
	query := `SELECT COALESCE(SUM(amount), 0) as total FROM withdrawals WHERE status = $1`

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
func (r *WithdrawalRepository) GetStuckWithdrawals(ctx context.Context, slaThreshold time.Duration) ([]*entities.Withdrawal, error) {
	cutoff := time.Now().Add(-slaThreshold)
	query := `
		SELECT id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, bridge_transfer_id, tx_hash, error_message,
			idempotency_key, created_at, updated_at, completed_at
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

// MarkTimeout marks a withdrawal as timed out
func (r *WithdrawalRepository) MarkTimeout(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE withdrawals SET status = $1, updated_at = $2 WHERE id = $3`

	_, err := r.db.ExecContext(ctx, query, entities.WithdrawalStatusTimeout, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to mark withdrawal timeout: %w", err)
	}

	return nil
}

// GetByBridgeTransferID retrieves a withdrawal by Bridge transfer ID
func (r *WithdrawalRepository) GetByBridgeTransferID(ctx context.Context, transferID string) (*entities.Withdrawal, error) {
	query := `
		SELECT id, user_id, withdrawal_type, currency, amount, source_account,
			circle_wallet_id, destination_type, destination_chain, destination_address, bank_account_id,
			fee_amount, fee_currency, status, bridge_transfer_id, tx_hash, error_message,
			idempotency_key, created_at, updated_at, completed_at
		FROM withdrawals
		WHERE bridge_transfer_id = $1
	`

	var withdrawal entities.Withdrawal
	err := r.db.GetContext(ctx, &withdrawal, query, transferID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get withdrawal by bridge transfer ID: %w", err)
	}

	return &withdrawal, nil
}
