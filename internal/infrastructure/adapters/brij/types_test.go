package brij

import "testing"

func TestFormatAtomicAmount(t *testing.T) {
	cases := []struct {
		name   string
		atomic int64
		want   string
	}{
		{"positive", 67_600_000, "67.600000"},
		{"negative", -1_500_000, "-1.500000"},
		{"zero", 0, "0.000000"},
		{"sub-unit", 100_000, "0.100000"},
		{"negative sub-unit", -100_000, "-0.100000"},
		{"negative whole", -2_000_000_000, "-2000.000000"},
		{"fractional units", 1_234_567, "1.234567"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAtomicAmount(tc.atomic); got != tc.want {
				t.Errorf("formatAtomicAmount(%d) = %q, want %q", tc.atomic, got, tc.want)
			}
		})
	}
}
