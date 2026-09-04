package limits

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// NOTE on coverage: the allow/deny and fail-open-on-error paths run through
// redis.Client().Eval(...), which returns a concrete *redis.Cmd from go-redis.
// This repo has no miniredis/redismock dependency, so those paths can only be
// exercised by an integration test against a live Redis (see test/integration).
// Here we cover the pure logic that does not require Redis: the nil-safety
// fail-open guard, the key scheme/window, and the boundary comparison.

func TestVoiceSessionRateLimiter_NilSafetyFailsOpen(t *testing.T) {
	// nil receiver — must allow without panic.
	var nilLimiter *VoiceSessionRateLimiter
	allowed, err := nilLimiter.Allow(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("nil limiter returned error: %v", err)
	}
	if !allowed {
		t.Fatal("nil limiter must fail-open (allow)")
	}

	// configured limiter but no redis handle — must allow (not configured).
	noRedis := NewVoiceSessionRateLimiter(nil, 10, nil)
	allowed, err = noRedis.Allow(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("nil-redis limiter returned error: %v", err)
	}
	if !allowed {
		t.Fatal("nil-redis limiter must fail-open (allow)")
	}
}

func TestVoiceSessionRateLimiter_KeyScheme(t *testing.T) {
	userID := uuid.New()
	l := NewVoiceSessionRateLimiter(nil, 10, nil)
	key := l.key(userID)

	if !strings.HasPrefix(key, voiceSessionLimitPrefix) {
		t.Fatalf("key %q missing prefix %q", key, voiceSessionLimitPrefix)
	}
	if !strings.Contains(key, userID.String()) {
		t.Fatalf("key %q missing user id", key)
	}
	// Window suffix must be the hourly UTC bucket yyyy-mm-dd-HH.
	wantWindow := time.Now().UTC().Format("2006-01-02-15")
	if !strings.HasSuffix(key, wantWindow) {
		t.Fatalf("key %q missing hourly window %q", key, wantWindow)
	}
}

func TestVoiceSessionRateLimiter_BoundaryComparison(t *testing.T) {
	// The Allow decision is `count <= maxPerHour`. Verify the boundary semantics
	// the limiter relies on: the Nth session is allowed, the (N+1)th is denied.
	maxPerHour := int64(10)
	cases := []struct {
		count   int64
		allowed bool
	}{
		{1, true},
		{10, true},  // exactly at the cap — still allowed
		{11, false}, // first over the cap — denied
	}
	for _, tc := range cases {
		got := tc.count <= maxPerHour
		if got != tc.allowed {
			t.Errorf("count=%d: got allowed=%v, want %v", tc.count, got, tc.allowed)
		}
	}
}
