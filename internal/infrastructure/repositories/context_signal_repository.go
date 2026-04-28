package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type ContextSignalRepository struct {
	db *sqlx.DB
}

func NewContextSignalRepository(db *sqlx.DB) *ContextSignalRepository {
	return &ContextSignalRepository{db: db}
}

func (r *ContextSignalRepository) Upsert(ctx context.Context, s *entities.UserContextSignal) error {
	query := `INSERT INTO user_context_signals (id, user_id, signal_type, signal_data, confidence, is_active, last_seen_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (user_id, signal_type, (signal_data->>'key')) WHERE signal_data ? 'key'
		DO UPDATE SET signal_data = $4, confidence = $5, is_active = $6, last_seen_at = NOW()`
	_, err := r.db.ExecContext(ctx, query, s.ID, s.UserID, s.SignalType, s.SignalData, s.Confidence, s.IsActive)
	return err
}

func (r *ContextSignalRepository) GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]entities.UserContextSignal, error) {
	var signals []entities.UserContextSignal
	err := r.db.SelectContext(ctx, &signals, `SELECT * FROM user_context_signals WHERE user_id = $1 AND is_active = true ORDER BY confidence DESC`, userID)
	return signals, err
}

func (r *ContextSignalRepository) GetByType(ctx context.Context, userID uuid.UUID, signalType string) ([]entities.UserContextSignal, error) {
	var signals []entities.UserContextSignal
	err := r.db.SelectContext(ctx, &signals, `SELECT * FROM user_context_signals WHERE user_id = $1 AND signal_type = $2 AND is_active = true`, userID, signalType)
	return signals, err
}

func (r *ContextSignalRepository) Deactivate(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE user_context_signals SET is_active = false WHERE id = $1`, id)
	return err
}
