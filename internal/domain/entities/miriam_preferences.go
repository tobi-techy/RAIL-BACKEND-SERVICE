package entities

import (
	"time"

	"github.com/google/uuid"
)

// Autonomy levels for miriam_preferences (product language).
// Maps to control_level: observe→monitor, suggest→guided, act→full.
const (
	AutonomyObserve = "observe"
	AutonomySuggest = "suggest"
	AutonomyAct     = "act"
)

// Cadence labels map to daily_cap.
const (
	CadenceMinimal   = "minimal"   // daily_cap 2
	CadenceBalanced  = "balanced"  // daily_cap 6
	CadenceProactive = "proactive" // daily_cap 12
)

// MiriamPreferences is the user-editable discretion surface for Miriam.
type MiriamPreferences struct {
	UserID          uuid.UUID `json:"user_id" db:"user_id"`
	BriefingEnabled bool      `json:"briefing_enabled" db:"briefing_enabled"`
	BriefingHour    int       `json:"briefing_hour" db:"briefing_hour"`
	Timezone        *string   `json:"timezone,omitempty" db:"timezone"`
	QuietEnabled    bool      `json:"quiet_enabled" db:"quiet_enabled"`
	QuietStart      int       `json:"quiet_start" db:"quiet_start"`
	QuietEnd        int       `json:"quiet_end" db:"quiet_end"`
	DailyCap        int       `json:"daily_cap" db:"daily_cap"`
	AllowBriefings  bool      `json:"allow_briefings" db:"allow_briefings"`
	AllowRisk       bool      `json:"allow_risk" db:"allow_risk"`
	AllowNudges     bool      `json:"allow_nudges" db:"allow_nudges"`
	AllowFollowups  bool      `json:"allow_followups" db:"allow_followups"`
	AutonomyLevel   string    `json:"autonomy_level" db:"autonomy_level"`
	HumorRoasting   bool      `json:"humor_roasting" db:"humor_roasting"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// DefaultMiriamPreferences returns product defaults when no row exists.
func DefaultMiriamPreferences(userID uuid.UUID) MiriamPreferences {
	now := time.Now().UTC()
	return MiriamPreferences{
		UserID:          userID,
		BriefingEnabled: true,
		BriefingHour:    9,
		QuietEnabled:    true,
		QuietStart:      22,
		QuietEnd:        7,
		DailyCap:        6,
		AllowBriefings:  true,
		AllowRisk:       true,
		AllowNudges:     true,
		AllowFollowups:  true,
		AutonomyLevel:   AutonomySuggest,
		HumorRoasting:   false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// CadenceFromCap maps daily_cap to a product cadence label.
func CadenceFromCap(cap int) string {
	switch {
	case cap <= 2:
		return CadenceMinimal
	case cap >= 12:
		return CadenceProactive
	default:
		return CadenceBalanced
	}
}

// CapFromCadence maps cadence label to daily_cap.
func CapFromCadence(cadence string) int {
	switch cadence {
	case CadenceMinimal:
		return 2
	case CadenceProactive:
		return 12
	default:
		return 6
	}
}

// AutonomyToControlLevel maps prefs autonomy → miriam control_level.
func AutonomyToControlLevel(autonomy string) string {
	switch autonomy {
	case AutonomyObserve:
		return ControlLevelMonitor
	case AutonomyAct:
		return ControlLevelFull
	default:
		return ControlLevelGuided
	}
}

// ControlLevelToAutonomy maps control_level → prefs autonomy.
func ControlLevelToAutonomy(level string) string {
	switch level {
	case ControlLevelMonitor:
		return AutonomyObserve
	case ControlLevelFull:
		return AutonomyAct
	default:
		return AutonomySuggest
	}
}
