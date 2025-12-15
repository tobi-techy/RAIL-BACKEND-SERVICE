package funding_webhook

import (
	"context"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// ProcessorConfig holds configuration for the webhook processor
type ProcessorConfig struct {
	WorkerCount             int
	PollInterval            time.Duration
	MaxAttempts             int
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   time.Duration
}

// DefaultProcessorConfig returns default configuration
func DefaultProcessorConfig() ProcessorConfig {
	return ProcessorConfig{
		WorkerCount:             5,
		PollInterval:            5 * time.Second,
		MaxAttempts:             5,
		CircuitBreakerThreshold: 5,
		CircuitBreakerTimeout:   60 * time.Second,
	}
}

// Processor handles webhook event processing with retry logic
type Processor struct {
	config     ProcessorConfig
	jobRepo    *repositories.FundingEventJobRepository
	fundingSvc *funding.Service
	auditSvc   *adapters.AuditService
	logger     *logger.Logger

	// Circuit breaker state
	circuitBreaker *CircuitBreaker

	// Metrics
	meter             metric.Meter
	processedCounter  metric.Int64Counter
	durationHistogram metric.Float64Histogram
	retryCounter      metric.Int64Counter
	dlqCounter        metric.Int64Counter

	// Worker management
	wg             sync.WaitGroup
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// NewProcessor creates a new webhook processor
func NewProcessor(
	config ProcessorConfig,
	jobRepo *repositories.FundingEventJobRepository,
	fundingSvc *funding.Service,
	auditSvc *adapters.AuditService,
	logger *logger.Logger,
) (*Processor, error) {
	ctx, cancel := context.WithCancel(context.Background())

	meter := otel.Meter("funding-webhook-processor")

	// Initialize metrics
	processedCounter, err := meter.Int64Counter(
		"webhook.processed.total",
		metric.WithDescription("Total number of webhooks processed"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create processed counter: %w", err)
	}

	durationHistogram, err := meter.Float64Histogram(
		"webhook.processing.duration.seconds",
		metric.WithDescription("Webhook processing duration in seconds"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create duration histogram: %w", err)
	}

	retryCounter, err := meter.Int64Counter(
		"webhook.retry.total",
		metric.WithDescription("Total number of webhook retries"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create retry counter: %w", err)
	}

	dlqCounter, err := meter.Int64Counter(
		"webhook.dlq.total",
		metric.WithDescription("Total number of webhooks moved to DLQ"),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DLQ counter: %w", err)
	}

	return &Processor{
		config:            config,
		jobRepo:           jobRepo,
		fundingSvc:        fundingSvc,
		auditSvc:          auditSvc,
		logger:            logger,
		circuitBreaker:    NewCircuitBreaker(config.CircuitBreakerThreshold, config.CircuitBreakerTimeout),
		meter:             meter,
		processedCounter:  processedCounter,
		durationHistogram: durationHistogram,
		retryCounter:      retryCounter,
		dlqCounter:        dlqCounter,
		shutdownCtx:       ctx,
		shutdownCancel:    cancel,
	}, nil
}

// Start begins processing webhook events
func (p *Processor) Start(ctx context.Context) error {
	p.logger.Info("Starting webhook processor", "worker_count", p.config.WorkerCount)

	// Start worker goroutines
	for i := 0; i < p.config.WorkerCount; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}

	// Start metrics reporter
	p.wg.Add(1)
	go p.metricsReporter(ctx)

	p.logger.Info("Webhook processor started successfully")
	return nil
}

// Shutdown gracefully stops the processor
func (p *Processor) Shutdown(timeout time.Duration) error {
	p.logger.Info("Shutting down webhook processor", "timeout", timeout)

	p.shutdownCancel()

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info("Webhook processor shutdown complete")
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// worker processes jobs continuously
func (p *Processor) worker(ctx context.Context, workerID int) {
	defer p.wg.Done()

	p.logger.Info("Worker started", "worker_id", workerID)
	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Worker stopping", "worker_id", workerID)
			return
		case <-p.shutdownCtx.Done():
			p.logger.Info("Worker stopping due to shutdown", "worker_id", workerID)
			return
		case <-ticker.C:
			p.processBatch(ctx, workerID)
		}
	}
}

// processBatch fetches and processes a batch of jobs
func (p *Processor) processBatch(ctx context.Context, workerID int) {
	// Check circuit breaker
	if !p.circuitBreaker.CanProcess() {
		p.logger.Warn("Circuit breaker open, skipping batch", "worker_id", workerID)
		return
	}

	// Fetch pending jobs
	jobs, err := p.jobRepo.GetNextPendingJobs(ctx, 10) // Process up to 10 jobs per batch
	if err != nil {
		p.logger.Error("Failed to fetch pending jobs", "error", err, "worker_id", workerID)
		return
	}

	if len(jobs) == 0 {
		return // No jobs to process
	}

	p.logger.Debug("Processing batch", "worker_id", workerID, "job_count", len(jobs))

	// Process each job
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		case <-p.shutdownCtx.Done():
			return
		default:
			p.processJob(ctx, job)
		}
	}
}

// processJob processes a single job with retry logic
func (p *Processor) processJob(ctx context.Context, job *entities.FundingEventJob) {
	startTime := time.Now()

	p.logger.Info("Processing job",
		"job_id", job.ID,
		"tx_hash", job.TxHash,
		"chain", job.Chain,
		"attempt", job.AttemptCount+1,
	)

	// Mark job as processing
	job.MarkProcessing()
	if err := p.jobRepo.Update(ctx, job); err != nil {
		p.logger.Error("Failed to mark job as processing", "error", err, "job_id", job.ID)
		return
	}

	// Audit log: start processing
	p.auditSvc.LogAction(ctx, nil, "process_webhook", "funding_event_job", map[string]interface{}{
		"job_id":  job.ID.String(),
		"tx_hash": job.TxHash,
		"chain":   job.Chain,
		"attempt": job.AttemptCount,
	}, nil)

	// Create webhook entity from job
	webhook := &entities.ChainDepositWebhook{
		Chain:     job.Chain,
		TxHash:    job.TxHash,
		Token:     job.Token,
		Amount:    job.Amount.String(),
		Address:   job.ToAddress,
		BlockTime: job.FirstSeenAt,
	}

	// Process the deposit
	err := p.fundingSvc.ProcessChainDeposit(ctx, webhook)

	duration := time.Since(startTime)

	// Add processing log entry
	logEntry := entities.ProcessingLogEntry{
		Timestamp:  time.Now(),
		Attempt:    job.AttemptCount,
		DurationMs: duration.Milliseconds(),
		Metadata: map[string]interface{}{
			"worker_id": "processor",
		},
	}

	if err != nil {
		// Categorize error
		errorType := p.categorizeError(err)
		logEntry.Status = "failed"
		errMsg := err.Error()
		logEntry.Error = &errMsg
		logEntry.ErrorType = &errorType

		job.AddProcessingLog(logEntry)

		// Calculate next retry delay
		retryDelay := job.GetRetryDelay()
		job.MarkFailed(err, errorType, retryDelay)

		// Update circuit breaker
		p.circuitBreaker.RecordFailure()

		p.logger.Warn("Job processing failed",
			"job_id", job.ID,
			"tx_hash", job.TxHash,
			"error", err,
			"error_type", errorType,
			"attempt", job.AttemptCount,
			"next_retry_at", job.NextRetryAt,
		)

		// Record metrics
		p.processedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("status", "failed"),
				attribute.String("chain", string(job.Chain)),
				attribute.String("error_type", string(errorType)),
			),
		)

		if job.Status == entities.JobStatusDLQ {
			p.dlqCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("chain", string(job.Chain)),
					attribute.String("error_type", string(errorType)),
				),
			)

			// Audit log: moved to DLQ
			p.auditSvc.LogAction(ctx, nil, "move_to_dlq", "funding_event_job", map[string]interface{}{
				"job_id":         job.ID.String(),
				"tx_hash":        job.TxHash,
				"failure_reason": job.FailureReason,
			}, nil)
		} else {
			p.retryCounter.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String("chain", string(job.Chain)),
				),
			)
		}
	} else {
		// Success
		logEntry.Status = "completed"
		job.AddProcessingLog(logEntry)
		job.MarkCompleted()

		// Update circuit breaker
		p.circuitBreaker.RecordSuccess()

		p.logger.Info("Job processed successfully",
			"job_id", job.ID,
			"tx_hash", job.TxHash,
			"duration", duration,
		)

		// Record metrics
		p.processedCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("status", "completed"),
				attribute.String("chain", string(job.Chain)),
			),
		)

		// Audit log: completed
		p.auditSvc.LogAction(ctx, nil, "complete_webhook", "funding_event_job", map[string]interface{}{
			"job_id":      job.ID.String(),
			"tx_hash":     job.TxHash,
			"duration_ms": duration.Milliseconds(),
		}, nil)
	}

	// Record processing duration
	p.durationHistogram.Record(ctx, duration.Seconds(),
		metric.WithAttributes(
			attribute.String("chain", string(job.Chain)),
			attribute.String("status", string(job.Status)),
		),
	)

	// Update job in database
	if err := p.jobRepo.Update(ctx, job); err != nil {
		p.logger.Error("Failed to update job", "error", err, "job_id", job.ID)
	}
}

