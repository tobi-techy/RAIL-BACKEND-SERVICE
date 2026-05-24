package miriam_worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	miriamsvc "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"go.uber.org/zap"
)

type UserLister interface {
	ListMiriamWorkerUserIDs(ctx context.Context, limit int) ([]uuid.UUID, error)
}

type Worker struct {
	users    UserLister
	service  *miriamsvc.Service
	interval time.Duration
	limit    int
	logger   *zap.Logger
}

func NewWorker(users UserLister, service *miriamsvc.Service, logger *zap.Logger) *Worker {
	return &Worker{
		users: users, service: service, interval: 15 * time.Minute,
		limit: 500, logger: logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Miriam intelligence worker started", zap.Duration("interval", w.interval))
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
	evaluated := 0
	failed := 0
	for _, userID := range users {
		evalCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := w.service.EvaluateUser(evalCtx, userID, miriamsvc.EventWorkerSweep)
		cancel()
		if err != nil {
			failed++
			w.logger.Warn("miriam worker: user evaluation failed", zap.String("user_id", userID.String()), zap.Error(err))
			continue
		}
		evaluated++
	}
	w.logger.Info("miriam worker: run complete", zap.Int("evaluated", evaluated), zap.Int("failed", failed))
}
