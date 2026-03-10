package wallet

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Service handles wallet operations
type Service struct {
	walletRepo          WalletRepository
	provisioningJobRepo WalletProvisioningJobRepository
	auditService        AuditService
	onboardingService   OnboardingService
	logger              *zap.Logger
	config              Config
}

const (
	defaultWalletSetNamePrefix     = "STACK-WalletSet"
	walletProvisioningAsyncTimeout = 90 * time.Second
)

// Config captures runtime configuration for the wallet service
type Config struct {
	WalletSetNamePrefix string
	SupportedChains     []entities.WalletChain
	DefaultWalletSetID  string
}

// Repository interfaces
type WalletRepository interface {
	Create(ctx context.Context, wallet *entities.ManagedWallet) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.ManagedWallet, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
	GetByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error)
	GetByBridgeWalletID(ctx context.Context, bridgeWalletID string) (*entities.ManagedWallet, error)
	Update(ctx context.Context, wallet *entities.ManagedWallet) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WalletStatus) error
}

type WalletProvisioningJobRepository interface {
	Create(ctx context.Context, job *entities.WalletProvisioningJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error)
	Update(ctx context.Context, job *entities.WalletProvisioningJob) error
}

type AuditService interface {
	LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error
}

type OnboardingService interface {
	ProcessWalletCreationComplete(ctx context.Context, userID uuid.UUID) error
}

// NewService creates a new wallet service
func NewService(
	walletRepo WalletRepository,
	provisioningJobRepo WalletProvisioningJobRepository,
	auditService AuditService,
	onboardingService OnboardingService,
	logger *zap.Logger,
	cfg Config,
) *Service {
	if cfg.WalletSetNamePrefix == "" {
		cfg.WalletSetNamePrefix = defaultWalletSetNamePrefix
	}
	cfg.SupportedChains = normalizeSupportedChains(cfg.SupportedChains, logger)
	return &Service{
		walletRepo:          walletRepo,
		provisioningJobRepo: provisioningJobRepo,
		auditService:        auditService,
		onboardingService:   onboardingService,
		logger:              logger,
		config:              cfg,
	}
}

// SetOnboardingService sets the onboarding service (for dependency injection after creation)
func (s *Service) SetOnboardingService(onboardingService OnboardingService) {
	s.onboardingService = onboardingService
}

func normalizeSupportedChains(chains []entities.WalletChain, logger *zap.Logger) []entities.WalletChain {
	if len(chains) == 0 {
		return []entities.WalletChain{
			entities.WalletChainSOLDevnet,
		}
	}

	normalized := make([]entities.WalletChain, 0, len(chains))
	seen := make(map[entities.WalletChain]struct{})

	for _, chain := range chains {
		if !chain.IsValid() {
			logger.Warn("Ignoring unsupported wallet chain in configuration", zap.String("chain", string(chain)))
			continue
		}
		if _, ok := seen[chain]; ok {
			continue
		}
		seen[chain] = struct{}{}
		normalized = append(normalized, chain)
	}

	if len(normalized) == 0 {
		return []entities.WalletChain{
			entities.WalletChainSOLDevnet,
		}
	}

	return normalized
}

