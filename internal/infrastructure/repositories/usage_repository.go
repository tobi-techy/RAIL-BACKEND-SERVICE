package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
)

// UsageRepository implements the usage repository interface using PostgreSQL
type UsageRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewUsageRepository creates a new usage repository
func NewUsageRepository(db *sql.DB, logger *zap.Logger) *UsageRepository {
	return &UsageRepository{
		db:     db,
		logger: logger,
	}
}

// GetOrCreate retrieves or creates a usage record for a user
func (r *UsageRepository) GetOrCreate(ctx context.Context, userID uuid.UUID) (*entities.UserTransactionUsage, error) {
	usage, err := r.getByUserID(ctx, userID)
	if err == nil {
		return usage, nil
	}

	// Create new usage record
	now := time.Now().UTC()
	usage = &entities.UserTransactionUsage{
		ID:                       uuid.New(),
		UserID:                   userID,
		DailyDepositUsed:         decimal.Zero,
		DailyDepositResetAt:      limits.NextDailyReset(),
		MonthlyDepositUsed:       decimal.Zero,
		MonthlyDepositResetAt:    limits.NextMonthlyReset(),
		DailyWithdrawalUsed:      decimal.Zero,
		DailyWithdrawalResetAt:   limits.NextDailyReset(),
		MonthlyWithdrawalUsed:    decimal.Zero,
		MonthlyWithdrawalResetAt: limits.NextMonthlyReset(),
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	query := `
		INSERT INTO user_transaction_usage (
			id, user_id, 
			daily_deposit_used, daily_deposit_reset_at,
			monthly_deposit_used, monthly_deposit_reset_at,
			daily_withdrawal_used, daily_withdrawal_reset_at,
			monthly_withdrawal_used, monthly_withdrawal_reset_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id) DO NOTHING`

	_, err = r.db.ExecContext(ctx, query,
		usage.ID, usage.UserID,
		usage.DailyDepositUsed, usage.DailyDepositResetAt,
		usage.MonthlyDepositUsed, usage.MonthlyDepositResetAt,
		usage.DailyWithdrawalUsed, usage.DailyWithdrawalResetAt,
		usage.MonthlyWithdrawalUsed, usage.MonthlyWithdrawalResetAt,
		usage.CreatedAt, usage.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create usage record: %w", err)
	}

	// Re-fetch to handle race condition
	return r.getByUserID(ctx, userID)
}

func (r *UsageRepository) getByUserID(ctx context.Context, userID uuid.UUID) (*entities.UserTransactionUsage, error) {
	query := `
		SELECT id, user_id,
			daily_deposit_used, daily_deposit_reset_at,
			monthly_deposit_used, monthly_deposit_reset_at,
			daily_withdrawal_used, daily_withdrawal_reset_at,
			monthly_withdrawal_used, monthly_withdrawal_reset_at,
			created_at, updated_at
		FROM user_transaction_usage
		WHERE user_id = $1`

	usage := &entities.UserTransactionUsage{}
	err := r.db.QueryRowContext(ctx, query, userID).Scan(
		&usage.ID, &usage.UserID,
		&usage.DailyDepositUsed, &usage.DailyDepositResetAt,
		&usage.MonthlyDepositUsed, &usage.MonthlyDepositResetAt,
		&usage.DailyWithdrawalUsed, &usage.DailyWithdrawalResetAt,
		&usage.MonthlyWithdrawalUsed, &usage.MonthlyWithdrawalResetAt,
		&usage.CreatedAt, &usage.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("usage record not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}
	return usage, nil
}

// IncrementDepositUsage increments the deposit usage for a user
func (r *UsageRepository) IncrementDepositUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	query := `
		UPDATE user_transaction_usage
		SET daily_deposit_used = daily_deposit_used + $2,
			monthly_deposit_used = monthly_deposit_used + $2,
			updated_at = $3
		WHERE user_id = $1`

	result, err := r.db.ExecContext(ctx, query, userID, amount, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to increment deposit usage: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Create record first, then increment
		if _, err := r.GetOrCreate(ctx, userID); err != nil {
			return err
		}
		_, err = r.db.ExecContext(ctx, query, userID, amount, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("failed to increment deposit usage after create: %w", err)
		}
	}

	r.logger.Debug("Deposit usage incremented", zap.String("user_id", userID.String()), zap.String("amount", amount.String()))
	return nil
}

// IncrementWithdrawalUsage increments the withdrawal usage for a user
func (r *UsageRepository) IncrementWithdrawalUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	query := `
		UPDATE user_transaction_usage
		SET daily_withdrawal_used = daily_withdrawal_used + $2,
			monthly_withdrawal_used = monthly_withdrawal_used + $2,
			updated_at = $3
		WHERE user_id = $1`

	result, err := r.db.ExecContext(ctx, query, userID, amount, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to increment withdrawal usage: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		if _, err := r.GetOrCreate(ctx, userID); err != nil {
			return err
		}
		_, err = r.db.ExecContext(ctx, query, userID, amount, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("failed to increment withdrawal usage after create: %w", err)
		}
	}

	r.logger.Debug("Withdrawal usage incremented", zap.String("user_id", userID.String()), zap.String("amount", amount.String()))
	return nil
}

// ResetExpiredPeriods resets usage counters if their periods have expired
func (r *UsageRepository) ResetExpiredPeriods(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()

	query := `
		UPDATE user_transaction_usage
		SET 
			daily_deposit_used = CASE WHEN daily_deposit_reset_at <= $2 THEN 0 ELSE daily_deposit_used END,
			daily_deposit_reset_at = CASE WHEN daily_deposit_reset_at <= $2 THEN $3 ELSE daily_deposit_reset_at END,
			monthly_deposit_used = CASE WHEN monthly_deposit_reset_at <= $2 THEN 0 ELSE monthly_deposit_used END,
			monthly_deposit_reset_at = CASE WHEN monthly_deposit_reset_at <= $2 THEN $4 ELSE monthly_deposit_reset_at END,
			daily_withdrawal_used = CASE WHEN daily_withdrawal_reset_at <= $2 THEN 0 ELSE daily_withdrawal_used END,
			daily_withdrawal_reset_at = CASE WHEN daily_withdrawal_reset_at <= $2 THEN $3 ELSE daily_withdrawal_reset_at END,
			monthly_withdrawal_used = CASE WHEN monthly_withdrawal_reset_at <= $2 THEN 0 ELSE monthly_withdrawal_used END,
			monthly_withdrawal_reset_at = CASE WHEN monthly_withdrawal_reset_at <= $2 THEN $4 ELSE monthly_withdrawal_reset_at END,
			updated_at = $2
		WHERE user_id = $1`

	_, err := r.db.ExecContext(ctx, query, userID, now, limits.NextDailyReset(), limits.NextMonthlyReset())
	if err != nil {
		return fmt.Errorf("failed to reset expired periods: %w", err)
	}
	return nil
}
