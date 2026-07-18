package ai

import (
	"testing"
	"time"
)

func refNow() time.Time {
	// Wednesday, 2025-06-11 12:00 UTC
	return time.Date(2025, 6, 11, 12, 0, 0, 0, time.UTC)
}

func TestParseTimeframe(t *testing.T) {
	now := refNow()
	tests := []struct {
		msg       string
		wantSince int64
		wantUntil int64
		wantBias  bool
	}{
		{"how much did I spend last month", time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC).Unix(), time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).Unix(), true},
		{"what about this month", time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).Unix(), 0, true},
		{"my spending this year", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), 0, true},
		{"what did I buy today", time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC).Unix(), 0, true},
		{"tell me about my goals", 0, 0, false},
	}
	for _, tc := range tests {
		got := parseTimeframe(tc.msg, now)
		if got.sinceUnix != tc.wantSince || got.untilUnix != tc.wantUntil || got.recencyBias != tc.wantBias {
			t.Errorf("parseTimeframe(%q) = %+v, want since=%d until=%d bias=%v", tc.msg, got, tc.wantSince, tc.wantUntil, tc.wantBias)
		}
	}
}

func TestRankMemoriesByRecency_WindowBoost(t *testing.T) {
	now := refNow()
	tf := parseTimeframe("what did I spend last month", now) // May 2025
	may := time.Date(2025, 5, 15, 0, 0, 0, 0, time.UTC).Unix()
	jan := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC).Unix()

	results := []SupermemoryResult{
		{Memory: "Spent NGN 5000 on food in January", Similarity: 0.80, EventUnix: jan},
		{Memory: "Spent NGN 9000 on food in May", Similarity: 0.70, EventUnix: may},
	}
	ranked := rankMemoriesByRecency(results, tf, now, 0.5)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked, got %d", len(ranked))
	}
	// May is in-window (+0.30 => 1.00) and should outrank higher-similarity January (0.80-0.15=0.65).
	if ranked[0] != "Spent NGN 9000 on food in May" {
		t.Fatalf("expected in-window May memory first, got %q", ranked[0])
	}
}

func TestRankMemoriesByRecency_FiltersLowSimilarityAndDedupes(t *testing.T) {
	now := refNow()
	tf := timeframe{}
	results := []SupermemoryResult{
		{Memory: "Likes jollof rice", Similarity: 0.9},
		{Memory: "Likes jollof rice", Similarity: 0.85}, // duplicate
		{Memory: "Noise", Similarity: 0.2},              // below threshold
	}
	ranked := rankMemoriesByRecency(results, tf, now, 0.5)
	if len(ranked) != 1 || ranked[0] != "Likes jollof rice" {
		t.Fatalf("expected single deduped memory, got %v", ranked)
	}
}

func TestDedupePersonalMemory(t *testing.T) {
	memorySlot := "[MIRIAM'S MEMORY]\n- Goals: wants to save for a car\n"
	personal := personalMemoryPrefix + "wants to save for a car | worried about rent going up" + personalMemorySuffix

	got := dedupePersonalMemory(memorySlot, personal)
	// The car goal is already in the memory slot; only the rent worry should remain.
	if got == "" {
		t.Fatal("expected remaining distinct clause, got empty")
	}
	if contains(got, "save for a car") {
		t.Fatalf("expected car goal removed as duplicate, got %q", got)
	}
	if !contains(got, "worried about rent") {
		t.Fatalf("expected rent worry preserved, got %q", got)
	}

	// When everything overlaps, the slot collapses to empty.
	personalAllDup := personalMemoryPrefix + "wants to save for a car" + personalMemorySuffix
	if got := dedupePersonalMemory(memorySlot, personalAllDup); got != "" {
		t.Fatalf("expected empty when fully duplicated, got %q", got)
	}
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
