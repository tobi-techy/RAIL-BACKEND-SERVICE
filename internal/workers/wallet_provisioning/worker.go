package walletprovisioning

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"

	"go.uber.org/zap"
)

// Dependencies interfaces for the worker
type WalletRepository interface {
	Create(ctx context.Context, wallet *entities.ManagedWallet) error
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
}

type WalletSetRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletSet, error)
	GetActive(ctx context.Context) (*entities.WalletSet, error)
	Create(ctx context.Context, walletSet *entities.WalletSet) error
	GetByCircleWalletSetID(ctx context.Context, circleWalletSetID string) (*entities.WalletSet, error)
	Update(ctx context.Context, walletSet *entities.WalletSet) error
}

type ProvisioningJobRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error)
	Update(ctx context.Context, job *entities.WalletProvisioningJob) error
}

type CircleClient interface {
	CreateWalletSet(ctx context.Context, name string, entitySecretCiphertext string) (*entities.CircleWalletSetResponse, error)
	GetWalletSet(ctx context.Context, walletSetID string) (*entities.CircleWalletSetResponse, error)
	CreateWallet(ctx context.Context, req entities.CircleWalletCreateRequest) (*entities.CircleWalletCreateResponse, error)
	GetWallet(ctx context.Context, walletID string) (*entities.CircleWalletCreateResponse, error)
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
		},
		WalletSetNamePrefix: "STACK-WalletSet",
		DefaultWalletSetID:  "",
	}
}

// Worker handles wallet provisioning jobs with retries and audit logging
type Worker struct {
	walletRepo    WalletRepository
	walletSetRepo WalletSetRepository
	jobRepo       ProvisioningJobRepository
	circleClient  CircleClient
	auditService  AuditService
	userRepo      UserRepository
	config        Config
	logger        *zap.Logger
	metrics       *Metrics
}

// NewWorker creates a new wallet provisioning worker
func NewWorker(
	walletRepo WalletRepository,
	walletSetRepo WalletSetRepository,
	jobRepo ProvisioningJobRepository,
	circleClient CircleClient,
	auditService AuditService,
	userRepo UserRepository,
	config Config,
	logger *zap.Logger,
) *Worker {
	if config.WalletSetNamePrefix == "" {
		config.WalletSetNamePrefix = "STACK-WalletSet"
	}
	config.DefaultWalletSetID = strings.TrimSpace(config.DefaultWalletSetID)

	return &Worker{
		walletRepo:    walletRepo,
		walletSetRepo: walletSetRepo,
		jobRepo:       jobRepo,
		circleClient:  circleClient,
		auditService:  auditService,
		userRepo:      userRepo,
		config:        config,
		logger:        logger,
		metrics: &Metrics{
			ErrorsByType: make(map[string]int64),
		},
	}
}

// ProcessJob processes a single wallet provisioning job with idempotency
func (w *Worker) ProcessJob(ctx context.Context, jobID uuid.UUID) error {
	startTime := time.Now()
	w.logger.Info("Processing wallet provisioning job",
		zap.String("job_id", jobID.String()),
		zap.Time("started_at", startTime))

	// Get the job
	job, err := w.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		w.logger.Error("Failed to get provisioning job", zap.Error(err), zap.String("job_id", jobID.String()))
		return fmt.Errorf("failed to get provisioning job: %w", err)
	}

	// Check if job should be processed
	if !w.shouldProcessJob(job) {
		w.logger.Info("Job not eligible for processing",
			zap.String("job_id", jobID.String()),
			zap.String("status", string(job.Status)))
		return nil
	}

	// Mark job as started
	beforeJob := *job
	job.MarkStarted()
	if err := w.jobRepo.Update(ctx, job); err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	// Log job start audit
	resourceID := job.ID.String()
	w.auditService.LogWalletWorkerEvent(ctx, job.UserID, "provision_start", "wallet_provisioning_job",
		map[string]interface{}{
			"status":        string(beforeJob.Status),
			"attempt_count": beforeJob.AttemptCount,
		},
		map[string]interface{}{
			"status":        string(job.Status),
			"attempt_count": job.AttemptCount,
		},
		&resourceID, "success", nil)

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

	w.logger.Info("Finished processing wallet provisioning job",
		zap.String("job_id", jobID.String()),
		zap.String("final_status", string(job.Status)),
		zap.Duration("duration", duration),
		zap.Error(err))

	return err
}

