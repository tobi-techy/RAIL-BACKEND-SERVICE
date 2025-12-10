package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AllocationRepository handles persistence for smart allocation mode
type AllocationRepository struct {
	db     *sqlx.DB
	logger *logger.Logger
}

// NewAllocationRepository creates a new allocation repository
func NewAllocationRepository(db *sqlx.DB, logger *logger.Logger) *AllocationRepository {
	return &AllocationRepository{
		db:     db,
		logger: logger,
	}
}

// ============================================================================
// Smart Allocation Mode Operations
// ============================================================================

// GetMode retrieves the allocation mode for a user
func (r *AllocationRepository) GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error) {
	query := `
		SELECT user_id, active, ratio_spending, ratio_stash, paused_at, resumed_at, created_at, updated_at
		FROM smart_allocation_mode
		WHERE user_id = $1
	`

	var mode entities.SmartAllocationMode
	err := r.db.GetContext(ctx, &mode, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil // Not found, user hasn't enabled mode yet
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get allocation mode: %w", err)
	}

	return &mode, nil
}

// CreateMode creates a new allocation mode for a user
func (r *AllocationRepository) CreateMode(ctx context.Context, mode *entities.SmartAllocationMode) error {
	if err := mode.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	query := `
		INSERT INTO smart_allocation_mode (
			user_id, active, ratio_spending, ratio_stash, 
			paused_at, resumed_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.db.ExecContext(ctx, query,
		mode.UserID,
		mode.Active,
		mode.RatioSpending,
		mode.RatioStash,
		mode.PausedAt,
		mode.ResumedAt,
		mode.CreatedAt,
		mode.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create allocation mode: %w", err)
	}

	return nil
}

// UpdateMode updates an existing allocation mode
func (r *AllocationRepository) UpdateMode(ctx context.Context, mode *entities.SmartAllocationMode) error {
	if err := mode.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	query := `
		UPDATE smart_allocation_mode
		SET active = $2,
		    ratio_spending = $3,
		    ratio_stash = $4,
		    paused_at = $5,
		    resumed_at = $6,
		    updated_at = $7
		WHERE user_id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		mode.UserID,
		mode.Active,
		mode.RatioSpending,
		mode.RatioStash,
		mode.PausedAt,
		mode.ResumedAt,
		time.Now(),
	)
	if err != nil {
		return fmt.Errorf("failed to update allocation mode: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("allocation mode not found for user %s", mode.UserID)
	}

	return nil
}

// PauseMode marks the mode as paused for a user
func (r *AllocationRepository) PauseMode(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE smart_allocation_mode
		SET active = false,
		    paused_at = $2,
		    updated_at = $2
		WHERE user_id = $1
	`

	now := time.Now()
	result, err := r.db.ExecContext(ctx, query, userID, now)
	if err != nil {
		return fmt.Errorf("failed to pause allocation mode: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("allocation mode not found for user %s", userID)
	}

	return nil
}

// ResumeMode marks the mode as active for a user
func (r *AllocationRepository) ResumeMode(ctx context.Context, userID uuid.UUID) error {
	query := `
		UPDATE smart_allocation_mode
		SET active = true,
		    resumed_at = $2,
		    paused_at = NULL,
		    updated_at = $2
		WHERE user_id = $1
	`

	now := time.Now()
	result, err := r.db.ExecContext(ctx, query, userID, now)
	if err != nil {
		return fmt.Errorf("failed to resume allocation mode: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("allocation mode not found for user %s", userID)
	}

	return nil
}

// DeleteMode deletes the allocation mode for a user (for testing/cleanup)
func (r *AllocationRepository) DeleteMode(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM smart_allocation_mode WHERE user_id = $1`
	
	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete allocation mode: %w", err)
	}

	return nil
}

// ============================================================================
// Allocation Event Operations
// ============================================================================

