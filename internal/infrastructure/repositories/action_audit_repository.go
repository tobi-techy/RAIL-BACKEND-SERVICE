package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// ActionAuditRepository persists action audit entries.
type ActionAuditRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

// NewActionAuditRepository creates a new action audit repository.
func NewActionAuditRepository(db *sqlx.DB, logger *zap.Logger) *ActionAuditRepository {
	return &ActionAuditRepository{db: db, logger: logger}
}

// RecordAction inserts an action audit entry.
func (r *ActionAuditRepository) RecordAction(ctx context.Context, entry *entities.ActionAuditEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	paramsJSON, err := json.Marshal(entry.Params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO ai_action_audit (id, user_id, conversation_id, action, params, status, error_message, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		entry.ID, entry.UserID, entry.ConversationID, entry.Action,
		paramsJSON, entry.Status, entry.ErrorMessage, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert action audit: %w", err)
	}
	return nil
}

// ListRecentActions returns recent AI action receipts for a user.
func (r *ActionAuditRepository) ListRecentActions(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.ActionAuditEntry, error) {
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	rows, err := r.db.QueryxContext(ctx, `
		SELECT id, user_id, conversation_id, action, params, status, error_message, created_at
		FROM ai_action_audit
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list action audit: %w", err)
	}
	defer rows.Close()

	var actions []*entities.ActionAuditEntry
	for rows.Next() {
		entry := &entities.ActionAuditEntry{}
		var paramsJSON []byte
		var errMsg *string
		var createdAt time.Time
		if err := rows.Scan(
			&entry.ID,
			&entry.UserID,
			&entry.ConversationID,
			&entry.Action,
			&paramsJSON,
			&entry.Status,
			&errMsg,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan action audit: %w", err)
		}
		if len(paramsJSON) > 0 {
			_ = json.Unmarshal(paramsJSON, &entry.Params)
		}
		if errMsg != nil {
			entry.ErrorMessage = *errMsg
		}
		entry.CreatedAt = createdAt
		actions = append(actions, entry)
	}
	return actions, rows.Err()
}
