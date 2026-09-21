package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
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
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("fakeRedis.Set: unsupported value type %T", value)
	}
	f.data[key] = &fakeEntry{value: text, until: time.Now().Add(expiration)}
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
	ok, err := f.Exists(ctx, key)
	if err != nil {
		return false, err
	}
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
func (f *fakeRedis) Ping(ctx context.Context) error                             { return nil }
func (f *fakeRedis) Close() error                                               { return nil }
func (f *fakeRedis) Client() *redis.Client                                      { return nil }

type redisNilError struct{}

func (redisNilError) Error() string { return "redis: nil" }

// ---------------------------------------------------------------------------

func TestConfirmStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Minute, nil)
	conv := uuid.New()

	if _, ok := store.Peek(ctx, conv); ok {
		t.Fatal("a conversation with nothing staged must not report a confirm")
	}
	if err := store.Put(ctx, conv, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := store.Peek(ctx, conv)
	if !ok || got != "confirm_abc" {
		t.Fatalf("want confirm_abc, got %q ok=%v", got, ok)
	}
}

func TestConfirmStoreDeleteSettlesIt(t *testing.T) {
	// A settled challenge must not be tappable twice: Delete is what the confirm
	// path calls before it answers.
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Minute, nil)
	conv := uuid.New()

	if err := store.Put(ctx, conv, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}
	store.Delete(ctx, conv)
	if _, ok := store.Peek(ctx, conv); ok {
		t.Fatal("a deleted confirm must read as absent")
	}
}

func TestConfirmStoreExpires(t *testing.T) {
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Millisecond, nil)
	conv := uuid.New()

	if err := store.Put(ctx, conv, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok := store.Peek(ctx, conv); ok {
		t.Fatal("an expired confirm must read as absent")
	}
}

func TestConfirmStoreScopedPerConversation(t *testing.T) {
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Minute, nil)
	a, b := uuid.New(), uuid.New()

	if err := store.Put(ctx, a, "confirm_a"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Put(ctx, b, "confirm_b"); err != nil {
		t.Fatalf("put: %v", err)
	}

	gotA, _ := store.Peek(ctx, a)
	gotB, _ := store.Peek(ctx, b)
	if gotA != "confirm_a" || gotB != "confirm_b" {
		t.Fatalf("conversations must not share a confirm: %q %q", gotA, gotB)
	}

	store.Delete(ctx, a)
	if _, ok := store.Peek(ctx, a); ok {
		t.Fatal("deleting one conversation must clear only that one")
	}
	if _, ok := store.Peek(ctx, b); !ok {
		t.Fatal("deleting one conversation must not clear another")
	}
}

func TestConfirmStoreSecondChallengeSupersedesTheFirst(t *testing.T) {
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Minute, nil)
	conv := uuid.New()

	if err := store.Put(ctx, conv, "confirm_first"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Put(ctx, conv, "confirm_second"); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, _ := store.Peek(ctx, conv)
	if got != "confirm_second" {
		t.Fatalf("the newer challenge must win, got %q", got)
	}
}

func TestConfirmStoreEmptyIDIsNotStaged(t *testing.T) {
	// A refusal carries an empty confirm_id; that is not a challenge to tap.
	ctx := context.Background()
	store := NewConfirmStore(newFakeRedis(), time.Minute, nil)
	conv := uuid.New()

	if err := store.Put(ctx, conv, ""); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := store.Peek(ctx, conv); ok {
		t.Fatal("an empty confirm id must never be staged")
	}
}

func TestConfirmStoreNilRedisFailsClosed(t *testing.T) {
	// No Redis means no confirm can be read, so a tap settles nothing.
	ctx := context.Background()
	store := NewConfirmStore(nil, time.Minute, nil)
	conv := uuid.New()

	if err := store.Put(ctx, conv, "confirm_abc"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := store.Peek(ctx, conv); ok {
		t.Fatal("with no Redis there is nothing to read")
	}
}
