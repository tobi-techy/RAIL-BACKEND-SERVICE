package opportunity_sync

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/opportunity"
	"go.uber.org/zap"
)

// UserLister returns active user IDs for recommendation generation.
type UserLister interface {
	GetAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Worker syncs opportunities and generates weekly recommendations.
type Worker struct {
	svc        *opportunity.Service
	users      UserLister
	logger     *zap.Logger
	lastSync   string // "YYYY-MM-DD"
	lastPicked string // "YYYY-MM-DD"
}

func NewWorker(svc *opportunity.Service, users UserLister, logger *zap.Logger) *Worker {
	return &Worker{svc: svc, users: users, logger: logger}
}

func (w *Worker) Start(ctx context.Context) {
	// Run sync immediately on startup
	w.syncListings(ctx)

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Sync listings twice daily
	if w.lastSync != today || now.Hour() == 12 {
		w.syncListings(ctx)
		w.lastSync = today
	}

	// Generate weekly picks on Monday mornings
	if now.Weekday() == time.Monday && now.Hour() >= 8 && now.Hour() < 14 && w.lastPicked != today {
		w.generateWeeklyPicks(ctx)
		w.lastPicked = today
	}
}

func (w *Worker) syncListings(ctx context.Context) {
	w.logger.Info("Starting opportunity sync")
	if err := w.svc.SyncFromSuperteam(ctx); err != nil {
		w.logger.Error("Opportunity sync failed", zap.Error(err))
	}
}

func (w *Worker) generateWeeklyPicks(ctx context.Context) {
	w.logger.Info("Generating weekly opportunity recommendations")

	userIDs, err := w.users.GetAllActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("Failed to get users for opportunity picks", zap.Error(err))
		return
	}

	generated := 0
	for _, uid := range userIDs {
		if err := w.svc.GenerateWeeklyRecommendations(ctx, uid); err != nil {
			w.logger.Warn("Failed to generate recommendations", zap.String("user_id", uid.String()), zap.Error(err))
			continue
		}
		generated++
	}
	w.logger.Info("Weekly opportunity picks complete", zap.Int("users", generated))
}
