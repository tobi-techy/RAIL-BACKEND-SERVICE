package kyc_sync

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// JobRepository defines persistence operations used by the sync worker.
type JobRepository interface {
	GetNextPendingJobs(ctx context.Context, limit int) ([]*entities.KYCSyncJob, error)
	Update(ctx context.Context, job *entities.KYCSyncJob) error
	GetDLQJobs(ctx context.Context, limit int) ([]*entities.KYCSyncJob, error)
}

// DLQAlertHandler is called when jobs are moved to DLQ for escalation.
type DLQAlertHandler interface {
	HandleDLQAlert(ctx context.Context, jobs []*entities.KYCSyncJob) error
}

// SumsubWebhookProcessor defines KYC processing logic executed per queued webhook.
type SumsubWebhookProcessor interface {
	ProcessSumsubWebhook(ctx context.Context, payload *entities.SumsubWebhookPayload) error
}

// DiditWebhookProcessor defines Didit KYC processing logic.
type DiditWebhookProcessor interface {
	ProcessDiditWebhook(ctx context.Context, payload *entities.DiditWebhookPayload) error
}

// ProviderRetryProcessor handles per-provider retry jobs for Bridge and Alpaca.
type ProviderRetryProcessor interface {
	RetryBridgeSync(ctx context.Context, payload []byte) error
	RetryBridgeDiditSync(ctx context.Context, payload []byte) error
	RetryAlpacaSync(ctx context.Context, payload []byte) error
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
	jobRepo           JobRepository
	processor         SumsubWebhookProcessor
	diditProcessor    DiditWebhookProcessor
	providerRetryProc ProviderRetryProcessor
	dlqAlertHandler   DLQAlertHandler
	logger            *zap.Logger
	checkInterval     time.Duration
	batchSize         int
	baseRetryDelay    time.Duration
	stopCh            chan struct{}
}

// NewWorker creates a new KYC sync worker.
func NewWorker(jobRepo JobRepository, processor SumsubWebhookProcessor, logger *zap.Logger, config *Config) *Worker {
	return NewWorkerWithRetry(jobRepo, processor, nil, logger, config)
}

// NewWorkerWithRetry creates a new KYC sync worker with per-provider retry support.
func NewWorkerWithRetry(jobRepo JobRepository, processor SumsubWebhookProcessor, providerRetryProc ProviderRetryProcessor, logger *zap.Logger, config *Config, diditProcessor ...DiditWebhookProcessor) *Worker {
	return newWorkerWithDLQAlert(jobRepo, processor, providerRetryProc, logger, config, nil, diditProcessor...)
}

func newWorkerWithDLQAlert(jobRepo JobRepository, processor SumsubWebhookProcessor, providerRetryProc ProviderRetryProcessor, logger *zap.Logger, config *Config, dlqHandler DLQAlertHandler, diditProcessor ...DiditWebhookProcessor) *Worker {
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

	w := &Worker{
		jobRepo:           jobRepo,
		processor:         processor,
		providerRetryProc: providerRetryProc,
		dlqAlertHandler:   dlqHandler,
		logger:            logger,
		checkInterval:     config.CheckInterval,
		batchSize:         config.BatchSize,
		baseRetryDelay:    config.BaseRetryDelay,
		stopCh:            make(chan struct{}),
	}
	if len(diditProcessor) > 0 && diditProcessor[0] != nil {
		w.diditProcessor = diditProcessor[0]
	}
	return w
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

	var err error
	if job.Provider != nil {
		switch *job.Provider {
		case "bridge":
			if w.providerRetryProc != nil {
				err = w.providerRetryProc.RetryBridgeSync(ctx, job.Payload)
			}
		case "bridge_didit":
			if w.providerRetryProc != nil {
				err = w.providerRetryProc.RetryBridgeDiditSync(ctx, job.Payload)
			}
		case "alpaca":
			if w.providerRetryProc != nil {
				err = w.providerRetryProc.RetryAlpacaSync(ctx, job.Payload)
			}
		default:
			err = w.dispatchWebhookJob(ctx, job)
		}
	} else {
		err = w.dispatchWebhookJob(ctx, job)
	}

	if err != nil {
		retryDelay := time.Duration(job.AttemptCount) * w.baseRetryDelay
		job.MarkFailed(err, retryDelay)
		w.logger.Warn("KYC sync job processing failed",
			zap.String("job_id", job.ID.String()),
			zap.String("provider", stringVal(job.Provider)),
			zap.Int("attempt_count", job.AttemptCount),
			zap.String("status", string(job.Status)),
			zap.Error(err))
		if job.Status == entities.KYCSyncJobStatusDLQ {
			w.logger.Error("KYC sync job moved to DLQ - ESCALATION REQUIRED",
				zap.String("job_id", job.ID.String()),
				zap.String("provider", stringVal(job.Provider)),
				zap.String("dedupe_key", job.DedupeKey),
				zap.Int("attempt_count", job.AttemptCount),
				zap.String("last_error", stringVal(job.LastError)),
				zap.Time("created_at", job.CreatedAt),
				zap.Duration("total_time", time.Since(job.CreatedAt)))

			// Invoke DLQ alert handler for escalation
			if w.dlqAlertHandler != nil {
				if alertErr := w.dlqAlertHandler.HandleDLQAlert(ctx, []*entities.KYCSyncJob{job}); alertErr != nil {
					w.logger.Error("Failed to send DLQ alert",
						zap.String("job_id", job.ID.String()),
						zap.Error(alertErr))
				}
			}
		}
	} else {
		job.MarkCompleted()
	}

	if updateErr := w.jobRepo.Update(ctx, job); updateErr != nil {
		w.logger.Error("Failed to update KYC sync job",
			zap.String("job_id", job.ID.String()),
			zap.Error(updateErr))
	}
}

func (w *Worker) processSumsubJob(ctx context.Context, job *entities.KYCSyncJob) error {
	var payload entities.SumsubWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return w.processor.ProcessSumsubWebhook(ctx, &payload)
}

func (w *Worker) processDiditJob(ctx context.Context, job *entities.KYCSyncJob) error {
	var payload entities.DiditWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return w.diditProcessor.ProcessDiditWebhook(ctx, &payload)
}

func (w *Worker) dispatchWebhookJob(ctx context.Context, job *entities.KYCSyncJob) error {
	if strings.HasPrefix(job.DedupeKey, "didit:") && w.diditProcessor != nil {
		return w.processDiditJob(ctx, job)
	}
	return w.processSumsubJob(ctx, job)
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