// processJobInternal performs the actual wallet provisioning logic
func (w *Worker) processJobInternal(ctx context.Context, job *entities.WalletProvisioningJob) error {
	w.logger.Info("Starting wallet provisioning for user",
		zap.String("user_id", job.UserID.String()),
		zap.Int("chains_count", len(job.Chains)))

	// Get or create wallet set
	walletSet, err := w.ensureWalletSet(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure wallet set: %w", err)
	}

	// Process each chain with idempotency
	var successCount int
	var lastError error

	for _, chainStr := range job.Chains {
		chain := entities.WalletChain(chainStr)

		// Check if wallet already exists (idempotency)
		existingWallet, err := w.walletRepo.GetByUserAndChain(ctx, job.UserID, chain)
		if err == nil && existingWallet != nil && existingWallet.IsReady() {
			w.logger.Info("Wallet already exists for chain (idempotent)",
				zap.String("user_id", job.UserID.String()),
				zap.String("chain", string(chain)),
				zap.String("address", existingWallet.Address))
			successCount++
			continue
		}

		// Create wallet for chain
		if err := w.createWalletForChain(ctx, job, chain, walletSet); err != nil {
			w.logger.Error("Failed to create wallet for chain",
				zap.Error(err),
				zap.String("user_id", job.UserID.String()),
				zap.String("chain", string(chain)))
			lastError = err

			// Track error type
			w.metrics.ErrorsByType[w.classifyError(err)]++
		} else {
			successCount++
		}
	}

	// Determine overall result
	totalChains := len(job.Chains)
	if successCount == totalChains {
		w.logger.Info("All wallets created successfully",
			zap.String("user_id", job.UserID.String()),
			zap.Int("wallet_count", successCount))
		return nil
	} else if successCount > 0 {
		return fmt.Errorf("partial success: %d/%d wallets created, last error: %w", successCount, totalChains, lastError)
	} else {
		return fmt.Errorf("failed to create any wallets: %w", lastError)
	}
}

// createWalletForChain creates a developer-controlled wallet for a specific chain
func (w *Worker) createWalletForChain(ctx context.Context, job *entities.WalletProvisioningJob, chain entities.WalletChain, walletSet *entities.WalletSet) error {
	w.logger.Info("Creating developer-controlled wallet for chain",
		zap.String("user_id", job.UserID.String()),
		zap.String("chain", string(chain)))

	// Determine account type following developer-controlled-wallet pattern
	accountType := entities.AccountTypeEOA
	if chain.GetChainFamily() == "EVM" {
		// Use SCA for EVM chains to achieve unified addresses across all EVM chains
		// This ensures the same address works on ETH, MATIC, AVAX, BASE, etc.
		accountType = entities.AccountTypeSCA
	}
	// Solana and Aptos chains use EOA

	// Create Circle wallet request using pre-registered Entity Secret Ciphertext
	circleReq := entities.CircleWalletCreateRequest{
		WalletSetID: walletSet.CircleWalletSetID,
		Blockchains: []string{string(chain)},
		AccountType: string(accountType),
		Count:       1, // Create single wallet per chain
		// EntitySecretCiphertext is automatically added by Circle client from config
	}

	// Log request to job
	job.AddCircleRequest("create_wallet", circleReq, nil)

	// Create wallet in Circle using developer-controlled pattern
	circleResp, err := w.circleClient.CreateWallet(ctx, circleReq)
	if err != nil {
		// Log error response to job
		job.AddCircleRequest("create_wallet_error", circleReq, map[string]any{"error": err.Error()})
		return fmt.Errorf("failed to create wallet in Circle: %w", err)
	}

	// Log success response to job
	job.AddCircleRequest("create_wallet_success", circleReq, circleResp)

	// Find the address for the requested chain
	var address string
	// Handle both single address and addresses array responses
	if circleResp.Wallet.Address != "" {
		// Single address response (direct format)
		address = circleResp.Wallet.Address
	} else if len(circleResp.Wallet.Addresses) > 0 {
		// Addresses array response
		for _, addr := range circleResp.Wallet.Addresses {
			if addr.Blockchain == string(chain) {
				address = addr.Address
				break
			}
		}
	}

	if address == "" {
		return fmt.Errorf("no address found for chain %s in Circle response", chain)
	}

	// Create wallet record with Circle wallet ID for transaction operations
	wallet := &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         job.UserID,
		Chain:          chain,
		Address:        address,
		CircleWalletID: circleResp.Wallet.ID, // Store Circle wallet ID for transactions
		WalletSetID:    walletSet.ID,
		AccountType:    accountType,
		Status:         entities.WalletStatusLive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Validate wallet
	if err := wallet.Validate(); err != nil {
		return fmt.Errorf("wallet validation failed: %w", err)
	}

	// Persist wallet
	if err := w.walletRepo.Create(ctx, wallet); err != nil {
		return fmt.Errorf("failed to create wallet record: %w", err)
	}

	// Log audit event
	resourceID := wallet.ID.String()
	w.auditService.LogWalletWorkerEvent(ctx, job.UserID, "developer_wallet_created", "wallet",
		nil, wallet, &resourceID, "success", nil)

	w.logger.Info("Created developer-controlled wallet successfully",
		zap.String("user_id", job.UserID.String()),
		zap.String("chain", string(chain)),
		zap.String("address", address),
		zap.String("circle_wallet_id", circleResp.Wallet.ID))

	return nil
}