// categorizeError determines if an error is transient or permanent
func (p *Processor) categorizeError(err error) entities.FundingEventErrorType {
	if err == nil {
		return entities.ErrorTypeUnknown
	}

	errMsg := strings.ToLower(err.Error())

	// Validation errors (4xx equivalent) - permanent
	if strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "validation") ||
		strings.Contains(errMsg, "malformed") ||
		strings.Contains(errMsg, "bad request") ||
		strings.Contains(errMsg, "not found") && !strings.Contains(errMsg, "wallet not found") {
		return entities.ErrorTypePermanent
	}

	// RPC/Chain errors - transient but needs longer backoff
	if strings.Contains(errMsg, "rpc") ||
		strings.Contains(errMsg, "chain") ||
		strings.Contains(errMsg, "node") ||
		strings.Contains(errMsg, "provider") {
		return entities.ErrorTypeRPCFailure
	}

	// Network/timeout errors - transient
	if strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "network") ||
		strings.Contains(errMsg, "temporary") ||
		strings.Contains(errMsg, "unavailable") ||
		strings.Contains(errMsg, "too many requests") {
		return entities.ErrorTypeTransient
	}

	// HTTP status code errors
	var httpErr interface{ StatusCode() int }
	if errors.As(err, &httpErr) {
		statusCode := httpErr.StatusCode()
		if statusCode >= 500 {
			return entities.ErrorTypeTransient // 5xx errors are transient
		}
		if statusCode >= 400 && statusCode < 500 {
			if statusCode == http.StatusTooManyRequests || statusCode == http.StatusRequestTimeout {
				return entities.ErrorTypeTransient
			}
			return entities.ErrorTypePermanent // 4xx errors are permanent
		}
	}

	// Default to transient (safer to retry)
	return entities.ErrorTypeTransient
}