// CreateEvent creates a new allocation event
func (r *AllocationRepository) CreateEvent(ctx context.Context, event *entities.AllocationEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Marshal metadata to JSON
	var metadataJSON []byte
	var err error
	if event.Metadata != nil {
		metadataJSON, err = json.Marshal(event.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	query := `
		INSERT INTO allocation_events (
			id, user_id, total_amount, stash_amount, spending_amount,
			event_type, source_tx_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err = r.db.ExecContext(ctx, query,
		event.ID,
		event.UserID,
		event.TotalAmount,
		event.StashAmount,
		event.SpendingAmount,
		event.EventType,
		event.SourceTxID,
		metadataJSON,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create allocation event: %w", err)
	}

	return nil
}

// GetEventsByUserID retrieves allocation events for a user
func (r *AllocationRepository) GetEventsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AllocationEvent, error) {
	query := `
		SELECT id, user_id, total_amount, stash_amount, spending_amount,
		       event_type, source_tx_id, metadata, created_at
		FROM allocation_events
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryxContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query allocation events: %w", err)
	}
	defer rows.Close()

	var events []*entities.AllocationEvent
	for rows.Next() {
		var event entities.AllocationEvent
		var metadataJSON []byte

		err := rows.Scan(
			&event.ID,
			&event.UserID,
			&event.TotalAmount,
			&event.StashAmount,
			&event.SpendingAmount,
			&event.EventType,
			&event.SourceTxID,
			&metadataJSON,
			&event.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan allocation event: %w", err)
		}

		// Unmarshal metadata
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &event.Metadata); err != nil {
				r.logger.Warn("Failed to unmarshal event metadata", "event_id", event.ID, "error", err)
			}
		}

		events = append(events, &event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating allocation events: %w", err)
	}

	return events, nil
}

