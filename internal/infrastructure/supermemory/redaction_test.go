package supermemory

import "testing"

func TestRedactPII_PreservesFinancialSignal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain amount kept", "On Jan 2, you spent NGN 25000 on Groceries", "On Jan 2, you spent NGN 25000 on Groceries"},
		{"large amount kept", "average monthly spending is NGN 50000000", "average monthly spending is NGN 50000000"},
		{"compact date kept", "event 20250115 recorded", "event 20250115 recorded"},
		{"unix ts kept", "event_ts 1704067200 noted", "event_ts 1704067200 noted"},
		{"comma amount kept", "spent NGN 1,250,000 today", "spent NGN 1,250,000 today"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactPII(c.in); got != c.want {
				t.Fatalf("RedactPII(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactPII_StripsSensitive(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantHidden string // substring that must NOT appear
		wantTag    string // placeholder that must appear
	}{
		{"email", "reach me at jane.doe@example.com please", "jane.doe@example.com", "[email redacted]"},
		{"card", "card 4111 1111 1111 1111 used", "4111 1111 1111 1111", "[card redacted]"},
		{"card nospace", "card 4111111111111111 used", "4111111111111111", "[card redacted]"},
		{"ssn", "ssn 123-45-6789 on file", "123-45-6789", "[ssn redacted]"},
		{"labelled account", "transfer to account no 0123456789 done", "0123456789", "[account redacted]"},
		{"ng mobile", "call 08031234567 now", "08031234567", "[phone redacted]"},
		{"intl phone", "call +1 415-555-1234 now", "415-555-1234", "[phone redacted]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactPII(c.in)
			if contains(got, c.wantHidden) {
				t.Fatalf("RedactPII(%q) = %q, still contains %q", c.in, got, c.wantHidden)
			}
			if !contains(got, c.wantTag) {
				t.Fatalf("RedactPII(%q) = %q, missing %q", c.in, got, c.wantTag)
			}
		})
	}
}

func TestEventUnixFromTemporal(t *testing.T) {
	if _, ok := eventUnixFromTemporal(nil); ok {
		t.Fatal("nil temporal context should yield no timestamp")
	}
	ts, ok := eventUnixFromTemporal(&TemporalContext{EventDate: []string{"2025-01-15"}})
	if !ok {
		t.Fatal("expected timestamp from event date")
	}
	// 2025-01-15T00:00:00Z
	if ts != 1736899200 {
		t.Fatalf("unexpected unix ts: %d", ts)
	}
	ts2, ok := eventUnixFromTemporal(&TemporalContext{DocumentDate: "2025-01-15"})
	if !ok || ts2 != ts {
		t.Fatalf("document date fallback mismatch: %d vs %d", ts2, ts)
	}
	if _, ok := eventUnixFromTemporal(&TemporalContext{EventDate: []string{"not-a-date"}}); ok {
		t.Fatal("invalid date should yield no timestamp")
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
