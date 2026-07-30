package gameplay

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AchievementService interface for the worker
type AchievementService interface {
	CheckAndUnlock(ctx context.Context, userID uuid.UUID) (int, error)
}

// ActiveUserProvider returns active user IDs
type ActiveUserProvider interface {
	GetActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// AchievementChecker runs daily to evaluate achievements for all users
type AchievementChecker struct {
	achievementSvc AchievementService
	userProvider   ActiveUserProvider
	logger         *zap.Logger
}

func NewAchievementChecker(achievementSvc AchievementService, userProvider ActiveUserProvider, logger *zap.Logger) *AchievementChecker {
	return &AchievementChecker{achievementSvc: achievementSvc, userProvider: userProvider, logger: logger}
}

func (w *AchievementChecker) CheckAchievements(ctx context.Context) {
	userIDs, err := w.userProvider.GetActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("Failed to get active users for achievement check", zap.Error(err))
		return
	}

	totalUnlocked := 0
	for _, uid := range userIDs {
		unlocked, err := w.achievementSvc.CheckAndUnlock(ctx, uid)
		if err != nil {
			w.logger.Warn("Achievement check failed for user", zap.String("user_id", uid.String()), zap.Error(err))
			continue
		}
		totalUnlocked += unlocked
	}
	if totalUnlocked > 0 {
		w.logger.Info("Achievement checker completed", zap.Int("unlocked", totalUnlocked), zap.Int("users_checked", len(userIDs)))
	}
}
