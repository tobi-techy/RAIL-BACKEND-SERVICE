package yield_distribution

import (
	"context"
	"fmt"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/yield"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BridgeRewards fetches the accrued reward summary from Bridge.
type BridgeRewards interface {
	GetRewardsSummary(ctx context.Context, currency string) (*yield.RewardSummary, error)
}

// Worker runs the monthly yield distribution.
type Worker struct {
	yieldSvc *yield.Service
	bridge   BridgeRewards
	logger   *zap.Logger
}

func NewWorker(yieldSvc *yield.Service, bridge BridgeRewards, logger *zap.Logger) *Worker {
	return &Worker{yieldSvc: yieldSvc, bridge: bridge, logger: logger}
}

// Run executes the distribution for the given period.
// Call this AFTER Bridge has paid out the monthly reward to Rail's wallet.
// periodStart and periodEnd define the earning window (e.g. March 1 00:00 → March 31 23:59).
func (w *Worker) Run(ctx context.Context, periodStart, periodEnd time.Time) error {
	freezeTime := time.Now()

	summary, err := w.bridge.GetRewardsSummary(ctx, "usdb")
	if err != nil {
		return fmt.Errorf("yield worker: get bridge rewards: %w", err)
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

	return w.yieldSvc.RunDistribution(ctx, periodStart, periodEnd, freezeTime, totalReward)
}
