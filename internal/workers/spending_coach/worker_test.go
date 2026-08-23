package spending_coach

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReframeForStep(t *testing.T) {
	cases := []struct {
		step     int
		body     string
		prefixes []string
	}{
		{1, "Skip the $X", []string{"Every dollar off Spend"}},
		{2, "skip dining out", []string{"Sprint phase", "snowball"}},
		{3, "hold the line", []string{"Hold the cushion"}},
		{4, "stay invested", []string{"Long game"}},
		{5, "stay invested", []string{"Long game"}},
		{6, "knock out principal", []string{"Momentum check"}},
		{7, "compound it", []string{"Wealth update"}},
		{0, "fallback", nil}, // no prefix
	}
	for _, c := range cases {
		out := reframeForStep(c.step, c.body)
		assert.NotEmpty(t, out)
		if c.prefixes == nil {
			// 0 → no prefix; the body should pass through.
			assert.True(t, strings.HasPrefix(out, "fallback"))
			continue
		}
		for _, p := range c.prefixes {
			assert.Contains(t, out, p, "step %d should prefix with %q", c.step, p)
		}
	}
}

func TestStepTitle(t *testing.T) {
	cases := []struct {
		step int
		want string
	}{
		{1, "starter fund"},
		{2, "sprint phase"},
		{3, "cushion"},
		{4, "long-game"},
		{5, "long-game"},
		{6, "long-game"},
		{7, "long-game"},
		{0, "weekly check"},
	}
	for _, c := range cases {
		got := stepTitle(c.step)
		assert.Contains(t, got, c.want, "step %d title should contain %q", c.step, c.want)
	}
}

func TestIsoWeek(t *testing.T) {
	// 2026-01-05 is a Monday, ISO week 2.
	w := isoWeek(parseTime(t, "2026-01-05T00:00:00Z"))
	assert.Equal(t, "2026-W02", w)
}

func TestDueForWeeklyCoach_NotMonday(t *testing.T) {
	// 2026-01-06 is a Tuesday at 09:00 UTC.
	now := parseTime(t, "2026-01-06T09:00:00Z")
	assert.False(t, dueForWeeklyCoach("NG", now, now))
}

func TestDueForWeeklyCoach_MondayWrongHour(t *testing.T) {
	now := parseTime(t, "2026-01-05T10:00:00Z")
	assert.False(t, dueForWeeklyCoach("NG", now, now))
}

func TestDueForWeeklyCoach_NGMonday9am(t *testing.T) {
	now := parseTime(t, "2026-01-05T08:00:00Z") // 9am Lagos
	assert.True(t, dueForWeeklyCoach("NG", now, now))
}

func TestDueForWeeklyCoach_UnknownCountry(t *testing.T) {
	// Unknown country → UTC 9am Monday.
	now := parseTime(t, "2026-01-05T09:00:00Z")
	assert.True(t, dueForWeeklyCoach("", now, now))
}
