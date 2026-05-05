package automation_worker

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/automation"
	"go.uber.org/zap"
)

// Worker evaluates scheduled automations on a regular interval.
type Worker struct {
	service  *automation.Service
	interval time.Duration
	logger   *zap.Logger
}

func NewWorker(service *automation.Service, logger *zap.Logger) *Worker {
	return &Worker{
		service:  service,
		interval: 5 * time.Minute,
		logger:   logger,
	}
}

// Start begins the automation evaluation loop.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("automation worker started", zap.Duration("interval", w.interval))
	ticker := time.NewTicker(w.interval)
	unfreezeTicker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer unfreezeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("automation worker stopped")
			return
		case <-unfreezeTicker.C:
			if err := w.service.ProcessPendingUnfreezes(ctx); err != nil {
				w.logger.Error("pending card unfreeze processing failed", zap.Error(err))
			}
		case <-ticker.C:
			if err := w.service.EvaluateScheduled(ctx); err != nil {
				w.logger.Error("automation evaluation failed", zap.Error(err))
			}
			if err := w.service.EvaluateAllBalanceThresholds(ctx); err != nil {
				w.logger.Error("balance threshold evaluation failed", zap.Error(err))
			}
			if err := w.service.EvaluateObligationDue(ctx); err != nil {
				w.logger.Error("obligation due evaluation failed", zap.Error(err))
			}
			if err := w.service.DeactivateCompletedGoalAutomations(ctx); err != nil {
				w.logger.Error("goal-linked deactivation failed", zap.Error(err))
			}
		}
	}
}