// GetEventsByDateRange retrieves allocation events for a user within a date range
func (r *AllocationRepository) GetEventsByDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) ([]*entities.AllocationEvent, error) {
	query := `
		SELECT id, user_id, total_amount, stash_amount, spending_amount,
		       event_type, source_tx_id, metadata, created_at
		FROM allocation_events
		WHERE user_id = $1
		  AND created_at >= $2
		  AND created_at <= $3
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryxContext(ctx, query, userID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query allocation events by date: %w", err)
	}
	defer rows.Close()

	var events []*entities.AllocationEvent
	for rows.Next() {
		var event entities.AllocationEvent
		var metadataJSON []byte

		err := rows.Scan(
			&event.ID,
			&event.UserID,
			&event.TotalAmount,
			&event.StashAmount,
			&event.SpendingAmount,
			&event.EventType,
			&event.SourceTxID,
			&metadataJSON,
			&event.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan allocation event: %w", err)
		}

		// Unmarshal metadata
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &event.Metadata); err != nil {
				r.logger.Warn("Failed to unmarshal event metadata", "event_id", event.ID, "error", err)
			}
		}

		events = append(events, &event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating allocation events: %w", err)
	}

	return events, nil
}

// ============================================================================
// Weekly Summary Operations
// ============================================================================

// CreateWeeklySummary creates a new weekly allocation summary
func (r *AllocationRepository) CreateWeeklySummary(ctx context.Context, summary *entities.WeeklyAllocationSummary) error {
	if err := summary.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	query := `
		INSERT INTO weekly_allocation_summaries (
			id, user_id, week_start, week_end, total_income, stash_added,
			spending_added, spending_used, spending_remaining, declines_count,
			mode_active_days, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (user_id, week_start) 
		DO UPDATE SET
			week_end = EXCLUDED.week_end,
			total_income = EXCLUDED.total_income,
			stash_added = EXCLUDED.stash_added,
			spending_added = EXCLUDED.spending_added,
			spending_used = EXCLUDED.spending_used,
			spending_remaining = EXCLUDED.spending_remaining,
			declines_count = EXCLUDED.declines_count,
			mode_active_days = EXCLUDED.mode_active_days,
			created_at = EXCLUDED.created_at
	`

	_, err := r.db.ExecContext(ctx, query,
		summary.ID,
		summary.UserID,
		summary.WeekStart,
		summary.WeekEnd,
		summary.TotalIncome,
		summary.StashAdded,
		summary.SpendingAdded,
		summary.SpendingUsed,
		summary.SpendingRemaining,
		summary.DeclinesCount,
		summary.ModeActiveDays,
		summary.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create weekly summary: %w", err)
	}

	return nil
}

// GetWeeklySummaryByDate retrieves a weekly summary for a user by week start date
func (r *AllocationRepository) GetWeeklySummaryByDate(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*entities.WeeklyAllocationSummary, error) {
	query := `
		SELECT id, user_id, week_start, week_end, total_income, stash_added,
		       spending_added, spending_used, spending_remaining, declines_count,
		       mode_active_days, created_at
		FROM weekly_allocation_summaries
		WHERE user_id = $1 AND week_start = $2
	`

	var summary entities.WeeklyAllocationSummary
	err := r.db.GetContext(ctx, &summary, query, userID, weekStart)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get weekly summary: %w", err)
	}

	return &summary, nil
}

// GetWeeklySummariesByUser retrieves all weekly summaries for a user
func (r *AllocationRepository) GetWeeklySummariesByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.WeeklyAllocationSummary, error) {
	query := `
		SELECT id, user_id, week_start, week_end, total_income, stash_added,
		       spending_added, spending_used, spending_remaining, declines_count,
		       mode_active_days, created_at
		FROM weekly_allocation_summaries
		WHERE user_id = $1
		ORDER BY week_start DESC
		LIMIT $2 OFFSET $3
	`

	var summaries []*entities.WeeklyAllocationSummary
	err := r.db.SelectContext(ctx, &summaries, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get weekly summaries: %w", err)
	}

	return summaries, nil
}

// GetLatestWeeklySummary retrieves the most recent weekly summary for a user
func (r *AllocationRepository) GetLatestWeeklySummary(ctx context.Context, userID uuid.UUID) (*entities.WeeklyAllocationSummary, error) {
	query := `
		SELECT id, user_id, week_start, week_end, total_income, stash_added,
		       spending_added, spending_used, spending_remaining, declines_count,
		       mode_active_days, created_at
		FROM weekly_allocation_summaries
		WHERE user_id = $1
		ORDER BY week_start DESC
		LIMIT 1
	`

	var summary entities.WeeklyAllocationSummary
	err := r.db.GetContext(ctx, &summary, query, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest weekly summary: %w", err)
	}

	return &summary, nil
}

// ============================================================================
// Aggregate Query Operations
// ============================================================================

// GetTotalStashAdded calculates total stash added for a user in a date range
func (r *AllocationRepository) GetTotalStashAdded(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(stash_amount), 0)
		FROM allocation_events
		WHERE user_id = $1
		  AND created_at >= $2
		  AND created_at <= $3
	`

	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, query, userID, startDate, endDate)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get total stash added: %w", err)
	}

	return total, nil
}

// GetTotalSpendingAdded calculates total spending added for a user in a date range
func (r *AllocationRepository) GetTotalSpendingAdded(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (decimal.Decimal, error) {
	query := `
		SELECT COALESCE(SUM(spending_amount), 0)
		FROM allocation_events
		WHERE user_id = $1
		  AND created_at >= $2
		  AND created_at <= $3
	`

	var total decimal.Decimal
	err := r.db.GetContext(ctx, &total, query, userID, startDate, endDate)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get total spending added: %w", err)
	}

	return total, nil
}

// CountDeclinesInDateRange counts spending declines for a user in a date range
func (r *AllocationRepository) CountDeclinesInDateRange(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM transactions
		WHERE user_id = $1
		  AND declined_due_to_7030 = true
		  AND created_at >= $2
		  AND created_at <= $3
	`

	var count int
	err := r.db.GetContext(ctx, &count, query, userID, startDate, endDate)
	if err != nil {
		return 0, fmt.Errorf("failed to count declines: %w", err)
	}

	return count, nil
}
