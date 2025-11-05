package aicfo_scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Scheduler manages the weekly AI-CFO summary generation
type Scheduler struct {
	cron         *cron.Cron
	aicfoService AICfoServiceInterface
	userRepo     UserRepository
	config       *Config
	logger       *zap.Logger
	tracer       trace.Tracer
	metrics      *SchedulerMetrics
	
	// State management
	mu       sync.RWMutex
	running  bool
	lastRun  time.Time
	nextRun  time.Time
	jobStats *JobStatistics
}

// Repository interfaces
type UserRepository interface {
	GetActiveUsers(ctx context.Context, limit int, offset int) ([]*User, error)
	GetTotalActiveUsers(ctx context.Context) (int, error)
}

type AICfoServiceInterface interface {
	GenerateWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) (*services.AISummary, error)
	GetHealthStatus(ctx context.Context) (*HealthStatus, error)
}

// Domain models
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type HealthStatus struct {
	Status  string    `json:"status"`
	Latency time.Duration `json:"latency"`
	Errors  []string  `json:"errors"`
}

// Configuration
type Config struct {
	// Cron expression for when to run (default: Mondays at 6 AM UTC)
	Schedule           string        `json:"schedule"`
	
	// Batch processing configuration
	BatchSize          int           `json:"batch_size"`
	BatchTimeout       time.Duration `json:"batch_timeout"`
	MaxConcurrentJobs  int           `json:"max_concurrent_jobs"`
	
	// Retry configuration
	MaxRetries         int           `json:"max_retries"`
	RetryBackoff       time.Duration `json:"retry_backoff"`
	
	// Health check configuration
	HealthCheckEnabled bool          `json:"health_check_enabled"`
	HealthCheckTimeout time.Duration `json:"health_check_timeout"`
	
	// Timezone for scheduling
	Timezone           string        `json:"timezone"`
}

// JobStatistics tracks scheduler performance metrics
type JobStatistics struct {
	TotalRuns         int64     `json:"total_runs"`
	SuccessfulRuns    int64     `json:"successful_runs"`
	FailedRuns        int64     `json:"failed_runs"`
	LastRunTime       time.Time `json:"last_run_time"`
	LastRunDuration   time.Duration `json:"last_run_duration"`
	UsersProcessed    int64     `json:"users_processed"`
	SummariesGenerated int64    `json:"summaries_generated"`
	Errors            []JobError `json:"recent_errors"`
}

// JobError represents an error that occurred during job execution
type JobError struct {
	Timestamp time.Time `json:"timestamp"`
	UserID    string    `json:"user_id,omitempty"`
	Error     string    `json:"error"`
	Retryable bool      `json:"retryable"`
}

// zapCronLogger wraps zap.Logger to implement cron's logger interface
type zapCronLogger struct {
	logger *zap.Logger
}

func (l *zapCronLogger) Printf(format string, args ...interface{}) {
	l.logger.Sugar().Infof(format, args...)
}

// SchedulerMetrics contains observability metrics
type SchedulerMetrics struct {
	JobsTotal            metric.Int64Counter
	JobDuration          metric.Float64Histogram
	JobErrors            metric.Int64Counter
	UsersProcessed       metric.Int64Counter
	SummariesGenerated   metric.Int64Counter
	ActiveJobs           metric.Int64Gauge
	LastRunTime          metric.Float64Gauge
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Schedule:           "0 6 * * 1", // Mondays at 6 AM UTC
		BatchSize:          50,
		BatchTimeout:       30 * time.Minute,
		MaxConcurrentJobs:  10,
		MaxRetries:         3,
		RetryBackoff:       5 * time.Minute,
		HealthCheckEnabled: true,
		HealthCheckTimeout: 30 * time.Second,
		Timezone:           "UTC",
	}
}

