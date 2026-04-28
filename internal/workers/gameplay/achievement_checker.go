package gameplay

import (
	"context"
	"time"

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
	stop           chan struct{}
	lastDate       string
}

func NewAchievementChecker(achievementSvc AchievementService, userProvider ActiveUserProvider, logger *zap.Logger) *AchievementChecker {
	return &AchievementChecker{achievementSvc: achievementSvc, userProvider: userProvider, logger: logger, stop: make(chan struct{})}
}

func (w *AchievementChecker) Start(ctx context.Context) {
	w.logger.Info("Achievement checker worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			if time.Now().UTC().Hour() == 2 && w.lastDate != time.Now().UTC().Format("2006-01-02") {
				w.lastDate = time.Now().UTC().Format("2006-01-02")
				w.run(ctx)
			}
		}
	}
}

func (w *AchievementChecker) run(ctx context.Context) {
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

func (w *AchievementChecker) Stop() { close(w.stop) }
