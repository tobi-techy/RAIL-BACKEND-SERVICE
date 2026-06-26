package ai

import "testing"

func TestNewVoiceDailyLimiter_LimitFallback(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"zero falls back to default", 0, defaultVoiceDailyLimitUSD},
		{"negative falls back to default", -50, defaultVoiceDailyLimitUSD},
		{"explicit limit honored", 250, 250},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// redis is unused for construction; CheckAndRecord is not called.
			l := NewVoiceDailyLimiter(nil, tt.input)
			if l.limitUSD != tt.want {
				t.Fatalf("limitUSD = %v, want %v", l.limitUSD, tt.want)
			}
		})
	}
}
