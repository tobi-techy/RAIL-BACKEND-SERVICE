// Package travel_recovery periodically reconciles stuck BRIJ flight bookings:
// reversing abandoned holds whose escrow never funded, finalizing ticketed
// bookings, and re-delivering tickets that failed to send. All logic lives in
// travel.Service.RunRecovery; this worker only schedules it.
package travel_recovery

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Reconciler is satisfied by *travel.Service.
type Reconciler interface {
	RunRecovery(ctx context.Context)
}

type Worker struct {
	svc           Reconciler
	logger        *zap.Logger
	checkInterval time.Duration
	stopOnce      sync.Once
	mu            sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
}

func NewWorker(svc Reconciler, logger *zap.Logger) *Worker {
	return &Worker{
		svc:           svc,
		logger:        logger,
		checkInterval: 2 * time.Minute,
	}
}

func (w *Worker) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.cancel = cancel
	w.done = make(chan struct{})
	w.mu.Unlock()
	defer close(w.done)
	defer cancel()

	w.logger.Info("Starting travel recovery worker", zap.Duration("check_interval", w.checkInterval))
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
			w.runRecovery(runCtx)
		}
	}
}

// runRecovery executes a reconciliation pass with panic recovery so a single
// bad booking can never take the worker down silently.
func (w *Worker) runRecovery(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("travel recovery panicked", zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	w.svc.RunRecovery(ctx)
}

// Stop cancels the run context and waits for the in-flight recovery pass to
// finish before returning, so shutdown never leaves database or BRIJ calls
// running after stopWorkers completes. It is safe to call more than once and
// to call concurrently with Start.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		w.mu.Lock()
		cancel := w.cancel
		done := w.done
		w.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
}
