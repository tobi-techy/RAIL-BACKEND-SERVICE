package deposit_cleanup

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// DepositRepository interface for deposit operations
type DepositRepository interface {
	GetPendingDepositsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]*entities.Deposit, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, confirmedAt *time.Time) error
}

// Worker handles cleanup of stale pending deposits
type Worker struct {
	depositRepo     DepositRepository
	timeoutHours    int
	checkInterval   time.Duration
	batchSize       int
	logger          *logger.Logger
	stopCh          chan struct{}
}

// Config holds worker configuration
type Config struct {
	TimeoutHours  int
	CheckInterval time.Duration
	BatchSize     int
}

// DefaultConfig returns default worker configuration
func DefaultConfig() *Config {
	return &Config{
		TimeoutHours:  entities.DepositTimeoutHours,
		CheckInterval: 1 * time.Hour,
		BatchSize:     100,
	}
}

// NewWorker creates a new deposit cleanup worker
func NewWorker(depositRepo DepositRepository, config *Config, logger *logger.Logger) *Worker {
	if config == nil {
		config = DefaultConfig()
	}
	return &Worker{
		depositRepo:   depositRepo,
		timeoutHours:  config.TimeoutHours,
		checkInterval: config.CheckInterval,
		batchSize:     config.BatchSize,
		logger:        logger,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the cleanup worker
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Starting deposit cleanup worker",
		"timeout_hours", w.timeoutHours,
		"check_interval", w.checkInterval.String())

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	// Run immediately on start
	w.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Deposit cleanup worker stopped (context cancelled)")
			return
		case <-w.stopCh:
			w.logger.Info("Deposit cleanup worker stopped")
			return
		case <-ticker.C:
			w.cleanup(ctx)
		}
	}
}

// Stop stops the worker
func (w *Worker) Stop() {
	close(w.stopCh)
}

// cleanup processes stale pending deposits
func (w *Worker) cleanup(ctx context.Context) {
	cutoff := time.Now().Add(-time.Duration(w.timeoutHours) * time.Hour)
	
	w.logger.Info("Running deposit cleanup",
		"cutoff", cutoff.Format(time.RFC3339))

	deposits, err := w.depositRepo.GetPendingDepositsOlderThan(ctx, cutoff, w.batchSize)
	if err != nil {
		w.logger.Error("Failed to get pending deposits", "error", err)
		return
	}

	if len(deposits) == 0 {
		w.logger.Debug("No stale pending deposits found")
		return
	}

	w.logger.Info("Found stale pending deposits", "count", len(deposits))

	expiredCount := 0
	for _, deposit := range deposits {
		// Validate status transition
		currentStatus := entities.DepositStatus(deposit.Status)
		if !currentStatus.CanTransitionTo(entities.DepositStatusExpired) {
			w.logger.Warn("Cannot expire deposit - invalid status transition",
				"deposit_id", deposit.ID,
				"current_status", deposit.Status)
			continue
		}

		err := w.depositRepo.UpdateStatus(ctx, deposit.ID, string(entities.DepositStatusExpired), nil)
		if err != nil {
			w.logger.Error("Failed to expire deposit",
				"deposit_id", deposit.ID,
				"error", err)
			continue
		}

		expiredCount++
		w.logger.Info("Expired stale deposit",
			"deposit_id", deposit.ID,
			"created_at", deposit.CreatedAt.Format(time.RFC3339))
	}

	w.logger.Info("Deposit cleanup completed",
		"processed", len(deposits),
		"expired", expiredCount)
}

// RunOnce runs cleanup once (for testing or manual trigger)
func (w *Worker) RunOnce(ctx context.Context) {
	w.cleanup(ctx)
}
