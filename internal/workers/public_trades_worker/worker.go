// Package public_trades_worker periodically ingests new public trade
// disclosures (congressional filings) for the public figures users copy,
// turning them into copy-trading signals. The copy trading worker then
// executes those signals as real brokerage orders.
package public_trades_worker

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DisclosureIngester is implemented by the copy trading service.
type DisclosureIngester interface {
	IngestPublicDisclosures(ctx context.Context) error
}

type Worker struct {
	ingester     DisclosureIngester
	logger       *zap.Logger
	pollInterval time.Duration
	initialDelay time.Duration
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

func NewWorker(ingester DisclosureIngester, logger *zap.Logger) *Worker {
	return &Worker{
		ingester: ingester,
		logger:   logger,
		// Disclosure datasets update at most daily.
		pollInterval: 6 * time.Hour,
		initialDelay: 2 * time.Minute,
		stopCh:       make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting public trades worker", zap.Duration("poll_interval", w.pollInterval))
	w.wg.Add(1)
	go w.run(ctx)
}

func (w *Worker) Stop() {
	w.logger.Info("Stopping public trades worker")
	close(w.stopCh)
	w.wg.Wait()
	w.logger.Info("Public trades worker stopped")
}

func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	select {
	case <-ctx.Done():
		return
	case <-w.stopCh:
		return
	case <-time.After(w.initialDelay):
		w.ingest(ctx)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.ingest(ctx)
		}
	}
}

func (w *Worker) ingest(ctx context.Context) {
	start := time.Now()
	if err := w.ingester.IngestPublicDisclosures(ctx); err != nil {
		w.logger.Error("Public disclosure ingestion failed", zap.Error(err))
		return
	}
	w.logger.Info("Public disclosure ingestion completed", zap.Duration("duration", time.Since(start)))
}
