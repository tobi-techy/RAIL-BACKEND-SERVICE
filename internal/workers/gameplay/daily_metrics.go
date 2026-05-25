package gameplay

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// CardTransactionCounter checks if a user had card transactions on a given day
type CardTransactionCounter interface {
	CountCardTransactionsForDate(ctx context.Context, userID uuid.UUID, date time.Time) (int, error)
}

// StashBalanceProvider provides stash balance for growth tracking
type StashBalanceProvider interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// ChallengeUpdater updates challenge progress
type ChallengeUpdater interface {
	UpdateProgress(ctx context.Context, userID uuid.UUID, metric string, newProgress decimal.Decimal) error
}

// StreakRecorder records streak activity
type StreakRecorder interface {
	RecordActivity(ctx context.Context, userID uuid.UUID, streakType entities.StreakType) error
}

// DailyMetricsWorker runs daily to update no-spend streaks, stash growth challenges, and milestone challenges
type DailyMetricsWorker struct {
	userProvider ActiveUserProvider
	cardCounter  CardTransactionCounter
	balances     StashBalanceProvider
	challenges   ChallengeUpdater
	streaks      StreakRecorder
	logger       *zap.Logger
	stop         chan struct{}
	lastDate     string
}

func NewDailyMetricsWorker(
	userProvider ActiveUserProvider,
	cardCounter CardTransactionCounter,
	balances StashBalanceProvider,
	challenges ChallengeUpdater,
	streaks StreakRecorder,
	logger *zap.Logger,
) *DailyMetricsWorker {
	return &DailyMetricsWorker{
		userProvider: userProvider, cardCounter: cardCounter, balances: balances,
		challenges: challenges, streaks: streaks, logger: logger, stop: make(chan struct{}),
	}
}

func (w *DailyMetricsWorker) Start(ctx context.Context) {
	w.logger.Info("Daily metrics worker started")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			// Run at 3am UTC daily
			if time.Now().UTC().Hour() == 3 && w.lastDate != time.Now().UTC().Format("2006-01-02") {
				w.lastDate = time.Now().UTC().Format("2006-01-02")
				w.run(ctx)
			}
		}
	}
}

func (w *DailyMetricsWorker) run(ctx context.Context) {
	userIDs, err := w.userProvider.GetActiveUserIDs(ctx)
	if err != nil {
		w.logger.Error("Failed to get active users for daily metrics", zap.Error(err))
		return
	}

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	noSpendCount, stashGrowthCount := 0, 0

	for _, uid := range userIDs {
		// No-spend streak: check if user had zero card transactions yesterday
		if w.cardCounter != nil {
			txCount, err := w.cardCounter.CountCardTransactionsForDate(ctx, uid, yesterday)
			if err == nil && txCount == 0 {
				w.streaks.RecordActivity(ctx, uid, entities.StreakTypeNoSpend)
				noSpendCount++
				// Update no_spend_days challenge
				w.challenges.UpdateProgress(ctx, uid, "no_spend_days", decimal.NewFromInt(1))
			}
		}

		// Stash growth: check current stash balance for stash_growth challenge
		if w.balances != nil {
			stashBal, err := w.balances.GetAccountBalance(ctx, uid, entities.AccountTypeStashBalance)
			if err == nil && stashBal.GreaterThan(decimal.Zero) {
				// Update stash_growth challenge with current balance (challenge tracks cumulative)
				w.challenges.UpdateProgress(ctx, uid, "stash_growth", stashBal)
				stashGrowthCount++
			}
		}
	}

	if noSpendCount > 0 || stashGrowthCount > 0 {
		w.logger.Info("Daily metrics completed",
			zap.Int("no_spend_streaks", noSpendCount),
			zap.Int("stash_growth_updates", stashGrowthCount))
	}
}

func (w *DailyMetricsWorker) Stop() { close(w.stop) }
