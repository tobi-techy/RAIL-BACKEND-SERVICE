package travel_recovery

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

type fakeReconciler struct{}

func (fakeReconciler) RunRecovery(ctx context.Context) {}

func TestStopBeforeStart(t *testing.T) {
	w := NewWorker(fakeReconciler{}, zap.NewNop())
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked when the worker was never started")
	}
}

func TestStopAfterStartAndIdempotent(t *testing.T) {
	w := NewWorker(fakeReconciler{}, zap.NewNop())
	go w.Start(context.Background())
	time.Sleep(50 * time.Millisecond)
	w.Stop()
	w.Stop() // must not panic or block
}