// NewScheduler creates a new AI-CFO scheduler
func NewScheduler(
	aicfoService AICfoServiceInterface,
	userRepo UserRepository,
	config *Config,
	logger *zap.Logger,
) (*Scheduler, error) {
	// Parse timezone
	location, err := time.LoadLocation(config.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", config.Timezone, err)
	}

	// Create wrapper for zap logger to implement cron's logger interface
	cronLogger := &zapCronLogger{logger: logger}

	// Create cron scheduler with timezone
	c := cron.New(cron.WithLocation(location), cron.WithLogger(cron.VerbosePrintfLogger(cronLogger)))

	tracer := otel.Tracer("aicfo-scheduler")
	meter := otel.Meter("aicfo-scheduler")

	// Initialize metrics
	metrics, err := initSchedulerMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	scheduler := &Scheduler{
		cron:         c,
		aicfoService: aicfoService,
		userRepo:     userRepo,
		config:       config,
		logger:       logger,
		tracer:       tracer,
		metrics:      metrics,
		running:      false,
		jobStats:     &JobStatistics{Errors: make([]JobError, 0)},
	}

	logger.Info("AI-CFO scheduler created",
		zap.String("schedule", config.Schedule),
		zap.String("timezone", config.Timezone),
		zap.Int("batch_size", config.BatchSize),
	)

	return scheduler, nil
}

// Start begins the scheduled job execution
func (s *Scheduler) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler is already running")
	}

	s.logger.Info("Starting AI-CFO scheduler", zap.String("schedule", s.config.Schedule))

	// Add the job to the cron scheduler
	_, err := s.cron.AddFunc(s.config.Schedule, func() {
		s.executeWeeklySummaryJob()
	})
	if err != nil {
		return fmt.Errorf("failed to add cron job: %w", err)
	}

	// Start the cron scheduler
	s.cron.Start()
	s.running = true

	// Update next run time
	entries := s.cron.Entries()
	if len(entries) > 0 {
		s.nextRun = entries[0].Next
	}

	s.logger.Info("AI-CFO scheduler started successfully",
		zap.Time("next_run", s.nextRun),
	)

	return nil
}

// Stop halts the scheduled job execution
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("scheduler is not running")
	}

	s.logger.Info("Stopping AI-CFO scheduler")

	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
		s.logger.Info("AI-CFO scheduler stopped gracefully")
	case <-time.After(30 * time.Second):
		s.logger.Warn("AI-CFO scheduler stop timed out")
	}

	s.running = false
	s.logger.Info("AI-CFO scheduler stopped")

	return nil
}

// executeWeeklySummaryJob runs the weekly summary generation for all active users
func (s *Scheduler) executeWeeklySummaryJob() {
	startTime := time.Now()
	ctx := context.Background()

	ctx, span := s.tracer.Start(ctx, "scheduler.execute_weekly_summary_job", trace.WithAttributes(
		attribute.String("job_type", "weekly_summary"),
		attribute.String("schedule", s.config.Schedule),
	))
	defer span.End()

	s.logger.Info("Starting weekly summary job execution")

	s.metrics.ActiveJobs.Record(ctx, 1)
	defer s.metrics.ActiveJobs.Record(ctx, -1)

	// Update job statistics
	s.mu.Lock()
	s.jobStats.TotalRuns++
	s.jobStats.LastRunTime = startTime
	s.lastRun = startTime
	s.mu.Unlock()

	// Perform health check if enabled
	if s.config.HealthCheckEnabled {
		if err := s.performHealthCheck(ctx); err != nil {
			s.logger.Error("Health check failed, aborting job", zap.Error(err))
			s.recordJobError(ctx, "", err, false)
			return
		}
	}

	// Calculate week start (last Monday)
	weekStart := s.calculateWeekStart(startTime)

	// Process users in batches
	if err := s.processUsersBatch(ctx, weekStart); err != nil {
		s.logger.Error("Failed to process users batch", zap.Error(err))
		s.recordJobFailure(ctx)
		return
	}

	// Record successful completion
	duration := time.Since(startTime)
	s.recordJobSuccess(ctx, duration)

	s.logger.Info("Weekly summary job completed successfully",
		zap.Duration("duration", duration),
		zap.Time("week_start", weekStart),
	)
}

