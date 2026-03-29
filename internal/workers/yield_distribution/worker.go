package yield_distribution

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/lulo"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// RewardsProvider fetches the accrued reward summary from the yield provider.
type RewardsProvider interface {
	GetRewardsSummary(ctx context.Context, currency string) (*yield.RewardSummary, error)
}

// Worker runs the monthly yield distribution.
type Worker struct {
	yieldSvc   *yield.Service
	rewards    RewardsProvider
	luloClient *lulo.Client
	db         *sqlx.DB
	logger     *zap.Logger
}

func NewWorker(yieldSvc *yield.Service, rewards RewardsProvider, luloClient *lulo.Client, db *sqlx.DB, logger *zap.Logger) *Worker {
	return &Worker{yieldSvc: yieldSvc, rewards: rewards, luloClient: luloClient, db: db, logger: logger}
}

// Run executes the distribution for the given period.
// periodStart and periodEnd define the earning window (e.g. March 1 00:00 → March 31 23:59).
func (w *Worker) Run(ctx context.Context, periodStart, periodEnd time.Time) error {
	freezeTime := time.Now()

	// Snapshot the current cumulative interest BEFORE reading the delta.
	// This is the value we'll persist as the high-water mark after success.
	currentInterest, err := di.GetCurrentLuloInterest(ctx, w.luloClient)
	if err != nil {
		return fmt.Errorf("yield worker: get current lulo interest: %w", err)
	}

	summary, err := w.rewards.GetRewardsSummary(ctx, "usdc")
	if err != nil {
		return fmt.Errorf("yield worker: get rewards: %w", err)
	}

	totalReward, err := decimal.NewFromString(summary.Rewards)
	if err != nil || totalReward.LessThanOrEqual(decimal.Zero) {
		w.logger.Info("Yield distribution skipped: no reward available",
			zap.String("rewards", summary.Rewards))
		return nil
	}

	w.logger.Info("Starting yield distribution",
		zap.String("period_start", periodStart.Format(time.DateOnly)),
		zap.String("period_end", periodEnd.Format(time.DateOnly)),
		zap.String("total_reward", totalReward.String()),
	)

	if err := w.yieldSvc.RunDistribution(ctx, periodStart, periodEnd, freezeTime, totalReward); err != nil {
		w.logger.Error("Yield distribution failed",
			zap.String("period_start", periodStart.Format(time.DateOnly)),
			zap.String("period_end", periodEnd.Format(time.DateOnly)),
			zap.String("total_reward", totalReward.String()),
			zap.Error(err),
		)
		return fmt.Errorf("yield worker: distribution failed: %w", err)
	}

	// Advance the high-water mark ONLY after successful distribution.
	if err := di.AdvanceYieldMark(ctx, w.db, currentInterest); err != nil {
		w.logger.Error("Failed to advance yield high-water mark (distribution succeeded, mark stale — next run will re-distribute this delta)",
			zap.String("current_interest", currentInterest.String()),
			zap.Error(err),
		)
		// Don't return error — distribution succeeded, users got their yield.
		// The mark being stale means next run will try to re-distribute, but
		// RunDistribution's period idempotency will catch it if same period.
	}

	w.logger.Info("Yield distribution completed successfully",
		zap.String("period_start", periodStart.Format(time.DateOnly)),
		zap.String("period_end", periodEnd.Format(time.DateOnly)),
		zap.String("total_reward", totalReward.String()),
	)
	return nil
}
