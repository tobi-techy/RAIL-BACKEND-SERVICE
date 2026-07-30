package engagement_worker

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"github.com/rail-service/rail_service/internal/domain/services/growthmail"
	"github.com/rail-service/rail_service/internal/workers/growth_engine"
	"github.com/rail-service/rail_service/internal/workers/growth_mail"
	"github.com/rail-service/rail_service/internal/workers/scheduled_notifications"
	"go.uber.org/zap"
)

type Worker struct {
	notifWorker *scheduled_notifications.Worker
	mailWorker  *growth_mail.Worker
	engineWorker *growth_engine.Worker
	logger      *zap.Logger
}

func NewWorker(
	userRepo scheduled_notifications.UserRepo,
	pushSender scheduled_notifications.PushSender,
	mailService *growthmail.Service,
	engineService *growthengine.Service,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		notifWorker:  scheduled_notifications.NewWorker(userRepo, pushSender, logger),
		mailWorker:   growth_mail.NewWorker(mailService, logger),
		engineWorker: growth_engine.NewWorker(engineService, logger),
		logger:       logger,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Engagement worker started")

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	w.runAll(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Engagement worker stopped")
			return
		case <-ticker.C:
			w.runAll(ctx)
		}
	}
}

func (w *Worker) runAll(ctx context.Context) {
	if w.notifWorker != nil {
		w.notifWorker.RunIfDue(ctx)
	}
	if w.mailWorker != nil {
		w.mailWorker.Run(ctx)
	}
	if w.engineWorker != nil {
		w.engineWorker.Run(ctx)
	}
}
