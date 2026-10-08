package cache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"go.uber.org/zap"
)

// ErrRedisUnavailable is returned by commands the breaker short-circuited
// without touching the network. It is a real error, so callers still decide
// whether to fail open or closed — they just stop paying a round-trip and a log
// line for a Redis that is already known to be failing.
var ErrRedisUnavailable = errors.New("redis unavailable (circuit breaker open)")

const (
	// Consecutive failures before the breaker opens. High enough that a single
	// command-level rejection (WRONGTYPE, a bad script) cannot black-hole Redis
	// for the whole process.
	redisBreakerFailureThreshold = 5
	// How long the breaker stays open before letting a single probe through.
	redisBreakerCooldown = 15 * time.Second
	// A probe that never reports back must not wedge the breaker permanently.
	redisBreakerProbeTimeout = 30 * time.Second
)

// redisBreaker is a go-redis Hook that stops the process from hammering a Redis
// that is systematically failing.
//
// Why this exists: on 2026-10-08 an Upstash quota breach made every Redis call
// fail. Each API request then paid 3-5 commands (rate-limit script, token
// blacklist, session cache read and write) and emitted 3-5 ERROR lines, and the
// failure re-armed itself on every request — the service burned its way through
// the quota being rejected. Collapsing that to one probe per cooldown window
// bounds both the traffic and the log volume.
//
// A short-circuited command never reaches the server, so it costs no quota. The
// hook covers every command on the client it is attached to, including the raw
// client handed out by RedisClient.Client().
type redisBreaker struct {
	logger   *zap.Logger
	cooldown time.Duration

	mu             sync.Mutex
	failures       int
	open           bool
	openUntil      time.Time
	probeStartedAt time.Time
	probing        bool
}

func newRedisBreaker(logger *zap.Logger) *redisBreaker {
	return newRedisBreakerWithCooldown(logger, redisBreakerCooldown)
}

func newRedisBreakerWithCooldown(logger *zap.Logger, cooldown time.Duration) *redisBreaker {
	if cooldown <= 0 {
		cooldown = redisBreakerCooldown
	}
	return &redisBreaker{logger: logger, cooldown: cooldown}
}

// admit decides whether a command may proceed. While the breaker is open it
// rejects everything except a single probe once the cooldown has elapsed.
func (b *redisBreaker) admit() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.open {
		return nil
	}

	now := time.Now()
	if now.Before(b.openUntil) {
		return ErrRedisUnavailable
	}
	if b.probing && now.Sub(b.probeStartedAt) < redisBreakerProbeTimeout {
		return ErrRedisUnavailable
	}
	// Cooldown elapsed (or the previous probe wedged): let exactly one through.
	b.probing = true
	b.probeStartedAt = now
	return nil
}

func (b *redisBreaker) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, b.admit()
}

func (b *redisBreaker) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, b.admit()
}

func (b *redisBreaker) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	b.observe(cmd.Err())
	return nil
}

func (b *redisBreaker) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	for _, cmd := range cmds {
		if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
			b.observe(err)
			return nil
		}
	}
	b.observe(nil)
	return nil
}

func (b *redisBreaker) observe(err error) {
	if b == nil {
		return
	}
	// A short-circuited command says nothing about Redis' health.
	if errors.Is(err, ErrRedisUnavailable) {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if err == nil || errors.Is(err, redis.Nil) {
		if b.open {
			b.logger.Info("Redis recovered — circuit breaker closed")
		}
		b.failures = 0
		b.open = false
		b.probing = false
		return
	}

	if b.open {
		// A failed probe: stay open for another cooldown.
		b.openUntil = time.Now().Add(b.cooldown)
		b.probing = false
		return
	}

	b.failures++
	if b.failures < redisBreakerFailureThreshold {
		return
	}

	b.open = true
	b.probing = false
	b.openUntil = time.Now().Add(b.cooldown)
	b.logger.Error("Redis is failing — circuit breaker open, skipping Redis calls until it recovers",
		zap.Int("consecutive_failures", b.failures),
		zap.Duration("retry_after", b.cooldown),
		zap.Error(err))
}
