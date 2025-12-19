package onboardingprocessor

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// Dependencies interfaces for the worker
type OnboardingJobRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.OnboardingJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.OnboardingJob, error)
	GetPendingJobs(ctx context.Context, limit int) ([]*entities.OnboardingJob, error)
	Update(ctx context.Context, job *entities.OnboardingJob) error
}

type OnboardingService interface {
	StartOnboarding(ctx context.Context, userID uuid.UUID, userEmail, userPhone string) error
	GetOnboardingStatus(ctx context.Context, userID uuid.UUID) (*entities.OnboardingStatusResponse, error)
}

type WalletService interface {
	CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error
	GetWalletStatus(ctx context.Context, userID uuid.UUID) (*entities.WalletStatusResponse, error)
}

type UserRepository interface {
	GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
	Update(ctx context.Context, user *entities.UserProfile) error
}

// Metrics tracks worker performance metrics
type Metrics struct {
	TotalJobsProcessed int64
	SuccessfulJobs     int64
	FailedJobs         int64
	TotalRetries       int64
	AverageDuration    time.Duration
	LastProcessedAt    time.Time
	ErrorsByType       map[string]int64
}

// Config holds worker configuration
type Config struct {
	MaxAttempts         int
	BaseBackoffDuration time.Duration
	MaxBackoffDuration  time.Duration
	JitterFactor        float64
	MaxConcurrentJobs   int
	PollInterval        time.Duration
}

// DefaultConfig returns default worker configuration
func DefaultConfig() Config {
	return Config{
		MaxAttempts:         5,
		BaseBackoffDuration: 1 * time.Minute,
		MaxBackoffDuration:  30 * time.Minute,
		JitterFactor:        0.1,
		MaxConcurrentJobs:   5,
		PollInterval:        30 * time.Second,
	}
}

// Worker handles onboarding jobs with retries and audit logging
type Worker struct {
	jobRepo           OnboardingJobRepository
	onboardingService OnboardingService
	walletService     WalletService
	userRepo          UserRepository
	config            Config
	logger            *zap.Logger
	metrics           *Metrics
	stopChan          chan struct{}
	jobProcessingChan chan uuid.UUID
}

// NewWorker creates a new onboarding worker
func NewWorker(
	jobRepo OnboardingJobRepository,
	onboardingService OnboardingService,
	walletService WalletService,
	userRepo UserRepository,
	config Config,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		jobRepo:           jobRepo,
		onboardingService: onboardingService,
		walletService:     walletService,
		userRepo:          userRepo,
		config:            config,
		logger:            logger,
		metrics: &Metrics{
			ErrorsByType: make(map[string]int64),
		},
		stopChan:          make(chan struct{}),
		jobProcessingChan: make(chan uuid.UUID, config.MaxConcurrentJobs),
	}
}

// Start starts the worker scheduler
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting onboarding worker",
		zap.Int("max_concurrent", w.config.MaxConcurrentJobs),
		zap.Duration("poll_interval", w.config.PollInterval))

	// Start job processor goroutines
	for i := 0; i < w.config.MaxConcurrentJobs; i++ {
		go w.jobProcessor(ctx, i)
	}

	// Start scheduler
	go w.scheduler(ctx)

	w.logger.Info("Onboarding worker started successfully")
}

// Stop stops the worker
func (w *Worker) Stop() {
	w.logger.Info("Stopping onboarding worker")
	close(w.stopChan)
}

