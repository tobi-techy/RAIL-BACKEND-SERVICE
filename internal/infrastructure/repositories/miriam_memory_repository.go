package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// MiriamMemoryRepository persists user facts and tone profiles.
type MiriamMemoryRepository struct {
	db *sqlx.DB
}

func NewMiriamMemoryRepository(db *sqlx.DB) *MiriamMemoryRepository {
	return &MiriamMemoryRepository{db: db}
}

// --- User Facts ---

// SaveFact inserts a new fact. If an existing active fact in the same category
// is semantically replaced, pass its ID as supersedes to chain them.
func (r *MiriamMemoryRepository) SaveFact(ctx context.Context, fact *entities.MiriamUserFact, supersedes *uuid.UUID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowxContext(ctx, `
		INSERT INTO miriam_user_facts (user_id, category, fact, source, confidence, first_observed_at, last_confirmed_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id, created_at, first_observed_at, last_confirmed_at`,
		fact.UserID, fact.Category, fact.Fact, fact.Source, fact.Confidence,
	).Scan(&fact.ID, &fact.CreatedAt, &fact.FirstObservedAt, &fact.LastConfirmedAt)
	if err != nil {
		return fmt.Errorf("insert fact: %w", err)
	}

	if supersedes != nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE miriam_user_facts SET superseded_by = $1 WHERE id = $2 AND user_id = $3`,
			fact.ID, *supersedes, fact.UserID,
		)
		if err != nil {
			return fmt.Errorf("supersede old fact: %w", err)
		}
	}

	return tx.Commit()
}

// GetActiveFacts returns all non-superseded facts for a user, ordered by recency.
// Capped at 50 to prevent unbounded prompt injection.
func (r *MiriamMemoryRepository) GetActiveFacts(ctx context.Context, userID uuid.UUID) ([]*entities.MiriamUserFact, error) {
	var facts []*entities.MiriamUserFact
	err := r.db.SelectContext(ctx, &facts, `
		SELECT id, user_id, category, fact, source, confidence, superseded_by,
		       first_observed_at, last_confirmed_at, created_at
		FROM miriam_user_facts
		WHERE user_id = $1 AND superseded_by IS NULL
		ORDER BY last_confirmed_at DESC
		LIMIT 50`, userID)
	if err != nil {
		return nil, fmt.Errorf("get active facts: %w", err)
	}
	return facts, nil
}

// GetActiveFactsByCategory returns active facts filtered by category.
func (r *MiriamMemoryRepository) GetActiveFactsByCategory(ctx context.Context, userID uuid.UUID, category string) ([]*entities.MiriamUserFact, error) {
	var facts []*entities.MiriamUserFact
	err := r.db.SelectContext(ctx, &facts, `
		SELECT id, user_id, category, fact, source, confidence, superseded_by,
		       first_observed_at, last_confirmed_at, created_at
		FROM miriam_user_facts
		WHERE user_id = $1 AND category = $2 AND superseded_by IS NULL
		ORDER BY last_confirmed_at DESC
		LIMIT 20`, userID, category)
	if err != nil {
		return nil, fmt.Errorf("get facts by category: %w", err)
	}
	return facts, nil
}

// ConfirmFact bumps the last_confirmed_at timestamp for an existing fact.
func (r *MiriamMemoryRepository) ConfirmFact(ctx context.Context, factID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_user_facts SET last_confirmed_at = NOW()
		WHERE id = $1 AND user_id = $2 AND superseded_by IS NULL`, factID, userID)
	if err != nil {
		return fmt.Errorf("confirm fact: %w", err)
	}
	return nil
}

// DeleteFact soft-deletes by self-superseding (sets superseded_by = id).
func (r *MiriamMemoryRepository) DeleteFact(ctx context.Context, factID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_user_facts SET superseded_by = id
		WHERE id = $1 AND user_id = $2`, factID, userID)
	if err != nil {
		return fmt.Errorf("delete fact: %w", err)
	}
	return nil
}

// --- Tone Profile ---

// GetToneProfile returns the user's tone profile, or nil if none exists.
func (r *MiriamMemoryRepository) GetToneProfile(ctx context.Context, userID uuid.UUID) (*entities.MiriamToneProfile, error) {
	var profile entities.MiriamToneProfile
	err := r.db.GetContext(ctx, &profile, `
		SELECT user_id, formality, directness, warmth, humor, brevity,
		       preferred_name, language_style, sample_count, created_at, updated_at
		FROM miriam_tone_profiles
		WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tone profile: %w", err)
	}
	return &profile, nil
}

