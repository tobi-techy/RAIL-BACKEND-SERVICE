package portfolio_snapshot_worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/analytics"
	"go.uber.org/zap"
)

// AccountLister interface for listing all accounts
type AccountLister interface {
	ListAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Worker takes daily portfolio snapshots for all users
type Worker struct {
	analyticsService *analytics.PortfolioAnalyticsService
	accountLister    AccountLister
	logger           *zap.Logger
	stopCh           chan struct{}
}

func NewWorker(
	analyticsService *analytics.PortfolioAnalyticsService,
	accountLister AccountLister,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		analyticsService: analyticsService,
		accountLister:    accountLister,
		logger:           logger,
		stopCh:           make(chan struct{}),
	}
}

// Start begins the worker processing loop
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting portfolio snapshot worker")

	// Calculate time until next 4 PM ET (market close)
	nextRun := w.nextMarketClose()
	timer := time.NewTimer(time.Until(nextRun))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Portfolio snapshot worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Portfolio snapshot worker stopped")
			return
		case <-timer.C:
			w.takeSnapshots(ctx)
			// Schedule next run
			nextRun = w.nextMarketClose()
			timer.Reset(time.Until(nextRun))
		}
	}
}

// Stop signals the worker to stop
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) takeSnapshots(ctx context.Context) {
	w.logger.Info("Taking daily portfolio snapshots")

	if w.accountLister == nil {
		w.logger.Warn("Account lister not configured")
		return
	}

	userIDs, err := w.accountLister.ListAllActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("Failed to list user IDs", zap.Error(err))
		return
	}

	successCount := 0
	for _, userID := range userIDs {
		if err := w.analyticsService.TakeSnapshot(ctx, userID); err != nil {
			w.logger.Error("Failed to take snapshot",
				zap.String("user_id", userID.String()),
				zap.Error(err))
		} else {
			successCount++
		}
	}

	w.logger.Info("Portfolio snapshots completed",
		zap.Int("total", len(userIDs)),
		zap.Int("success", successCount))
}

func (w *Worker) nextMarketClose() time.Time {
	// 4 PM ET = 21:00 UTC (during EST) or 20:00 UTC (during EDT)
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Now().In(loc)

	// Set to 4 PM today
	marketClose := time.Date(now.Year(), now.Month(), now.Day(), 16, 0, 0, 0, loc)

	// If we've passed 4 PM, schedule for tomorrow
	if now.After(marketClose) {
		marketClose = marketClose.AddDate(0, 0, 1)
	}

	// Skip weekends
	for marketClose.Weekday() == time.Saturday || marketClose.Weekday() == time.Sunday {
		marketClose = marketClose.AddDate(0, 0, 1)
	}

	return marketClose
}

// TakeSnapshotNow takes a snapshot immediately (for manual trigger)
func (w *Worker) TakeSnapshotNow(ctx context.Context, userID uuid.UUID) error {
	return w.analyticsService.TakeSnapshot(ctx, userID)
}
