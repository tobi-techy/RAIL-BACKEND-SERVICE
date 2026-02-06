package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
)

// WithdrawalSecurityStore implements middleware.WithdrawalSecurityStore
type WithdrawalSecurityStore struct {
	db *sqlx.DB
}

// NewWithdrawalSecurityStore creates a new withdrawal security store
func NewWithdrawalSecurityStore(db *sqlx.DB) *WithdrawalSecurityStore {
	return &WithdrawalSecurityStore{db: db}
}

// GetTodayWithdrawalCount returns the number of withdrawals made today by a user
func (s *WithdrawalSecurityStore) GetTodayWithdrawalCount(ctx context.Context, userID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*) FROM withdrawals 
		WHERE user_id = $1 AND created_at >= CURRENT_DATE`
	var count int
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return count, err
}

// GetTodayWithdrawalTotal returns the total amount withdrawn today by a user
func (s *WithdrawalSecurityStore) GetTodayWithdrawalTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0) FROM withdrawals 
		WHERE user_id = $1 AND created_at >= CURRENT_DATE AND status NOT IN ('failed', 'reversed')`
	var total decimal.Decimal
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&total)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	return total, err
}

// GetUserCreatedAt returns when the user account was created
func (s *WithdrawalSecurityStore) GetUserCreatedAt(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	query := `SELECT created_at FROM users WHERE id = $1`
	var createdAt time.Time
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&createdAt)
	return createdAt, err
}

// DepositSecurityStore implements deposit limit checks
type DepositSecurityStore struct {
	db *sqlx.DB
}

// NewDepositSecurityStore creates a new deposit security store
func NewDepositSecurityStore(db *sqlx.DB) *DepositSecurityStore {
	return &DepositSecurityStore{db: db}
}

// GetTodayDepositTotal returns the total amount deposited today by a user
func (s *DepositSecurityStore) GetTodayDepositTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0) FROM deposits 
		WHERE user_id = $1 AND created_at >= CURRENT_DATE AND status NOT IN ('failed', 'expired')`
	var total decimal.Decimal
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&total)
	if err == sql.ErrNoRows {
		return decimal.Zero, nil
	}
	return total, err
}