// UpsertToneProfile creates or updates the tone profile with exponential moving average.
// newWeights are the detected dimensions from the latest message; they blend with existing values.
func (r *MiriamMemoryRepository) UpsertToneProfile(ctx context.Context, userID uuid.UUID, formality, directness, warmth, humor, brevity decimal.Decimal, preferredName, languageStyle string) error {
	// EMA alpha: new observations have ~20% weight, decaying over time.
	// LEAST/GREATEST clamps prevent floating-point drift from violating CHECK constraints.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_tone_profiles (user_id, formality, directness, warmth, humor, brevity, preferred_name, language_style, sample_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1)
		ON CONFLICT (user_id) DO UPDATE SET
			formality = LEAST(1, GREATEST(0, miriam_tone_profiles.formality * 0.8 + $2 * 0.2)),
			directness = LEAST(1, GREATEST(0, miriam_tone_profiles.directness * 0.8 + $3 * 0.2)),
			warmth = LEAST(1, GREATEST(0, miriam_tone_profiles.warmth * 0.8 + $4 * 0.2)),
			humor = LEAST(1, GREATEST(0, miriam_tone_profiles.humor * 0.8 + $5 * 0.2)),
			brevity = LEAST(1, GREATEST(0, miriam_tone_profiles.brevity * 0.8 + $6 * 0.2)),
			preferred_name = CASE WHEN $7 = '' THEN miriam_tone_profiles.preferred_name ELSE $7 END,
			language_style = CASE WHEN $8 = 'standard' THEN miriam_tone_profiles.language_style ELSE $8 END,
			sample_count = miriam_tone_profiles.sample_count + 1,
			updated_at = NOW()`,
		userID, formality, directness, warmth, humor, brevity, preferredName, languageStyle)
	if err != nil {
		return fmt.Errorf("upsert tone profile: %w", err)
	}
	return nil
}

// --- Staleness & Decay ---

// DecayStaleFacts reduces confidence of facts not confirmed within the given duration.
func (r *MiriamMemoryRepository) DecayStaleFacts(ctx context.Context, staleDuration time.Duration, decayAmount decimal.Decimal) (int64, error) {
	cutoff := time.Now().Add(-staleDuration)
	result, err := r.db.ExecContext(ctx, `
		UPDATE miriam_user_facts
		SET confidence = GREATEST(0.1, confidence - $1)
		WHERE superseded_by IS NULL
		  AND last_confirmed_at < $2
		  AND confidence > 0.1`, decayAmount, cutoff)
	if err != nil {
		return 0, fmt.Errorf("decay stale facts: %w", err)
	}
	return result.RowsAffected()
}

// ExpireLowConfidenceFacts soft-deletes facts below a confidence threshold.
func (r *MiriamMemoryRepository) ExpireLowConfidenceFacts(ctx context.Context, threshold decimal.Decimal) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE miriam_user_facts
		SET superseded_by = id
		WHERE superseded_by IS NULL AND confidence <= $1`, threshold)
	if err != nil {
		return 0, fmt.Errorf("expire low confidence facts: %w", err)
	}
	return result.RowsAffected()
}

// --- Embeddings ---

// SetFactEmbedding stores the embedding vector for a fact.
func (r *MiriamMemoryRepository) SetFactEmbedding(ctx context.Context, factID uuid.UUID, embedding []float32) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_user_facts SET embedding = $1 WHERE id = $2`, pgvectorFromFloat32(embedding), factID)
	if err != nil {
		return fmt.Errorf("set fact embedding: %w", err)
	}
	return nil
}

// SimilarFact wraps a fact with its cosine distance from the query vector.
type SimilarFact = entities.SimilarFact

// FindSimilarFacts returns active facts for a user whose embedding is similar to the given vector.
func (r *MiriamMemoryRepository) FindSimilarFacts(ctx context.Context, userID uuid.UUID, embedding []float32, category string, limit int) ([]SimilarFact, error) {
	var facts []SimilarFact
	err := r.db.SelectContext(ctx, &facts, `
		SELECT id, user_id, category, fact, source, confidence, superseded_by,
		       first_observed_at, last_confirmed_at, created_at,
		       (embedding <=> $3) AS distance
		FROM miriam_user_facts
		WHERE user_id = $1 AND category = $2 AND superseded_by IS NULL AND embedding IS NOT NULL
		ORDER BY embedding <=> $3
		LIMIT $4`, userID, category, pgvectorFromFloat32(embedding), limit)
	if err != nil {
		return nil, fmt.Errorf("find similar facts: %w", err)
	}
	return facts, nil
}

// pgvectorFromFloat32 converts a float32 slice to pgvector string format.
func pgvectorFromFloat32(v []float32) string {
	if len(v) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", f))
	}
	sb.WriteByte(']')
	return sb.String()
}

// --- Memory Summary ---

// GetMemorySummary returns the compressed narrative for a user.
func (r *MiriamMemoryRepository) GetMemorySummary(ctx context.Context, userID uuid.UUID) (*entities.MiriamMemorySummary, error) {
	var s entities.MiriamMemorySummary
	err := r.db.GetContext(ctx, &s, `
		SELECT user_id, summary, fact_count, last_summarized_at, created_at
		FROM miriam_memory_summaries WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get memory summary: %w", err)
	}
	return &s, nil
}

// UpsertMemorySummary creates or replaces the memory summary.
func (r *MiriamMemoryRepository) UpsertMemorySummary(ctx context.Context, userID uuid.UUID, summary string, factCount int) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_memory_summaries (user_id, summary, fact_count, last_summarized_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			summary = $2, fact_count = $3, last_summarized_at = NOW()`,
		userID, summary, factCount)
	if err != nil {
		return fmt.Errorf("upsert memory summary: %w", err)
	}
	return nil
}

// --- User-Facing ---

// DeleteFactByUser allows a user to delete a specific fact they own.
func (r *MiriamMemoryRepository) DeleteFactByUser(ctx context.Context, factID, userID uuid.UUID) error {
	return r.DeleteFact(ctx, factID, userID)
}

// GetAllActiveUserIDs returns distinct user IDs that have active facts (for batch workers).
func (r *MiriamMemoryRepository) GetAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.SelectContext(ctx, &ids, `
		SELECT DISTINCT user_id FROM miriam_user_facts WHERE superseded_by IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("get active user ids: %w", err)
	}
	return ids, nil
}
