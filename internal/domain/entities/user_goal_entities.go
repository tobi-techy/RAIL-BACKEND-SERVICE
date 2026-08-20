package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Goal categories for user_goals. The Baby Step ladder maps to specific
// categories so the seed helper can build a default plan without a code
// dependency on the Baby Step number.
const (
	GoalCategoryStarterEmergency = "starter_emergency"
	GoalCategoryDebtPayoff       = "debt_payoff"
	GoalCategoryFullEmergency    = "full_emergency"
	GoalCategoryRetirement       = "retirement"
	GoalCategoryCollege          = "college"
	GoalCategoryMortgage         = "mortgage"
	GoalCategoryWealth           = "wealth"
	GoalCategoryFreeform         = "freeform"
)

// Goal sources for user_goals. Tracks how the goal was created so the audit log
// can distinguish chat-driven, onboarding-seeded, and manual creations.
const (
	GoalSourceMiriamChat      = "miriam_chat"
	GoalSourceMiriamOnboard   = "miriam_onboarding"
	GoalSourceManual          = "manual"
	GoalSourceMiriamPulse     = "miriam_pulse"
)

// Progress event kinds for user_goal_progress_events.
const (
	ProgressEventCreated         = "created"
	ProgressEventArchived        = "archived"
	ProgressEventCompleted       = "completed"
	ProgressEventMilestone25     = "milestone_25"
	ProgressEventMilestone50     = "milestone_50"
	ProgressEventMilestone75     = "milestone_75"
	ProgressEventMilestone100    = "milestone_100"
	ProgressEventPaceBehind      = "pace_behind"
	ProgressEventPaceAhead       = "pace_ahead"
	ProgressEventStepAdvanced    = "step_advanced"
	ProgressEventProgressUpdated = "progress_updated"
)

// UserGoal is a free-standing savings goal surfaced by Miriam. Distinct from
// the existing `goals` table (which is bound to automation evaluation) so the
// Baby Steps coaching ladder can evolve without automation regressions.
type UserGoal struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	UserID         uuid.UUID       `json:"user_id" db:"user_id"`
	Name           string          `json:"name" db:"name"`
	TargetAmount   decimal.Decimal `json:"target_amount" db:"target_amount"`
	TargetCurrency string          `json:"target_currency" db:"target_currency"`
	CurrentAmount  decimal.Decimal `json:"current_amount" db:"current_amount"`
	Deadline       *time.Time      `json:"deadline,omitempty" db:"deadline"`
	BabyStep       *int            `json:"baby_step,omitempty" db:"baby_step"`
	Category       string          `json:"category" db:"category"`
	Source         string          `json:"source" db:"source"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
	ArchivedAt     *time.Time      `json:"archived_at,omitempty" db:"archived_at"`
}

// IsActive returns true when the goal is neither completed nor archived.
func (g *UserGoal) IsActive() bool {
	return g.CompletedAt == nil && g.ArchivedAt == nil
}

// IsCompleted returns true when the goal has reached its target and is marked
// complete. Used by the goal_progress worker for step-advance checks.
func (g *UserGoal) IsCompleted() bool {
	if g.CompletedAt != nil {
		return true
	}
	if g.TargetAmount.IsPositive() && g.CurrentAmount.GreaterThanOrEqual(g.TargetAmount) {
		return true
	}
	return false
}

// PercentComplete returns current/target as a percentage (0-100). Returns 0
// when target is zero (defensive — should be guarded by a CHECK constraint
// upstream but we never want to divide by zero in code).
func (g *UserGoal) PercentComplete() decimal.Decimal {
	if g.TargetAmount.IsZero() {
		return decimal.Zero
	}
	pct := g.CurrentAmount.Div(g.TargetAmount).Mul(decimal.NewFromInt(100))
	if pct.GreaterThan(decimal.NewFromInt(100)) {
		return decimal.NewFromInt(100)
	}
	return pct
}

// UserGoalProgressEvent is a single audit row for what happened to a goal.
// Append-only; never updated. Payload is a small JSONB blob for event-specific
// metadata (e.g., previous_amount on progress_updated, new_step on step_advanced).
type UserGoalProgressEvent struct {
	ID            uuid.UUID       `json:"id" db:"id"`
	UserID        uuid.UUID       `json:"user_id" db:"user_id"`
	GoalID        uuid.UUID       `json:"goal_id" db:"goal_id"`
	Kind          string          `json:"kind" db:"kind"`
	Pct           *decimal.Decimal `json:"pct,omitempty" db:"pct"`
	CurrentAmount *decimal.Decimal `json:"current_amount,omitempty" db:"current_amount"`
	TargetAmount  *decimal.Decimal `json:"target_amount,omitempty" db:"target_amount"`
	Payload       json.RawMessage `json:"payload,omitempty" db:"payload"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
}
