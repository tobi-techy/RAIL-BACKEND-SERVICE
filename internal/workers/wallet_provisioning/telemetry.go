package walletprovisioning

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	// Tracer and meter names
	tracerName = "wallet-provisioning-worker"
	meterName  = "wallet-provisioning-worker"

	// Metric names
	metricJobsProcessed    = "wallet_provisioning_jobs_processed_total"
	metricJobDuration      = "wallet_provisioning_job_duration_seconds"
	metricJobRetries       = "wallet_provisioning_job_retries_total"
	metricJobErrors        = "wallet_provisioning_job_errors_total"
	metricWalletsCreated   = "wallet_provisioning_wallets_created_total"
	metricActiveJobs       = "wallet_provisioning_active_jobs"
	metricSchedulerRunning = "wallet_provisioning_scheduler_running"
)

// TelemetryWorker wraps the worker with OpenTelemetry instrumentation
type TelemetryWorker struct {
	worker *Worker
	tracer trace.Tracer
	logger *zap.Logger

	// Metrics
	jobsProcessedCounter  metric.Int64Counter
	jobDurationHistogram  metric.Float64Histogram
	jobRetriesCounter     metric.Int64Counter
	jobErrorsCounter      metric.Int64Counter
	walletsCreatedCounter metric.Int64Counter
	activeJobsGauge       metric.Int64UpDownCounter
}

