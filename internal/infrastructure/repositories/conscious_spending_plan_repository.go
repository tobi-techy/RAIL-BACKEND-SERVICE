package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type ConsciousSpendingPlanRepository struct {
	db *sqlx.DB
}

func NewConsciousSpendingPlanRepository(db *sqlx.DB) *ConsciousSpendingPlanRepository {
	return &ConsciousSpendingPlanRepository{db: db}
}

func (r *ConsciousSpendingPlanRepository) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	var plan entities.ConsciousSpendingPlan
	err := r.db.GetContext(ctx, &plan, `SELECT * FROM conscious_spending_plans WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conscious spending plan: %w", err)
	}
	return &plan, nil
}

func (r *ConsciousSpendingPlanRepository) Upsert(ctx context.Context, plan *entities.ConsciousSpendingPlan) error {
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO conscious_spending_plans (
			user_id, take_home_income, currency, fixed_costs, investments, savings,
			guilt_free_spending, fixed_costs_pct, investments_pct, savings_pct,
			guilt_free_spending_pct, status, check_in_cadence, committed_at
		) VALUES (
			:user_id, :take_home_income, :currency, :fixed_costs, :investments, :savings,
			:guilt_free_spending, :fixed_costs_pct, :investments_pct, :savings_pct,
			:guilt_free_spending_pct, :status, :check_in_cadence, :committed_at
		)
		ON CONFLICT (user_id) DO UPDATE SET
			take_home_income = EXCLUDED.take_home_income,
			currency = EXCLUDED.currency,
			fixed_costs = EXCLUDED.fixed_costs,
			investments = EXCLUDED.investments,
			savings = EXCLUDED.savings,
			guilt_free_spending = EXCLUDED.guilt_free_spending,
			fixed_costs_pct = EXCLUDED.fixed_costs_pct,
			investments_pct = EXCLUDED.investments_pct,
			savings_pct = EXCLUDED.savings_pct,
			guilt_free_spending_pct = EXCLUDED.guilt_free_spending_pct,
			status = EXCLUDED.status,
			check_in_cadence = EXCLUDED.check_in_cadence,
			committed_at = EXCLUDED.committed_at,
			updated_at = NOW()`, plan)
	if err != nil {
		return fmt.Errorf("upsert conscious spending plan: %w", err)
	}
	return nil
}

func (r *ConsciousSpendingPlanRepository) ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	type checkInRow struct {
		entities.ConsciousSpendingPlan
		Country string `db:"country"`
	}
	var rows []checkInRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT csp.*, COALESCE(u.country, '') AS country
		FROM conscious_spending_plans csp
		JOIN users u ON u.id = csp.user_id
		WHERE csp.status = 'committed'
		ORDER BY csp.updated_at`); err != nil {
		return nil, fmt.Errorf("list committed conscious spending plan check-ins: %w", err)
	}
	checkIns := make([]entities.ConsciousSpendingPlanCheckIn, 0, len(rows))
	for i := range rows {
		checkIns = append(checkIns, entities.ConsciousSpendingPlanCheckIn{
			Plan: rows[i].ConsciousSpendingPlan, Country: rows[i].Country,
		})
	}
	return checkIns, nil
}
