package goal_progress

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func makeGoal(category string, current, target string) *entities.UserGoal {
	g := &entities.UserGoal{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		Name:           "Visa card",
		TargetAmount:   decimal.RequireFromString(target),
		CurrentAmount:  decimal.RequireFromString(current),
		TargetCurrency: "USD",
		Category:       category,
		Source:         entities.GoalSourceManual,
		CreatedAt:      time.Now().AddDate(0, 0, -10),
	}
	return g
}

func TestMilestoneCopy_StarterEmergency(t *testing.T) {
	cases := []struct {
		milestone int
		current   string
		contains  []string
	}{
		{25, "250", []string{"$250.00", "starter"}},
		{50, "500", []string{"$500.00", "starter"}},
		{75, "750", []string{"starter"}},
		{100, "1000", []string{"snowball"}},
	}
	for _, c := range cases {
		g := makeGoal(entities.GoalCategoryStarterEmergency, c.current, "1000")
		title, body := milestoneCopy(g, c.milestone)
		assert.NotEmpty(t, title, "milestone %d should have a title", c.milestone)
		assert.NotEmpty(t, body, "milestone %d should have a body", c.milestone)
		for _, s := range c.contains {
			assert.Contains(t, body, s, "milestone %d body should contain %q", c.milestone, s)
		}
	}
}

func TestMilestoneCopy_DebtPayoff(t *testing.T) {
	g := makeGoal(entities.GoalCategoryDebtPayoff, "500", "1000")
	title, body := milestoneCopy(g, 50)
	assert.Contains(t, body, "$500.00")
	assert.Contains(t, body, "$1000.00")
	// Goal name appears in the title for debt-payoff milestones.
	assert.Contains(t, title, "Visa")
}

func TestMilestoneCopy_DebtPayoff_At100(t *testing.T) {
	g := makeGoal(entities.GoalCategoryDebtPayoff, "1000", "1000")
	title, body := milestoneCopy(g, 100)
	assert.Contains(t, title, "paid off")
	assert.Contains(t, body, "Roll")
}

func TestMilestoneCopy_Freeform(t *testing.T) {
	g := makeGoal(entities.GoalCategoryFreeform, "500", "1000")
	title, body := milestoneCopy(g, 50)
	assert.NotEmpty(t, title)
	assert.Contains(t, body, "50%")
}

func TestPaceBehindCopy_DeadlineImminent(t *testing.T) {
	g := makeGoal(entities.GoalCategoryDebtPayoff, "100", "1000")
	title, body := paceBehindCopy(g, 14)
	assert.NotEmpty(t, title)
	assert.Contains(t, body, "$100")
	assert.Contains(t, body, "$1000")
	assert.Contains(t, body, "14")
}

func TestPaceBehindCopy_DaysLeftClamped(t *testing.T) {
	g := makeGoal(entities.GoalCategoryStarterEmergency, "100", "1000")
	_, body := paceBehindCopy(g, 0)
	assert.NotContains(t, body, "0 days") // should be clamped to 1
	assert.Contains(t, body, "1 day")
}

func TestStepAdvanceCopy(t *testing.T) {
	cases := []struct {
		from, to int
		contains string
	}{
		{1, 2, "smallest debt"},
		{2, 3, "debts"},
		{3, 4, "retirement"},
		{4, 5, "college"},
		{5, 6, "home"},
		{6, 7, "wealth"},
		{7, 8, "Step 8"},
	}
	for _, c := range cases {
		title, body := stepAdvanceCopy(c.from, c.to)
		assert.NotEmpty(t, title, "step %d→%d title", c.from, c.to)
		assert.Contains(t, body, c.contains, "step %d→%d body should contain %q", c.from, c.to, c.contains)
	}
}
