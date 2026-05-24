package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type ProactiveNudgeRepository struct {
	db *sqlx.DB
}

func NewProactiveNudgeRepository(db *sqlx.DB) *ProactiveNudgeRepository {
	return &ProactiveNudgeRepository{db: db}
}

func (r *ProactiveNudgeRepository) CreateNudge(ctx context.Context, n *entities.ProactiveNudge) error {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO proactive_nudges (
			id, user_id, trigger_type, priority, message, action_suggestion,
			expires_at, delivered_at, dismissed_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		n.ID, n.UserID, n.TriggerType, n.Priority, n.Message, n.ActionSuggestion,
		n.ExpiresAt, n.DeliveredAt, n.DismissedAt)
	if err != nil {
		return fmt.Errorf("create proactive nudge: %w", err)
	}
	return nil
}

func (r *ProactiveNudgeRepository) ListPendingNudges(ctx context.Context, userID uuid.UUID) ([]entities.ProactiveNudge, error) {
	var nudges []entities.ProactiveNudge
	err := r.db.SelectContext(ctx, &nudges, `
		SELECT id, user_id, trigger_type, priority, message, action_suggestion,
		       expires_at, delivered_at, dismissed_at, created_at
		FROM proactive_nudges
		WHERE user_id = $1
		  AND delivered_at IS NULL
		  AND dismissed_at IS NULL
		  AND expires_at > NOW()
		ORDER BY priority DESC, created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list pending nudges: %w", err)
	}
	return nudges, nil
}

func (r *ProactiveNudgeRepository) MarkDelivered(ctx context.Context, nudgeID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE proactive_nudges SET delivered_at = NOW() WHERE id = $1`, nudgeID)
	if err != nil {
		return fmt.Errorf("mark nudge delivered: %w", err)
	}
	return nil
}

func (r *ProactiveNudgeRepository) MarkDismissed(ctx context.Context, nudgeID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE proactive_nudges SET dismissed_at = NOW() WHERE id = $1`, nudgeID)
	if err != nil {
		return fmt.Errorf("mark nudge dismissed: %w", err)
	}
	return nil
}

func (r *ProactiveNudgeRepository) ExpireOldNudges(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM proactive_nudges WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("expire old nudges: %w", err)
	}
	return result.RowsAffected()
}

func (r *ProactiveNudgeRepository) HasRecentNudgeByType(ctx context.Context, userID uuid.UUID, triggerType string, since time.Time) (bool, error) {
	var count int
	err := r.db.GetContext(ctx, &count, `
		SELECT COUNT(*)
		FROM proactive_nudges
		WHERE user_id = $1
		  AND trigger_type = $2
		  AND created_at >= $3`, userID, triggerType, since)
	if err != nil {
		return false, fmt.Errorf("has recent nudge by type: %w", err)
	}
	return count > 0, nil
}

func (r *ProactiveNudgeRepository) GetNudge(ctx context.Context, nudgeID uuid.UUID) (*entities.ProactiveNudge, error) {
	var n entities.ProactiveNudge
	err := r.db.GetContext(ctx, &n, `
		SELECT id, user_id, trigger_type, priority, message, action_suggestion,
		       expires_at, delivered_at, dismissed_at, created_at
		FROM proactive_nudges
		WHERE id = $1`, nudgeID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get nudge: %w", err)
	}
	return &n, nil
}
