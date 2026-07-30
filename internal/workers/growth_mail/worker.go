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

func (w *Worker) Run(ctx context.Context) {
	now := time.Now().UTC()
	if now.Hour() != 11 {
		return
	}
	sent, failed, err := w.service.SendDue(ctx, now)
	if err != nil {
		w.logger.Error("Growth mail run failed", zap.Error(err))
		return
	}
	w.logger.Info("Growth mail run complete", zap.Int("sent", sent), zap.Int("failed", failed))
}
