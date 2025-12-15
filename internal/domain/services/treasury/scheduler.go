package treasury

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rail-service/rail_service/pkg/logger"
)

// Scheduler manages periodic execution of treasury operations
type Scheduler struct {
	engine          *Engine
	logger          *logger.Logger
	interval        time.Duration
	monitorInterval time.Duration
	stopCh          chan struct{}
	wg              sync.WaitGroup
	running         bool
	mu              sync.RWMutex
}

// NewScheduler creates a new treasury scheduler
func NewScheduler(engine *Engine, logger *logger.Logger) *Scheduler {
	return &Scheduler{
		engine:          engine,
		logger:          logger,
		interval:        engine.config.SchedulerInterval,
		monitorInterval: engine.config.HealthCheckInterval,
		stopCh:          make(chan struct{}),
		running:         false,
	}
}

// Start begins the scheduler's periodic execution
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is already running")
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting Treasury Scheduler",
		"settlement_interval", s.interval,
		"monitor_interval", s.monitorInterval)

	// Initialize engine
	if err := s.engine.Initialize(ctx); err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("failed to initialize treasury engine: %w", err)
	}

	// Start net settlement cycle goroutine
	s.wg.Add(1)
	go s.runSettlementCycle(ctx)

	// Start job monitoring goroutine
	s.wg.Add(1)
	go s.runJobMonitoring(ctx)

	s.logger.Info("Treasury Scheduler started successfully")
	return nil
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is not running")
	}
	s.mu.Unlock()

	s.logger.Info("Stopping Treasury Scheduler")

	// Signal goroutines to stop
	close(s.stopCh)

	// Wait for goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("Treasury Scheduler stopped gracefully")
	case <-time.After(30 * time.Second):
		s.logger.Warn("Treasury Scheduler stop timeout - forcing shutdown")
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	return nil
}

// IsRunning returns true if the scheduler is currently running
func (s *Scheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// runSettlementCycle executes net settlement cycles periodically
func (s *Scheduler) runSettlementCycle(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("Net settlement cycle started", "interval", s.interval)

	// Run immediately on start
	s.executeSettlementCycle(ctx)

	for {
		select {
		case <-s.stopCh:
			s.logger.Info("Net settlement cycle stopped")
			return
		case <-ticker.C:
			s.executeSettlementCycle(ctx)
		case <-ctx.Done():
			s.logger.Info("Net settlement cycle cancelled by context")
			return
		}
	}
}

// executeSettlementCycle wraps the engine's RunNetSettlementCycle with error handling
func (s *Scheduler) executeSettlementCycle(ctx context.Context) {
	s.logger.Info("Executing net settlement cycle")
	startTime := time.Now()

	// Create timeout context for this cycle
	cycleCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Execute cycle
	if err := s.engine.RunNetSettlementCycle(cycleCtx); err != nil {
		s.logger.Error("Net settlement cycle failed",
			"error", err,
			"duration", time.Since(startTime))
	} else {
		s.logger.Info("Net settlement cycle completed successfully",
			"duration", time.Since(startTime))
	}
}

// runJobMonitoring monitors in-flight conversion jobs periodically
func (s *Scheduler) runJobMonitoring(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.monitorInterval)
	defer ticker.Stop()

	s.logger.Info("Job monitoring started", "interval", s.monitorInterval)

	for {
		select {
		case <-s.stopCh:
			s.logger.Info("Job monitoring stopped")
			return
		case <-ticker.C:
			s.executeJobMonitoring(ctx)
		case <-ctx.Done():
			s.logger.Info("Job monitoring cancelled by context")
			return
		}
	}
}

// executeJobMonitoring wraps the engine's MonitorConversionJobs with error handling
func (s *Scheduler) executeJobMonitoring(ctx context.Context) {
	// Create timeout context for monitoring
	monitorCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Execute monitoring
	if err := s.engine.MonitorConversionJobs(monitorCtx); err != nil {
		s.logger.Error("Job monitoring failed", "error", err)
	}
}

// TriggerImmediateCycle triggers an immediate net settlement cycle (manual trigger)
func (s *Scheduler) TriggerImmediateCycle(ctx context.Context) error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running {
		return fmt.Errorf("scheduler is not running")
	}

	s.logger.Info("Manually triggered immediate net settlement cycle")
	s.executeSettlementCycle(ctx)

	return nil
}

// GetStatus returns the current status of the scheduler
func (s *Scheduler) GetStatus() *SchedulerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &SchedulerStatus{
		Running:         s.running,
		Interval:        s.interval,
		MonitorInterval: s.monitorInterval,
	}
}

// SchedulerStatus represents the current state of the scheduler
type SchedulerStatus struct {
	Running         bool          `json:"running"`
	Interval        time.Duration `json:"interval"`
	MonitorInterval time.Duration `json:"monitor_interval"`
}
