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

// A failed leader probe must NOT be read as "we are the only replica". On
// 2026-10-08 one Upstash quota breach (every Redis command returning an error)
// made all replicas fail open, run the full ~30-worker fleet, and stampede the
// Postgres connection pool into "remaining connection slots are reserved for
// roles with the SUPERUSER attribute".
func TestDecideWorkerStart(t *testing.T) {
	redisDown := errors.New("ERR max requests limit exceeded. Limit: 500000, Usage: 500000")

	tests := []struct {
		name             string
		leaderElection   bool
		redisClientAvail bool
		won              bool
		acquireErr       error
		want             workerStartDecision
	}{
		{"election off always starts the fleet", false, true, false, redisDown, startWorkerFleet},
		{"no Redis client at all starts the fleet", true, false, false, nil, startWorkerFleet},
		{"won the lease", true, true, true, nil, startWorkerFleet},
		{"lost the lease", true, true, false, nil, stayHTTPOnly},
		{"Redis error stays HTTP-only", true, true, false, redisDown, stayHTTPOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideWorkerStart(tt.leaderElection, tt.redisClientAvail, tt.won, tt.acquireErr)
			if got != tt.want {
				t.Fatalf("decideWorkerStart() = %v, want %v", got, tt.want)
			}
		})
	}
}