// CreateWalletsForUser creates developer-controlled wallets for a user across specified chains
// This follows the developer-controlled-wallet pattern where we use a pre-registered Entity Secret Ciphertext
func (s *Service) CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error {
	s.logger.Info("Creating developer-controlled wallets for user",
		zap.String("userID", userID.String()),
		zap.Any("chains", chains))

	if len(chains) == 0 {
		chains = s.config.SupportedChains
	}

	// Check if user already has a provisioning job
	existingJob, err := s.provisioningJobRepo.GetByUserID(ctx, userID)
	if err == nil && existingJob != nil {
		s.logger.Info("User already has a provisioning job",
			zap.String("userID", userID.String()),
			zap.String("jobID", existingJob.ID.String()),
			zap.String("status", string(existingJob.Status)))

		// If job is in progress or queued, don't create a new one
		if existingJob.Status == entities.ProvisioningStatusQueued ||
			existingJob.Status == entities.ProvisioningStatusInProgress {
			return nil
		}
	}

	// Convert chain types to strings
	chainStrings := make([]string, len(chains))
	for i, chain := range chains {
		chainStrings[i] = string(chain)
	}

	// Create provisioning job
	job := &entities.WalletProvisioningJob{
		ID:           uuid.New(),
		UserID:       userID,
		Chains:       chainStrings,
		Status:       entities.ProvisioningStatusQueued,
		AttemptCount: 0,
		MaxAttempts:  3,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.provisioningJobRepo.Create(ctx, job); err != nil {
		return fmt.Errorf("failed to create provisioning job: %w", err)
	}

	// Process asynchronously so API requests return immediately after queueing.
	go s.processWalletProvisioningAsync(job.ID, userID)

	return nil
}

func (s *Service) processWalletProvisioningAsync(jobID, userID uuid.UUID) {
	bgCtx, cancel := context.WithTimeout(context.Background(), walletProvisioningAsyncTimeout)
	defer cancel()

	if err := s.ProcessWalletProvisioningJob(bgCtx, jobID); err != nil {
		s.logger.Error("Asynchronous wallet provisioning job failed",
			zap.Error(err),
			zap.String("jobID", jobID.String()),
			zap.String("userID", userID.String()))
	}
}

// ProcessWalletProvisioningJob processes a wallet provisioning job
func (s *Service) ProcessWalletProvisioningJob(ctx context.Context, jobID uuid.UUID) error {
	s.logger.Info("Processing wallet provisioning job", zap.String("jobID", jobID.String()))

	// Get the job
	job, err := s.provisioningJobRepo.GetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get provisioning job: %w", err)
	}

	if job.Status != entities.ProvisioningStatusQueued && job.Status != entities.ProvisioningStatusRetry {
		s.logger.Info("Job is not in queued/retry status",
			zap.String("jobID", jobID.String()),
			zap.String("status", string(job.Status)))
		return nil
	}

	// Mark job as started
	job.MarkStarted()
	if err := s.provisioningJobRepo.Update(ctx, job); err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	// Get or create wallet set
	walletSet, err := s.ensureWalletSet(ctx)
	if err != nil {
		job.MarkFailed(fmt.Sprintf("Failed to ensure wallet set: %v", err), 5*time.Minute)
		s.provisioningJobRepo.Update(ctx, job)
		return fmt.Errorf("failed to ensure wallet set: %w", err)
	}

	// Separate EVM and non-EVM chains so EVM chains share one wallet address
	var evmChains, otherChains []entities.WalletChain
	for _, chainStr := range job.Chains {
		chain := entities.WalletChain(chainStr)
		if chain.GetChainFamily() == "EVM" {
			evmChains = append(evmChains, chain)
		} else {
			otherChains = append(otherChains, chain)
		}
	}

	var lastErr error
	successCount := 0

	// Create a single SCA wallet for all EVM chains, then store one record per chain
	if len(evmChains) > 0 {
		created, err := s.createEVMWallets(ctx, job.UserID, evmChains, walletSet, job)
		if err != nil {
			s.logger.Error("Failed to create EVM wallets",
				zap.Error(err), zap.String("userID", job.UserID.String()))
			lastErr = err
		}
		successCount += created
	}

	// Create individual wallets for non-EVM chains (SOL, APT, etc.)
	for _, chain := range otherChains {
		if err := s.createWalletForChain(ctx, job.UserID, chain, walletSet, job); err != nil {
			s.logger.Error("Failed to create wallet for chain",
				zap.Error(err),
				zap.String("userID", job.UserID.String()),
				zap.String("chain", string(chain)))
			lastErr = err
		} else {
			successCount++
		}
	}

	// Update job status based on results
	if successCount == len(job.Chains) {
		// All wallets created successfully
		job.MarkCompleted()
		s.logger.Info("All wallets created successfully",
			zap.String("jobID", jobID.String()),
			zap.String("userID", job.UserID.String()),
			zap.Int("walletCount", successCount))

		// Trigger onboarding completion callback
		if s.onboardingService != nil {
			if err := s.onboardingService.ProcessWalletCreationComplete(ctx, job.UserID); err != nil {
				s.logger.Warn("Failed to process wallet creation complete in onboarding service",
					zap.Error(err),
					zap.String("userID", job.UserID.String()))
			} else {
				s.logger.Info("Wallet provisioning completed and onboarding status updated",
					zap.String("userID", job.UserID.String()))
			}
		}

	} else if successCount > 0 {
		// Partial success - mark as failed but note partial success
		job.MarkFailed(fmt.Sprintf("Partial success: %d/%d wallets created. Last error: %v",
			successCount, len(job.Chains), lastErr), 10*time.Minute)
	} else {
		// Complete failure
		job.MarkFailed(fmt.Sprintf("Failed to create any wallets: %v", lastErr), 10*time.Minute)
	}

	if err := s.provisioningJobRepo.Update(ctx, job); err != nil {
		s.logger.Error("Failed to update job final status", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogWalletEvent(ctx, job.UserID, "wallet_provisioning_processed", "provisioning_job",
		nil, map[string]any{
			"job_id":        job.ID,
			"status":        string(job.Status),
			"success_count": successCount,
			"total_chains":  len(job.Chains),
		}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	return lastErr
}

// GetWalletAddresses returns wallet addresses for a user, optionally filtered by chain
func (s *Service) GetWalletAddresses(ctx context.Context, userID uuid.UUID, chain *entities.WalletChain) (*entities.WalletAddressesResponse, error) {
	s.logger.Debug("Getting wallet addresses",
		zap.String("userID", userID.String()),
		zap.Any("chain", chain))

	var wallets []*entities.ManagedWallet
	var err error

	if chain != nil {
		// Get wallet for specific chain
		wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, *chain)
		if err != nil {
			return nil, fmt.Errorf("failed to get wallet for chain %s: %w", *chain, err)
		}
		if wallet != nil {
			wallets = []*entities.ManagedWallet{wallet}
		}
	} else {
		// Get all wallets for user
		wallets, err = s.walletRepo.GetByUserID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to get wallets for user: %w", err)
		}
	}

	// Convert to response format
	var walletResponses []entities.WalletAddressResponse
	for _, wallet := range wallets {
		if wallet.IsReady() {
			walletResponses = append(walletResponses, entities.WalletAddressResponse{
				Chain:   wallet.Chain,
				Address: wallet.Address,
				Status:  string(wallet.Status),
			})
		}
	}

	return &entities.WalletAddressesResponse{
		Wallets: walletResponses,
	}, nil
}

// GetWalletStatus returns comprehensive wallet status for a user
func (s *Service) GetWalletStatus(ctx context.Context, userID uuid.UUID) (*entities.WalletStatusResponse, error) {
	s.logger.Debug("Getting wallet status", zap.String("userID", userID.String()))

	// Get all wallets for user
	wallets, err := s.walletRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallets: %w", err)
	}

	// Get provisioning job if exists
	provisioningJob, err := s.provisioningJobRepo.GetByUserID(ctx, userID)
	if err != nil {
		// Not finding a job is okay
		provisioningJob = nil
	}

	// Count wallets by status
	var readyCount, pendingCount, failedCount int
	walletsByChain := make(map[string]entities.WalletChainStatus)

	for _, wallet := range wallets {
		switch wallet.Status {
		case entities.WalletStatusLive:
			readyCount++
		case entities.WalletStatusCreating:
			pendingCount++
		case entities.WalletStatusFailed:
			failedCount++
		}

		// Add to chain status map
		chainStatus := entities.WalletChainStatus{
			Chain:     wallet.Chain,
			Status:    string(wallet.Status),
			CreatedAt: &wallet.CreatedAt,
		}

		if wallet.IsReady() {
			chainStatus.Address = &wallet.Address
		}

		walletsByChain[string(wallet.Chain)] = chainStatus
	}

	// Create response
	response := &entities.WalletStatusResponse{
		UserID:         userID,
		TotalWallets:   len(wallets),
		ReadyWallets:   readyCount,
		PendingWallets: pendingCount,
		FailedWallets:  failedCount,
		WalletsByChain: walletsByChain,
	}

	// Add provisioning job info if exists
	if provisioningJob != nil {
		progress := fmt.Sprintf("%d/%d chains", readyCount+failedCount, len(provisioningJob.Chains))
		if len(provisioningJob.Chains) > 0 {
			percentage := float64(readyCount+failedCount) / float64(len(provisioningJob.Chains)) * 100
			progress = fmt.Sprintf("%.0f%% complete", percentage)
		}

		response.ProvisioningJob = &entities.WalletProvisioningJobResponse{
			ID:           provisioningJob.ID,
			Status:       string(provisioningJob.Status),
			Progress:     progress,
			AttemptCount: provisioningJob.AttemptCount,
			MaxAttempts:  provisioningJob.MaxAttempts,
			ErrorMessage: provisioningJob.ErrorMessage,
			NextRetryAt:  provisioningJob.NextRetryAt,
			CreatedAt:    provisioningJob.CreatedAt,
		}
	}

	return response, nil
}

