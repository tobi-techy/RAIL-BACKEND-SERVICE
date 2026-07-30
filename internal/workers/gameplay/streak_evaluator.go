package gameplay

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// StreakService interface for the worker
type StreakService interface {
	CheckAndResetStreaks(ctx context.Context) (int, error)
	GetNearBreakingStreaks(ctx context.Context) ([]*entities.UserStreak, error)
}

// PushNotifier sends push notifications
type PushNotifier interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// StreakEvaluator runs daily to reset broken streaks and send reminders
type StreakEvaluator struct {
	streakSvc StreakService
	notifier  PushNotifier
	logger    *zap.Logger
}

func NewStreakEvaluator(streakSvc StreakService, notifier PushNotifier, logger *zap.Logger) *StreakEvaluator {
	return &StreakEvaluator{streakSvc: streakSvc, notifier: notifier, logger: logger}
}

func (w *StreakEvaluator) EvaluateStreaks(ctx context.Context) {
	// Send reminders for near-breaking streaks
	nearBreaking, err := w.streakSvc.GetNearBreakingStreaks(ctx)
	if err == nil {
		for _, s := range nearBreaking {
			if w.notifier != nil {
				w.notifier.SendToUser(ctx, s.UserID,
					"Miriam sees the streak",
					"One deposit today keeps the streak alive. Future-you gets bragging rights.",
					map[string]interface{}{"type": "streak_warning", "streak_type": string(s.StreakType)})
			}
		}
		if len(nearBreaking) > 0 {
			w.logger.Info("Sent streak warning notifications", zap.Int("count", len(nearBreaking)))
		}
	}

	// Reset broken streaks
	reset, err := w.streakSvc.CheckAndResetStreaks(ctx)
	if err != nil {
		w.logger.Error("Failed to reset streaks", zap.Error(err))
		return
	}
	if reset > 0 {
		w.logger.Info("Reset broken streaks", zap.Int("count", reset))
	}
}