// ProcessJob processes a single onboarding job with idempotency
func (w *Worker) ProcessJob(ctx context.Context, jobID uuid.UUID) error {
	startTime := time.Now()
	w.logger.Info("Processing onboarding job",
		zap.String("job_id", jobID.String()),
		zap.Time("started_at", startTime))

	// Get the job
	job, err := w.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		w.logger.Error("Failed to get onboarding job", zap.Error(err), zap.String("job_id", jobID.String()))
		return fmt.Errorf("failed to get onboarding job: %w", err)
	}

	// Check if job should be processed
	if !job.IsEligibleForProcessing() {
		w.logger.Info("Job not eligible for processing",
			zap.String("job_id", jobID.String()),
			zap.String("status", string(job.Status)))
		return nil
	}

	// Mark job as started
	job.MarkStarted()
	if err := w.jobRepo.Update(ctx, job); err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	// Process the job
	err = w.processJobInternal(ctx, job)

	// Update metrics
	duration := time.Since(startTime)
	w.updateMetrics(err, duration)

	// Handle result
	if err != nil {
		w.handleJobFailure(ctx, job, err)
	} else {
		w.handleJobSuccess(ctx, job)
	}

	// Final update
	if updateErr := w.jobRepo.Update(ctx, job); updateErr != nil {
		w.logger.Error("Failed to update job after processing", zap.Error(updateErr))
	}

	w.logger.Info("Finished processing onboarding job",
		zap.String("job_id", jobID.String()),
		zap.String("final_status", string(job.Status)),
		zap.Duration("duration", duration),
		zap.Error(err))

	return err
}

