package middleware

import (
	"testing"
	"time"
)

// During a Redis outage the auth middlewares log once per request, which buries
// the signal under thousands of identical lines. The limiter must keep the first
// line and report how many it swallowed, not drop the information.
func TestRepeatLogLimiter_SuppressesWithinWindowAndReportsTheCount(t *testing.T) {
	limiter := newRepeatLogLimiter(time.Minute)
	now := time.Now()

	allow, suppressed := limiter.allow(now)
	if !allow || suppressed != 0 {
		t.Fatalf("first occurrence must log (allow=%v suppressed=%d)", allow, suppressed)
	}

	for i := 0; i < 5; i++ {
		allow, suppressed = limiter.allow(now.Add(time.Duration(i) * time.Second))
		if allow {
			t.Fatalf("occurrence %d inside the window must not log", i+1)
		}
		if suppressed != 0 {
			t.Fatalf("a suppressed line must not itself report a count, got %d", suppressed)
		}
	}

	// The next logged line carries the number of lines that were dropped.
	allow, suppressed = limiter.allow(now.Add(time.Minute))
	if !allow {
		t.Fatal("the first occurrence after the window must log")
	}
	if suppressed != 5 {
		t.Fatalf("suppressed_since_last_log = %d, want 5", suppressed)
	}

	// And the counter resets for the new window.
	allow, suppressed = limiter.allow(now.Add(time.Minute + time.Second))
	if allow || suppressed != 0 {
		t.Fatalf("expected suppression in the new window (allow=%v suppressed=%d)", allow, suppressed)
	}
}

func TestRepeatLogLimiter_NilIsSafe(t *testing.T) {
	var limiter *repeatLogLimiter
	allow, suppressed := limiter.allow(time.Now())
	if !allow || suppressed != 0 {
		t.Fatalf("a nil limiter must always log (allow=%v suppressed=%d)", allow, suppressed)
	}
}
