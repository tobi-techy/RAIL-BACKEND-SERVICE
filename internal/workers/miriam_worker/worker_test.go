package miriam_worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

type fakeUserLister struct {
	users []uuid.UUID
}

func (f *fakeUserLister) ListMiriamWorkerUserIDs(_ context.Context, _ int) ([]uuid.UUID, error) {
	return f.users, nil
}

type fakeBrain struct {
	evaluated []struct {
		userID    uuid.UUID
		eventType string
	}
	selfReviewed []uuid.UUID
}

func (f *fakeBrain) Evaluate(_ context.Context, userID uuid.UUID, eventType string) (*miriamsvc.IntelligenceResult, error) {
	f.evaluated = append(f.evaluated, struct {
		userID    uuid.UUID
		eventType string
	}{userID, eventType})
	return &miriamsvc.IntelligenceResult{UserID: userID}, nil
}

func (f *fakeBrain) RunSelfReview(_ context.Context, userID uuid.UUID) error {
	f.selfReviewed = append(f.selfReviewed, userID)
	return nil
}

func newTestWorker(users UserLister, brain *fakeBrain, adaptive bool) *Worker {
	w := &Worker{
		users:       users,
		runner:      brain,
		interval:    15 * time.Minute,
		limit:       500,
		concurrency: 10,
		logger:      zap.NewNop(),
		hotUsers:    make(map[uuid.UUID]time.Time),
		hotTTL:      5 * time.Minute,
	}
	if adaptive {
		w.SetAdaptive(nil, 0, 0)
	}
	return w
}

// TestNotifyFlagsHotUsers verifies that Notify marks users for fast
// re-evaluation and popHotUsers returns them (and expires stale ones).
func TestNotifyFlagsHotUsers(t *testing.T) {
	brain := &fakeBrain{}
	w := newTestWorker(&fakeUserLister{}, brain, true)

	uid := uuid.New()
	_ = w.Notify(context.Background(), uid)

	hot := w.popHotUsers()
	assert.Len(t, hot, 1)
	assert.Equal(t, uid, hot[0])

	// After popping, the user is no longer hot until the next Notify.
	assert.Empty(t, w.popHotUsers())
}

// TestNotifyRespectsTTL verifies that hot-user entries older than the TTL are
// ignored and cleaned up on pop.
func TestNotifyRespectsTTL(t *testing.T) {
	brain := &fakeBrain{}
	w := newTestWorker(&fakeUserLister{}, brain, true)
	w.hotTTL = 1 * time.Millisecond

	uid := uuid.New()
	_ = w.Notify(context.Background(), uid)
	time.Sleep(5 * time.Millisecond)

	assert.Empty(t, w.popHotUsers(), "expired hot user should not be returned")
}

// TestAdaptiveFastTickEvaluatesHotUsers verifies that the adaptive fast ticker
// evaluates hot users with the read-only autonomous event type.
func TestAdaptiveFastTickEvaluatesHotUsers(t *testing.T) {
	brain := &fakeBrain{}
	w := newTestWorker(&fakeUserLister{}, brain, true)
	w.fastInterval = 10 * time.Millisecond
	w.slowInterval = 1 * time.Hour

	uid := uuid.New()
	_ = w.Notify(context.Background(), uid)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()
	<-ctx.Done()
	<-done

	var found bool
	for _, ev := range brain.evaluated {
		if ev.userID == uid && ev.eventType == miriamsvc.EventAutonomousTick {
			found = true
			break
		}
	}
	assert.True(t, found, "hot user should be evaluated with EventAutonomousTick")
}

// TestAdaptiveSlowTickEvaluatesAllUsers verifies that the slow ticker still
// runs a full sweep with the autonomous event type.
func TestAdaptiveSlowTickEvaluatesAllUsers(t *testing.T) {
	uid := uuid.New()
	brain := &fakeBrain{}
	w := newTestWorker(&fakeUserLister{users: []uuid.UUID{uid}}, brain, true)
	w.fastInterval = 1 * time.Hour
	w.slowInterval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()
	<-ctx.Done()
	<-done

	var found bool
	for _, ev := range brain.evaluated {
		if ev.userID == uid && ev.eventType == miriamsvc.EventAutonomousTick {
			found = true
			break
		}
	}
	assert.True(t, found, "slow sweep should evaluate all users with EventAutonomousTick")
}

// TestClassicModeUsesWorkerSweep verifies that non-adaptive workers still use
// EventWorkerSweep for backward compatibility.
func TestClassicModeUsesWorkerSweep(t *testing.T) {
	uid := uuid.New()
	brain := &fakeBrain{}
	w := newTestWorker(&fakeUserLister{users: []uuid.UUID{uid}}, brain, false)
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()
	<-ctx.Done()
	<-done

	var found bool
	for _, ev := range brain.evaluated {
		if ev.userID == uid && ev.eventType == miriamsvc.EventWorkerSweep {
			found = true
			break
		}
	}
	assert.True(t, found, "classic mode should use EventWorkerSweep")
}
