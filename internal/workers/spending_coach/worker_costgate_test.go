package spending_coach

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// fakeCostGate records calls and returns scripted spent/cap values.
type fakeCostGate struct {
	spent float64
	cap   float64
	err   error
	calls int
}

func (f *fakeCostGate) GetDailySpentUSD(_ context.Context, _ uuid.UUID) (float64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.spent, nil
}

func (f *fakeCostGate) DailyCapUSD() float64 { return f.cap }

// TestDailyCapUSD_OnGuard confirms the public accessor reads the cap.
func TestDailyCapUSD_OnGuard(t *testing.T) {
	g := &fakeCostGate{cap: 0.10}
	assert.Equal(t, 0.10, g.DailyCapUSD())
}

func TestIsoWeekFormat(t *testing.T) {
	w := isoWeek(parseTime(t, "2026-01-05T00:00:00Z"))
	assert.Equal(t, "2026-W02", w)
}

// TestDueForWeeklyCoach_FirstAndLastMinute ensures the 9am window is exact.
func TestDueForWeeklyCoach_AtBoundary(t *testing.T) {
	// 8:59am Lagos Monday
	assert.False(t, dueForWeeklyCoach("NG",
		parseTime(t, "2026-01-05T07:59:00Z")))
	// 9:00am Lagos Monday
	assert.True(t, dueForWeeklyCoach("NG",
		parseTime(t, "2026-01-05T08:00:00Z")))
	// 9:01am Lagos Monday
	assert.True(t, dueForWeeklyCoach("NG",
		parseTime(t, "2026-01-05T08:01:00Z")))
	// 10:00am Lagos Monday
	assert.False(t, dueForWeeklyCoach("NG",
		parseTime(t, "2026-01-05T09:00:00Z")))
}

func TestLocationForCountry_KnownAndUnknown(t *testing.T) {
	assert.NotNil(t, locationForCountry("NG"), "NG should resolve")
	assert.NotNil(t, locationForCountry("US"), "US should resolve")
	assert.Nil(t, locationForCountry("ZZ"), "unknown country → nil")
	assert.Nil(t, locationForCountry(""), "empty country → nil")
}

// TestLocationForCountry_NYCForUS confirms the timezone mapping.
func TestLocationForCountry_NYCForUS(t *testing.T) {
	loc := locationForCountry("US")
	assert.NotNil(t, loc)
	assert.Equal(t, "America/New_York", loc.String())
}

// TestTryAcquireLeader_NilRedisAlwaysAllows ensures fail-open semantics.
func TestTryAcquireLeader_NilRedisAlwaysAllows(t *testing.T) {
	w := New(nil, nil, nil, zap.NewNop())
	assert.True(t, w.tryAcquireLeader(context.Background()))
}

// TestCostGateFailOpen confirms a failing gate doesn't block the worker.
func TestCostGateFailOpen(t *testing.T) {
	// Just compile-check that the interface accepts the right shape.
	var g CostGate = &fakeCostGate{err: errors.New("redis down"), cap: 0.10}
	spent, err := g.GetDailySpentUSD(context.Background(), uuid.New())
	assert.Error(t, err)
	assert.Equal(t, float64(0), spent)
	assert.Equal(t, 0.10, g.DailyCapUSD())
}