// processJobInternal performs the actual onboarding logic
func (w *Worker) processJobInternal(ctx context.Context, job *entities.OnboardingJob) error {
	w.logger.Info("Starting onboarding processing for user",
		zap.String("user_id", job.UserID.String()),
		zap.String("job_type", string(job.JobType)))

	// Get user details
	user, err := w.userRepo.GetUserEntityByID(ctx, job.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Process based on job type
	switch job.JobType {
	case entities.OnboardingJobTypeFullOnboarding:
		return w.processFullOnboarding(ctx, job, user)
	case entities.OnboardingJobTypeKYCOnly:
		return w.processKYCOnly(ctx, job, user)
	case entities.OnboardingJobTypeWalletOnly:
		return w.processWalletOnly(ctx, job, user)
	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}
}

// processFullOnboarding processes full onboarding (KYC + wallet)
func (w *Worker) processFullOnboarding(ctx context.Context, job *entities.OnboardingJob, user *entities.User) error {
	w.logger.Info("Processing full onboarding",
		zap.String("user_id", user.ID.String()))

	// Step 1: Start KYC process
	if err := w.startKYCProcess(ctx, job, user); err != nil {
		return fmt.Errorf("failed to start KYC process: %w", err)
	}

	// Step 2: Create wallets
	if err := w.createWallets(ctx, job, user); err != nil {
		return fmt.Errorf("failed to create wallets: %w", err)
	}

	w.logger.Info("Full onboarding completed successfully",
		zap.String("user_id", user.ID.String()))

	return nil
}

// processKYCOnly processes KYC-only onboarding
func (w *Worker) processKYCOnly(ctx context.Context, job *entities.OnboardingJob, user *entities.User) error {
	w.logger.Info("Processing KYC-only onboarding",
		zap.String("user_id", user.ID.String()))

	return w.startKYCProcess(ctx, job, user)
}

// processWalletOnly processes wallet-only onboarding
func (w *Worker) processWalletOnly(ctx context.Context, job *entities.OnboardingJob, user *entities.User) error {
	w.logger.Info("Processing wallet-only onboarding",
		zap.String("user_id", user.ID.String()))

	return w.createWallets(ctx, job, user)
}

// startKYCProcess starts the KYC verification process
func (w *Worker) startKYCProcess(ctx context.Context, job *entities.OnboardingJob, user *entities.User) error {
	w.logger.Info("Starting KYC process",
		zap.String("user_id", user.ID.String()))

	// Check if KYC is already in progress or completed
	status, err := w.onboardingService.GetOnboardingStatus(ctx, user.ID)
	if err == nil && status != nil {
		if status.KYCStatus == string(entities.KYCStatusApproved) {
			w.logger.Info("KYC already approved",
				zap.String("user_id", user.ID.String()))
			return nil
		}
		if status.KYCStatus == string(entities.KYCStatusProcessing) {
			w.logger.Info("KYC already in progress",
				zap.String("user_id", user.ID.String()))
			return nil
		}
	}

	// Start KYC process
	userPhone := ""
	if user.Phone != nil {
		userPhone = *user.Phone
	}

	if err := w.onboardingService.StartOnboarding(ctx, user.ID, user.Email, userPhone); err != nil {
		w.logger.Error("Failed to start KYC process",
			zap.String("user_id", user.ID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to start KYC process: %w", err)
	}

	w.logger.Info("KYC process started successfully",
		zap.String("user_id", user.ID.String()))

	return nil
}

// createWallets creates wallets for the user
func (w *Worker) createWallets(ctx context.Context, job *entities.OnboardingJob, user *entities.User) error {
	w.logger.Info("Creating wallets for user",
		zap.String("user_id", user.ID.String()))

	// Check if wallets already exist
	walletStatus, err := w.walletService.GetWalletStatus(ctx, user.ID)
	if err == nil && walletStatus != nil && len(walletStatus.WalletsByChain) > 0 {
		w.logger.Info("Wallets already exist",
			zap.String("user_id", user.ID.String()),
			zap.Int("wallet_count", len(walletStatus.WalletsByChain)))
		return nil
	}

	// Extract wallet chains from job payload
	var chains []entities.WalletChain
	if walletChains, ok := job.Payload["wallet_chains"].([]interface{}); ok {
		for _, chainStr := range walletChains {
			if chain, ok := chainStr.(string); ok {
				chains = append(chains, entities.WalletChain(chain))
			}
		}
	}

	// Default to SOL-DEVNET if not specified
	if len(chains) == 0 {
		chains = []entities.WalletChain{
			entities.WalletChainSOLDevnet,
		}
	}

	// Create wallets
	if err := w.walletService.CreateWalletsForUser(ctx, user.ID, chains); err != nil {
		w.logger.Error("Failed to create wallets",
			zap.String("user_id", user.ID.String()),
			zap.Error(err))
		return fmt.Errorf("failed to create wallets: %w", err)
	}

	w.logger.Info("Wallets created successfully",
		zap.String("user_id", user.ID.String()),
		zap.Int("chain_count", len(chains)))

	return nil
}

// handleJobSuccess marks the job as completed
func (w *Worker) handleJobSuccess(ctx context.Context, job *entities.OnboardingJob) {
	job.MarkCompleted()

	w.logger.Info("Onboarding job completed successfully",
		zap.String("job_id", job.ID.String()),
		zap.String("user_id", job.UserID.String()),
		zap.Int("attempt_count", job.AttemptCount))
}

// handleJobFailure determines retry strategy
func (w *Worker) handleJobFailure(ctx context.Context, job *entities.OnboardingJob, err error) {
	errorMsg := err.Error()

	// Determine if error is retryable
	isRetryable := w.isRetryableError(err)

	// Calculate next retry time if retryable
	var retryDelay time.Duration
	if isRetryable && job.AttemptCount < w.config.MaxAttempts {
		retryDelay = w.calculateBackoff(job.AttemptCount)
		job.MarkFailed(errorMsg, retryDelay)

		w.logger.Warn("Job failed, will retry",
			zap.String("job_id", job.ID.String()),
			zap.String("user_id", job.UserID.String()),
			zap.Int("attempt_count", job.AttemptCount),
			zap.Int("max_attempts", w.config.MaxAttempts),
			zap.Duration("retry_in", retryDelay),
			zap.Error(err))
	} else {
		// No more retries or non-retryable error
		job.MarkFailed(errorMsg, 0)
		job.Status = entities.OnboardingJobStatusFailed

		w.logger.Error("Job failed permanently",
			zap.String("job_id", job.ID.String()),
			zap.String("user_id", job.UserID.String()),
			zap.Int("attempt_count", job.AttemptCount),
			zap.Bool("retryable", isRetryable),
			zap.Error(err))
	}
}

// scheduler polls for pending jobs
func (w *Worker) scheduler(ctx context.Context) {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Scheduler stopped due to context cancellation")
			return
		case <-w.stopChan:
			w.logger.Info("Scheduler stopped")
			return
		case <-ticker.C:
			w.pollForJobs(ctx)
		}
	}
}

// pollForJobs gets pending jobs and queues them for processing
func (w *Worker) pollForJobs(ctx context.Context) {
	jobs, err := w.jobRepo.GetPendingJobs(ctx, w.config.MaxConcurrentJobs)
	if err != nil {
		w.logger.Error("Failed to get pending jobs", zap.Error(err))
		return
	}

	if len(jobs) == 0 {
		return
	}

	w.logger.Debug("Found pending jobs", zap.Int("count", len(jobs)))

	for _, job := range jobs {
		select {
		case w.jobProcessingChan <- job.ID:
			w.logger.Debug("Queued job for processing", zap.String("job_id", job.ID.String()))
		default:
			w.logger.Warn("Job processing queue is full, skipping job", zap.String("job_id", job.ID.String()))
		}
	}
}

// jobProcessor processes jobs from the queue
func (w *Worker) jobProcessor(ctx context.Context, workerID int) {
	w.logger.Debug("Started job processor", zap.Int("worker_id", workerID))

	for {
		select {
		case <-ctx.Done():
			w.logger.Debug("Job processor stopped due to context cancellation", zap.Int("worker_id", workerID))
			return
		case <-w.stopChan:
			w.logger.Debug("Job processor stopped", zap.Int("worker_id", workerID))
			return
		case jobID := <-w.jobProcessingChan:
			w.logger.Debug("Processing job", zap.Int("worker_id", workerID), zap.String("job_id", jobID.String()))

			if err := w.ProcessJob(ctx, jobID); err != nil {
				w.logger.Error("Failed to process job",
					zap.Int("worker_id", workerID),
					zap.String("job_id", jobID.String()),
					zap.Error(err))
			}
		}
	}
}

// isRetryableError determines if an error should trigger a retry
func (w *Worker) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check error message for known transient issues
	errMsg := err.Error()
	transientPatterns := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"temporary failure",
		"network",
		"unavailable",
		"circuit breaker",
		"rate limit",
		"service unavailable",
	}

	for _, pattern := range transientPatterns {
		if contains(errMsg, pattern) {
			return true
		}
	}

	// Default to retryable for unknown errors (conservative approach)
	return true
}

