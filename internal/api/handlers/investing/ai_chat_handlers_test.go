package investing

import (
	"strings"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

func TestFriendlyCeilingMessage_Daily(t *testing.T) {
	msg := friendlyCeilingMessage(&ai.ExceededError{Scope: "daily", LimitUSD: 0.10, SpentUSD: 0.11})
	if !strings.Contains(msg, "today") && !strings.Contains(msg, "midnight") {
		t.Fatalf("daily ceiling message should mention today/midnight, got: %q", msg)
	}
}

func TestFriendlyCeilingMessage_Monthly(t *testing.T) {
	msg := friendlyCeilingMessage(&ai.ExceededError{Scope: "monthly", LimitUSD: 1.00, SpentUSD: 1.01})
	if !strings.Contains(msg, "month") {
		t.Fatalf("monthly ceiling message should mention month, got: %q", msg)
	}
}

func TestFriendlyCeilingMessage_NilSafe(t *testing.T) {
	msg := friendlyCeilingMessage(nil)
	if msg == "" {
		t.Fatal("nil error should still produce a fallback message")
	}
}

func TestFriendlyCeilingMessage_UnknownScope(t *testing.T) {
	msg := friendlyCeilingMessage(&ai.ExceededError{Scope: "weird", LimitUSD: 0, SpentUSD: 0})
	if msg == "" {
		t.Fatal("unknown scope should fall back, not return empty string")
	}
}

func TestNextResetUnix_DailyIsTomorrowMidnightUTC(t *testing.T) {
	reset := nextResetUnix("daily")
	got := time.Unix(reset, 0).UTC()
	now := time.Now().UTC()

	// Reset must be strictly in the future.
	if !got.After(now) {
		t.Fatalf("daily reset %s is not after now %s", got, now)
	}
	// Reset must be at 00:00:00 UTC.
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
		t.Fatalf("daily reset should be midnight UTC, got %s", got)
	}
	// Reset must be within 25 hours of now.
	if got.Sub(now) > 25*time.Hour {
		t.Fatalf("daily reset too far away: %s", got.Sub(now))
	}
}

func TestNextResetUnix_MonthlyIsFirstOfNextMonth(t *testing.T) {
	reset := nextResetUnix("monthly")
	got := time.Unix(reset, 0).UTC()

	if got.Day() != 1 || got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
		t.Fatalf("monthly reset should be 1st of next month at 00:00:00 UTC, got %s", got)
	}
}

func TestNextResetUnix_UnknownScopeFallsBackToOneHour(t *testing.T) {
	reset := nextResetUnix("mystery")
	got := time.Unix(reset, 0).UTC()
	now := time.Now().UTC()

	delta := got.Sub(now)
	if delta < 30*time.Minute || delta > 90*time.Minute {
		t.Fatalf("unknown-scope fallback should be ~1h, got %s", delta)
	}
}
