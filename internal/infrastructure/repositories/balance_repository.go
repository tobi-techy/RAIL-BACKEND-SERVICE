package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BalanceRepository handles balance persistence operations
type BalanceRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewBalanceRepository creates a new balance repository
func NewBalanceRepository(db *sql.DB, logger *zap.Logger) *BalanceRepository {
	return &BalanceRepository{
		db:     db,
		logger: logger,
	}
}

// Get retrieves the balance for a specific user
func (r *BalanceRepository) Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error) {
	query := `
		SELECT user_id, buying_power, pending_deposits, currency, updated_at
		FROM balances
		WHERE user_id = $1
	`

	balance := &entities.Balance{}
	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&balance.UserID,
		&balance.BuyingPower,
		&balance.PendingDeposits,
		&balance.Currency,
		&balance.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("balance not found")
		}
		r.logger.Error("failed to get balance",
			zap.Error(err),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	return balance, nil
}

// UpdateBuyingPower updates the buying power for a user (atomic operation)
func (r *BalanceRepository) UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Use ON CONFLICT to handle upsert (insert if not exists, update if exists)
	query := `
		INSERT INTO balances (user_id, buying_power, pending_deposits, currency, updated_at)
		VALUES ($1, $2, 0, 'USD', $3)
		ON CONFLICT (user_id)
		DO UPDATE SET 
			buying_power = balances.buying_power + EXCLUDED.buying_power,
			updated_at = EXCLUDED.updated_at
	`

	result, err := r.db.ExecContext(ctx, query, userID, amount, time.Now())
	if err != nil {
		r.logger.Error("failed to update buying power",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.String()),
		)
		return fmt.Errorf("failed to update buying power: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	r.logger.Info("buying power updated",
		zap.String("user_id", userID.String()),
		zap.String("amount_added", amount.String()),
		zap.Int64("rows_affected", rowsAffected),
	)

	return nil
}

// UpdatePendingDeposits updates the pending deposits for a user
func (r *BalanceRepository) UpdatePendingDeposits(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	// Use ON CONFLICT to handle upsert (insert if not exists, update if exists)
	query := `
		INSERT INTO balances (user_id, buying_power, pending_deposits, currency, updated_at)
		VALUES ($1, 0, $2, 'USD', $3)
		ON CONFLICT (user_id)
		DO UPDATE SET 
			pending_deposits = balances.pending_deposits + EXCLUDED.pending_deposits,
			updated_at = EXCLUDED.updated_at
	`

	result, err := r.db.ExecContext(ctx, query, userID, amount, time.Now())
	if err != nil {
		r.logger.Error("failed to update pending deposits",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.String()),
		)
		return fmt.Errorf("failed to update pending deposits: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	r.logger.Info("pending deposits updated",
		zap.String("user_id", userID.String()),
		zap.String("amount_added", amount.String()),
		zap.Int64("rows_affected", rowsAffected),
	)

	return nil
}

// GetOrCreate gets a user's balance, creating a zero balance if it doesn't exist
func (r *BalanceRepository) GetOrCreate(ctx context.Context, userID uuid.UUID) (*entities.Balance, error) {
	balance, err := r.Get(ctx, userID)
	if err != nil {
		if err.Error() == "balance not found" {
			// Create a new balance record with zero values
			balance = &entities.Balance{
				UserID:          userID,
				BuyingPower:     decimal.Zero,
				PendingDeposits: decimal.Zero,
				Currency:        "USD",
				UpdatedAt:       time.Now(),
			}

			err = r.Create(ctx, balance)
			if err != nil {
				return nil, fmt.Errorf("failed to create balance: %w", err)
			}
			return balance, nil
		}
		return nil, err
	}
	return balance, nil
}

// Create creates a new balance record
func (r *BalanceRepository) Create(ctx context.Context, balance *entities.Balance) error {
	query := `
		INSERT INTO balances (user_id, buying_power, pending_deposits, currency, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO NOTHING
	`

	_, err := r.db.ExecContext(ctx, query,
		balance.UserID,
		balance.BuyingPower,
		balance.PendingDeposits,
		balance.Currency,
		balance.UpdatedAt,
	)

	if err != nil {
		r.logger.Error("failed to create balance",
			zap.Error(err),
			zap.String("user_id", balance.UserID.String()),
		)
		return fmt.Errorf("failed to create balance: %w", err)
	}

	r.logger.Info("balance created",
		zap.String("user_id", balance.UserID.String()),
		zap.String("buying_power", balance.BuyingPower.String()),
	)

	return nil
}

// DeductBuyingPower deducts amount from user's buying power
func (r *BalanceRepository) DeductBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	query := `
		UPDATE balances 
		SET 
			buying_power = buying_power - $2,
			updated_at = $3
		WHERE user_id = $1 AND buying_power >= $2
	`

	result, err := r.db.ExecContext(ctx, query, userID, amount, time.Now())
	if err != nil {
		r.logger.Error("failed to deduct buying power",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.String()),
		)
		return fmt.Errorf("failed to deduct buying power: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("insufficient buying power or user not found")
	}

	r.logger.Info("buying power deducted",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
	)

	return nil
}

// AddBuyingPower adds amount to user's buying power
func (r *BalanceRepository) AddBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return r.UpdateBuyingPower(ctx, userID, amount)
}

// TransferFromPendingToBuyingPower atomically moves amount from pending to buying power
func (r *BalanceRepository) TransferFromPendingToBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Update balances atomically
	query := `
		UPDATE balances 
		SET 
			buying_power = buying_power + $2,
			pending_deposits = pending_deposits - $2,
			updated_at = $3
		WHERE user_id = $1 AND pending_deposits >= $2
	`

	result, err := tx.ExecContext(ctx, query, userID, amount, time.Now())
	if err != nil {
		r.logger.Error("failed to transfer from pending to buying power",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("amount", amount.String()),
		)
		return fmt.Errorf("failed to transfer balance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("insufficient pending deposits or user not found")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	r.logger.Info("transferred from pending to buying power",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
	)

	return nil
}
