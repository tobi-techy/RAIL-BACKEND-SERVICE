package miriam_worker

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type UserLister interface {
	ListMiriamWorkerUserIDs(ctx context.Context, limit int) ([]uuid.UUID, error)
}

type Worker struct {
	users       UserLister
	service     *miriamsvc.Service
	brain       *miriamsvc.IntelligenceOrchestrator
	interval    time.Duration
	limit       int
	concurrency int
	logger      *zap.Logger
}

// NewWorker creates a Miriam worker with the classic service for backward compatibility.
func NewWorker(users UserLister, service *miriamsvc.Service, logger *zap.Logger) *Worker {
	return &Worker{
		users: users, service: service, interval: 15 * time.Minute,
		limit: 500, concurrency: 10, logger: logger,
	}
}

// NewWorkerWithIntelligence creates a Miriam worker with the unified intelligence orchestrator.
func NewWorkerWithIntelligence(users UserLister, service *miriamsvc.Service, brain *miriamsvc.IntelligenceOrchestrator, logger *zap.Logger) *Worker {
	return &Worker{
		users: users, service: service, brain: brain, interval: 15 * time.Minute,
		limit: 500, concurrency: 10, logger: logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Miriam intelligence worker started", zap.Duration("interval", w.interval), zap.Int("concurrency", w.concurrency))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.run(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Miriam intelligence worker stopped")
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	users, err := w.users.ListMiriamWorkerUserIDs(ctx, w.limit)
	if err != nil {
		w.logger.Error("miriam worker: list users failed", zap.Error(err))
		return
	}

	var evaluated, failed int64
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(w.concurrency)

	for _, uid := range users {
		userID := uid
		g.Go(func() error {
			evalCtx, cancel := context.WithTimeout(gCtx, 30*time.Second)
			defer cancel()

			var evalErr error
			if w.brain != nil {
				_, evalErr = w.brain.Evaluate(evalCtx, userID, miriamsvc.EventWorkerSweep)
			} else {
				evalErr = w.service.EvaluateUser(evalCtx, userID, miriamsvc.EventWorkerSweep)
			}
			if evalErr != nil {
				atomic.AddInt64(&failed, 1)
				w.logger.Warn("miriam worker: user evaluation failed", zap.String("user_id", userID.String()), zap.Error(evalErr))
			} else {
				atomic.AddInt64(&evaluated, 1)
			}
			return nil // don't abort other goroutines on individual failure
		})
	}
	_ = g.Wait()
	w.logger.Info("miriam worker: run complete", zap.Int64("evaluated", evaluated), zap.Int64("failed", failed))
}
