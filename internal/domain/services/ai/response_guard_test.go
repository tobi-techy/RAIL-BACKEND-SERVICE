package ai

import (
	"strings"
	"testing"
)

func TestGuardResponse_StripsUngroundedAmounts(t *testing.T) {
	grounding := `{"spend_balance":"2600.00","stash_balance":"1200.00"}`
	// $9,999 is nowhere in the grounding — it must be dropped. $2,600 is grounded.
	in := "You've got $2,600 in spend. Also you spent $9,999 at a casino last night."
	out := GuardResponse(in, grounding, "")
	if guardContains(out, "9,999") {
		t.Fatalf("ungrounded amount survived: %q", out)
	}
	if !guardContains(out, "2,600") {
		t.Fatalf("grounded amount was stripped: %q", out)
	}
}

func TestGuardResponse_KeepsGroundedDerivations(t *testing.T) {
	grounding := `{"spend_balance":"2600.00"}`
	// Half of 2600 = 1300, a legitimate derived suggestion.
	in := "Move half — that's $1,300 — into your stash?"
	out := GuardResponse(in, grounding, "")
	if !guardContains(out, "1,300") {
		t.Fatalf("grounded derivation (half) was stripped: %q", out)
	}
}

func TestGuardResponse_NoGroundingLeavesContentAlone(t *testing.T) {
	in := "You spent $9,999 somewhere."
	out := GuardResponse(in, "", "")
	if out != in {
		t.Fatalf("with no grounding corpus the reply must be untouched, got %q", out)
	}
}

func TestGuardResponse_SurfacesMissedAnomaly(t *testing.T) {
	anomalies := "[ANOMALIES DETECTED]\n[HIGH] Duplicate charge — Two $60 charges at MTN within 48h"
	in := "Your balance looks healthy this week."
	out := GuardResponse(in, "", anomalies)
	if !guardContains(out, "flagging") {
		t.Fatalf("missed anomaly was not surfaced: %q", out)
	}
}

func TestGuardResponse_DoesNotDoubleSurfaceAnomaly(t *testing.T) {
	anomalies := "[HIGH] Duplicate charge — Two $60 charges at MTN"
	in := "Heads up — I noticed a duplicate charge you might want to check."
	out := GuardResponse(in, "", anomalies)
	if countSubstr(out, "duplicate") > 1 {
		t.Fatalf("anomaly double-surfaced: %q", out)
	}
}

func TestGuardResponse_StripsSlopPrefixAndMarkdown(t *testing.T) {
	in := "Great question! Your **spend** balance is solid."
	out := GuardResponse(in, "", "")
	if guardContains(out, "Great question") {
		t.Fatalf("slop prefix survived: %q", out)
	}
	if guardContains(out, "**") {
		t.Fatalf("markdown survived: %q", out)
	}
}

func TestGuardResponse_EmptyStaysEmpty(t *testing.T) {
	if out := GuardResponse("", "x", "y"); out != "" {
		t.Fatalf("empty content should stay empty, got %q", out)
	}
}

func guardContains(haystack, needle string) bool {
	return strings.Count(haystack, needle) > 0
}

func countSubstr(s, sub string) int {
	return strings.Count(s, sub)
}