// RetryFailedWalletProvisioning retries failed wallet provisioning jobs
func (s *Service) RetryFailedWalletProvisioning(ctx context.Context, limit int) error {
	s.logger.Info("Retrying failed wallet provisioning jobs", zap.Int("limit", limit))

	jobs, err := s.provisioningJobRepo.GetRetryableJobs(ctx, limit)
	if err != nil {
		return fmt.Errorf("failed to get retryable jobs: %w", err)
	}

	s.logger.Info("Found retryable jobs", zap.Int("count", len(jobs)))

	for _, job := range jobs {
		if job.NextRetryAt != nil && time.Now().Before(*job.NextRetryAt) {
			continue
		}

		s.logger.Info("Retrying wallet provisioning job",
			zap.String("jobID", job.ID.String()),
			zap.String("userID", job.UserID.String()))

		if err := s.ProcessWalletProvisioningJob(ctx, job.ID); err != nil {
			s.logger.Error("Failed to retry provisioning job",
				zap.Error(err),
				zap.String("jobID", job.ID.String()))
		}
	}

	return nil
}

// ensureWalletSet is no longer used — wallet creation is handled by Bridge in onboarding_processor.
func (s *Service) ensureWalletSet(ctx context.Context) (*entities.WalletSet, error) {
	return nil, fmt.Errorf("wallet sets are no longer used; wallets are created via Bridge")
}

