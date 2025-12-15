package funding_webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// Manager coordinates webhook processor and reconciliation worker
type Manager struct {
	processor  *Processor
	reconciler *Reconciler
	logger     *logger.Logger
	isRunning  bool
}

// NewManager creates a new worker manager
func NewManager(
	processorConfig ProcessorConfig,
	reconciliationConfig ReconciliationConfig,
	jobRepo *repositories.FundingEventJobRepository,
	depositRepo *repositories.DepositRepository,
	fundingSvc *funding.Service,
	auditSvc *adapters.AuditService,
	logger *logger.Logger,
) (*Manager, error) {
	// Create processor
	processor, err := NewProcessor(
		processorConfig,
		jobRepo,
		fundingSvc,
		auditSvc,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	// Create chain validator
	validator := NewChainValidator(logger, nil) // Use default RPC endpoints

	// Create reconciler
	reconciler, err := NewReconciler(
		reconciliationConfig,
		jobRepo,
		depositRepo,
		fundingSvc,
		validator,
		auditSvc,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create reconciler: %w", err)
	}

	return &Manager{
		processor:  processor,
		reconciler: reconciler,
		logger:     logger,
		isRunning:  false,
	}, nil
}

// Start starts all workers
func (m *Manager) Start(ctx context.Context) error {
	if m.isRunning {
		return fmt.Errorf("manager already running")
	}

	m.logger.Info("Starting funding webhook workers")

	// Start processor
	if err := m.processor.Start(ctx); err != nil {
		return fmt.Errorf("failed to start processor: %w", err)
	}

	// Start reconciler
	if err := m.reconciler.Start(ctx); err != nil {
		// If reconciler fails, stop processor before returning
		_ = m.processor.Shutdown(5 * time.Second)
		return fmt.Errorf("failed to start reconciler: %w", err)
	}

	m.isRunning = true
	m.logger.Info("Funding webhook workers started successfully")

	return nil
}

// Shutdown gracefully stops all workers
func (m *Manager) Shutdown(timeout time.Duration) error {
	if !m.isRunning {
		return nil
	}

	m.logger.Info("Shutting down funding webhook workers", "timeout", timeout)

	// Calculate per-worker timeout
	workerTimeout := timeout / 2

	// Shutdown processor
	if err := m.processor.Shutdown(workerTimeout); err != nil {
		m.logger.Error("Processor shutdown error", "error", err)
	}

	// Shutdown reconciler
	if err := m.reconciler.Shutdown(workerTimeout); err != nil {
		m.logger.Error("Reconciler shutdown error", "error", err)
	}

	m.isRunning = false
	m.logger.Info("Funding webhook workers shutdown complete")

	return nil
}

// IsRunning returns whether the manager is currently running
func (m *Manager) IsRunning() bool {
	return m.isRunning
}
