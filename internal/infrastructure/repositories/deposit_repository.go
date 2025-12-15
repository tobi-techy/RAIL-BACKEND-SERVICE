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
			id, user_id, virtual_account_id, amount, status,
			tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
			alpaca_funding_tx_id, alpaca_funded_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`

	_, err := r.db.ExecContext(ctx, query,
		deposit.ID,
		deposit.UserID,
		deposit.VirtualAccountID,
		deposit.Amount,
		deposit.Status,
		deposit.TxHash,
		deposit.Chain,
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
		SELECT id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
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
		SELECT id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
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
		SET virtual_account_id = $2,
			amount = $3,
			status = $4,
			tx_hash = $5,
			chain = $6,
			off_ramp_tx_id = $7,
			off_ramp_initiated_at = $8,
			off_ramp_completed_at = $9,
			alpaca_funding_tx_id = $10,
			alpaca_funded_at = $11
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query,
		deposit.ID,
		deposit.VirtualAccountID,
		deposit.Amount,
		deposit.Status,
		deposit.TxHash,
		deposit.Chain,
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
		SELECT id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
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
		SELECT id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
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
		SELECT id, user_id, virtual_account_id, amount, status,
			   tx_hash, chain, off_ramp_tx_id, off_ramp_initiated_at, off_ramp_completed_at,
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
