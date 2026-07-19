package memorycompression

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// MemorySummarizer compresses a user's facts into a narrative summary.
type MemorySummarizer interface {
	SummarizeMemory(ctx context.Context, userID uuid.UUID) error
}

// MemoryMetrics provides memory health data for the compression worker.
type MemoryMetrics interface {
	GetUsersNeedingCompression(ctx context.Context, threshold int, since time.Duration) ([]uuid.UUID, error)
	CountActiveFacts(ctx context.Context, userID uuid.UUID) (int, error)
}

// Worker runs periodically to compress users with many facts into summaries.
// This prevents memory quality degradation as fact counts grow.
type Worker struct {
	summarizer MemorySummarizer
	metrics    MemoryMetrics
	logger     *zap.Logger

	// Config
	factThreshold int           // minimum facts to trigger compression (default: 30)
	maxRunTime    time.Duration // max time per run (default: 5 minutes)
	interval      time.Duration // how often to run (default: 24 hours)
}

// WorkerConfig configures the compression worker.
type WorkerConfig struct {
	FactThreshold int
	MaxRunTime    time.Duration
	Interval      time.Duration
}

// DefaultWorkerConfig returns sensible defaults.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		FactThreshold: 30,
		MaxRunTime:    5 * time.Minute,
		Interval:      24 * time.Hour,
	}
}

// NewWorker creates a new memory compression worker.
func NewWorker(summarizer MemorySummarizer, metrics MemoryMetrics, cfg WorkerConfig, logger *zap.Logger) *Worker {
	if cfg.FactThreshold <= 0 {
		cfg.FactThreshold = 30
	}
	if cfg.MaxRunTime <= 0 {
		cfg.MaxRunTime = 5 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	return &Worker{
		summarizer:    summarizer,
		metrics:       metrics,
		logger:        logger,
		factThreshold: cfg.FactThreshold,
		maxRunTime:    cfg.MaxRunTime,
		interval:      cfg.Interval,
	}
}

// Run starts the compression worker loop. It blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Info("memory compression worker started",
		zap.Int("fact_threshold", w.factThreshold),
		zap.Duration("interval", w.interval))

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run once immediately on start.
	w.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory compression worker stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce executes a single compression pass. It finds users with more facts
// than the threshold who haven't been summarized recently, then compresses
// their facts into a narrative summary.
func (w *Worker) runOnce(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, w.maxRunTime)
	defer cancel()

	start := time.Now()
	w.logger.Info("memory compression pass starting")

	// Find users who need compression (haven't been summarized in 7+ days).
	userIDs, err := w.metrics.GetUsersNeedingCompression(runCtx, w.factThreshold, 7*24*time.Hour)
	if err != nil {
		w.logger.Error("failed to get users needing compression", zap.Error(err))
		return
	}

	if len(userIDs) == 0 {
		w.logger.Debug("no users need compression")
		return
	}

	w.logger.Info("found users needing compression", zap.Int("count", len(userIDs)))

	compressed := 0
	failed := 0
	for _, userID := range userIDs {
		select {
		case <-runCtx.Done():
			w.logger.Warn("compression run timed out", zap.Int("compressed", compressed))
			return
		default:
		}

		if err := w.summarizer.SummarizeMemory(runCtx, userID); err != nil {
			w.logger.Debug("failed to compress memory",
				zap.String("user_id", userID.String()),
				zap.Error(err))
			failed++
			continue
		}
		compressed++
	}

	elapsed := time.Since(start)
	w.logger.Info("memory compression pass complete",
		zap.Int("compressed", compressed),
		zap.Int("failed", failed),
		zap.Duration("elapsed", elapsed))
}
