package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type MiriamPredictionRepository struct {
	db *sqlx.DB
}

func NewMiriamPredictionRepository(db *sqlx.DB) *MiriamPredictionRepository {
	return &MiriamPredictionRepository{db: db}
}

func (r *MiriamPredictionRepository) UpsertPrediction(ctx context.Context, p *entities.MiriamPrediction) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_predictions (
			id, user_id, prediction_type, horizon, probability, severity,
			projected_amount, reasoning, data_snapshot, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (id) DO UPDATE SET
			prediction_type = EXCLUDED.prediction_type,
			horizon = EXCLUDED.horizon,
			probability = EXCLUDED.probability,
			severity = EXCLUDED.severity,
			projected_amount = EXCLUDED.projected_amount,
			reasoning = EXCLUDED.reasoning,
			data_snapshot = EXCLUDED.data_snapshot,
			expires_at = EXCLUDED.expires_at`,
		p.ID, p.UserID, p.PredictionType, p.Horizon, p.Probability, p.Severity,
		p.ProjectedAmount, p.Reasoning, p.DataSnapshot, p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("upsert miriam prediction: %w", err)
	}
	return nil
}

func (r *MiriamPredictionRepository) ListActivePredictions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamPrediction, error) {
	var predictions []entities.MiriamPrediction
	err := r.db.SelectContext(ctx, &predictions, `
		SELECT id, user_id, prediction_type, horizon, probability, severity,
		       projected_amount, reasoning, data_snapshot, expires_at, created_at
		FROM miriam_predictions
		WHERE user_id = $1 AND expires_at > NOW()
		ORDER BY expires_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list active miriam predictions: %w", err)
	}
	return predictions, nil
}

func (r *MiriamPredictionRepository) ExpireOldPredictions(ctx context.Context, before time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM miriam_predictions WHERE expires_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("expire old miriam predictions: %w", err)
	}
	return result.RowsAffected()
}
