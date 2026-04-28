package repositories

import (
	"context"
	"database/sql"
	"fmt"

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
		ORDER BY last_confirmed_at DESC`, userID, category)
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
