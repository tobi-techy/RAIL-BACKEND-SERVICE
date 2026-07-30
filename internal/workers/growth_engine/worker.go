package growth_engine

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"go.uber.org/zap"
)

type Worker struct {
	service  *growthengine.Service
	logger   *zap.Logger
	interval time.Duration
}

func NewWorker(service *growthengine.Service, logger *zap.Logger) *Worker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Worker{
		service:  service,
		logger:   logger,
		interval: 2 * time.Hour,
	}
}

func (w *Worker) Run(ctx context.Context) {
	segmented, queued, err := w.service.RunSegmentation(ctx)
	if err != nil {
		w.logger.Error("Growth engine run failed", zap.Error(err))
		return
	}
	w.logger.Info("Growth engine run complete", zap.Int("segmented", segmented), zap.Int("queued", queued))
}
