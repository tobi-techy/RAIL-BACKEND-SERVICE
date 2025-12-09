package copy_trading_worker

import (
	"context"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"go.uber.org/zap"
)

// SignalRepository defines the interface for fetching pending signals
type SignalRepository interface {
	GetPendingSignals(ctx context.Context, limit int) ([]*entities.Signal, error)
	UpdateSignalStatus(ctx context.Context, signalID interface{}, status entities.SignalStatus, processedCount, failedCount int) error
}

// Worker processes copy trading signals asynchronously
type Worker struct {
	service        *copytrading.Service
	repo           SignalRepository
	logger         *zap.Logger
	pollInterval   time.Duration
	batchSize      int
	workerPoolSize int
	stopCh         chan struct{}
	wg             sync.WaitGroup
}

// NewWorker creates a new copy trading worker
func NewWorker(service *copytrading.Service, repo SignalRepository, logger *zap.Logger) *Worker {
	return &Worker{
		service:        service,
		repo:           repo,
		logger:         logger,
		pollInterval:   5 * time.Second,
		batchSize:      50,
		workerPoolSize: 10,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the worker processing loop
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting copy trading worker",
		zap.Duration("poll_interval", w.pollInterval),
		zap.Int("batch_size", w.batchSize),
		zap.Int("worker_pool_size", w.workerPoolSize))

	w.wg.Add(1)
	go w.run(ctx)
}

// Stop gracefully stops the worker
func (w *Worker) Stop() {
	w.logger.Info("Stopping copy trading worker")
	close(w.stopCh)
	w.wg.Wait()
	w.logger.Info("Copy trading worker stopped")
}

func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	signals, err := w.repo.GetPendingSignals(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("Failed to fetch pending signals", zap.Error(err))
		return
	}

	if len(signals) == 0 {
		return
	}

	w.logger.Info("Processing signals batch", zap.Int("count", len(signals)))

	// Create worker pool
	signalCh := make(chan *entities.Signal, len(signals))
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < w.workerPoolSize; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for signal := range signalCh {
				w.processSignal(ctx, signal)
			}
		}(i)
	}

	// Send signals to workers
	for _, signal := range signals {
		signalCh <- signal
	}
	close(signalCh)

	// Wait for all workers to complete
	wg.Wait()

	w.logger.Info("Batch processing completed", zap.Int("processed", len(signals)))
}

func (w *Worker) processSignal(ctx context.Context, signal *entities.Signal) {
	startTime := time.Now()

	err := w.service.ProcessSignal(ctx, signal)
	if err != nil {
		w.logger.Error("Failed to process signal",
			zap.String("signal_id", signal.ID.String()),
			zap.Error(err),
			zap.Duration("duration", time.Since(startTime)))
		return
	}

	w.logger.Info("Signal processed successfully",
		zap.String("signal_id", signal.ID.String()),
		zap.String("ticker", signal.AssetTicker),
		zap.Duration("duration", time.Since(startTime)))
}
