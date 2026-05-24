package repositories

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type HealthScoreRepository struct {
	db *sqlx.DB
}

func NewHealthScoreRepository(db *sqlx.DB) *HealthScoreRepository {
	return &HealthScoreRepository{db: db}
}

func (r *HealthScoreRepository) SaveHealthScore(ctx context.Context, s *entities.MiriamFinancialHealthScore) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_financial_health_scores (
			id, user_id, overall_score, budget_score, savings_score, debt_score,
			runway_score, stability_score, trend, previous_score, reasoning,
			data_snapshot, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())`,
		s.ID, s.UserID, s.OverallScore, s.BudgetScore, s.SavingsScore, s.DebtScore,
		s.RunwayScore, s.StabilityScore, s.Trend, s.PreviousScore, s.Reasoning,
		s.DataSnapshot)
	if err != nil {
		return fmt.Errorf("save health score: %w", err)
	}
	return nil
}

func (r *HealthScoreRepository) GetRecentScores(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamFinancialHealthScore, error) {
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}
	var scores []entities.MiriamFinancialHealthScore
	err := r.db.SelectContext(ctx, &scores, `
		SELECT id, user_id, overall_score, budget_score, savings_score, debt_score,
		       runway_score, stability_score, trend, previous_score, reasoning,
		       data_snapshot, created_at
		FROM miriam_financial_health_scores
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent health scores: %w", err)
	}
	return scores, nil
}

func (r *HealthScoreRepository) GetLatestScore(ctx context.Context, userID uuid.UUID) (*entities.MiriamFinancialHealthScore, error) {
	var s entities.MiriamFinancialHealthScore
	err := r.db.GetContext(ctx, &s, `
		SELECT id, user_id, overall_score, budget_score, savings_score, debt_score,
		       runway_score, stability_score, trend, previous_score, reasoning,
		       data_snapshot, created_at
		FROM miriam_financial_health_scores
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest health score: %w", err)
	}
	return &s, nil
}
