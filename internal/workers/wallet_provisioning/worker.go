package walletprovisioning

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// WalletRepository is retained so the scheduler can still query jobs.
type WalletRepository interface {
	Create(ctx context.Context, wallet *entities.ManagedWallet) error
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
}

type ProvisioningJobRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error)
	Update(ctx context.Context, job *entities.WalletProvisioningJob) error
}

type AuditService interface {
	LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error
	LogWalletWorkerEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}, resourceID *string, status string, errorMsg *string) error
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
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
	ChainsToProvision   []entities.WalletChain
	WalletSetNamePrefix string
	DefaultWalletSetID  string
}

// DefaultConfig returns default worker configuration
func DefaultConfig() Config {
	return Config{
		MaxAttempts:         5,
		BaseBackoffDuration: 1 * time.Minute,
		MaxBackoffDuration:  30 * time.Minute,
		JitterFactor:        0.1,
		ChainsToProvision: []entities.WalletChain{
			entities.WalletChainSOLDevnet,
			entities.WalletChainMATICAmoy,
			entities.WalletChainAVAXFuji,
		},
		WalletSetNamePrefix: "STACK-WalletSet",
	}
}

// Worker handles wallet provisioning jobs.
// Wallet creation is now handled by Bridge during onboarding; this worker is a no-op.
type Worker struct {
	jobRepo      ProvisioningJobRepository
	auditService AuditService
	config       Config
	logger       *zap.Logger
	metricsMu    sync.Mutex
	metrics      *Metrics
}

// NewWorker creates a new wallet provisioning worker.
func NewWorker(
	jobRepo ProvisioningJobRepository,
	auditService AuditService,
	config Config,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		jobRepo:      jobRepo,
		auditService: auditService,
		config:       config,
		logger:       logger,
		metrics: &Metrics{
			ErrorsByType: make(map[string]int64),
		},
	}
}

// ProcessJob marks the job as completed immediately.
// Wallet creation is handled by Bridge during onboarding; no Circle provisioning needed.
func (w *Worker) ProcessJob(ctx context.Context, jobID uuid.UUID) error {
	w.logger.Info("Wallet provisioning job received (no-op: wallets created via Bridge onboarding)",
		zap.String("job_id", jobID.String()))

	job, err := w.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return nil // Job not found — nothing to do
	}

	job.MarkCompleted()
	if err := w.jobRepo.Update(ctx, job); err != nil {
		w.logger.Warn("Failed to mark provisioning job completed", zap.Error(err))
	}

	w.metricsMu.Lock()
	w.metrics.TotalJobsProcessed++
	w.metrics.SuccessfulJobs++
	w.metrics.LastProcessedAt = time.Now()
	w.metricsMu.Unlock()
	return nil
}

// GetMetrics returns a copy of current worker metrics.
func (w *Worker) GetMetrics() Metrics {
	w.metricsMu.Lock()
	m := *w.metrics
	w.metricsMu.Unlock()
	return m
}

// classifyError categorizes errors for metrics
func (w *Worker) classifyError(err error) string {
	if err == nil {
		return "none"
	}
	msg := err.Error()
	switch {
	case contains(msg, "timeout"):
		return "timeout"
	case contains(msg, "connection"):
		return "network"
	case contains(msg, "validation"):
		return "validation"
	default:
		return "unknown"
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
