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

// DepositRepository implements the deposit repository interface
type DepositRepository struct {
	db *sqlx.DB
}

// NewDepositRepository creates a new deposit repository
func NewDepositRepository(db *sqlx.DB) *DepositRepository {
	return &DepositRepository{db: db}
}

// Create creates a new deposit
func (r *DepositRepository) Create(ctx context.Context, deposit *entities.Deposit) error {
	query := `
		INSERT INTO deposits (
			id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			tx_hash, chain, token, confirmed_at,
			off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			alpaca_funding_tx_id, alpaca_funded_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
	`

	_, err := r.db.ExecContext(ctx, query,
		deposit.ID,
		deposit.IdempotencyKey,
		deposit.CorrelationID,
		deposit.UserID,
		deposit.VirtualAccountID,
		deposit.Amount,
		deposit.Status,
		deposit.TxHash,
		deposit.Chain,
		deposit.Token,
		deposit.ConfirmedAt,
		deposit.OffRampTxID,
		deposit.OffRampInitiatedAt,
		deposit.OffRampCompletedAt,
		deposit.AlpacaFundingTxID,
		deposit.AlpacaFundedAt,
		deposit.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create deposit: %w", err)
	}

	return nil
}

// GetByID retrieves a deposit by ID
func (r *DepositRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE id = $1
	`

	var deposit entities.Deposit
	err := r.db.GetContext(ctx, &deposit, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("deposit not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get deposit: %w", err)
	}

	return &deposit, nil
}

// GetByOffRampTxID retrieves a deposit by off-ramp transaction ID
func (r *DepositRepository) GetByOffRampTxID(ctx context.Context, txID string) (*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE off_ramp_tx_id = $1
	`

	var deposit entities.Deposit
	err := r.db.GetContext(ctx, &deposit, query, txID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("deposit not found: %w", err)
		}
		return nil, fmt.Errorf("failed to get deposit: %w", err)
	}

	return &deposit, nil
}

// Update updates a deposit
func (r *DepositRepository) Update(ctx context.Context, deposit *entities.Deposit) error {
	query := `
		UPDATE deposits
		SET idempotency_key = $2,
			virtual_account_id = $3,
			amount = $4,
			status = $5,
			tx_hash = $6,
			chain = $7,
			token = $8,
			confirmed_at = $9,
			off_ramp_tx_id = $10,
			off_ramp_initiated_at = $11,
			off_ramp_completed_at = $12,
			alpaca_funding_tx_id = $13,
			alpaca_funded_at = $14,
			updated_at = NOW()
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query,
		deposit.ID,
		deposit.IdempotencyKey,
		deposit.VirtualAccountID,
		deposit.Amount,
		deposit.Status,
		deposit.TxHash,
		deposit.Chain,
		deposit.Token,
		deposit.ConfirmedAt,
		deposit.OffRampTxID,
		deposit.OffRampInitiatedAt,
		deposit.OffRampCompletedAt,
		deposit.AlpacaFundingTxID,
		deposit.AlpacaFundedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update deposit: %w", err)
	}

	return nil
}

// ListByUserID retrieves all deposits for a user
func (r *DepositRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	var deposits []*entities.Deposit
	err := r.db.SelectContext(ctx, &deposits, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list deposits: %w", err)
	}

	return deposits, nil
}

// GetByUserID retrieves deposits for a user with pagination
func (r *DepositRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	var deposits []*entities.Deposit
	err := r.db.SelectContext(ctx, &deposits, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get deposits: %w", err)
	}

	return deposits, nil
}

// GetByTxHash retrieves a deposit by transaction hash
func (r *DepositRepository) GetByTxHash(ctx context.Context, txHash string) (*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE tx_hash = $1
	`

	var deposit entities.Deposit
	err := r.db.GetContext(ctx, &deposit, query, txHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("deposit not found")
		}
		return nil, fmt.Errorf("failed to get deposit: %w", err)
	}

	return &deposit, nil
}

// GetByIdempotencyKey retrieves a deposit by idempotency key
// This is the primary method for checking deposit idempotency
func (r *DepositRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*entities.Deposit, error) {
	query := `
		SELECT id, idempotency_key, correlation_id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, token, confirmed_at,
			   off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			   alpaca_funding_tx_id, alpaca_funded_at, created_at
		FROM deposits
		WHERE idempotency_key = $1
	`

	var deposit entities.Deposit
	err := r.db.GetContext(ctx, &deposit, query, idempotencyKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("deposit not found")
		}
		return nil, fmt.Errorf("failed to get deposit by idempotency key: %w", err)
	}

	return &deposit, nil
}

// UpdateStatus updates the status of a deposit
func (r *DepositRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error {
	query := `
		UPDATE deposits
		SET status = $2
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("failed to update deposit status: %w", err)
	}

	return nil
}

// GetTotalCompletedDeposits returns the sum of all completed deposits
func (r *DepositRepository) GetTotalCompletedDeposits(ctx context.Context) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0)
		FROM deposits
		WHERE status = 'broker_funded'
	`

	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, query)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get total completed deposits: %w", err)
	}

	return total, nil
}

// CountConfirmedByUserIDSince counts confirmed deposits for a user since a given time (for frequency signal)
func (r *DepositRepository) CountConfirmedByUserIDSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM deposits WHERE user_id = $1 AND status = 'broker_funded' AND created_at >= $2`,
		userID, since)
	return count, err
}

// CountPendingByUserID counts pending deposits for a user (for Station status)
func (r *DepositRepository) CountPendingByUserID(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM deposits
		WHERE user_id = $1
		AND status IN ('pending', 'processing', 'confirming', 'off_ramp_pending')
	`

	var count int
	err := r.db.GetContext(ctx, &count, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to count pending deposits: %w", err)
	}

	return count, nil
}

// DeletePendingDeposit removes a pending deposit by ID (only deposits with "pending" status can be deleted)
// This maintains audit trail compliance - only pending deposits can be removed
func (r *DepositRepository) DeletePendingDeposit(ctx context.Context, id uuid.UUID) error {
	// First get the deposit to log full details for audit and validate status
	var deposit entities.Deposit
	err := r.db.GetContext(ctx, &deposit, `
		SELECT id, user_id, amount, tx_hash, status, created_at 
		FROM deposits WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deposit not found: %w", err)
	}

	// Only allow deletion of pending deposits
	if deposit.Status != "pending" {
		return fmt.Errorf("cannot delete deposit with status '%s' - only pending deposits can be deleted", deposit.Status)
	}

	query := `DELETE FROM deposits WHERE id = $1 AND status = 'pending'`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete pending deposit: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("deposit not found or not in pending status")
	}

	return nil
}
