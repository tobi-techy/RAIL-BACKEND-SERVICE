package repositories

import (
	"context"
	"encoding/json"
	"fmt"

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