// metricsReporter periodically reports metrics
func (p *Processor) metricsReporter(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.shutdownCtx.Done():
			return
		case <-ticker.C:
			p.reportMetrics(ctx)
		}
	}
}

// reportMetrics fetches and reports current metrics
func (p *Processor) reportMetrics(ctx context.Context) {
	metrics, err := p.jobRepo.GetMetrics(ctx)
	if err != nil {
		p.logger.Error("Failed to get metrics", "error", err)
		return
	}

	p.logger.Info("Webhook processing metrics",
		"total_received", metrics.TotalReceived,
		"total_processed", metrics.TotalProcessed,
		"total_failed", metrics.TotalFailed,
		"success_rate", fmt.Sprintf("%.2f%%", metrics.SuccessRate),
		"avg_retry_count", fmt.Sprintf("%.2f", metrics.AverageRetryCount),
		"avg_latency", metrics.AverageLatency,
		"dlq_depth", metrics.DLQDepth,
		"pending_count", metrics.PendingCount,
	)

	// Circuit breaker status
	if !p.circuitBreaker.CanProcess() {
		p.logger.Warn("Circuit breaker is OPEN",
			"failure_count", p.circuitBreaker.failureCount,
			"opened_at", p.circuitBreaker.openedAt,
		)
	}
}

// CircuitBreaker implements a simple circuit breaker pattern
type CircuitBreaker struct {
	threshold    int
	timeout      time.Duration
	failureCount int
	openedAt     *time.Time
	mu           sync.RWMutex
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
	}
}

// CanProcess checks if processing is allowed
func (cb *CircuitBreaker) CanProcess() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.openedAt == nil {
		return true
	}

	// Check if timeout has passed
	if time.Since(*cb.openedAt) > cb.timeout {
		return true // Try to recover
	}

	return false
}

// RecordFailure records a processing failure
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++

	if cb.failureCount >= cb.threshold && cb.openedAt == nil {
		now := time.Now()
		cb.openedAt = &now
	}
}

// RecordSuccess records a processing success
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Reset on success
	if cb.openedAt != nil && time.Since(*cb.openedAt) > cb.timeout {
		cb.failureCount = 0
		cb.openedAt = nil
	} else if cb.failureCount > 0 {
		cb.failureCount--
	}
}
