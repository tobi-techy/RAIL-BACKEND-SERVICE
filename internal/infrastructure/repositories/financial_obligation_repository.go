package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// FinancialObligationRepository persists manual obligations used by Miriam's operating plans.
type FinancialObligationRepository struct {
	db *sqlx.DB
}

func NewFinancialObligationRepository(db *sqlx.DB) *FinancialObligationRepository {
	return &FinancialObligationRepository{db: db}
}

func (r *FinancialObligationRepository) Create(ctx context.Context, obligation *entities.FinancialObligation) error {
	query := `
		INSERT INTO financial_obligations (
			id, user_id, type, name, amount, currency, cadence, due_date, due_day,
			priority, counterparty, status, metadata, created_at, updated_at
		)
		VALUES (
			:id, :user_id, :type, :name, :amount, :currency, :cadence, :due_date, :due_day,
			:priority, :counterparty, :status, :metadata, NOW(), NOW()
		)
		RETURNING created_at, updated_at`
	stmt, err := r.db.PrepareNamedContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare create financial obligation: %w", err)
	}
	defer stmt.Close()

	if err := stmt.GetContext(ctx, obligation, obligation); err != nil {
		return fmt.Errorf("create financial obligation: %w", err)
	}
	return nil
}

func (r *FinancialObligationRepository) GetByID(ctx context.Context, userID, id uuid.UUID) (*entities.FinancialObligation, error) {
	var obligation entities.FinancialObligation
	err := r.db.GetContext(ctx, &obligation, `SELECT * FROM financial_obligations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("get financial obligation: %w", err)
	}
	return &obligation, nil
}

func (r *FinancialObligationRepository) ListByUser(ctx context.Context, userID uuid.UUID, status, obligationType string) ([]entities.FinancialObligation, error) {
	args := []interface{}{userID}
	conditions := []string{"user_id = $1"}
	if status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if obligationType != "" {
		args = append(args, obligationType)
		conditions = append(conditions, fmt.Sprintf("type = $%d", len(args)))
	}

	query := `SELECT * FROM financial_obligations WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY CASE priority
			WHEN 'critical' THEN 1
			WHEN 'high' THEN 2
			WHEN 'medium' THEN 3
			ELSE 4
		END, COALESCE(due_date, created_at), created_at DESC`
	var list []entities.FinancialObligation
	if err := r.db.SelectContext(ctx, &list, query, args...); err != nil {
		return nil, fmt.Errorf("list financial obligations: %w", err)
	}
	return list, nil
}

func (r *FinancialObligationRepository) Update(ctx context.Context, obligation *entities.FinancialObligation) error {
	query := `
		UPDATE financial_obligations
		SET type = :type,
		    name = :name,
		    amount = :amount,
		    currency = :currency,
		    cadence = :cadence,
		    due_date = :due_date,
		    due_day = :due_day,
		    priority = :priority,
		    counterparty = :counterparty,
		    status = :status,
		    metadata = :metadata,
		    updated_at = NOW()
		WHERE id = :id AND user_id = :user_id
		RETURNING updated_at`
	stmt, err := r.db.PrepareNamedContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare update financial obligation: %w", err)
	}
	defer stmt.Close()

	if err := stmt.GetContext(ctx, obligation, obligation); err != nil {
		return fmt.Errorf("update financial obligation: %w", err)
	}
	return nil
}

func (r *FinancialObligationRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM financial_obligations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete financial obligation: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
