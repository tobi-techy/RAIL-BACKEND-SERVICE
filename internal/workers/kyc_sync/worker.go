package kyc_sync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// JobRepository defines persistence operations used by the sync worker.
type JobRepository interface {
	GetNextPendingJobs(ctx context.Context, limit int) ([]*entities.KYCSyncJob, error)
	Update(ctx context.Context, job *entities.KYCSyncJob) error
}

// SumsubWebhookProcessor defines KYC processing logic executed per queued webhook.
type SumsubWebhookProcessor interface {
	ProcessSumsubWebhook(ctx context.Context, payload *entities.SumsubWebhookPayload) error
}

// Config controls KYC sync worker behavior.
type Config struct {
	CheckInterval  time.Duration
	BatchSize      int
	BaseRetryDelay time.Duration
}

// DefaultConfig returns sensible defaults for processing queued KYC sync jobs.
func DefaultConfig() *Config {
	return &Config{
		CheckInterval:  5 * time.Second,
		BatchSize:      20,
		BaseRetryDelay: 2 * time.Minute,
	}
}

// Worker periodically processes queued Sumsub webhook jobs.
type Worker struct {
	jobRepo        JobRepository
	processor      SumsubWebhookProcessor
	logger         *zap.Logger
	checkInterval  time.Duration
	batchSize      int
	baseRetryDelay time.Duration
	stopCh         chan struct{}
}

// NewWorker creates a new KYC sync worker.
func NewWorker(jobRepo JobRepository, processor SumsubWebhookProcessor, logger *zap.Logger, config *Config) *Worker {
	if config == nil {
		config = DefaultConfig()
	}
	if config.CheckInterval <= 0 {
		config.CheckInterval = DefaultConfig().CheckInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultConfig().BatchSize
	}
	if config.BaseRetryDelay <= 0 {
		config.BaseRetryDelay = DefaultConfig().BaseRetryDelay
	}

	return &Worker{
		jobRepo:        jobRepo,
		processor:      processor,
		logger:         logger,
		checkInterval:  config.CheckInterval,
		batchSize:      config.BatchSize,
		baseRetryDelay: config.BaseRetryDelay,
		stopCh:         make(chan struct{}),
	}
}

// Start begins periodic polling and processing.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting KYC sync worker",
		zap.Duration("check_interval", w.checkInterval),
		zap.Int("batch_size", w.batchSize),
		zap.Duration("base_retry_delay", w.baseRetryDelay),
	)

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	w.run(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("KYC sync worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("KYC sync worker stopped")
			return
		case <-ticker.C:
			w.run(ctx)
		}
	}
}

// Stop signals the worker to stop.
func (w *Worker) Stop() {
	close(w.stopCh)
}

func (w *Worker) run(ctx context.Context) {
	if w.jobRepo == nil || w.processor == nil {
		return
	}

	jobs, err := w.jobRepo.GetNextPendingJobs(ctx, w.batchSize)
	if err != nil {
		w.logger.Error("Failed to fetch pending KYC sync jobs", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}

	for _, job := range jobs {
		w.processJob(ctx, job)
	}
}

func (w *Worker) processJob(ctx context.Context, job *entities.KYCSyncJob) {
	if job == nil {
		return
	}

	if job.NextRetryAt != nil && time.Now().Before(*job.NextRetryAt) {
		return
	}

	job.MarkProcessing()
	if err := w.jobRepo.Update(ctx, job); err != nil {
		w.logger.Error("Failed to set KYC sync job processing state",
			zap.String("job_id", job.ID.String()),
			zap.Error(err))
		return
	}

	var payload entities.SumsubWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		job.MarkFailed(err, 0)
		if updateErr := w.jobRepo.Update(ctx, job); updateErr != nil {
			w.logger.Error("Failed to update malformed KYC sync job",
				zap.String("job_id", job.ID.String()),
				zap.Error(updateErr))
		}
		return
	}

	err := w.processor.ProcessSumsubWebhook(ctx, &payload)
	if err != nil {
		retryDelay := time.Duration(job.AttemptCount) * w.baseRetryDelay
		job.MarkFailed(err, retryDelay)
		w.logger.Warn("KYC sync job processing failed",
			zap.String("job_id", job.ID.String()),
			zap.Int("attempt_count", job.AttemptCount),
			zap.String("status", string(job.Status)),
			zap.Error(err))
	} else {
		job.MarkCompleted()
	}

	if updateErr := w.jobRepo.Update(ctx, job); updateErr != nil {
		w.logger.Error("Failed to update KYC sync job",
			zap.String("job_id", job.ID.String()),
			zap.Error(updateErr))
	}
}
