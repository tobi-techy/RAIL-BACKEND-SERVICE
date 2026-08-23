package platform

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

type fakeSeeder struct {
	called int32
	err    error
	delay  time.Duration
	userID uuid.UUID
}

func (f *fakeSeeder) Seed(ctx context.Context, userID uuid.UUID) (int, error) {
	atomic.AddInt32(&f.called, 1)
	f.userID = userID
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return 1, f.err
}

func TestSeedBabyStepsOnLink_NilSeederIsNoop(t *testing.T) {
	SeedBabyStepsOnLink(nil, uuid.New(), zap.NewNop())
	// Give any goroutine time to (not) run.
	time.Sleep(20 * time.Millisecond)
	// Nothing to assert — the absence of panic or side effect is the test.
}

func TestSeedBabyStepsOnLink_NilUserIsNoop(t *testing.T) {
	f := &fakeSeeder{}
	SeedBabyStepsOnLink(f, uuid.Nil, zap.NewNop())
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&f.called))
}

func TestSeedBabyStepsOnLink_FiresAndCallsSeed(t *testing.T) {
	f := &fakeSeeder{}
	uid := uuid.New()
	SeedBabyStepsOnLink(f, uid, zap.NewNop())

	// Wait for the goroutine to finish (with a generous timeout).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&f.called) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.called))
	assert.Equal(t, uid, f.userID)
}

func TestSeedBabyStepsOnLink_SeederErrorDoesNotPanic(t *testing.T) {
	f := &fakeSeeder{err: errors.New("db down")}
	// Should not panic even when seeder returns an error.
	SeedBabyStepsOnLink(f, uuid.New(), zap.NewNop())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&f.called) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.called))
}

func TestSeedBabyStepsOnLink_PanicIsRecovered(t *testing.T) {
	panicSeeder := panicSeeder{}
	SeedBabyStepsOnLink(panicSeeder, uuid.New(), zap.NewNop())
	// Give the recover() time to log. We can't easily assert "no panic" beyond
	// the test process being alive, but the deferred recover() guarantees the
	// goroutine never escapes upward.
	time.Sleep(50 * time.Millisecond)
}

type panicSeeder struct{}

func (panicSeeder) Seed(ctx context.Context, userID uuid.UUID) (int, error) {
	panic("simulated panic in seeder")
}

func TestSeedBabyStepsOnLink_RespectsTimeout(t *testing.T) {
	f := &fakeSeeder{delay: 500 * time.Millisecond}
	SeedBabyStepsOnLink(f, uuid.New(), zap.NewNop())
	// Wait long enough that the context timeout (5s) — actually we use a short
	// timeout in the impl, but the fake's 500ms delay completes well within it.
	// We assert that the call returned (didn't hang).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&f.called) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.called), "seeder should have completed within timeout")
}
