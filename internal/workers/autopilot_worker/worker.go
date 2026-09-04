package autopilot_worker

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/ai"
	"go.uber.org/zap"
)

// Worker runs Miriam's daily autopilot loop plus a Sunday weekly audit.
type Worker struct {
	svc      *ai.AutopilotService
	logger   *zap.Logger
	interval time.Duration
}

func NewWorker(svc *ai.AutopilotService, logger *zap.Logger) *Worker {
	return &Worker{
		svc:      svc,
		logger:   logger,
		interval: 30 * time.Minute,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("autopilot worker started", zap.Duration("interval", w.interval))

	// Tick immediately so a restart at 6:05 still catches the morning window.
	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("autopilot worker stopped")
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	now := time.Now().UTC()
	hour := now.Hour()

	if now.Weekday() == time.Sunday && hour >= 18 && hour < 19 {
		w.logger.Info("autopilot: weekly audit phase")
		w.svc.RunWeeklyAudit(ctx)
	}

	switch {
	case hour >= 6 && hour < 7:
		w.logger.Info("autopilot: morning scan phase")
		w.svc.RunMorningScan(ctx)

	case hour >= 12 && hour < 13:
		w.logger.Info("autopilot: midday check phase")
		w.svc.RunMiddayCheck(ctx)

	case hour >= 18 && hour < 19:
		w.logger.Info("autopilot: evening review phase")
		w.svc.RunEveningReview(ctx)
	}
}
