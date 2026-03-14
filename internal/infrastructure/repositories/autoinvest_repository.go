package repositories

import (
	"context"
	"database/sql"
	"errors"
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
		INSERT INTO auto_invest_events (id, user_id, basket_id, amount, order_id, correlation_id, status, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.ExecContext(ctx, query,
		event.ID,
		event.UserID,
		event.BasketID,
		event.Amount,
		event.OrderID,
		event.CorrelationID,
		event.Status,
		event.Error,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create auto-invest event: %w", err)
	}

	return nil
}

// GetEventByCorrelation returns the event for a given correlation ID, or nil if not found.
func (r *AutoInvestRepository) GetEventByCorrelation(ctx context.Context, correlationID string) (*entities.AutoInvestEvent, error) {
	var event entities.AutoInvestEvent
	err := r.db.GetContext(ctx, &event, `SELECT id, user_id, basket_id, amount, order_id, correlation_id, status, error, created_at FROM auto_invest_events WHERE correlation_id = $1 LIMIT 1`, correlationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get auto-invest event: %w", err)
	}
	return &event, nil
}

// HasProcessedCorrelation checks if a correlation ID has already completed successfully.
// Pending events are NOT considered processed — they may be deferred (e.g. market closed)
// and must be retried.
func (r *AutoInvestRepository) HasProcessedCorrelation(ctx context.Context, correlationID string) (bool, error) {
	var exists bool
	err := r.db.GetContext(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM auto_invest_events WHERE correlation_id = $1 AND status = 'completed')`, correlationID)
	if err != nil {
		return false, fmt.Errorf("failed to check correlation ID: %w", err)
	}
	return exists, nil
}

// UpdateEventAmount updates the amount of a pending auto-invest event owned by userID.
func (r *AutoInvestRepository) UpdateEventAmount(ctx context.Context, userID, eventID uuid.UUID, amount decimal.Decimal) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE auto_invest_events SET amount = $1 WHERE id = $2 AND user_id = $3 AND status = 'pending'`,
		amount, eventID, userID)
	if err != nil {
		return fmt.Errorf("failed to update auto-invest event amount: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("auto-invest event not found, not pending, or wrong user")
	}
	return nil
}

// UpdateEventStatus updates the status of an auto-invest event owned by userID.
func (r *AutoInvestRepository) UpdateEventStatus(ctx context.Context, userID, eventID uuid.UUID, status entities.AutoInvestStatus, errMsg *string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE auto_invest_events SET status = $1, error = $2 WHERE id = $3 AND user_id = $4`,
		status, errMsg, eventID, userID)
	if err != nil {
		return fmt.Errorf("failed to update auto-invest event status: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("auto-invest event not found or wrong user")
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
