// Package investment_sync keeps Rail's investment read model honest.
//
// Glider is the source of truth for portfolio positions and async operations, so
// this worker polls open operations, reconciles holdings and (when enabled)
// triggers rebalances for portfolios that have drifted past their threshold. It
// never invents state: whatever it cannot confirm stays flagged as stale.
package investment_sync

import (
	"context"
	"time"

	investmentsvc "github.com/rail-service/rail_service/internal/domain/services/investment"
	"go.uber.org/zap"
)

// Worker synchronizes Glider portfolio state.
type Worker struct {
	service       *investmentsvc.Service
	autoRebalance bool
	interval      time.Duration
	batchSize     int
	logger        *zap.Logger
	stopCh        chan struct{}
}

// NewWorker builds the sync worker. interval and batchSize fall back to sane
// values so a missing config cannot produce a hot loop.
func NewWorker(
	service *investmentsvc.Service,
	autoRebalance bool,
	interval time.Duration,
	batchSize int,
	logger *zap.Logger,
) *Worker {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Worker{
		service:       service,
		autoRebalance: autoRebalance,
		interval:      interval,
		batchSize:     batchSize,
		logger:        logger,
		stopCh:        make(chan struct{}),
	}
}

// Start runs the worker until the context is cancelled or Stop is called.
func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	w.logger.Info("Starting investment sync worker", zap.Duration("interval", w.interval))
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Investment sync worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Investment sync worker stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	close(w.stopCh)
}

// runOnce performs one synchronization pass: settle in-flight operations, then
// reconcile positions, then (optionally) converge drifted portfolios.
func (w *Worker) runOnce(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	polled, err := w.service.PollOperations(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("investment operation poll failed", zap.Error(err))
	}
	if polled > 0 {
		w.logger.Info("investment operations polled", zap.Int("count", polled))
	}

	if !w.autoRebalance {
		return
	}
	triggered, err := w.service.RunRebalanceSweep(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("investment rebalance sweep failed", zap.Error(err))
		return
	}
	if triggered > 0 {
		w.logger.Info("investment rebalances triggered", zap.Int("count", triggered))
	}
}
