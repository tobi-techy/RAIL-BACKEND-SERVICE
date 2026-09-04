package platform

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// stubRedis is a minimal in-memory cache.RedisClient for coordinator tests.
// It only implements the Incr path used by the coordinator; other methods
// return errors so accidental use shows up immediately.
type stubRedis struct {
	mu    sync.Mutex
	store map[string]int64
}

func newStubRedis() *stubRedis { return &stubRedis{store: map[string]int64{}} }

func (r *stubRedis) Incr(_ context.Context, key string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key]++
	return r.store[key], nil
}

func (r *stubRedis) Expire(_ context.Context, _ string, _ time.Duration) error { return nil }

func (r *stubRedis) Get(_ context.Context, _ string, _ interface{}) error {
	return errors.New("not implemented")
}
func (r *stubRedis) Set(_ context.Context, _ string, _ interface{}, _ time.Duration) error {
	return errors.New("not implemented")
}
func (r *stubRedis) Del(_ context.Context, _ string) error            { return errors.New("not implemented") }
func (r *stubRedis) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
func (r *stubRedis) SetNX(_ context.Context, _ string, _ interface{}, _ time.Duration) (bool, error) {
	return false, errors.New("not implemented")
}
func (r *stubRedis) IncrBy(_ context.Context, _ string, _ int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (r *stubRedis) Keys(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (r *stubRedis) Ping(_ context.Context) error                       { return nil }
func (r *stubRedis) Close() error                                       { return nil }
func (r *stubRedis) Client() *redis.Client                              { return nil }

// ensure stubRedis satisfies the interface.
var _ cache.RedisClient = (*stubRedis)(nil)

func TestProactiveCoordinator_AllowsUnderCap(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 3, nil)
	uid := uuid.New()

	for i := 0; i < 3; i++ {
		ok := c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)
		if !ok {
			t.Fatalf("call %d should be allowed (cap=3)", i+1)
		}
	}
}

func TestProactiveCoordinator_BlocksAtGlobalCap(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 2, nil)
	uid := uuid.New()

	for i := 0; i < 2; i++ {
		ok := c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)
		if !ok {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	// Third call must be blocked.
	ok := c.Allow(context.Background(), uid, ProactiveCategoryBriefing, false)
	if ok {
		t.Fatalf("third call should be blocked by global cap")
	}
}

func TestProactiveCoordinator_CriticalBypassesCap(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 1, nil)
	uid := uuid.New()

	// Saturate the cap.
	_ = c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)
	_ = c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)

	// critical=true bypasses.
	ok := c.Allow(context.Background(), uid, ProactiveCategoryNudge, true)
	if !ok {
		t.Fatalf("critical calls must bypass the global cap")
	}
}

func TestProactiveCoordinator_ReceiptAlwaysAllowed(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 1, nil)
	uid := uuid.New()

	_ = c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)
	_ = c.Allow(context.Background(), uid, ProactiveCategoryNudge, false)

	// Receipt category is critical.
	ok := c.Allow(context.Background(), uid, ProactiveCategoryReceipt, false)
	if !ok {
		t.Fatalf("receipts must always be allowed")
	}
}

func TestProactiveCoordinator_CategoryOverrideCap(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 10, map[string]int{
		ProactiveCategorySpendingCoach: 1,
	})
	uid := uuid.New()

	if !c.Allow(context.Background(), uid, ProactiveCategorySpendingCoach, false) {
		t.Fatalf("first spending_coach call should be allowed")
	}
	if c.Allow(context.Background(), uid, ProactiveCategorySpendingCoach, false) {
		t.Fatalf("second spending_coach call should be blocked by per-category override")
	}
}

func TestProactiveCoordinator_DifferentCategoriesCountSeparately(t *testing.T) {
	redis := newStubRedis()
	c := NewProactiveCoordinator(redis, zap.NewNop(), 10, nil)
	uid := uuid.New()

	for i := 0; i < 5; i++ {
		if !c.Allow(context.Background(), uid, ProactiveCategoryNudge, false) {
			t.Fatalf("nudge %d should be allowed", i+1)
		}
	}
	// A different category should still be allowed (per-category counter).
	if !c.Allow(context.Background(), uid, ProactiveCategoryBriefing, false) {
		t.Fatalf("briefing should be allowed even after 5 nudges")
	}
}

func TestProactiveCoordinator_DisabledWhenRedisNil(t *testing.T) {
	c := NewProactiveCoordinator(nil, zap.NewNop(), 1, nil)
	uid := uuid.New()
	for i := 0; i < 5; i++ {
		if !c.Allow(context.Background(), uid, ProactiveCategoryNudge, false) {
			t.Fatalf("call %d should be allowed when redis is nil (fail-open)", i+1)
		}
	}
}

func TestProactiveCoordinator_NilReceiverAllows(t *testing.T) {
	var c *ProactiveCoordinator
	if !c.Allow(context.Background(), uuid.New(), ProactiveCategoryNudge, false) {
		t.Fatalf("nil coordinator should allow (safe default)")
	}
}
