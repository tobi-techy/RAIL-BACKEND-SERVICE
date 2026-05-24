package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
)

type NotificationPreferenceRepository struct {
	db *sqlx.DB
}

func NewNotificationPreferenceRepository(db *sqlx.DB) *NotificationPreferenceRepository {
	return &NotificationPreferenceRepository{db: db}
}

func (r *NotificationPreferenceRepository) GetPreferences(ctx context.Context, userID uuid.UUID) (*miriamsvc.NotificationPreferences, error) {
	var raw []byte
	err := r.db.GetContext(ctx, &raw, `
		SELECT preferences FROM miriam_notification_preferences WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get notification preferences: %w", err)
	}

	var prefs miriamsvc.NotificationPreferences
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return nil, fmt.Errorf("unmarshal notification preferences: %w", err)
	}
	return &prefs, nil
}

func (r *NotificationPreferenceRepository) SavePreferences(ctx context.Context, p *miriamsvc.NotificationPreferences) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal notification preferences: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO miriam_notification_preferences (user_id, preferences, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			preferences = EXCLUDED.preferences,
			updated_at = NOW()`,
		p.UserID, raw)
	if err != nil {
		return fmt.Errorf("save notification preferences: %w", err)
	}
	return nil
}
