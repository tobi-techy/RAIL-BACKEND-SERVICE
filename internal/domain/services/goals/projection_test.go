package goals

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: build a goal with the most common fields pre-filled.
func makeGoal(current, target string, deadline *time.Time, babyStep *int) *entities.UserGoal {
	g := &entities.UserGoal{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Name:           "test",
		TargetAmount:   decimal.RequireFromString(target),
		CurrentAmount:  decimal.RequireFromString(current),
		TargetCurrency: "USD",
		Deadline:       deadline,
		BabyStep:       babyStep,
		Category:       entities.GoalCategoryFreeform,
		Source:         entities.GoalSourceManual,
		CreatedAt:      time.Now().AddDate(0, 0, -30),
	}
	return g
}

func TestProjectPace_NoDeadline(t *testing.T) {
	g := makeGoal("500", "1000", nil, nil)
	report := ProjectPace(g, time.Now())
	assert.True(t, report.PctComplete.Equal(decimal.NewFromInt(50)))
	assert.False(t, report.OnPace) // no deadline → defaults to false
	assert.Equal(t, 0, report.DaysRemaining)
}

func TestProjectPace_OnPace(t *testing.T) {
	now := time.Now()
	created := now.AddDate(0, 0, -30)
	deadline := now.AddDate(0, 0, 30)
	g := makeGoal("500", "1000", &deadline, nil)
	g.CreatedAt = created
	// 30 days in, 30 days remaining. Pct complete 50%, pct expected 50% → on pace.
	report := ProjectPace(g, now)
	assert.True(t, report.OnPace, "should be on pace when pct complete == pct expected")
	assert.True(t, report.PctComplete.Equal(report.PctExpected))
}

func TestProjectPace_Behind(t *testing.T) {
	now := time.Now()
	created := now.AddDate(0, 0, -90)
	deadline := now.AddDate(0, 0, 10)
	g := makeGoal("100", "1000", &deadline, nil)
	g.CreatedAt = created
	// 90 days in, 10 days remaining. Pct complete 10%, pct expected 90% → behind.
	report := ProjectPace(g, now)
	assert.False(t, report.OnPace)
	assert.True(t, report.BehindBy.IsPositive(), "behind amount should be positive")
}

func TestProjectPace_Ahead(t *testing.T) {
	now := time.Now()
	created := now.AddDate(0, 0, -10)
	deadline := now.AddDate(0, 0, 90)
	g := makeGoal("900", "1000", &deadline, nil)
	g.CreatedAt = created
	// 10 days in, 90 days remaining. Pct complete 90%, pct expected ~10% → ahead.
	report := ProjectPace(g, now)
	assert.True(t, report.OnPace)
	assert.True(t, report.BehindBy.IsNegative(), "ahead → behind-by should be negative")
}

func TestProjectPace_ZeroCurrent(t *testing.T) {
	now := time.Now()
	deadline := now.AddDate(0, 0, 30)
	g := makeGoal("0", "1000", &deadline, nil)
	g.CreatedAt = now.AddDate(0, 0, -10)
	report := ProjectPace(g, now)
	assert.True(t, report.ProjectedFinal.IsZero())
	assert.False(t, report.OnPace)
}

func TestNextMilestone(t *testing.T) {
	cases := []struct {
		pct  string
		want int
	}{
		{"0", 25},
		{"24.99", 25},
		{"25", 50},
		{"49.99", 50},
		{"50", 75},
		{"75", 100},
		{"99.99", 100},
		{"100", 0},
		{"150", 0}, // over-fund capped at 100
	}
	for _, c := range cases {
		pct := decimal.RequireFromString(c.pct)
		got := NextMilestone(pct)
		assert.Equal(t, c.want, got, "NextMilestone(%s) = %d, want %d", c.pct, got, c.want)
	}
}

func TestMilestoneKindFor(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{25, entities.ProgressEventMilestone25},
		{50, entities.ProgressEventMilestone50},
		{75, entities.ProgressEventMilestone75},
		{100, entities.ProgressEventMilestone100},
		{0, ""},
		{33, ""},
		{200, ""},
	}
	for _, c := range cases {
		got := MilestoneKindFor(c.in)
		assert.Equal(t, c.want, got)
	}
}

func TestPercentComplete_CapsAt100(t *testing.T) {
	g := makeGoal("1500", "1000", nil, nil)
	pct := g.PercentComplete()
	require.True(t, pct.LessThanOrEqual(decimal.NewFromInt(100)))
	assert.True(t, pct.Equal(decimal.NewFromInt(100)))
}

func TestPercentComplete_ZeroTarget(t *testing.T) {
	g := makeGoal("500", "0", nil, nil)
	pct := g.PercentComplete()
	assert.True(t, pct.IsZero())
}

func TestUserGoalIsActive(t *testing.T) {
	g := makeGoal("100", "1000", nil, nil)
	assert.True(t, g.IsActive())

	now := time.Now()
	g.CompletedAt = &now
	assert.False(t, g.IsActive())

	g2 := makeGoal("100", "1000", nil, nil)
	g2.ArchivedAt = &now
	assert.False(t, g2.IsActive())
}

func TestUserGoalIsCompleted(t *testing.T) {
	g := makeGoal("100", "1000", nil, nil)
	assert.False(t, g.IsCompleted())

	g2 := makeGoal("1000", "1000", nil, nil)
	assert.True(t, g2.IsCompleted())

	g3 := makeGoal("500", "1000", nil, nil)
	now := time.Now()
	g3.CompletedAt = &now
	assert.True(t, g3.IsCompleted())
}
