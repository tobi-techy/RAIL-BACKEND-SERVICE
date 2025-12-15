package reconciliation

import (
	"context"
	"sync"
	"time"

	"github.com/rail-service/rail_service/pkg/logger"
)

// Scheduler handles automated reconciliation runs
type Scheduler struct {
	service *Service
	logger  *logger.Logger

	// Cron intervals
	hourlyInterval time.Duration
	dailyInterval  time.Duration

	// Control
	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	running bool
}

// SchedulerConfig holds scheduler configuration
type SchedulerConfig struct {
	HourlyInterval time.Duration // Default: 1 hour
	DailyInterval  time.Duration // Default: 24 hours
	DailyRunTime   string        // Time of day to run daily check (e.g., "02:00")
}

// NewScheduler creates a new reconciliation scheduler
func NewScheduler(service *Service, logger *logger.Logger, config *SchedulerConfig) *Scheduler {
	if config.HourlyInterval == 0 {
		config.HourlyInterval = 1 * time.Hour
	}
	if config.DailyInterval == 0 {
		config.DailyInterval = 24 * time.Hour
	}

	return &Scheduler{
		service:        service,
		logger:         logger,
		hourlyInterval: config.HourlyInterval,
		dailyInterval:  config.DailyInterval,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the reconciliation scheduler
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting reconciliation scheduler",
		"hourly_interval", s.hourlyInterval,
		"daily_interval", s.dailyInterval,
	)

	// Start hourly reconciliation goroutine
	s.wg.Add(1)
	go s.runHourlyReconciliation(ctx)

	// Start daily reconciliation goroutine
	s.wg.Add(1)
	go s.runDailyReconciliation(ctx)

	return nil
}

// Stop gracefully stops the reconciliation scheduler
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	s.logger.Info("Stopping reconciliation scheduler")

	close(s.stopCh)
	s.wg.Wait()

	s.logger.Info("Reconciliation scheduler stopped")
	return nil
}

// runHourlyReconciliation runs hourly reconciliation checks
func (s *Scheduler) runHourlyReconciliation(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.hourlyInterval)
	defer ticker.Stop()

	// Run immediately on start
	s.executeReconciliation(ctx, "hourly")

	for {
		select {
		case <-ticker.C:
			s.executeReconciliation(ctx, "hourly")
		case <-s.stopCh:
			s.logger.Info("Hourly reconciliation goroutine stopping")
			return
		case <-ctx.Done():
			s.logger.Info("Hourly reconciliation goroutine cancelled")
			return
		}
	}
}

// runDailyReconciliation runs daily reconciliation checks
func (s *Scheduler) runDailyReconciliation(ctx context.Context) {
	defer s.wg.Done()

	// Calculate time until next daily run (e.g., 2 AM)
	now := time.Now()
	nextRun := s.calculateNextDailyRun(now)
	initialDelay := time.Until(nextRun)

	s.logger.Info("Daily reconciliation scheduled",
		"next_run", nextRun,
		"initial_delay", initialDelay,
	)

	// Wait for initial delay
	select {
	case <-time.After(initialDelay):
		s.executeReconciliation(ctx, "daily")
	case <-s.stopCh:
		s.logger.Info("Daily reconciliation goroutine stopping before first run")
		return
	case <-ctx.Done():
		s.logger.Info("Daily reconciliation goroutine cancelled before first run")
		return
	}

	// Run daily thereafter
	ticker := time.NewTicker(s.dailyInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.executeReconciliation(ctx, "daily")
		case <-s.stopCh:
			s.logger.Info("Daily reconciliation goroutine stopping")
			return
		case <-ctx.Done():
			s.logger.Info("Daily reconciliation goroutine cancelled")
			return
		}
	}
}

// calculateNextDailyRun calculates the next daily run time (default: 2 AM)
func (s *Scheduler) calculateNextDailyRun(from time.Time) time.Time {
	// Default to 2 AM
	targetHour := 2
	targetMinute := 0

	next := time.Date(from.Year(), from.Month(), from.Day(), targetHour, targetMinute, 0, 0, from.Location())

	// If we've already passed 2 AM today, schedule for tomorrow
	if next.Before(from) {
		next = next.Add(24 * time.Hour)
	}

	return next
}

// executeReconciliation safely executes a reconciliation run
func (s *Scheduler) executeReconciliation(ctx context.Context, runType string) {
	s.logger.Info("Starting scheduled reconciliation", "run_type", runType)

	// Create a timeout context for the reconciliation
	reconciliationCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	startTime := time.Now()

	report, err := s.service.RunReconciliation(reconciliationCtx, runType)
	if err != nil {
		s.logger.Error("Scheduled reconciliation failed",
			"run_type", runType,
			"error", err,
			"duration", time.Since(startTime),
		)
		return
	}

	s.logger.Info("Scheduled reconciliation completed",
		"run_type", runType,
		"report_id", report.ID,
		"total_checks", report.TotalChecks,
		"passed", report.PassedChecks,
		"failed", report.FailedChecks,
		"exceptions", report.ExceptionsCount,
		"duration", time.Since(startTime),
	)
}

// RunManualReconciliation triggers a manual reconciliation run
func (s *Scheduler) RunManualReconciliation(ctx context.Context) error {
	s.logger.Info("Starting manual reconciliation")

	report, err := s.service.RunReconciliation(ctx, "manual")
	if err != nil {
		s.logger.Error("Manual reconciliation failed", "error", err)
		return err
	}

	s.logger.Info("Manual reconciliation completed",
		"report_id", report.ID,
		"total_checks", report.TotalChecks,
		"passed", report.PassedChecks,
		"failed", report.FailedChecks,
		"exceptions", report.ExceptionsCount,
	)

	return nil
}
