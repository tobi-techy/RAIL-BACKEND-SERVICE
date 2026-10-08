package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// countingHook records how many commands actually reached the client. It is
// added AFTER the breaker, and go-redis runs BeforeProcess hooks in add order,
// so a short-circuited command never reaches this hook.
type countingHook struct{ attempts *int32 }

func (h countingHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	atomic.AddInt32(h.attempts, 1)
	return ctx, nil
}

func (h countingHook) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (h countingHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	atomic.AddInt32(h.attempts, 1)
	return ctx, nil
}

func (h countingHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func newBreakerTestClient(t *testing.T, cooldown time.Duration) (*redis.Client, *miniredis.Miniredis, *int32) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: 0})
	t.Cleanup(func() { _ = rdb.Close() })

	var attempts int32
	rdb.AddHook(newRedisBreakerWithCooldown(zap.NewNop(), cooldown))
	rdb.AddHook(countingHook{attempts: &attempts})
	return rdb, mr, &attempts
}

// The point of the breaker: once Redis is failing, requests stop paying a
// round-trip (and a log line) per command. Without it, an Upstash quota breach
// made every API request issue several doomed commands.
func TestRedisBreaker_OpensAndStopsReachingRedis(t *testing.T) {
	cooldown := time.Hour // long enough that the test never re-probes
	rdb, mr, attempts := newBreakerTestClient(t, cooldown)
	ctx := context.Background()

	require.NoError(t, rdb.Set(ctx, "k", "v", time.Minute).Err())
	require.Equal(t, int32(1), atomic.LoadInt32(attempts))

	mr.SetError("ERR max requests limit exceeded. Limit: 500000, Usage: 500000")

	// Threshold-1 failures do not trip it, and each still reaches Redis.
	for i := 0; i < redisBreakerFailureThreshold-1; i++ {
		require.Error(t, rdb.Get(ctx, "k").Err())
	}
	require.Equal(t, int32(redisBreakerFailureThreshold), atomic.LoadInt32(attempts))

	// The threshold failure opens the breaker.
	require.Error(t, rdb.Get(ctx, "k").Err())
	require.Equal(t, int32(redisBreakerFailureThreshold+1), atomic.LoadInt32(attempts))

	// Everything after that is short-circuited without touching the client.
	for i := 0; i < 10; i++ {
		err := rdb.Get(ctx, "k").Err()
		require.ErrorIs(t, err, ErrRedisUnavailable)
	}
	require.Equal(t, int32(redisBreakerFailureThreshold+1), atomic.LoadInt32(attempts),
		"commands must not reach Redis while the breaker is open")
}

func TestRedisBreaker_NilReplyIsNotAFailure(t *testing.T) {
	rdb, _, attempts := newBreakerTestClient(t, time.Hour)
	ctx := context.Background()

	for i := 0; i < redisBreakerFailureThreshold*3; i++ {
		require.ErrorIs(t, rdb.Get(ctx, "missing").Err(), redis.Nil)
	}
	// Every miss still reached Redis — a cache miss is not an outage.
	require.Equal(t, int32(redisBreakerFailureThreshold*3), atomic.LoadInt32(attempts))
	require.NoError(t, rdb.Set(ctx, "k", "v", time.Minute).Err())
}

func TestRedisBreaker_ClosesAfterASuccessfulProbe(t *testing.T) {
	cooldown := 20 * time.Millisecond
	rdb, mr, _ := newBreakerTestClient(t, cooldown)
	ctx := context.Background()

	mr.SetError("ERR max requests limit exceeded")
	for i := 0; i < redisBreakerFailureThreshold+1; i++ {
		require.Error(t, rdb.Get(ctx, "k").Err())
	}
	require.ErrorIs(t, rdb.Get(ctx, "k").Err(), ErrRedisUnavailable)

	mr.SetError("")

	// While the cooldown is still running it stays open.
	require.ErrorIs(t, rdb.Get(ctx, "k").Err(), ErrRedisUnavailable)

	time.Sleep(cooldown + 20*time.Millisecond)

	// The probe succeeds and traffic flows again.
	require.NoError(t, rdb.Set(ctx, "k", "v", time.Minute).Err())
	require.NoError(t, rdb.Ping(ctx).Err())
}

func TestRedisBreaker_FailedProbeKeepsItOpen(t *testing.T) {
	cooldown := 20 * time.Millisecond
	rdb, mr, _ := newBreakerTestClient(t, cooldown)
	ctx := context.Background()

	mr.SetError("ERR max requests limit exceeded")
	for i := 0; i < redisBreakerFailureThreshold+1; i++ {
		require.Error(t, rdb.Get(ctx, "k").Err())
	}

	time.Sleep(cooldown + 20*time.Millisecond)

	// The probe fails (Redis is still erroring), so the breaker re-opens.
	require.Error(t, rdb.Get(ctx, "k").Err())
	require.ErrorIs(t, rdb.Get(ctx, "k").Err(), ErrRedisUnavailable)
}

func TestRedisBreaker_SingleCommandErrorDoesNotOpenIt(t *testing.T) {
	rdb, _, _ := newBreakerTestClient(t, time.Hour)
	ctx := context.Background()

	// A per-command rejection must not black-hole Redis for the process.
	require.Error(t, rdb.Do(ctx, "NOTACOMMAND").Err())
	require.NoError(t, rdb.Set(ctx, "k", "v", time.Minute).Err())
}

// A pipeline must be short-circuited as a unit and must not crash the breaker.
func TestRedisBreaker_Pipeline(t *testing.T) {
	rdb, mr, _ := newBreakerTestClient(t, time.Hour)
	ctx := context.Background()

	pipe := rdb.Pipeline()
	pipe.Set(ctx, "a", "1", time.Minute)
	pipe.Set(ctx, "b", "2", time.Minute)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	mr.SetError("ERR max requests limit exceeded")
	for i := 0; i < redisBreakerFailureThreshold+1; i++ {
		p := rdb.Pipeline()
		p.Get(ctx, "a")
		_, _ = p.Exec(ctx)
	}

	p := rdb.Pipeline()
	p.Get(ctx, "a")
	_, err = p.Exec(ctx)
	require.True(t, errors.Is(err, ErrRedisUnavailable), "pipeline error = %v", err)
}
