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

type MiriamDecisionRepository struct {
	db *sqlx.DB
}

func NewMiriamDecisionRepository(db *sqlx.DB) *MiriamDecisionRepository {
	return &MiriamDecisionRepository{db: db}
}

func (r *MiriamDecisionRepository) CreateDecision(ctx context.Context, d *entities.MiriamDecision) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_decisions (
			id, user_id, mandate_id, decision_type, reason, confidence_score,
			factors, original_amount, adjusted_amount, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		d.ID, d.UserID, d.MandateID, d.DecisionType, d.Reason, d.ConfidenceScore,
		d.Factors, d.OriginalAmount, d.AdjustedAmount)
	if err != nil {
		return fmt.Errorf("create miriam decision: %w", err)
	}
	return nil
}

func (r *MiriamDecisionRepository) RecentDecisions(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamDecision, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > 50 {
		limit = 50
	}
	var decisions []entities.MiriamDecision
	err := r.db.SelectContext(ctx, &decisions, `
		SELECT id, user_id, mandate_id, decision_type, reason, confidence_score,
		       factors, original_amount, adjusted_amount, created_at
		FROM miriam_decisions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent miriam decisions: %w", err)
	}
	return decisions, nil
}

func (r *MiriamDecisionRepository) GetDecision(ctx context.Context, decisionID uuid.UUID) (*entities.MiriamDecision, error) {
	var d entities.MiriamDecision
	err := r.db.GetContext(ctx, &d, `
		SELECT id, user_id, mandate_id, decision_type, reason, confidence_score,
		       factors, original_amount, adjusted_amount, created_at
		FROM miriam_decisions
		WHERE id = $1`, decisionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get miriam decision: %w", err)
	}
	return &d, nil
}

func (r *MiriamDecisionRepository) CreateOutcome(ctx context.Context, o *entities.MiriamDecisionOutcome) error {
	if o.ID == uuid.Nil {
		o.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_decision_outcomes (
			id, user_id, decision_id, outcome, outcome_delay, feedback_score, metadata, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`,
		o.ID, o.UserID, o.DecisionID, o.Outcome, o.OutcomeDelay, o.FeedbackScore, o.Metadata)
	if err != nil {
		return fmt.Errorf("create miriam decision outcome: %w", err)
	}
	return nil
}

func (r *MiriamDecisionRepository) GetLearningModel(ctx context.Context, userID uuid.UUID, category string) (*entities.MiriamLearningModel, error) {
	var m entities.MiriamLearningModel
	err := r.db.GetContext(ctx, &m, `
		SELECT user_id, category, total_decisions, positive_outcomes, negative_outcomes,
		       current_bias, confidence_weight, last_updated_at
		FROM miriam_learning_models
		WHERE user_id = $1 AND category = $2`, userID, category)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get miriam learning model: %w", err)
	}
	return &m, nil
}

func (r *MiriamDecisionRepository) UpsertLearningModel(ctx context.Context, m *entities.MiriamLearningModel) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_learning_models (
			user_id, category, total_decisions, positive_outcomes, negative_outcomes,
			current_bias, confidence_weight, last_updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (user_id, category) DO UPDATE SET
			total_decisions = EXCLUDED.total_decisions,
			positive_outcomes = EXCLUDED.positive_outcomes,
			negative_outcomes = EXCLUDED.negative_outcomes,
			current_bias = EXCLUDED.current_bias,
			confidence_weight = EXCLUDED.confidence_weight,
			last_updated_at = NOW()`,
		m.UserID, m.Category, m.TotalDecisions, m.PositiveOutcomes, m.NegativeOutcomes,
		m.CurrentBias, m.ConfidenceWeight)
	if err != nil {
		return fmt.Errorf("upsert miriam learning model: %w", err)
	}
	return nil
}

func (r *MiriamDecisionRepository) ListOutcomesSince(ctx context.Context, userID uuid.UUID, category string, since time.Time) ([]entities.MiriamDecisionOutcome, error) {
	var outcomes []entities.MiriamDecisionOutcome
	err := r.db.SelectContext(ctx, &outcomes, `
		SELECT o.id, o.user_id, o.decision_id, o.outcome, o.outcome_delay,
		       o.feedback_score, o.metadata, o.created_at
		FROM miriam_decision_outcomes o
		JOIN miriam_decisions d ON d.id = o.decision_id
		WHERE o.user_id = $1 AND d.mandate_id IN (
			SELECT id FROM miriam_autopilot_mandates WHERE user_id = $1
		) AND o.created_at >= $2
		ORDER BY o.created_at DESC`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("list miriam outcomes since: %w", err)
	}
	return outcomes, nil
}
