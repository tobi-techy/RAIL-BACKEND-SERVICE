package ledger

import (
	"testing"
	"time"
)

// TestLagosBucketDateUsesLagosCivilDateAtUTCMidnight pins the bucket key to the
// Lagos calendar day. Lagos midnight converted to UTC encodes as the previous
// day, which split one Lagos business day across two velocity buckets.
func TestLagosBucketDateUsesLagosCivilDateAtUTCMidnight(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			// 23:30 UTC on Oct 6 is 00:30 WAT on Oct 7 — already the next Lagos day.
			name: "africa_lagos_is_ahead_of_utc",
			now:  time.Date(2026, 10, 6, 23, 30, 0, 0, time.UTC),
			want: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
		},
		{
			// 22:30 UTC is 23:30 WAT on the same Lagos day.
			name: "same_lagos_day",
			now:  time.Date(2026, 10, 6, 22, 30, 0, 0, time.UTC),
			want: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lagosBucketDate(tc.now)
			if !got.Equal(tc.want) {
				t.Fatalf("lagosBucketDate(%s) = %s, want %s", tc.now, got, tc.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("lagosBucketDate(%s) location = %s, want UTC", tc.now, got.Location())
			}
		})
	}
}
