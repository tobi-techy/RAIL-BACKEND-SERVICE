package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

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

func (r *BudgetRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.SpendingBudget, error) {
	var b entities.SpendingBudget
	err := r.db.GetContext(ctx, &b, `SELECT * FROM spending_budgets WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get budget: %w", err)
	}
	return &b, nil
}
