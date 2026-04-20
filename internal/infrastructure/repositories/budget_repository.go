package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
)

// SpendingBudget represents a user's monthly spending budget.
type SpendingBudget struct {
	ID           uuid.UUID       `db:"id" json:"id"`
	UserID       uuid.UUID       `db:"user_id" json:"user_id"`
	MonthlyLimit decimal.Decimal `db:"monthly_limit" json:"monthly_limit"`
	Currency     string          `db:"currency" json:"currency"`
	CreatedAt    time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time       `db:"updated_at" json:"updated_at"`
}

type BudgetRepository struct {
	db *sqlx.DB
}

func NewBudgetRepository(db *sqlx.DB) *BudgetRepository {
	return &BudgetRepository{db: db}
}

func (r *BudgetRepository) Upsert(ctx context.Context, userID uuid.UUID, limit decimal.Decimal, currency string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO spending_budgets (id, user_id, monthly_limit, currency, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET monthly_limit = $3, currency = $4, updated_at = NOW()`,
		uuid.New(), userID, limit, currency)
	if err != nil {
		return fmt.Errorf("upsert budget: %w", err)
	}
	return nil
}

func (r *BudgetRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*SpendingBudget, error) {
	var b SpendingBudget
	err := r.db.GetContext(ctx, &b, `SELECT * FROM spending_budgets WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get budget: %w", err)
	}
	return &b, nil
}
