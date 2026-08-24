package app

import (
	"errors"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/cache"
)

// Transient renewal failures (pool timeout, network blip) inside one lease TTL
// must keep the fleet running — tearing down on the first error caused the
// production flap loop where ~30 workers restarted every ~30s.
func TestShouldStandDownAfterRenewFailure(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-5 * time.Second)
	stale := now.Add(-workerLeaderTTL - time.Second)

	poolTimeout := errors.New("redis: connection pool timeout")

	tests := []struct {
		name        string
		err         error
		lastRenewed time.Time
		want        bool
	}{
		{"definitive loss demotes immediately", cache.ErrNotLeader, fresh, true},
		{"wrapped not-leader demotes immediately", errors.Join(poolTimeout, cache.ErrNotLeader), stale, true},
		{"transient within TTL tolerated", poolTimeout, fresh, false},
		{"transient past TTL stands down", poolTimeout, stale, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldStandDownAfterRenewFailure(tt.err, tt.lastRenewed, now); got != tt.want {
				t.Fatalf("shouldStandDownAfterRenewFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}
