package ai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/go-redis/redis/v8"
)

// fakeRedis is an in-memory cache.RedisClient with TTL honoring: an entry whose
// TTL has elapsed reads as a miss (go-redis would return nil).
type fakeRedis struct {
	mu   sync.Mutex
	data map[string]*fakeEntry
}

type fakeEntry struct {
	value string
	until time.Time
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{data: map[string]*fakeEntry{}}
}

func (f *fakeRedis) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := value.([]byte); ok {
		f.data[key] = &fakeEntry{value: string(b), until: time.Now().Add(expiration)}
		return nil
	}
	f.data[key] = &fakeEntry{value: value.(string), until: time.Now().Add(expiration)}
	return nil
}

func (f *fakeRedis) Get(ctx context.Context, key string, dest interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.data[key]
	if !ok || time.Now().After(e.until) {
		return redisNilError{}
	}
	switch d := dest.(type) {
	case *string:
		*d = e.value
	default:
		return nil
	}
	return nil
}

func (f *fakeRedis) Del(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeRedis) Exists(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.data[key]
	return ok && time.Now().Before(e.until), nil
}

func (f *fakeRedis) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) (bool, error) {
	ok, _ := f.Exists(ctx, key)
	if ok {
		return false, nil
	}
	return true, f.Set(ctx, key, value, expiration)
}

func (f *fakeRedis) Incr(ctx context.Context, key string) (int64, error) { return 1, nil }
func (f *fakeRedis) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	return value, nil
}
func (f *fakeRedis) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return nil
}
func (f *fakeRedis) Keys(ctx context.Context, pattern string) ([]string, error) { return nil, nil }
func (f *fakeRedis) Ping(ctx context.Context) error                              { return nil }
func (f *fakeRedis) Close() error                                                { return nil }

func (f *fakeRedis) Client() *redis.Client { return nil }

type redisNilError struct{}

func (redisNilError) Error() string { return "redis: nil" }

var _ cache.RedisClient = (*fakeRedis)(nil)

func TestOtpStoreCreateAndVerifyRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), time.Minute, 3, nil)
	convID := uuid.New()

	code, err := store.Create(ctx, convID, "send_money", map[string]interface{}{"amount": "100.00"}, "Send $100 to Ada")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d, want 6", len(code))
	}

	if !store.DryPeek(ctx, convID) {
		t.Fatal("DryPeek: expected pending confirmation")
	}

	action, err := store.Verify(ctx, convID, code)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if action.Tool != "send_money" {
		t.Fatalf("Verify tool = %q, want send_money", action.Tool)
	}
	if store.DryPeek(ctx, convID) {
		t.Fatal("Verify: expected entry to be consumed after success")
	}
}

func TestOtpStoreWrongCodeThenExactMatch(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), time.Minute, 3, nil)
	convID := uuid.New()

	code, err := store.Create(ctx, convID, "split_bill", nil, "Split bill")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}

	if _, err := store.Verify(ctx, convID, wrong); err == nil {
		t.Fatal("expected error for wrong code")
	}
	action, err := store.Verify(ctx, convID, code)
	if err != nil {
		t.Fatalf("Verify after retry: %v", err)
	}
	if action.Tool != "split_bill" {
		t.Fatalf("Verify tool = %q, want split_bill", action.Tool)
	}
}

func TestOtpStoreExhaustsAttempts(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), time.Minute, 2, nil)
	convID := uuid.New()

	if _, err := store.Create(ctx, convID, "pay_bill", nil, "Pay bill"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Verify(ctx, convID, "000000"); err == nil {
		t.Fatal("expected first wrong-code error")
	}
	if _, err := store.Verify(ctx, convID, "111111"); err == nil {
		t.Fatal("expected second wrong-code error")
	}
	if _, err := store.Verify(ctx, convID, "222222"); err == nil {
		t.Fatal("expected exhausted-attempts error")
	}
	if store.DryPeek(ctx, convID) {
		t.Fatal("expected entry to be deleted after exhausted attempts")
	}
}

func TestOtpStoreExpiredEntry(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), 1*time.Millisecond, 3, nil)
	convID := uuid.New()

	code, err := store.Create(ctx, convID, "send_money", nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	if _, err := store.Verify(ctx, convID, code); err == nil {
		t.Fatal("expected expired-code error")
	}
	if store.DryPeek(ctx, convID) {
		t.Fatal("expected entry to be cleared after expiry")
	}
}

func TestOtpStoreDeleteClearsConfirmation(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), time.Minute, 3, nil)
	convID := uuid.New()

	if _, err := store.Create(ctx, convID, "send_money", nil, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	store.Delete(ctx, convID)
	if store.DryPeek(ctx, convID) {
		t.Fatal("expected entry to be gone after Delete")
	}
}

func TestOtpStoreScopedPerConversation(t *testing.T) {
	ctx := context.Background()
	store := NewOtpStore(newFakeRedis(), time.Minute, 3, nil)
	convA, convB := uuid.New(), uuid.New()

	code, err := store.Create(ctx, convA, "send_money", nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if store.DryPeek(ctx, convB) {
		t.Fatal("conversation B must not see conversation A's OTP")
	}
	if _, err := store.Verify(ctx, convB, code); err == nil {
		t.Fatal("verifying against a different conversation must fail")
	}
}