// calculateBackoff calculates exponential backoff with jitter
func (w *Worker) calculateBackoff(attemptCount int) time.Duration {
	// Exponential backoff: base * 2^(attempt-1)
	backoff := w.config.BaseBackoffDuration * time.Duration(math.Pow(2, float64(attemptCount-1)))

	// Cap at max backoff
	if backoff > w.config.MaxBackoffDuration {
		backoff = w.config.MaxBackoffDuration
	}

	// Add jitter to prevent thundering herd
	jitter := time.Duration(float64(backoff) * w.config.JitterFactor * (rand.Float64()*2 - 1))
	backoff += jitter

	// Ensure minimum backoff
	if backoff < w.config.BaseBackoffDuration {
		backoff = w.config.BaseBackoffDuration
	}

	return backoff
}

// updateMetrics updates worker performance metrics
func (w *Worker) updateMetrics(err error, duration time.Duration) {
	w.metrics.TotalJobsProcessed++
	w.metrics.LastProcessedAt = time.Now()

	if err != nil {
		w.metrics.FailedJobs++
		w.metrics.ErrorsByType[w.classifyError(err)]++
	} else {
		w.metrics.SuccessfulJobs++
	}

	// Calculate rolling average duration
	if w.metrics.TotalJobsProcessed == 1 {
		w.metrics.AverageDuration = duration
	} else {
		alpha := 0.1 // Exponential moving average weight
		w.metrics.AverageDuration = time.Duration(
			float64(w.metrics.AverageDuration)*(1-alpha) + float64(duration)*alpha,
		)
	}
}

// classifyError categorizes errors for metrics
func (w *Worker) classifyError(err error) string {
	if err == nil {
		return "none"
	}

	errMsg := err.Error()
	if contains(errMsg, "timeout") {
		return "timeout"
	} else if contains(errMsg, "connection") {
		return "network"
	} else if contains(errMsg, "validation") {
		return "validation"
	} else if contains(errMsg, "rate limit") {
		return "rate_limit"
	}

	return "unknown"
}

// GetMetrics returns current worker metrics
func (w *Worker) GetMetrics() Metrics {
	return *w.metrics
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && substringIndex(s, substr) >= 0
}

func substringIndex(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
