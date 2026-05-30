package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type MandateSuggestionRepository struct {
	db *sqlx.DB
}

func NewMandateSuggestionRepository(db *sqlx.DB) *MandateSuggestionRepository {
	return &MandateSuggestionRepository{db: db}
}

func (r *MandateSuggestionRepository) CreateSuggestion(ctx context.Context, s *entities.MiriamMandateSuggestion) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	if s.Status == "" {
		s.Status = "pending"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_mandate_suggestions (
			id, user_id, name, action_type, reasoning,
			suggested_max_amount, suggested_max_day, suggested_min_balance,
			suggested_cooldown, confidence, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())`,
		s.ID, s.UserID, s.Name, s.ActionType, s.Reasoning,
		s.SuggestedMaxAmount, s.SuggestedMaxDay, s.SuggestedMinBalance,
		s.SuggestedCooldown, s.Confidence, s.Status)
	if err != nil {
		return fmt.Errorf("create mandate suggestion: %w", err)
	}
	return nil
}

func (r *MandateSuggestionRepository) ListPendingSuggestions(ctx context.Context, userID uuid.UUID) ([]entities.MiriamMandateSuggestion, error) {
	var suggestions []entities.MiriamMandateSuggestion
	err := r.db.SelectContext(ctx, &suggestions, `
		SELECT id, user_id, name, action_type, reasoning,
		       suggested_max_amount, suggested_max_day, suggested_min_balance,
		       suggested_cooldown, confidence, status, created_at,
		       dismissed_at, accepted_at
		FROM miriam_mandate_suggestions
		WHERE user_id = $1 AND status = 'pending'
		ORDER BY confidence DESC, created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list pending mandate suggestions: %w", err)
	}
	return suggestions, nil
}

func (r *MandateSuggestionRepository) DismissSuggestion(ctx context.Context, suggestionID uuid.UUID) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE miriam_mandate_suggestions
		SET status = 'dismissed', dismissed_at = $1
		WHERE id = $2`, now, suggestionID)
	if err != nil {
		return fmt.Errorf("dismiss mandate suggestion: %w", err)
	}
	return nil
}

func (r *MandateSuggestionRepository) AcceptSuggestion(ctx context.Context, suggestionID uuid.UUID) (*entities.MiriamAutopilotMandate, error) {
	var s entities.MiriamMandateSuggestion
	err := r.db.GetContext(ctx, &s, `
		SELECT id, user_id, name, action_type, reasoning,
		       suggested_max_amount, suggested_max_day, suggested_min_balance,
		       suggested_cooldown, confidence, status, created_at,
		       dismissed_at, accepted_at
		FROM miriam_mandate_suggestions
		WHERE id = $1`, suggestionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mandate suggestion: %w", err)
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	mandateID := uuid.New()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO miriam_autopilot_mandates (
			id, user_id, name, action_type, status,
			max_amount_per_action, max_amount_per_day,
			min_spend_balance, min_safe_to_spend,
			cooldown_minutes, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, 0, $8, $9, NOW(), NOW())`,
		mandateID, s.UserID, s.Name, s.ActionType,
		s.SuggestedMaxAmount, s.SuggestedMaxDay,
		s.SuggestedMinBalance, s.SuggestedCooldown,
		fmt.Sprintf(`{"from_suggestion":%q}`, suggestionID.String()))
	if err != nil {
		return nil, fmt.Errorf("create mandate from suggestion: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE miriam_mandate_suggestions
		SET status = 'accepted', accepted_at = $1
		WHERE id = $2`, now, suggestionID)
	if err != nil {
		return nil, fmt.Errorf("update suggestion status to accepted: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit accept suggestion: %w", err)
	}

	return &entities.MiriamAutopilotMandate{
		ID:                 mandateID,
		UserID:             s.UserID,
		Name:               s.Name,
		ActionType:         s.ActionType,
		Status:             "active",
		MaxAmountPerAction: s.SuggestedMaxAmount,
		MaxAmountPerDay:    s.SuggestedMaxDay,
		MinSpendBalance:    s.SuggestedMinBalance,
		MinSafeToSpend:     decimal.Zero,
		CooldownMinutes:    s.SuggestedCooldown,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

func (r *MandateSuggestionRepository) GetSuggestion(ctx context.Context, suggestionID uuid.UUID) (*entities.MiriamMandateSuggestion, error) {
	var s entities.MiriamMandateSuggestion
	err := r.db.GetContext(ctx, &s, `
		SELECT id, user_id, name, action_type, reasoning,
		       suggested_max_amount, suggested_max_day, suggested_min_balance,
		       suggested_cooldown, confidence, status, created_at,
		       dismissed_at, accepted_at
		FROM miriam_mandate_suggestions
		WHERE id = $1`, suggestionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mandate suggestion: %w", err)
	}
	return &s, nil
}
