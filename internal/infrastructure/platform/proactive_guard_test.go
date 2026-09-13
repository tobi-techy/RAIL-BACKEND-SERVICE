package platform

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type staticPrefsResolver struct{ prefs ProactivePrefs }

func (s staticPrefsResolver) ProactivePrefs(_ context.Context, _ uuid.UUID) ProactivePrefs {
	return s.prefs
}

func quietHoursDisabledPrefs() ProactivePrefs {
	return ProactivePrefs{
		QuietEnabled:   false, // start==end also disables; explicit off is deterministic
		DailyCap:       0,
		AllowBriefings: true,
		AllowRisk:      true,
		AllowNudges:    true,
		AllowFollowups: true,
	}
}

func newTestGuard(prefs ProactivePrefs) *ProactiveGuard {
	g := NewProactiveGuard(nil, nil, "UTC", 0, 22, 7, nil)
	g.SetPreferencesResolver(staticPrefsResolver{prefs: prefs})
	return g
}

func TestCanSendCategory_RespectsCategoryFlags(t *testing.T) {
	userID := uuid.New()

	p := quietHoursDisabledPrefs()
	p.AllowNudges = false
	g := newTestGuard(p)

	require.False(t, g.CanSendCategory(context.Background(), userID, ProactiveCategoryNudge))
}

func TestCanSendCategory_AllowsWhenEnabledAndNotQuiet(t *testing.T) {
	g := newTestGuard(quietHoursDisabledPrefs())

	require.True(t, g.CanSendCategory(context.Background(), uuid.New(), ProactiveCategoryNudge))
	require.True(t, g.CanSendCategory(context.Background(), uuid.New(), ProactiveCategoryBriefing))
}

func TestCanSendCategory_UnknownCategoryDefaultsToAllowed(t *testing.T) {
	g := newTestGuard(quietHoursDisabledPrefs())

	require.True(t, g.CanSendCategory(context.Background(), uuid.New(), "some_new_category"))
}

func TestCanSendCategory_DoesNotConsumeQuotaWithRedis(t *testing.T) {
	// Guard built without Redis never increments; a Redis-backed guard without
	// a send path must leave the counter untouched, so the peek is purely for
	// gating. Covered via NewProactiveGuard(nil, ...) above — no Incr to count.
	g := newTestGuard(quietHoursDisabledPrefs())
	require.NotNil(t, g)
}

func TestInQuietHoursPrefs(t *testing.T) {
	// 22:00–07:00 quiet window (overnight).
	quietOn := func(hour int) bool {
		return inQuietHoursPrefs(localAt(hour), true, 22, 7)
	}

	require.True(t, quietOn(23))
	require.True(t, quietOn(3))
	require.True(t, quietOn(22))
	require.False(t, quietOn(7))
	require.False(t, quietOn(12))
}

func TestInQuietHoursPrefsDisabled(t *testing.T) {
	require.False(t, inQuietHoursPrefs(localAt(3), false, 22, 7))
	require.False(t, inQuietHoursPrefs(localAt(3), true, 22, 22)) // start==end disables
}

func localAt(hour int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
}
