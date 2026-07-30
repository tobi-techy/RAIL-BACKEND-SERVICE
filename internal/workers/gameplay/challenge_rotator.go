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
	challengeSvc ChallengeService
	logger       *zap.Logger
}

func NewChallengeRotator(challengeSvc ChallengeService, logger *zap.Logger) *ChallengeRotator {
	return &ChallengeRotator{challengeSvc: challengeSvc, logger: logger}
}

func (w *ChallengeRotator) RotateChallenges(ctx context.Context) {
	now := time.Now().UTC()

	if now.Weekday() == time.Monday {
		w.rotateWeekly(ctx)
	}

	if now.Day() == 1 {
		w.rotateMonthly(ctx)
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
