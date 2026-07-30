package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// MiriamPreferencesRepository loads/saves miriam_preferences rows.
type MiriamPreferencesRepository struct {
	db     *sqlx.DB
	logger *zap.Logger
}

func NewMiriamPreferencesRepository(db *sqlx.DB, logger *zap.Logger) *MiriamPreferencesRepository {
	return &MiriamPreferencesRepository{db: db, logger: logger}
}

// Get returns preferences for the user, or defaults when no row exists.
func (r *MiriamPreferencesRepository) Get(ctx context.Context, userID uuid.UUID) (entities.MiriamPreferences, error) {
	var p entities.MiriamPreferences
	err := r.db.GetContext(ctx, &p, `
		SELECT user_id, briefing_enabled, briefing_hour, timezone,
		       quiet_enabled, quiet_start, quiet_end, daily_cap,
		       allow_briefings, allow_risk, allow_nudges, allow_followups,
		       autonomy_level, humor_roasting, created_at, updated_at
		FROM miriam_preferences WHERE user_id = $1`, userID)
	if err == sql.ErrNoRows {
		return entities.DefaultMiriamPreferences(userID), nil
	}
	if err != nil {
		return entities.MiriamPreferences{}, fmt.Errorf("get miriam preferences: %w", err)
	}
	return p, nil
}

// Upsert creates or updates the full preferences row.
func (r *MiriamPreferencesRepository) Upsert(ctx context.Context, p entities.MiriamPreferences) (entities.MiriamPreferences, error) {
	now := time.Now().UTC()
	p.UpdatedAt = now
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO miriam_preferences (
			user_id, briefing_enabled, briefing_hour, timezone,
			quiet_enabled, quiet_start, quiet_end, daily_cap,
			allow_briefings, allow_risk, allow_nudges, allow_followups,
			autonomy_level, humor_roasting, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16
		)
		ON CONFLICT (user_id) DO UPDATE SET
			briefing_enabled = EXCLUDED.briefing_enabled,
			briefing_hour = EXCLUDED.briefing_hour,
			timezone = EXCLUDED.timezone,
			quiet_enabled = EXCLUDED.quiet_enabled,
			quiet_start = EXCLUDED.quiet_start,
			quiet_end = EXCLUDED.quiet_end,
			daily_cap = EXCLUDED.daily_cap,
			allow_briefings = EXCLUDED.allow_briefings,
			allow_risk = EXCLUDED.allow_risk,
			allow_nudges = EXCLUDED.allow_nudges,
			allow_followups = EXCLUDED.allow_followups,
			autonomy_level = EXCLUDED.autonomy_level,
			humor_roasting = EXCLUDED.humor_roasting,
			updated_at = EXCLUDED.updated_at
	`,
		p.UserID, p.BriefingEnabled, p.BriefingHour, p.Timezone,
		p.QuietEnabled, p.QuietStart, p.QuietEnd, p.DailyCap,
		p.AllowBriefings, p.AllowRisk, p.AllowNudges, p.AllowFollowups,
		p.AutonomyLevel, p.HumorRoasting, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return entities.MiriamPreferences{}, fmt.Errorf("upsert miriam preferences: %w", err)
	}
	return r.Get(ctx, p.UserID)
}
