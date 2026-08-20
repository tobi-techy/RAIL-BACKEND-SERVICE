package spending_coach

import (
	"testing"
	"time"
)

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return tt
}