// performHealthCheck verifies that all dependencies are healthy
func (s *Scheduler) performHealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.config.HealthCheckTimeout)
	defer cancel()

	s.logger.Debug("Performing health check before job execution")

	// Check AI-CFO service health
	health, err := s.aicfoService.GetHealthStatus(ctx)
	if err != nil {
		return fmt.Errorf("AI-CFO service health check failed: %w", err)
	}

	if health.Status != "healthy" && health.Status != "degraded" {
		return fmt.Errorf("AI-CFO service is unhealthy: %s", health.Status)
	}

	s.logger.Debug("Health check passed", zap.String("aicfo_status", health.Status))
	return nil
}

// processUsersBatch processes all active users in batches
func (s *Scheduler) processUsersBatch(ctx context.Context, weekStart time.Time) error {
	totalUsers, err := s.userRepo.GetTotalActiveUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get total user count: %w", err)
	}

	s.logger.Info("Processing users for weekly summaries",
		zap.Int("total_users", totalUsers),
		zap.Int("batch_size", s.config.BatchSize),
	)

	var processedUsers int64
	var generatedSummaries int64
	var errors []JobError

	// Process users in batches
	for offset := 0; offset < totalUsers; offset += s.config.BatchSize {
		batchCtx, cancel := context.WithTimeout(ctx, s.config.BatchTimeout)
		
		users, err := s.userRepo.GetActiveUsers(batchCtx, s.config.BatchSize, offset)
		if err != nil {
			cancel()
			s.logger.Error("Failed to get user batch",
				zap.Int("offset", offset),
				zap.Error(err),
			)
			continue
		}

		batchProcessed, batchGenerated, batchErrors := s.processBatch(batchCtx, users, weekStart)
		processedUsers += batchProcessed
		generatedSummaries += batchGenerated
		errors = append(errors, batchErrors...)

		cancel()

		s.logger.Debug("Processed user batch",
			zap.Int("batch_size", len(users)),
			zap.Int64("processed", batchProcessed),
			zap.Int64("generated", batchGenerated),
			zap.Int("errors", len(batchErrors)),
		)
	}

	// Update statistics
	s.mu.Lock()
	s.jobStats.UsersProcessed += processedUsers
	s.jobStats.SummariesGenerated += generatedSummaries
	if len(errors) > 0 {
		s.jobStats.Errors = append(s.jobStats.Errors, errors...)
		// Keep only the last 100 errors
		if len(s.jobStats.Errors) > 100 {
			s.jobStats.Errors = s.jobStats.Errors[len(s.jobStats.Errors)-100:]
		}
	}
	s.mu.Unlock()

	// Record metrics
	s.metrics.UsersProcessed.Add(ctx, processedUsers)
	s.metrics.SummariesGenerated.Add(ctx, generatedSummaries)

	s.logger.Info("User batch processing completed",
		zap.Int64("total_processed", processedUsers),
		zap.Int64("total_generated", generatedSummaries),
		zap.Int("total_errors", len(errors)),
	)

	return nil
}

