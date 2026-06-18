package yield_distribution

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// RewardsProvider fetches the accrued reward summary from the yield provider.
// rate is the already-fetched current exchange rate — passed in to avoid a duplicate API call.
type RewardsProvider interface {
	GetRewardsSummary(ctx context.Context, currency string, currentRate decimal.Decimal) (*yield.RewardSummary, error)
}

// ExchangeRateFetcher returns the current yield provider exchange rate.
type ExchangeRateFetcher func(ctx context.Context) (decimal.Decimal, error)

// ExchangeRateAdvancer persists the new exchange rate high-water mark and records
// the distributed yield so reconciliation stays accurate.
type ExchangeRateAdvancer func(ctx context.Context, db *sqlx.DB, rate decimal.Decimal, distributedYield decimal.Decimal) error

// Worker runs the monthly yield distribution.
type Worker struct {
	yieldSvc       *yield.Service
	rewards        RewardsProvider
	getRate        ExchangeRateFetcher
	advanceRate    ExchangeRateAdvancer
	db             *sqlx.DB
	logger         *zap.Logger
}

func NewWorker(
	yieldSvc *yield.Service,
	rewards RewardsProvider,
	getRate ExchangeRateFetcher,
	advanceRate ExchangeRateAdvancer,
	db *sqlx.DB,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		yieldSvc:    yieldSvc,
		rewards:     rewards,
		getRate:     getRate,
		advanceRate: advanceRate,
		db:          db,
		logger:      logger,
	}
}

// Run executes the distribution for the given period.
// periodStart and periodEnd define the earning window (e.g. March 1 00:00 → March 31 23:59).
func (w *Worker) Run(ctx context.Context, periodStart, periodEnd time.Time) error {
	freezeTime := time.Now()

	currentRate, err := w.getRate(ctx)
	if err != nil {
		return fmt.Errorf("yield worker: get current exchange rate: %w", err)
	}

	summary, err := w.rewards.GetRewardsSummary(ctx, "usdc", currentRate)
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

	// Advance the exchange rate high-water mark ONLY after successful distribution.
	if err := w.advanceRate(ctx, w.db, currentRate, totalReward); err != nil {
		w.logger.Error("Failed to advance exchange rate mark (distribution succeeded, mark stale — next run will re-distribute this delta)",
			zap.String("current_rate", currentRate.String()),
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

	analytics.TrackEvent(ctx, "system", analytics.EventYieldDistributed, map[string]any{
		"total_reward": totalReward.InexactFloat64(),
		"period_start": periodStart.Format(time.DateOnly),
		"period_end":   periodEnd.Format(time.DateOnly),
	})

	return nil
}
