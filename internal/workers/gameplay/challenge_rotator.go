package gameplay

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// ChallengeService interface for the worker
type ChallengeService interface {
	ExpireOldChallenges(ctx context.Context, cType entities.ChallengeType) (int64, error)
	AssignWeeklyChallenges(ctx context.Context) error
	AssignMonthlyChallenges(ctx context.Context) error
}

// ChallengeRotator rotates weekly and monthly challenges
type ChallengeRotator struct {
	challengeSvc    ChallengeService
	logger          *zap.Logger
	stop            chan struct{}
	lastWeeklyDate  string
	lastMonthlyDate string
}

func NewChallengeRotator(challengeSvc ChallengeService, logger *zap.Logger) *ChallengeRotator {
	return &ChallengeRotator{challengeSvc: challengeSvc, logger: logger, stop: make(chan struct{})}
}

func (w *ChallengeRotator) Start(ctx context.Context) {
	w.logger.Info("Challenge rotator worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			now := time.Now().UTC()
			today := now.Format("2006-01-02")

			// Weekly rotation: Monday at 00:00 UTC
			if now.Weekday() == time.Monday && now.Hour() == 0 && w.lastWeeklyDate != today {
				w.lastWeeklyDate = today
				w.rotateWeekly(ctx)
			}

			// Monthly rotation: 1st of month at 00:00 UTC
			if now.Day() == 1 && now.Hour() == 0 && w.lastMonthlyDate != today {
				w.lastMonthlyDate = today
				w.rotateMonthly(ctx)
			}
		}
	}
}

func (w *ChallengeRotator) rotateWeekly(ctx context.Context) {
	expired, err := w.challengeSvc.ExpireOldChallenges(ctx, entities.ChallengeTypeWeekly)
	if err != nil {
		w.logger.Error("Failed to expire weekly challenges", zap.Error(err))
	} else if expired > 0 {
		w.logger.Info("Expired weekly challenges", zap.Int64("count", expired))
	}
	if err := w.challengeSvc.AssignWeeklyChallenges(ctx); err != nil {
		w.logger.Error("Failed to assign weekly challenges", zap.Error(err))
	}
}

func (w *ChallengeRotator) rotateMonthly(ctx context.Context) {
	expired, err := w.challengeSvc.ExpireOldChallenges(ctx, entities.ChallengeTypeMonthly)
	if err != nil {
		w.logger.Error("Failed to expire monthly challenges", zap.Error(err))
	} else if expired > 0 {
		w.logger.Info("Expired monthly challenges", zap.Int64("count", expired))
	}
	if err := w.challengeSvc.AssignMonthlyChallenges(ctx); err != nil {
		w.logger.Error("Failed to assign monthly challenges", zap.Error(err))
	}
}

func (w *ChallengeRotator) Stop() { close(w.stop) }