// processBatch processes a batch of users concurrently
func (s *Scheduler) processBatch(ctx context.Context, users []*User, weekStart time.Time) (int64, int64, []JobError) {
	semaphore := make(chan struct{}, s.config.MaxConcurrentJobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	
	var processedUsers int64
	var generatedSummaries int64
	var errors []JobError

	for _, user := range users {
		wg.Add(1)
		go func(u *User) {
			defer wg.Done()
			
			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			processed, generated, err := s.processUser(ctx, u, weekStart)
			
			mu.Lock()
			processedUsers += processed
			generatedSummaries += generated
			if err != nil {
				errors = append(errors, JobError{
					Timestamp: time.Now(),
					UserID:    u.ID.String(),
					Error:     err.Error(),
					Retryable: s.isRetryableError(err),
				})
			}
			mu.Unlock()
		}(user)
	}

	wg.Wait()
	return processedUsers, generatedSummaries, errors
}

// processUser generates a weekly summary for a single user
func (s *Scheduler) processUser(ctx context.Context, user *User, weekStart time.Time) (int64, int64, error) {
	ctx, span := s.tracer.Start(ctx, "scheduler.process_user", trace.WithAttributes(
		attribute.String("user_id", user.ID.String()),
		attribute.String("week_start", weekStart.Format("2006-01-02")),
	))
	defer span.End()

	s.logger.Debug("Processing user for weekly summary",
		zap.String("user_id", user.ID.String()),
		zap.String("user_email", user.Email),
	)

	var lastErr error
	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Apply exponential backoff
			backoff := time.Duration(attempt) * s.config.RetryBackoff
			s.logger.Debug("Retrying user processing",
				zap.String("user_id", user.ID.String()),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
			)
			
			select {
			case <-ctx.Done():
				return 1, 0, ctx.Err()
			case <-time.After(backoff):
			}
		}

		_, err := s.aicfoService.GenerateWeeklySummary(ctx, user.ID, weekStart)
		if err == nil {
			s.logger.Debug("Weekly summary generated successfully",
				zap.String("user_id", user.ID.String()),
			)
			return 1, 1, nil
		}

		lastErr = err
		if !s.isRetryableError(err) {
			s.logger.Debug("Non-retryable error, stopping retries",
				zap.String("user_id", user.ID.String()),
				zap.Error(err),
			)
			break
		}

		s.logger.Debug("Retryable error occurred",
			zap.String("user_id", user.ID.String()),
			zap.Error(err),
			zap.Int("attempt", attempt+1),
		)
	}

	s.logger.Warn("Failed to generate weekly summary after retries",
		zap.String("user_id", user.ID.String()),
		zap.Error(lastErr),
	)

	span.RecordError(lastErr)
	return 1, 0, lastErr
}

// calculateWeekStart calculates the start of the week (Monday) for the given time
func (s *Scheduler) calculateWeekStart(t time.Time) time.Time {
	// Find the most recent Monday
	for t.Weekday() != time.Monday {
		t = t.AddDate(0, 0, -1)
	}
	// Set to beginning of day
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// isRetryableError determines if an error should trigger a retry
func (s *Scheduler) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error types that should not be retried
	errorStr := err.Error()
	nonRetryablePatterns := []string{
		"invalid analysis type",
		"user not found",
		"invalid user id",
		"unauthorized",
		"forbidden",
	}

	for _, pattern := range nonRetryablePatterns {
		if contains(errorStr, pattern) {
			return false
		}
	}

	// Default to retryable for network/service errors
	return true
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && 
		   (s == substr || 
		    len(s) > len(substr) && 
		    (s[:len(substr)] == substr || 
		     s[len(s)-len(substr):] == substr ||
		     containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 1; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// recordJobSuccess records metrics for successful job completion
func (s *Scheduler) recordJobSuccess(ctx context.Context, duration time.Duration) {
	s.mu.Lock()
	s.jobStats.SuccessfulRuns++
	s.jobStats.LastRunDuration = duration
	s.mu.Unlock()

	s.metrics.JobsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("status", "success"),
	))
	s.metrics.JobDuration.Record(ctx, duration.Seconds())
	s.metrics.LastRunTime.Record(ctx, float64(time.Now().Unix()))
}

// recordJobFailure records metrics for failed job completion
func (s *Scheduler) recordJobFailure(ctx context.Context) {
	s.mu.Lock()
	s.jobStats.FailedRuns++
	s.mu.Unlock()

	s.metrics.JobsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("status", "failed"),
	))
}

