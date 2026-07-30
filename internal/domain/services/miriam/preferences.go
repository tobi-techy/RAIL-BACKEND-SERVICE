package miriam

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// PreferencesStore is the persistence port for Miriam prefs.
type PreferencesStore interface {
	Get(ctx context.Context, userID uuid.UUID) (entities.MiriamPreferences, error)
	Upsert(ctx context.Context, p entities.MiriamPreferences) (entities.MiriamPreferences, error)
}

// ControlLevelWriter syncs autonomy_level → control_level.
type ControlLevelWriter interface {
	SetControlLevel(ctx context.Context, userID uuid.UUID, level string) error
}

// PreferencesService is the thin domain layer for miriam_preferences.
type PreferencesService struct {
	store   PreferencesStore
	control ControlLevelWriter
	logger  *zap.Logger
}

func NewPreferencesService(store PreferencesStore, control ControlLevelWriter, logger *zap.Logger) *PreferencesService {
	return &PreferencesService{store: store, control: control, logger: logger}
}

// Get returns prefs with defaults on miss.
func (s *PreferencesService) Get(ctx context.Context, userID uuid.UUID) (entities.MiriamPreferences, error) {
	if s.store == nil {
		return entities.DefaultMiriamPreferences(userID), nil
	}
	return s.store.Get(ctx, userID)
}

// UpdateRequest is a partial update from API or chat tools.
type UpdateRequest struct {
	BriefingEnabled *bool   `json:"briefing_enabled"`
	BriefingHour    *int    `json:"briefing_hour"`
	Timezone        *string `json:"timezone"`
	QuietEnabled    *bool   `json:"quiet_enabled"`
	QuietStart      *int    `json:"quiet_start"`
	QuietEnd        *int    `json:"quiet_end"`
	DailyCap        *int    `json:"daily_cap"`
	Cadence         *string `json:"cadence"` // minimal|balanced|proactive — maps to daily_cap
	AllowBriefings  *bool   `json:"allow_briefings"`
	AllowRisk       *bool   `json:"allow_risk"`
	AllowNudges     *bool   `json:"allow_nudges"`
	AllowFollowups  *bool   `json:"allow_followups"`
	AutonomyLevel   *string `json:"autonomy_level"`
	HumorRoasting   *bool   `json:"humor_roasting"`
}

// Update applies a partial update, syncs autonomy → control_level, returns saved prefs.
func (s *PreferencesService) Update(ctx context.Context, userID uuid.UUID, req UpdateRequest) (entities.MiriamPreferences, error) {
	if s.store == nil {
		return entities.MiriamPreferences{}, fmt.Errorf("preferences store unavailable")
	}
	cur, err := s.store.Get(ctx, userID)
	if err != nil {
		return entities.MiriamPreferences{}, err
	}
	if req.BriefingEnabled != nil {
		cur.BriefingEnabled = *req.BriefingEnabled
	}
	if req.BriefingHour != nil {
		h := *req.BriefingHour
		if h < 0 || h > 23 {
			return entities.MiriamPreferences{}, fmt.Errorf("briefing_hour must be 0-23")
		}
		cur.BriefingHour = h
	}
	if req.Timezone != nil {
		tz := strings.TrimSpace(*req.Timezone)
		if tz == "" {
			cur.Timezone = nil
		} else {
			cur.Timezone = &tz
		}
	}
	if req.QuietEnabled != nil {
		cur.QuietEnabled = *req.QuietEnabled
	}
	if req.QuietStart != nil {
		h := *req.QuietStart
		if h < 0 || h > 23 {
			return entities.MiriamPreferences{}, fmt.Errorf("quiet_start must be 0-23")
		}
		cur.QuietStart = h
	}
	if req.QuietEnd != nil {
		h := *req.QuietEnd
		if h < 0 || h > 23 {
			return entities.MiriamPreferences{}, fmt.Errorf("quiet_end must be 0-23")
		}
		cur.QuietEnd = h
	}
	if req.Cadence != nil {
		cur.DailyCap = entities.CapFromCadence(strings.ToLower(strings.TrimSpace(*req.Cadence)))
	}
	if req.DailyCap != nil {
		c := *req.DailyCap
		if c < 0 || c > 50 {
			return entities.MiriamPreferences{}, fmt.Errorf("daily_cap must be 0-50")
		}
		cur.DailyCap = c
	}
	if req.AllowBriefings != nil {
		cur.AllowBriefings = *req.AllowBriefings
	}
	if req.AllowRisk != nil {
		cur.AllowRisk = *req.AllowRisk
	}
	if req.AllowNudges != nil {
		cur.AllowNudges = *req.AllowNudges
	}
	if req.AllowFollowups != nil {
		cur.AllowFollowups = *req.AllowFollowups
	}
	if req.AutonomyLevel != nil {
		a := strings.ToLower(strings.TrimSpace(*req.AutonomyLevel))
		switch a {
		case entities.AutonomyObserve, entities.AutonomySuggest, entities.AutonomyAct:
			cur.AutonomyLevel = a
		default:
			return entities.MiriamPreferences{}, fmt.Errorf("autonomy_level must be observe, suggest, or act")
		}
	}
	if req.HumorRoasting != nil {
		cur.HumorRoasting = *req.HumorRoasting
	}
	cur.UserID = userID

	saved, err := s.store.Upsert(ctx, cur)
	if err != nil {
		return entities.MiriamPreferences{}, err
	}

	// Keep control_level in sync so mandate gating and prefs share one truth.
	if s.control != nil {
		level := entities.AutonomyToControlLevel(saved.AutonomyLevel)
		if err := s.control.SetControlLevel(ctx, userID, level); err != nil && s.logger != nil {
			s.logger.Warn("sync control level from prefs failed",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	return saved, nil
}

// APIView is the JSON shape returned to clients.
func APIView(p entities.MiriamPreferences, timezoneSource string) map[string]interface{} {
	tz := ""
	if p.Timezone != nil {
		tz = *p.Timezone
	}
	return map[string]interface{}{
		"briefing_enabled": p.BriefingEnabled,
		"briefing_hour":    p.BriefingHour,
		"timezone":         tz,
		"timezone_source":  timezoneSource,
		"quiet_enabled":    p.QuietEnabled,
		"quiet_start":      p.QuietStart,
		"quiet_end":        p.QuietEnd,
		"cadence":          entities.CadenceFromCap(p.DailyCap),
		"daily_cap":        p.DailyCap,
		"allow": map[string]bool{
			"briefings": p.AllowBriefings,
			"risk":      p.AllowRisk,
			"nudges":    p.AllowNudges,
			"followups": p.AllowFollowups,
		},
		"autonomy_level": p.AutonomyLevel,
		"humor_roasting": p.HumorRoasting,
	}
}