// createEVMWallets is no longer used — wallet creation is handled by Bridge in onboarding_processor.
func (s *Service) createEVMWallets(ctx context.Context, userID uuid.UUID, evmChains []entities.WalletChain,
	walletSet *entities.WalletSet, job *entities.WalletProvisioningJob) (int, error) {
	return 0, fmt.Errorf("EVM wallet creation via Circle is no longer supported; use Bridge onboarding")
}

// createWalletForChain is no longer used — wallet creation is handled by Bridge in onboarding_processor.
func (s *Service) createWalletForChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain,
	walletSet *entities.WalletSet, job *entities.WalletProvisioningJob) error {
	return fmt.Errorf("wallet creation via Circle is no longer supported; use Bridge onboarding")
}

// HealthCheck performs health checks on the wallet service
func (s *Service) HealthCheck(ctx context.Context) error {
	s.logger.Info("Wallet service health check passed")
	return nil
}

// GetProvisioningJobByUserID retrieves the provisioning job for a user
func (s *Service) GetProvisioningJobByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error) {
	s.logger.Debug("Getting provisioning job for user",
		zap.String("userID", userID.String()))

	job, err := s.provisioningJobRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get provisioning job for user",
			zap.Error(err),
			zap.String("userID", userID.String()))
		return nil, fmt.Errorf("failed to get provisioning job: %w", err)
	}

	return job, nil
}

// GetWalletByUserAndChain retrieves a wallet for a specific user and chain.
// For EVM chains, if the exact chain row isn't found, it falls back to any other
// EVM chain wallet for the user — all EVM chains share the same address.
func (s *Service) GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	s.logger.Debug("Getting wallet for user and chain",
		zap.String("userID", userID.String()),
		zap.String("chain", string(chain)))

	wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err == nil && wallet != nil {
		return wallet, nil
	}

	// For EVM chains, fall back to any other EVM wallet — they share the same address.
	if chain.GetChainFamily() == "EVM" {
		allWallets, listErr := s.walletRepo.GetByUserID(ctx, userID)
		if listErr == nil {
			for _, w := range allWallets {
				if w.GetChainFamily() == "EVM" && w.IsReady() {
					s.logger.Debug("EVM wallet fallback: returning wallet from sibling chain",
						zap.String("userID", userID.String()),
						zap.String("requested_chain", string(chain)),
						zap.String("found_chain", string(w.Chain)),
						zap.String("address", w.Address))
					// Return a copy with the requested chain so callers see the right chain label.
					copy := *w
					copy.Chain = chain
					return &copy, nil
				}
			}
		}
	}

	if err != nil {
		s.logger.Debug("Wallet not found for user and chain",
			zap.String("userID", userID.String()),
			zap.String("chain", string(chain)))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}
	return nil, fmt.Errorf("wallet not found for chain %s", chain)
}

// GetMetrics returns service metrics for monitoring
func (s *Service) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"service":   "wallet",
		"timestamp": time.Now(),
	}
}

// SupportedChains returns the configured wallet chains
func (s *Service) SupportedChains() []entities.WalletChain {
	return append([]entities.WalletChain(nil), s.config.SupportedChains...)
}
