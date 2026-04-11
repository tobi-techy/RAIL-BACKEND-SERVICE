package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AIUsageRepository handles persistence for AI usage tracking.
type AIUsageRepository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewAIUsageRepository creates a new AI usage repository.
func NewAIUsageRepository(db *sql.DB, logger *zap.Logger) *AIUsageRepository {
	return &AIUsageRepository{db: db, logger: logger}
}

func aiUsagePeriodStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Upsert atomically increments usage counters for the current billing period.
func (r *AIUsageRepository) Upsert(ctx context.Context, userID uuid.UUID, messages int, voiceSeconds int, cost decimal.Decimal, model string) error {
	now := time.Now()
	ps := aiUsagePeriodStart(now)
	modelCallsJSON, _ := json.Marshal(map[string]int{model: 1})

	query := `
		INSERT INTO ai_usage (id, user_id, period_start, message_count, voice_seconds, estimated_cost_usd, model_calls, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, period_start) DO UPDATE SET
			message_count = ai_usage.message_count + EXCLUDED.message_count,
			voice_seconds = ai_usage.voice_seconds + EXCLUDED.voice_seconds,
			estimated_cost_usd = ai_usage.estimated_cost_usd + EXCLUDED.estimated_cost_usd,
			model_calls = COALESCE(
				(SELECT jsonb_object_agg(key, COALESCE((ai_usage.model_calls->>key)::int, 0) + COALESCE((EXCLUDED.model_calls->>key)::int, 0))
				 FROM jsonb_each_text(ai_usage.model_calls || EXCLUDED.model_calls)),
				'{}'::jsonb
			),
			updated_at = EXCLUDED.updated_at`

	_, err := r.db.ExecContext(ctx, query,
		uuid.New(), userID, ps, messages, voiceSeconds, cost, modelCallsJSON, now,
	)
	if err != nil {
		return fmt.Errorf("upsert ai usage: %w", err)
	}
	return nil
}

// GetByUserPeriod returns usage for a user in a specific billing period.
// Returns a zero-value AIUsage if no row exists.
func (r *AIUsageRepository) GetByUserPeriod(ctx context.Context, userID uuid.UUID, period time.Time) (*entities.AIUsage, error) {
	ps := aiUsagePeriodStart(period)

	query := `
		SELECT id, user_id, period_start, message_count, voice_seconds, estimated_cost_usd, model_calls, updated_at
		FROM ai_usage WHERE user_id = $1 AND period_start = $2`

	u := &entities.AIUsage{}
	var modelCallsJSON []byte
	err := r.db.QueryRowContext(ctx, query, userID, ps).Scan(
		&u.ID, &u.UserID, &u.PeriodStart, &u.MessageCount,
		&u.VoiceSeconds, &u.EstimatedCost, &modelCallsJSON, &u.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return &entities.AIUsage{
			UserID:        userID,
			PeriodStart:   ps,
			EstimatedCost: decimal.Zero,
			ModelCalls:    map[string]int{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get ai usage: %w", err)
	}
	if len(modelCallsJSON) > 0 {
		_ = json.Unmarshal(modelCallsJSON, &u.ModelCalls)
	}
	if u.ModelCalls == nil {
		u.ModelCalls = map[string]int{}
	}
	return u, nil
}