// ensureWalletSet gets or creates an active developer-controlled wallet set
func (w *Worker) ensureWalletSet(ctx context.Context) (*entities.WalletSet, error) {
	// First, try to use configured default wallet set ID
	if w.config.DefaultWalletSetID != "" {
		if walletSet, err := w.walletSetRepo.GetByCircleWalletSetID(ctx, w.config.DefaultWalletSetID); err == nil && walletSet != nil {
			w.logger.Debug("Using configured default wallet set",
				zap.String("wallet_set_id", walletSet.ID.String()),
				zap.String("circle_wallet_set_id", walletSet.CircleWalletSetID))
			return walletSet, nil
		}

		w.logger.Info("Configured Circle wallet set not present locally, hydrating",
			zap.String("circle_wallet_set_id", w.config.DefaultWalletSetID))

		circleSet, err := w.circleClient.GetWalletSet(ctx, w.config.DefaultWalletSetID)
		if err == nil && circleSet != nil {
			walletSet := &entities.WalletSet{
				ID:                uuid.New(),
				Name:              circleSet.WalletSet.Name,
				CircleWalletSetID: circleSet.WalletSet.ID,
				Status:            entities.WalletSetStatusActive,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}

			if createErr := w.walletSetRepo.Create(ctx, walletSet); createErr != nil {
				w.logger.Warn("Failed to persist configured wallet set",
					zap.Error(createErr),
					zap.String("circle_wallet_set_id", walletSet.CircleWalletSetID))

				existing, fetchErr := w.walletSetRepo.GetByCircleWalletSetID(ctx, walletSet.CircleWalletSetID)
				if fetchErr == nil && existing != nil {
					return existing, nil
				}

				return nil, fmt.Errorf("failed to persist configured wallet set: %w", createErr)
			}

			return walletSet, nil
		}

		if err != nil {
			w.logger.Warn("Failed to load configured Circle wallet set",
				zap.Error(err),
				zap.String("circle_wallet_set_id", w.config.DefaultWalletSetID))
		}
	}

	// Try to get existing active wallet set
	walletSet, err := w.walletSetRepo.GetActive(ctx)
	if err == nil && walletSet != nil {
		w.logger.Debug("Using existing active wallet set",
			zap.String("wallet_set_id", walletSet.ID.String()),
			zap.String("circle_wallet_set_id", walletSet.CircleWalletSetID))
		return walletSet, nil
	}

	w.logger.Info("Creating new developer-controlled wallet set")

	// Create new wallet set in Circle using pre-registered Entity Secret Ciphertext
	setName := fmt.Sprintf("%s-%s", w.config.WalletSetNamePrefix, time.Now().Format("20060102-150405"))
	circleResp, err := w.circleClient.CreateWalletSet(ctx, setName, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create Circle wallet set: %w", err)
	}

	// Create wallet set record
	walletSet = &entities.WalletSet{
		ID:                uuid.New(),
		Name:              setName,
		CircleWalletSetID: circleResp.WalletSet.ID,
		Status:            entities.WalletSetStatusActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := w.walletSetRepo.Create(ctx, walletSet); err != nil {
		return nil, fmt.Errorf("failed to create wallet set record: %w", err)
	}

	w.logger.Info("Created new developer-controlled wallet set",
		zap.String("wallet_set_id", walletSet.ID.String()),
		zap.String("circle_wallet_set_id", walletSet.CircleWalletSetID))

	return walletSet, nil
}

// shouldProcessJob checks if a job should be processed
func (w *Worker) shouldProcessJob(job *entities.WalletProvisioningJob) bool {
	// Job must be in queued or retry status
	if job.Status != entities.ProvisioningStatusQueued && job.Status != entities.ProvisioningStatusRetry {
		return false
	}

	// For retry status, check if it's time to retry
	if job.Status == entities.ProvisioningStatusRetry {
		if job.NextRetryAt == nil {
			return false
		}
		return time.Now().After(*job.NextRetryAt)
	}

	return true
}

// handleJobSuccess marks the job as completed and logs audit
func (w *Worker) handleJobSuccess(ctx context.Context, job *entities.WalletProvisioningJob) {
	job.MarkCompleted()

	resourceID := job.ID.String()
	w.auditService.LogWalletWorkerEvent(ctx, job.UserID, "provision_complete", "wallet_provisioning_job",
		map[string]interface{}{
			"attempt_count": job.AttemptCount,
			"chains":        job.Chains,
		},
		map[string]interface{}{
			"status":       string(job.Status),
			"completed_at": job.CompletedAt,
		},
		&resourceID, "success", nil)

	w.logger.Info("Wallet provisioning completed successfully",
		zap.String("job_id", job.ID.String()),
		zap.String("user_id", job.UserID.String()),
		zap.Int("attempt_count", job.AttemptCount))
}

// handleJobFailure determines retry strategy and logs audit
func (w *Worker) handleJobFailure(ctx context.Context, job *entities.WalletProvisioningJob, err error) {
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
		job.Status = entities.ProvisioningStatusFailed // Force to failed status

		w.logger.Error("Job failed permanently",
			zap.String("job_id", job.ID.String()),
			zap.String("user_id", job.UserID.String()),
			zap.Int("attempt_count", job.AttemptCount),
			zap.Bool("retryable", isRetryable),
			zap.Error(err))
	}

	// Log audit event
	resourceID := job.ID.String()
	status := "failed"
	if job.Status == entities.ProvisioningStatusRetry {
		status = "retry_scheduled"
	}

	w.auditService.LogWalletWorkerEvent(ctx, job.UserID, "provision_failed", "wallet_provisioning_job",
		map[string]interface{}{
			"attempt_count": job.AttemptCount - 1, // Before increment
			"status":        entities.ProvisioningStatusInProgress,
		},
		map[string]interface{}{
			"status":        string(job.Status),
			"attempt_count": job.AttemptCount,
			"next_retry_at": job.NextRetryAt,
			"error_type":    w.classifyError(err),
		},
		&resourceID, status, &errorMsg)
}

// isRetryableError determines if an error should trigger a retry
func (w *Worker) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Circle API errors
	if circleErr, ok := err.(entities.CircleErrorResponse); ok {
		// Retry on 5xx errors and 429 (rate limit)
		return circleErr.Code >= 500 || circleErr.Code == 429
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

// classifyError categorizes errors for metrics
func (w *Worker) classifyError(err error) string {
	if err == nil {
		return "none"
	}

	if circleErr, ok := err.(entities.CircleErrorResponse); ok {
		if circleErr.Code >= 500 {
			return "circle_5xx"
		} else if circleErr.Code == 429 {
			return "circle_rate_limit"
		} else if circleErr.Code >= 400 {
			return "circle_4xx"
		}
	}

	errMsg := err.Error()
	if contains(errMsg, "timeout") {
		return "timeout"
	} else if contains(errMsg, "connection") {
		return "network"
	} else if contains(errMsg, "validation") {
		return "validation"
	}

	return "unknown"
}

// updateMetrics updates worker performance metrics
func (w *Worker) updateMetrics(err error, duration time.Duration) {
	w.metrics.TotalJobsProcessed++
	w.metrics.LastProcessedAt = time.Now()

	if err != nil {
		w.metrics.FailedJobs++
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
