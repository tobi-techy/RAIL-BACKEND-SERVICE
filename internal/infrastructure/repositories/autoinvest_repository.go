package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// AutoInvestRepository handles auto-invest database operations
type AutoInvestRepository struct {
	db *sqlx.DB
}

// NewAutoInvestRepository creates a new auto-invest repository
func NewAutoInvestRepository(db *sqlx.DB) *AutoInvestRepository {
	return &AutoInvestRepository{db: db}
}

// GetUserSettings retrieves auto-invest settings for a user
func (r *AutoInvestRepository) GetUserSettings(ctx context.Context, userID uuid.UUID) (*entities.AutoInvestSettings, error) {
	var settings entities.AutoInvestSettings
	query := `
		SELECT user_id, enabled, basket_id, threshold, created_at, updated_at
		FROM auto_invest_settings
		WHERE user_id = $1
	`

	err := r.db.GetContext(ctx, &settings, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get auto-invest settings: %w", err)
	}

	return &settings, nil
}

// SaveUserSettings creates or updates auto-invest settings
func (r *AutoInvestRepository) SaveUserSettings(ctx context.Context, settings *entities.AutoInvestSettings) error {
	query := `
		INSERT INTO auto_invest_settings (user_id, enabled, basket_id, threshold, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			basket_id = EXCLUDED.basket_id,
			threshold = EXCLUDED.threshold,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.db.ExecContext(ctx, query,
		settings.UserID,
		settings.Enabled,
		settings.BasketID,
		settings.Threshold,
		settings.CreatedAt,
		settings.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save auto-invest settings: %w", err)
	}

	return nil
}

// CreateEvent records an auto-invest event
func (r *AutoInvestRepository) CreateEvent(ctx context.Context, event *entities.AutoInvestEvent) error {
	query := `
		INSERT INTO auto_invest_events (id, user_id, basket_id, amount, order_id, status, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.ExecContext(ctx, query,
		event.ID,
		event.UserID,
		event.BasketID,
		event.Amount,
		event.OrderID,
		event.Status,
		event.Error,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create auto-invest event: %w", err)
	}

	return nil
}

// GetPendingUsers returns users with stash balance above threshold and auto-invest enabled
func (r *AutoInvestRepository) GetPendingUsers(ctx context.Context, threshold decimal.Decimal) ([]uuid.UUID, error) {
	query := `
		SELECT DISTINCT ais.user_id
		FROM auto_invest_settings ais
		INNER JOIN ledger_accounts la ON la.user_id = ais.user_id AND la.account_type = 'stash_balance'
		WHERE ais.enabled = true
		AND la.balance >= $1
		AND NOT EXISTS (
			SELECT 1 FROM auto_invest_events aie
			WHERE aie.user_id = ais.user_id
			AND aie.status = 'pending'
		)
	`

	var userIDs []uuid.UUID
	err := r.db.SelectContext(ctx, &userIDs, query, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending users: %w", err)
	}

	return userIDs, nil
}
