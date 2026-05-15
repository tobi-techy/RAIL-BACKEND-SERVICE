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
	lastRun string
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

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Growth mail worker stopped")
			return
		case <-ticker.C:
			w.runIfDue(ctx)
		}
	}
}

func (w *Worker) runIfDue(ctx context.Context) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	if now.Hour() != 11 || w.lastRun == today {
		return
	}

	w.lastRun = today
	sent, failed, err := w.service.SendDue(ctx, now)
	if err != nil {
		w.logger.Error("Growth mail run failed", zap.Error(err))
		return
	}
	w.logger.Info("Growth mail run complete", zap.Int("sent", sent), zap.Int("failed", failed))
}
