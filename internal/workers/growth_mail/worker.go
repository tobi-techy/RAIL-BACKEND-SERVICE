package growth_mail

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/growthmail"
	"go.uber.org/zap"
)

type Worker struct {
	service *growthmail.Service
	logger  *zap.Logger
}

func NewWorker(service *growthmail.Service, logger *zap.Logger) *Worker {
	return &Worker{service: service, logger: logger}
}

func (w *Worker) Start(ctx context.Context) {
	if w.service == nil {
		return
	}
	w.logger.Info("Growth mail worker started")

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	w.run(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Growth mail worker stopped")
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

func (w *Worker) run(ctx context.Context) {
	now := time.Now().UTC()
	sent, failed, err := w.service.SendDue(ctx, now)
	if err != nil {
		w.logger.Error("Growth mail run failed", zap.Error(err))
		return
	}
	w.logger.Info("Growth mail run complete", zap.Int("sent", sent), zap.Int("failed", failed))
}
