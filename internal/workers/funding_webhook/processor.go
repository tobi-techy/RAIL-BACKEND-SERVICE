package funding_webhook

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

const (
	maxAttempts       = 5                // Increased from 3 to 5
	initialDelay      = 5 * time.Minute  // Initial retry delay
	maxDelay          = 30 * time.Minute // Maximum retry delay
	pollInterval      = 5 * time.Second
	backoffMultiplier = 2.0 // Exponential backoff multiplier
)

// DLQAlertService interface for alerting when deposits fail
type DLQAlertService interface {
	AlertDepositFailed(ctx context.Context, jobID uuid.UUID, txHash string, amount decimal.Decimal, chain string, failureReason string) error
}

// calculateNextRetry calculates the next retry time with exponential backoff
func calculateNextRetry(attempt int) time.Time {
	delay := initialDelay
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * backoffMultiplier)
		if delay >= maxDelay {
			delay = maxDelay
			break
		}
	}
	return time.Now().Add(delay)
}

// Processor handles webhook event processing with simple retry
type Processor struct {
	jobRepo     *repositories.FundingEventJobRepository
	fundingSvc  *funding.Service
	logger      *logger.Logger
	dlqAlertSvc DLQAlertService

	wg             sync.WaitGroup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// ProcessorConfig holds configuration for the webhook processor
type ProcessorConfig struct {
	WorkerCount             int
	PollInterval            time.Duration
	MaxAttempts             int
	CircuitBreakerThreshold int           // unused, kept for backward compat
	CircuitBreakerTimeout   time.Duration // unused, kept for backward compat
}

// DefaultProcessorConfig returns default configuration
func DefaultProcessorConfig() ProcessorConfig {
	return ProcessorConfig{
		WorkerCount:  2,
		PollInterval: pollInterval,
		MaxAttempts:  maxAttempts,
	}
}

// NewProcessor creates a new webhook processor
func NewProcessor(
	_ ProcessorConfig,
	jobRepo *repositories.FundingEventJobRepository,
	fundingSvc *funding.Service,
	_ interface{}, // auditSvc - no longer used directly
	logger *logger.Logger,
) (*Processor, error) {
	ctx, cancel := context.WithCancel(context.Background())
	return &Processor{
		jobRepo:        jobRepo,
		fundingSvc:     fundingSvc,
		logger:         logger,
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
	}, nil
}

// SetDLQAlertService sets the DLQ alert service for failed deposit notifications
func (p *Processor) SetDLQAlertService(alertSvc DLQAlertService) {
	p.dlqAlertSvc = alertSvc
}

// Start begins processing webhook events
func (p *Processor) Start(ctx context.Context) error {
	p.logger.Info("Starting webhook processor (simple retry mode)")

	p.wg.Add(1)
	go p.worker(ctx)

	return nil
}

// Shutdown gracefully stops the processor
func (p *Processor) Shutdown(timeout time.Duration) error {
	p.shutdownCancel()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// worker polls for pending jobs and processes them
func (p *Processor) worker(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.shutdownCtx.Done():
			return
		case <-ticker.C:
			p.processBatch(ctx)
		}
	}
}

// processBatch fetches and processes pending jobs
func (p *Processor) processBatch(ctx context.Context) {
	jobs, err := p.jobRepo.GetNextPendingJobs(ctx, 10)
	if err != nil {
		p.logger.Error("Failed to fetch pending jobs", "error", err)
		return
	}

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
			p.processJob(ctx, job)
		}
	}
}

// processJob processes a single job with simple retry (3 attempts, 5min apart)
func (p *Processor) processJob(ctx context.Context, job *entities.FundingEventJob) {
	p.logger.Info("Processing webhook job",
		"job_id", job.ID,
		"tx_hash", job.TxHash,
		"attempt", job.AttemptCount+1,
	)

	// Skip if max attempts exceeded
	if job.AttemptCount >= maxAttempts {
		job.Status = entities.JobStatusDLQ
		failureMsg := fmt.Sprintf("exceeded %d attempts", maxAttempts)
		job.FailureReason = &failureMsg
		if err := p.jobRepo.Update(ctx, job); err != nil {
			p.logger.Error("Failed to move job to DLQ", "error", err, "job_id", job.ID)
		}

		// Alert about failed deposit
		if p.dlqAlertSvc != nil {
			amount := job.Amount
			chain := string(job.Chain)
			if err := p.dlqAlertSvc.AlertDepositFailed(ctx, job.ID, job.TxHash, amount, chain, failureMsg); err != nil {
				p.logger.Error("Failed to send DLQ alert", "error", err, "job_id", job.ID)
			}
		}

		p.logger.Warn("Job moved to DLQ after max attempts - ALERT SENT",
			"job_id", job.ID, "tx_hash", job.TxHash)
		return
	}

	// Skip if not yet time to retry
	if job.NextRetryAt != nil && time.Now().Before(*job.NextRetryAt) {
		return
	}

	job.MarkProcessing()
	_ = p.jobRepo.Update(ctx, job)

	webhook := &entities.ChainDepositWebhook{
		Chain:     job.Chain,
		TxHash:    job.TxHash,
		Token:     job.Token,
		Amount:    job.Amount.String(),
		Address:   job.ToAddress,
		BlockTime: job.FirstSeenAt,
	}

	err := p.fundingSvc.ProcessChainDeposit(ctx, webhook)
	if err != nil {
		job.AttemptCount++
		job.Status = entities.JobStatusPending

		// Calculate next retry with exponential backoff
		nextRetry := calculateNextRetry(job.AttemptCount)
		job.NextRetryAt = &nextRetry

		errMsg := err.Error()
		job.FailureReason = &errMsg

		p.logger.Warn("Job processing failed, will retry with exponential backoff",
			"job_id", job.ID,
			"tx_hash", job.TxHash,
			"error", err,
			"attempt", job.AttemptCount,
			"next_retry", nextRetry,
		)
	} else {
		job.MarkCompleted()
		p.logger.Info("Job processed successfully",
			"job_id", job.ID, "tx_hash", job.TxHash)
	}

	if err := p.jobRepo.Update(ctx, job); err != nil {
		p.logger.Error("Failed to update job", "error", err, "job_id", job.ID)
	}
}
