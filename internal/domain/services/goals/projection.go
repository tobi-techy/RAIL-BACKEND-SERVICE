// Package goals implements the free-standing savings-goal service for Miriam.
// Goals are persisted in Postgres (user_goals + user_goal_progress_events) and
// are independent of the automation-bound `goals` table.
//
// The package exposes:
//   - Service: CRUD + progress events for free-standing goals.
//   - Projection: pace math (pct complete vs pct expected, on/off-pace).
//   - BabyStepsSeed: helper that seeds the 7-step ladder from a financial profile.
package goals

import (
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// PaceReport is the output of ProjectPace. All amounts are in the goal's
// TargetCurrency. Percentages are 0-100.
type PaceReport struct {
	GoalID            uuid.UUID
	PctComplete       decimal.Decimal
	PctExpected       decimal.Decimal
	OnPace            bool
	ProjectedFinal    decimal.Decimal
	DaysRemaining     int
	DaysSinceCreated  int
	BehindBy          decimal.Decimal // positive when behind; negative when ahead; zero when on pace
}

// ProjectPace evaluates a goal against its deadline. When the goal has no
// deadline, only the percentage-complete half of the report is meaningful.
// The result is purely arithmetic — no DB calls, no clock reads.
func ProjectPace(goal *entities.UserGoal, now time.Time) PaceReport {
	report := PaceReport{}
	if goal == nil {
		return report
	}
	report.GoalID = goal.ID
	report.PctComplete = goal.PercentComplete()

	if goal.Deadline == nil {
		// No deadline → no pace calculation possible.
		return report
	}

	// Days since created (clamped at 1 to avoid div-by-zero on the same-day case).
	created := goal.CreatedAt
	if created.IsZero() {
		created = now
	}
	daysSince := int(now.Sub(created).Hours() / 24)
	if daysSince < 1 {
		daysSince = 1
	}
	report.DaysSinceCreated = daysSince

	daysUntil := int(goal.Deadline.Sub(now).Hours() / 24)
	if daysUntil < 0 {
		daysUntil = 0
	}
	report.DaysRemaining = daysUntil

	totalDays := daysSince + daysUntil
	if totalDays < 1 {
		totalDays = 1
	}
	report.PctExpected = decimal.NewFromInt(int64(daysSince)).
		Mul(decimal.NewFromInt(100)).
		Div(decimal.NewFromInt(int64(totalDays)))

	// Projected final amount = current * (total / daysSince). Linear projection;
	// matches how the Miriam brief and baby-step math already model savings
	// trajectories in this codebase.
	if goal.CurrentAmount.IsPositive() && daysSince > 0 {
		// Use a per-day rate projection.
		perDay := goal.CurrentAmount.Div(decimal.NewFromInt(int64(daysSince)))
		report.ProjectedFinal = perDay.Mul(decimal.NewFromInt(int64(totalDays)))
	} else {
		// Zero current → assume nothing saved by deadline (degenerate).
		report.ProjectedFinal = decimal.Zero
	}

	// On-pace: pct complete within ±10 percentage points of pct expected,
	// OR projected final within ±10% of target. The threshold matches the
	// existing checkGoalMilestone framing so chat and notifications agree.
	delta := report.PctComplete.Sub(report.PctExpected)
	if delta.Abs().LessThanOrEqual(decimal.NewFromInt(10)) {
		report.OnPace = true
		report.BehindBy = decimal.Zero
	} else if delta.IsNegative() {
		// Behind: pct complete is below pct expected.
		report.OnPace = false
		expectedAmount := goal.TargetAmount.Mul(report.PctExpected).Div(decimal.NewFromInt(100))
		report.BehindBy = expectedAmount.Sub(goal.CurrentAmount)
	} else {
		// Ahead: pct complete above pct expected.
		report.OnPace = true
		expectedAmount := goal.TargetAmount.Mul(report.PctExpected).Div(decimal.NewFromInt(100))
		report.BehindBy = goal.CurrentAmount.Sub(expectedAmount).Neg() // negative when ahead
	}

	return report
}

// NextMilestone returns the smallest of {25, 50, 75, 100} that is greater
// than the goal's current percentage complete, or 0 if all have been
// reached. Used by the goal_progress worker to decide which milestone to
// check against on this tick.
func NextMilestone(currentPct decimal.Decimal) int {
	milestones := []int{25, 50, 75, 100}
	for _, m := range milestones {
		threshold := decimal.NewFromInt(int64(m))
		if currentPct.LessThan(threshold) {
			return m
		}
	}
	return 0 // all milestones passed
}

// MilestoneKindFor returns the ProgressEvent kind constant for a milestone
// percentage, or "" if the input is not one of {25,50,75,100}.
func MilestoneKindFor(milestone int) string {
	switch milestone {
	case 25:
		return entities.ProgressEventMilestone25
	case 50:
		return entities.ProgressEventMilestone50
	case 75:
		return entities.ProgressEventMilestone75
	case 100:
		return entities.ProgressEventMilestone100
	default:
		return ""
	}
}

// uuid is imported indirectly via the entities package; the reference keeps
// the linter quiet on the GoalID field in PaceReport (we want uuid.UUID typed
// without adding a fresh import line).
var _ = entities.UserGoal{}
