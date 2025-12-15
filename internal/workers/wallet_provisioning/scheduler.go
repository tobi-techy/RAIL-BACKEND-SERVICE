package walletprovisioning

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SchedulerConfig holds configuration for the worker scheduler
type SchedulerConfig struct {
	PollInterval    time.Duration // How often to check for new jobs
	MaxConcurrency  int           // Maximum number of jobs to process concurrently
	JobBatchSize    int           // Number of jobs to fetch per poll
	ShutdownTimeout time.Duration // How long to wait for in-flight jobs during shutdown
	EnableRetries   bool          // Whether to process retry jobs
}

// DefaultSchedulerConfig returns default scheduler configuration
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		PollInterval:    30 * time.Second,
		MaxConcurrency:  5,
		JobBatchSize:    10,
		ShutdownTimeout: 60 * time.Second,
		EnableRetries:   true,
	}
}

// Scheduler manages the background processing of wallet provisioning jobs
type Scheduler struct {
	worker  *Worker
	jobRepo ProvisioningJobRepository
	config  SchedulerConfig
	logger  *zap.Logger

	// Concurrency control
	semaphore chan struct{}
	wg        sync.WaitGroup

	// Lifecycle management
	ctx          context.Context
	cancel       context.CancelFunc
	shutdownChan chan struct{}
	isRunning    bool
	mu           sync.RWMutex
}

// NewScheduler creates a new worker scheduler
func NewScheduler(
	worker *Worker,
	jobRepo ProvisioningJobRepository,
	config SchedulerConfig,
	logger *zap.Logger,
) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		worker:       worker,
		jobRepo:      jobRepo,
		config:       config,
		logger:       logger,
		semaphore:    make(chan struct{}, config.MaxConcurrency),
		ctx:          ctx,
		cancel:       cancel,
		shutdownChan: make(chan struct{}),
	}
}

// Start begins the scheduler's polling loop
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is already running")
	}
	s.isRunning = true
	s.mu.Unlock()

	s.logger.Info("Starting wallet provisioning scheduler",
		zap.Duration("poll_interval", s.config.PollInterval),
		zap.Int("max_concurrency", s.config.MaxConcurrency),
		zap.Int("batch_size", s.config.JobBatchSize))

	// Start the polling loop
	go s.pollLoop()

	return nil
}

// Stop gracefully shuts down the scheduler
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is not running")
	}
	s.mu.Unlock()

	s.logger.Info("Stopping wallet provisioning scheduler",
		zap.Duration("shutdown_timeout", s.config.ShutdownTimeout))

	// Cancel context to stop polling
	s.cancel()

	// Wait for in-flight jobs with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("All in-flight jobs completed, scheduler stopped gracefully")
	case <-time.After(s.config.ShutdownTimeout):
		s.logger.Warn("Shutdown timeout reached, some jobs may not have completed",
			zap.Duration("timeout", s.config.ShutdownTimeout))
	}

	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()

	close(s.shutdownChan)

	return nil
}

// IsRunning returns whether the scheduler is currently running
func (s *Scheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// pollLoop continuously polls for jobs to process
func (s *Scheduler) pollLoop() {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()

	// Process jobs immediately on start
	s.processAvailableJobs()

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("Poll loop stopped due to context cancellation")
			return

		case <-ticker.C:
			s.processAvailableJobs()
		}
	}
}

// processAvailableJobs fetches and processes available jobs
func (s *Scheduler) processAvailableJobs() {
	s.logger.Debug("Checking for available jobs")

	// Fetch retryable jobs if enabled
	if s.config.EnableRetries {
		retryJobs, err := s.jobRepo.GetRetryableJobs(s.ctx, s.config.JobBatchSize)
		if err != nil {
			s.logger.Error("Failed to fetch retryable jobs", zap.Error(err))
		} else if len(retryJobs) > 0 {
			s.logger.Info("Found retryable jobs to process", zap.Int("count", len(retryJobs)))
			for _, job := range retryJobs {
				s.enqueueJob(job.ID.String())
			}
		}
	}

	// Note: For queued jobs, we rely on the immediate processing when they're created
	// or use a message queue/event bus for real-time processing
}

// enqueueJob attempts to process a job, respecting concurrency limits
func (s *Scheduler) enqueueJob(jobID string) {
	select {
	case <-s.ctx.Done():
		s.logger.Debug("Skipping job enqueue, scheduler is stopping", zap.String("job_id", jobID))
		return

	case s.semaphore <- struct{}{}:
		// Acquired semaphore, can process job
		s.wg.Add(1)
		go s.processJobAsync(jobID)

	default:
		// Semaphore full, log and skip (will be picked up in next poll)
		s.logger.Warn("Concurrency limit reached, job will be processed in next poll",
			zap.String("job_id", jobID),
			zap.Int("max_concurrency", s.config.MaxConcurrency))
	}
}

// processJobAsync processes a job asynchronously
func (s *Scheduler) processJobAsync(jobID string) {
	defer func() {
		<-s.semaphore // Release semaphore
		s.wg.Done()

		// Recover from panics to prevent scheduler crash
		if r := recover(); r != nil {
			s.logger.Error("Panic in job processing",
				zap.String("job_id", jobID),
				zap.Any("panic", r))
		}
	}()

	// Parse job ID
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		s.logger.Error("Invalid job ID", zap.String("job_id", jobID), zap.Error(err))
		return
	}

	// Process the job with context
	ctx := s.ctx
	if err := s.worker.ProcessJob(ctx, jobUUID); err != nil {
		s.logger.Error("Job processing failed",
			zap.String("job_id", jobID),
			zap.Error(err))
	}
}

// GetStatus returns the current status of the scheduler
func (s *Scheduler) GetStatus() SchedulerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Calculate active jobs (approximate based on semaphore)
	activeJobs := s.config.MaxConcurrency - len(s.semaphore)

	return SchedulerStatus{
		IsRunning:      s.isRunning,
		PollInterval:   s.config.PollInterval,
		MaxConcurrency: s.config.MaxConcurrency,
		ActiveJobs:     activeJobs,
		WorkerMetrics:  s.worker.GetMetrics(),
	}
}

// SchedulerStatus represents the current state of the scheduler
type SchedulerStatus struct {
	IsRunning      bool          `json:"isRunning"`
	PollInterval   time.Duration `json:"pollInterval"`
	MaxConcurrency int           `json:"maxConcurrency"`
	ActiveJobs     int           `json:"activeJobs"`
	WorkerMetrics  Metrics       `json:"workerMetrics"`
}
