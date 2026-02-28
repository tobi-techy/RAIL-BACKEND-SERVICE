package p2p_expiry

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// P2PService interface for processing expired transfers
type P2PService interface {
	ProcessExpiredTransfers(ctx context.Context) (int, error)
}

// Worker handles expiry of unclaimed P2P transfers
type Worker struct {
	p2pService    P2PService
	checkInterval time.Duration
	logger        *zap.Logger
	stopCh        chan struct{}
}

// NewWorker creates a new P2P expiry worker
func NewWorker(p2pService P2PService, checkInterval time.Duration, logger *zap.Logger) *Worker {
	if checkInterval == 0 {
		checkInterval = 1 * time.Hour
	}
	return &Worker{
		p2pService:    p2pService,
		checkInterval: checkInterval,
		logger:        logger,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the expiry worker
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting P2P expiry worker", zap.Duration("check_interval", w.checkInterval))

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	w.process(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("P2P expiry worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("P2P expiry worker stopped")
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

// Stop stops the worker
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) process(ctx context.Context) {
	processed, err := w.p2pService.ProcessExpiredTransfers(ctx)
	if err != nil {
		w.logger.Error("Failed to process expired P2P transfers", zap.Error(err))
		return
	}
	if processed > 0 {
		w.logger.Info("Processed expired P2P transfers", zap.Int("count", processed))
	}
}