// NewTelemetryWorker creates a new instrumented worker
func NewTelemetryWorker(worker *Worker, logger *zap.Logger) (*TelemetryWorker, error) {
	tracer := otel.Tracer(tracerName)
	meter := otel.Meter(meterName)

	// Create metrics
	jobsProcessedCounter, err := meter.Int64Counter(
		metricJobsProcessed,
		metric.WithDescription("Total number of wallet provisioning jobs processed"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	jobDurationHistogram, err := meter.Float64Histogram(
		metricJobDuration,
		metric.WithDescription("Duration of wallet provisioning job processing"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	jobRetriesCounter, err := meter.Int64Counter(
		metricJobRetries,
		metric.WithDescription("Total number of job retries"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	jobErrorsCounter, err := meter.Int64Counter(
		metricJobErrors,
		metric.WithDescription("Total number of job errors by type"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	walletsCreatedCounter, err := meter.Int64Counter(
		metricWalletsCreated,
		metric.WithDescription("Total number of wallets created by chain"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	activeJobsGauge, err := meter.Int64UpDownCounter(
		metricActiveJobs,
		metric.WithDescription("Number of currently active jobs"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	return &TelemetryWorker{
		worker:                worker,
		tracer:                tracer,
		logger:                logger,
		jobsProcessedCounter:  jobsProcessedCounter,
		jobDurationHistogram:  jobDurationHistogram,
		jobRetriesCounter:     jobRetriesCounter,
		jobErrorsCounter:      jobErrorsCounter,
		walletsCreatedCounter: walletsCreatedCounter,
		activeJobsGauge:       activeJobsGauge,
	}, nil
}

// ProcessJob processes a job with full telemetry instrumentation
func (tw *TelemetryWorker) ProcessJob(ctx context.Context, jobID uuid.UUID) error {
	// Start span for job processing
	ctx, span := tw.tracer.Start(ctx, "ProcessWalletProvisioningJob",
		trace.WithAttributes(
			attribute.String("job.id", jobID.String()),
			attribute.String("worker.type", "wallet_provisioning"),
		),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	defer span.End()

	// Increment active jobs
	tw.activeJobsGauge.Add(ctx, 1)
	defer tw.activeJobsGauge.Add(ctx, -1)

	startTime := time.Now()

	// Process the job
	err := tw.worker.ProcessJob(ctx, jobID)

	duration := time.Since(startTime).Seconds()

	// Determine result status
	status := "success"
	if err != nil {
		status = "failed"
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)

		// Classify and record error
		errorType := tw.worker.classifyError(err)
		tw.jobErrorsCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("error.type", errorType),
			),
		)

		span.SetAttributes(
			attribute.String("error.type", errorType),
		)
	} else {
		span.SetStatus(codes.Ok, "Job processed successfully")
	}

	// Record metrics
	tw.jobsProcessedCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("status", status),
		),
	)

	tw.jobDurationHistogram.Record(ctx, duration,
		metric.WithAttributes(
			attribute.String("status", status),
		),
	)

	// Add span attributes for completion
	span.SetAttributes(
		attribute.String("job.status", status),
		attribute.Float64("job.duration_seconds", duration),
	)

	tw.logger.Debug("Job telemetry recorded",
		zap.String("job_id", jobID.String()),
		zap.String("status", status),
		zap.Float64("duration_seconds", duration))

	return err
}

// RecordRetry records a retry attempt
func (tw *TelemetryWorker) RecordRetry(ctx context.Context, jobID uuid.UUID, attemptCount int) {
	tw.jobRetriesCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("job.id", jobID.String()),
			attribute.Int("attempt.count", attemptCount),
		),
	)
}

// RecordWalletCreated records a wallet creation
func (tw *TelemetryWorker) RecordWalletCreated(ctx context.Context, userID uuid.UUID, chain string) {
	tw.walletsCreatedCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("user.id", userID.String()),
			attribute.String("chain", chain),
		),
	)
}

// TraceWalletCreation creates a child span for wallet creation on a specific chain
func (tw *TelemetryWorker) TraceWalletCreation(ctx context.Context, userID uuid.UUID, chain string) (context.Context, trace.Span) {
	return tw.tracer.Start(ctx, "CreateWalletForChain",
		trace.WithAttributes(
			attribute.String("user.id", userID.String()),
			attribute.String("chain", chain),
		),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

// TraceCircleAPICall creates a span for Circle API calls
func (tw *TelemetryWorker) TraceCircleAPICall(ctx context.Context, operation string) (context.Context, trace.Span) {
	return tw.tracer.Start(ctx, "CircleAPI:"+operation,
		trace.WithAttributes(
			attribute.String("api.provider", "circle"),
			attribute.String("api.operation", operation),
		),
		trace.WithSpanKind(trace.SpanKindClient),
	)
}

// TelemetryScheduler wraps the scheduler with OpenTelemetry instrumentation
type TelemetryScheduler struct {
	scheduler             *Scheduler
	tracer                trace.Tracer
	logger                *zap.Logger
	schedulerRunningGauge metric.Int64UpDownCounter
}

// NewTelemetryScheduler creates a new instrumented scheduler
func NewTelemetryScheduler(scheduler *Scheduler, logger *zap.Logger) (*TelemetryScheduler, error) {
	tracer := otel.Tracer(tracerName)
	meter := otel.Meter(meterName)

	schedulerRunningGauge, err := meter.Int64UpDownCounter(
		metricSchedulerRunning,
		metric.WithDescription("Whether the scheduler is currently running (1=running, 0=stopped)"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, err
	}

	return &TelemetryScheduler{
		scheduler:             scheduler,
		tracer:                tracer,
		logger:                logger,
		schedulerRunningGauge: schedulerRunningGauge,
	}, nil
}

// Start starts the scheduler with telemetry
func (ts *TelemetryScheduler) Start() error {
	ctx := context.Background()

	// Create span for scheduler start
	_, span := ts.tracer.Start(ctx, "SchedulerStart",
		trace.WithAttributes(
			attribute.String("scheduler.type", "wallet_provisioning"),
		),
	)
	defer span.End()

	err := ts.scheduler.Start()
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return err
	}

	// Update running gauge
	ts.schedulerRunningGauge.Add(ctx, 1)

	span.SetStatus(codes.Ok, "Scheduler started successfully")
	ts.logger.Info("Scheduler started with telemetry enabled")

	return nil
}

// Stop stops the scheduler with telemetry
func (ts *TelemetryScheduler) Stop() error {
	ctx := context.Background()

	// Create span for scheduler stop
	_, span := ts.tracer.Start(ctx, "SchedulerStop",
		trace.WithAttributes(
			attribute.String("scheduler.type", "wallet_provisioning"),
		),
	)
	defer span.End()

	err := ts.scheduler.Stop()
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return err
	}

	// Update running gauge
	ts.schedulerRunningGauge.Add(ctx, -1)

	span.SetStatus(codes.Ok, "Scheduler stopped successfully")
	ts.logger.Info("Scheduler stopped with telemetry recorded")

	return nil
}

// IsRunning delegates to the underlying scheduler
func (ts *TelemetryScheduler) IsRunning() bool {
	return ts.scheduler.IsRunning()
}

// GetStatus delegates to the underlying scheduler
func (ts *TelemetryScheduler) GetStatus() SchedulerStatus {
	return ts.scheduler.GetStatus()
}

// Helper function to extract trace context for propagation
func ExtractTraceContext(ctx context.Context) map[string]string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}

	return map[string]string{
		"trace_id": spanContext.TraceID().String(),
		"span_id":  spanContext.SpanID().String(),
	}
}

// Helper function to add common attributes to spans
func AddCommonAttributes(span trace.Span, userID uuid.UUID, jobID uuid.UUID) {
	span.SetAttributes(
		attribute.String("user.id", userID.String()),
		attribute.String("job.id", jobID.String()),
		attribute.String("service.name", "wallet-provisioning"),
	)
}
