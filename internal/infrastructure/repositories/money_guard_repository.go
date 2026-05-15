package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type MoneyGuardRepository struct {
	db *sqlx.DB
}

func NewMoneyGuardRepository(db *sqlx.DB) *MoneyGuardRepository {
	return &MoneyGuardRepository{db: db}
}

func (r *MoneyGuardRepository) GetSettings(ctx context.Context, userID uuid.UUID) (*entities.MoneyGuardSettings, error) {
	var settings entities.MoneyGuardSettings
	err := r.db.GetContext(ctx, &settings, `SELECT * FROM money_guard_settings WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get money guard settings: %w", err)
	}
	return &settings, nil
}

func (r *MoneyGuardRepository) UpsertSettings(ctx context.Context, settings *entities.MoneyGuardSettings) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO money_guard_settings (
			user_id, guardian_mode, decimal_sweep_enabled, stash_raid_limit_per_month,
			card_cooldown_minutes, safe_to_spend_floor, recovery_mode_until,
			last_weekly_autopsy_sent_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			guardian_mode = EXCLUDED.guardian_mode,
			decimal_sweep_enabled = EXCLUDED.decimal_sweep_enabled,
			stash_raid_limit_per_month = EXCLUDED.stash_raid_limit_per_month,
			card_cooldown_minutes = EXCLUDED.card_cooldown_minutes,
			safe_to_spend_floor = EXCLUDED.safe_to_spend_floor,
			recovery_mode_until = EXCLUDED.recovery_mode_until,
			last_weekly_autopsy_sent_at = EXCLUDED.last_weekly_autopsy_sent_at,
			updated_at = NOW()`,
		settings.UserID,
		settings.GuardianMode,
		settings.DecimalSweepEnabled,
		settings.StashRaidLimitPerMonth,
		settings.CardCooldownMinutes,
		settings.SafeToSpendFloor,
		settings.RecoveryModeUntil,
		settings.LastWeeklyAutopsySentAt,
	)
	if err != nil {
		return fmt.Errorf("upsert money guard settings: %w", err)
	}
	return nil
}

func (r *MoneyGuardRepository) CreateCap(ctx context.Context, cap *entities.SpendingCap) error {
	query := `
		INSERT INTO spending_caps (
			id, user_id, scope, scope_value, period, limit_amount, currency,
			enforcement_action, is_active, created_at, updated_at
		)
		VALUES (
			:id, :user_id, :scope, :scope_value, :period, :limit_amount, :currency,
			:enforcement_action, :is_active, NOW(), NOW()
		)
		RETURNING created_at, updated_at`
	stmt, err := r.db.PrepareNamedContext(ctx, query)
	if err != nil {
		return fmt.Errorf("prepare create cap: %w", err)
	}
	defer stmt.Close()
	if err := stmt.GetContext(ctx, cap, cap); err != nil {
		return fmt.Errorf("create cap: %w", err)
	}
	return nil
}

func (r *MoneyGuardRepository) ListCaps(ctx context.Context, userID uuid.UUID, activeOnly bool) ([]entities.SpendingCap, error) {
	query := `SELECT * FROM spending_caps WHERE user_id = $1`
	args := []interface{}{userID}
	if activeOnly {
		query += ` AND is_active = true`
	}
	query += ` ORDER BY created_at DESC`
	var caps []entities.SpendingCap
	if err := r.db.SelectContext(ctx, &caps, query, args...); err != nil {
		return nil, fmt.Errorf("list caps: %w", err)
	}
	return caps, nil
}

func (r *MoneyGuardRepository) DeleteCap(ctx context.Context, userID, id uuid.UUID) error {
	res, err := r.db.ExecContext(ctx, `UPDATE spending_caps SET is_active = false, updated_at = NOW() WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete cap: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *MoneyGuardRepository) RecordEvent(ctx context.Context, event *entities.MoneyGuardEvent) error {
	if len(event.Metadata) == 0 {
		event.Metadata = json.RawMessage(`{}`)
	}
	_, err := r.db.NamedExecContext(ctx, `
		INSERT INTO money_guard_events (
			id, user_id, event_type, severity, amount, currency, merchant,
			category, action, message, metadata, occurred_at
		)
		VALUES (
			:id, :user_id, :event_type, :severity, :amount, :currency, :merchant,
			:category, :action, :message, :metadata, :occurred_at
		)`, event)
	if err != nil {
		return fmt.Errorf("record money guard event: %w", err)
	}
	return nil
}

func (r *MoneyGuardRepository) CountEvents(ctx context.Context, userID uuid.UUID, since time.Time, severities ...string) (int, error) {
	args := []interface{}{userID, since}
	query := `SELECT COUNT(*) FROM money_guard_events WHERE user_id = $1 AND occurred_at >= $2`
	if len(severities) > 0 {
		query += ` AND severity = ANY($3)`
		args = append(args, pq.Array(severities))
	}
	var count int
	if err := r.db.GetContext(ctx, &count, query, args...); err != nil {
		return 0, fmt.Errorf("count money guard events: %w", err)
	}
	return count, nil
}

func (r *MoneyGuardRepository) CountEventsByType(ctx context.Context, userID uuid.UUID, eventType string, since time.Time) (int, error) {
	var count int
	if err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM money_guard_events WHERE user_id = $1 AND event_type = $2 AND occurred_at >= $3`,
		userID, eventType, since,
	); err != nil {
		return 0, fmt.Errorf("count money guard events by type: %w", err)
	}
	return count, nil
}
