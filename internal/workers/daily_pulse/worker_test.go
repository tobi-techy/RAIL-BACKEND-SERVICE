package daily_pulse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDueForDailyPulseUsesUserLocalTime(t *testing.T) {
	now := time.Date(2026, 5, 22, 8, 15, 0, 0, time.UTC) // 9:15am in Lagos

	localDate, ok := dueForDailyPulse("NG", now)

	require.True(t, ok)
	require.Equal(t, "2026-05-22", localDate)
}

func TestPulseFromBriefUsesTopInsight(t *testing.T) {
	brief := map[string]interface{}{
		"insights": []interface{}{
			map[string]interface{}{
				"id":       "spend-runway-low",
				"type":     "runway",
				"severity": "critical",
				"title":    "Spend balance may not last the week",
				"body":     "Spend is $42.00. At $9.00/day, that is about 5 days of runway.",
			},
		},
		"next_actions": []interface{}{
			map[string]interface{}{"id": "open-recovery-plan", "type": "recovery_plan"},
		},
	}

	title, body, data := pulseFromBrief(brief)

	require.Equal(t, "Spend balance may not last the week", title)
	require.Contains(t, body, "$42.00")
	require.Equal(t, "spend-runway-low", data["insight_id"])
	require.Equal(t, "recovery_plan", data["next_action_type"])
}
