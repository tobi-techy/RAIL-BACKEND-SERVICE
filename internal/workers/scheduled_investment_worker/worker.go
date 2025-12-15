package scheduled_investment_worker

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/market"
	"go.uber.org/zap"
)

// Worker processes scheduled investments and market alerts
type Worker struct {
	scheduledService *investing.ScheduledInvestmentService
	marketService    *market.MarketDataService
	logger           *zap.Logger
	stopCh           chan struct{}
}

func NewWorker(
	scheduledService *investing.ScheduledInvestmentService,
	marketService *market.MarketDataService,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		scheduledService: scheduledService,
		marketService:    marketService,
		logger:           logger,
		stopCh:           make(chan struct{}),
	}
}

// Start begins the worker processing loop
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting scheduled investment worker")

	// Process scheduled investments every minute
	investmentTicker := time.NewTicker(1 * time.Minute)
	defer investmentTicker.Stop()

	// Check market alerts every 30 seconds
	alertTicker := time.NewTicker(30 * time.Second)
	defer alertTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Scheduled investment worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Scheduled investment worker stopped")
			return
		case <-investmentTicker.C:
			w.processScheduledInvestments(ctx)
		case <-alertTicker.C:
			w.checkMarketAlerts(ctx)
		}
	}
}

// Stop signals the worker to stop
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) processScheduledInvestments(ctx context.Context) {
	if w.scheduledService == nil {
		return
	}

	if err := w.scheduledService.ProcessDueInvestments(ctx); err != nil {
		w.logger.Error("Failed to process scheduled investments", zap.Error(err))
	}
}

func (w *Worker) checkMarketAlerts(ctx context.Context) {
	if w.marketService == nil {
		return
	}

	if err := w.marketService.CheckAlerts(ctx); err != nil {
		w.logger.Error("Failed to check market alerts", zap.Error(err))
	}
}
