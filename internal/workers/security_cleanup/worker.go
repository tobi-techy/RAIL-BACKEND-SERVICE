package security_cleanup

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/api/middleware"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"github.com/rail-service/rail_service/internal/domain/services/session"
)

type Worker struct {
	sessionService    *session.Service
	apikeyService     *apikey.Service
	userRateLimiter   *middleware.UserRateLimiter
	cron              *cron.Cron
	logger            *zap.Logger
}

func NewWorker(
	sessionService *session.Service,
	apikeyService *apikey.Service,
	userRateLimiter *middleware.UserRateLimiter,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		sessionService:  sessionService,
		apikeyService:   apikeyService,
		userRateLimiter: userRateLimiter,
		cron:            cron.New(),
		logger:          logger,
	}
}

func (w *Worker) Start() error {
	// Cleanup expired sessions every hour
	_, err := w.cron.AddFunc("0 * * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := w.sessionService.CleanupExpiredSessions(ctx); err != nil {
			w.logger.Error("Failed to cleanup expired sessions", zap.Error(err))
		}
	})
	if err != nil {
		return err
	}

	// Cleanup expired API keys every 6 hours
	_, err = w.cron.AddFunc("0 */6 * * *", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := w.apikeyService.CleanupExpiredKeys(ctx); err != nil {
			w.logger.Error("Failed to cleanup expired API keys", zap.Error(err))
		}
	})
	if err != nil {
		return err
	}

	// Cleanup old rate limit records every 2 hours
	_, err = w.cron.AddFunc("0 */2 * * *", func() {
		w.userRateLimiter.CleanupExpiredRateLimits()
	})
	if err != nil {
		return err
	}

	w.cron.Start()
	w.logger.Info("Security cleanup worker started")
	return nil
}

func (w *Worker) Stop() {
	w.cron.Stop()
	w.logger.Info("Security cleanup worker stopped")
}