package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const defaultLeaderKey = "rail:worker-leader"

// ErrNotLeader is returned when this process no longer holds the lock.
var ErrNotLeader = errors.New("not worker leader")

// LeaderLock is a Redis SET NX lock so only one API replica runs background
// workers. HTTP stays up on every replica.
type LeaderLock struct {
	rdb   *redis.Client
	key   string
	token string
	ttl   time.Duration
}

// NewLeaderLock builds a lock. ttl must be longer than the renew interval.
func NewLeaderLock(rdb *redis.Client, ttl time.Duration) *LeaderLock {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &LeaderLock{
		rdb:   rdb,
		key:   defaultLeaderKey,
		token: uuid.NewString(),
		ttl:   ttl,
	}
}

// TryAcquire claims the lock. Returns true if this process is now leader.
func (l *LeaderLock) TryAcquire(ctx context.Context) (bool, error) {
	if l == nil || l.rdb == nil {
		return false, fmt.Errorf("leader lock: redis client is nil")
	}
	return l.rdb.SetNX(ctx, l.key, l.token, l.ttl).Result()
}

// Renew extends the lock only if we still own it.
func (l *LeaderLock) Renew(ctx context.Context) error {
	if l == nil || l.rdb == nil {
		return fmt.Errorf("leader lock: redis client is nil")
	}
	ok, err := l.rdb.Eval(ctx, `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
  return 0
end`, []string{l.key}, l.token, l.ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if ok == 0 {
		return ErrNotLeader
	}
	return nil
}

// Release drops the lock only if we still own it.
func (l *LeaderLock) Release(ctx context.Context) {
	if l == nil || l.rdb == nil {
		return
	}
	_, _ = l.rdb.Eval(ctx, `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end`, []string{l.key}, l.token).Result()
}