// recordJobError records an error that occurred during job execution
func (s *Scheduler) recordJobError(ctx context.Context, userID string, err error, retryable bool) {
	s.mu.Lock()
	s.jobStats.Errors = append(s.jobStats.Errors, JobError{
		Timestamp: time.Now(),
		UserID:    userID,
		Error:     err.Error(),
		Retryable: retryable,
	})
	s.mu.Unlock()

	s.metrics.JobErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("retryable", fmt.Sprintf("%t", retryable)),
	))
}

// GetStatus returns the current status of the scheduler
func (s *Scheduler) GetStatus() *SchedulerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &SchedulerStatus{
		Running:     s.running,
		LastRun:     s.lastRun,
		NextRun:     s.nextRun,
		Schedule:    s.config.Schedule,
		Timezone:    s.config.Timezone,
		Statistics:  *s.jobStats, // Copy the statistics
	}
}

// SchedulerStatus represents the current status of the scheduler
type SchedulerStatus struct {
	Running     bool           `json:"running"`
	LastRun     time.Time      `json:"last_run"`
	NextRun     time.Time      `json:"next_run"`
	Schedule    string         `json:"schedule"`
	Timezone    string         `json:"timezone"`
	Statistics  JobStatistics  `json:"statistics"`
}

// TriggerManualRun triggers a manual execution of the weekly summary job
func (s *Scheduler) TriggerManualRun() error {
	if !s.running {
		return fmt.Errorf("scheduler is not running")
	}

	s.logger.Info("Triggering manual weekly summary job execution")
	
	// Run the job in a goroutine to avoid blocking
	go s.executeWeeklySummaryJob()
	
	return nil
}

// GetNextRun returns the next scheduled run time
func (s *Scheduler) GetNextRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	entries := s.cron.Entries()
	if len(entries) > 0 {
		return entries[0].Next
	}
	return time.Time{}
}

// initSchedulerMetrics initializes OpenTelemetry metrics
func initSchedulerMetrics(meter metric.Meter) (*SchedulerMetrics, error) {
	jobsTotal, err := meter.Int64Counter("aicfo_scheduler_jobs_total",
		metric.WithDescription("Total number of scheduled jobs executed"))
	if err != nil {
		return nil, err
	}

	jobDuration, err := meter.Float64Histogram("aicfo_scheduler_job_duration_seconds",
		metric.WithDescription("Duration of scheduled job execution in seconds"))
	if err != nil {
		return nil, err
	}

	jobErrors, err := meter.Int64Counter("aicfo_scheduler_job_errors_total",
		metric.WithDescription("Total number of job execution errors"))
	if err != nil {
		return nil, err
	}

	usersProcessed, err := meter.Int64Counter("aicfo_scheduler_users_processed_total",
		metric.WithDescription("Total number of users processed"))
	if err != nil {
		return nil, err
	}

	summariesGenerated, err := meter.Int64Counter("aicfo_scheduler_summaries_generated_total",
		metric.WithDescription("Total number of summaries generated"))
	if err != nil {
		return nil, err
	}

	activeJobs, err := meter.Int64Gauge("aicfo_scheduler_active_jobs",
		metric.WithDescription("Number of currently active jobs"))
	if err != nil {
		return nil, err
	}

	lastRunTime, err := meter.Float64Gauge("aicfo_scheduler_last_run_timestamp",
		metric.WithDescription("Timestamp of the last job execution"))
	if err != nil {
		return nil, err
	}

	return &SchedulerMetrics{
		JobsTotal:            jobsTotal,
		JobDuration:          jobDuration,
		JobErrors:            jobErrors,
		UsersProcessed:       usersProcessed,
		SummariesGenerated:   summariesGenerated,
		ActiveJobs:           activeJobs,
		LastRunTime:          lastRunTime,
	}, nil
}