package wallet

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// Service handles wallet operations - wallet set management, multi-chain wallet creation
type Service struct {
	walletRepo          WalletRepository
	walletSetRepo       WalletSetRepository
	provisioningJobRepo WalletProvisioningJobRepository
	circleClient        CircleClient
	auditService        AuditService
	entitySecretService EntitySecretService
	onboardingService   OnboardingService
	logger              *zap.Logger
	config              Config
}

const defaultWalletSetNamePrefix = "STACK-WalletSet"

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
	GetByCircleWalletID(ctx context.Context, circleWalletID string) (*entities.ManagedWallet, error)
	Update(ctx context.Context, wallet *entities.ManagedWallet) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WalletStatus) error
}

type WalletSetRepository interface {
	Create(ctx context.Context, walletSet *entities.WalletSet) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletSet, error)
	GetByCircleWalletSetID(ctx context.Context, circleWalletSetID string) (*entities.WalletSet, error)
	GetActive(ctx context.Context) (*entities.WalletSet, error)
	Update(ctx context.Context, walletSet *entities.WalletSet) error
}

type WalletProvisioningJobRepository interface {
	Create(ctx context.Context, job *entities.WalletProvisioningJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WalletProvisioningJob, error)
	GetRetryableJobs(ctx context.Context, limit int) ([]*entities.WalletProvisioningJob, error)
	Update(ctx context.Context, job *entities.WalletProvisioningJob) error
}

// External service interfaces
type CircleClient interface {
	CreateWalletSet(ctx context.Context, name string, entitySecretCiphertext string) (*entities.CircleWalletSetResponse, error)
	GetWalletSet(ctx context.Context, walletSetID string) (*entities.CircleWalletSetResponse, error)
	CreateWallet(ctx context.Context, req entities.CircleWalletCreateRequest) (*entities.CircleWalletCreateResponse, error)
	GetWallet(ctx context.Context, walletID string) (*entities.CircleWalletCreateResponse, error)
	HealthCheck(ctx context.Context) error
	GetMetrics() map[string]interface{}
}

type AuditService interface {
	LogWalletEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error
}

type EntitySecretService interface {
	GenerateEntitySecretCiphertext(ctx context.Context) (string, error)
}

type OnboardingService interface {
	ProcessWalletCreationComplete(ctx context.Context, userID uuid.UUID) error
}

// NewService creates a new wallet service
func NewService(
	walletRepo WalletRepository,
	walletSetRepo WalletSetRepository,
	provisioningJobRepo WalletProvisioningJobRepository,
	circleClient CircleClient,
	auditService AuditService,
	entitySecretService EntitySecretService,
	onboardingService OnboardingService,
	logger *zap.Logger,
	cfg Config,
) *Service {
	cfg.DefaultWalletSetID = strings.TrimSpace(cfg.DefaultWalletSetID)
	if cfg.WalletSetNamePrefix == "" {
		cfg.WalletSetNamePrefix = defaultWalletSetNamePrefix
	}

	cfg.SupportedChains = normalizeSupportedChains(cfg.SupportedChains, logger)

	// Entity secret is now generated dynamically, no configuration needed

	return &Service{
		walletRepo:          walletRepo,
		walletSetRepo:       walletSetRepo,
		provisioningJobRepo: provisioningJobRepo,
		circleClient:        circleClient,
		auditService:        auditService,
		entitySecretService: entitySecretService,
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

	// Process the job immediately (in production this might be done by a background worker)
	if err := s.ProcessWalletProvisioningJob(ctx, job.ID); err != nil {
		s.logger.Error("Failed to process wallet provisioning job",
			zap.Error(err),
			zap.String("jobID", job.ID.String()))
		return fmt.Errorf("failed to process provisioning job: %w", err)
	}

	return nil
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

	// Create wallets for each chain
	var lastErr error
	successCount := 0

	for _, chainStr := range job.Chains {
		chain := entities.WalletChain(chainStr)

		if err := s.createWalletForChain(ctx, job.UserID, chain, walletSet, job); err != nil {
			s.logger.Error("Failed to create wallet for chain",
				zap.Error(err),
				zap.String("userID", job.UserID.String()),
				zap.String("chain", chainStr))
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
		if time.Now().After(*job.NextRetryAt) {
			s.logger.Info("Retrying wallet provisioning job",
				zap.String("jobID", job.ID.String()),
				zap.String("userID", job.UserID.String()))

			if err := s.ProcessWalletProvisioningJob(ctx, job.ID); err != nil {
				s.logger.Error("Failed to retry provisioning job",
					zap.Error(err),
					zap.String("jobID", job.ID.String()))
			}
		}
	}

	return nil
}

// Helper methods

func (s *Service) ensureWalletSet(ctx context.Context) (*entities.WalletSet, error) {
	// First, try to use configured default wallet set ID
	if s.config.DefaultWalletSetID != "" {
		if walletSet, err := s.walletSetRepo.GetByCircleWalletSetID(ctx, s.config.DefaultWalletSetID); err == nil && walletSet != nil {
			s.logger.Debug("Using configured default wallet set",
				zap.String("walletSetID", walletSet.ID.String()),
				zap.String("circleWalletSetID", walletSet.CircleWalletSetID))
			return walletSet, nil
		}

		s.logger.Info("Configured Circle wallet set not found locally, attempting to hydrate",
			zap.String("circleWalletSetID", s.config.DefaultWalletSetID))

		circleSet, err := s.circleClient.GetWalletSet(ctx, s.config.DefaultWalletSetID)
		if err == nil && circleSet != nil {
			walletSet := &entities.WalletSet{
				ID:                uuid.New(),
				Name:              circleSet.WalletSet.Name,
				CircleWalletSetID: circleSet.WalletSet.ID,
				Status:            entities.WalletSetStatusActive,
				CreatedAt:         time.Now(),
				UpdatedAt:         time.Now(),
			}

			if createErr := s.walletSetRepo.Create(ctx, walletSet); createErr != nil {
				s.logger.Warn("Failed to persist hydrated wallet set, attempting to reuse existing record",
					zap.Error(createErr),
					zap.String("circleWalletSetID", walletSet.CircleWalletSetID))

				existing, fetchErr := s.walletSetRepo.GetByCircleWalletSetID(ctx, walletSet.CircleWalletSetID)
				if fetchErr == nil && existing != nil {
					return existing, nil
				}

				return nil, fmt.Errorf("failed to persist configured wallet set: %w", createErr)
			}

			return walletSet, nil
		}

		if err != nil {
			s.logger.Warn("Failed to load configured Circle wallet set from API",
				zap.String("circleWalletSetID", s.config.DefaultWalletSetID),
				zap.Error(err))
		}
	}

	// Try to get existing active wallet set
	walletSet, err := s.walletSetRepo.GetActive(ctx)
	if err == nil && walletSet != nil {
		s.logger.Debug("Using existing active wallet set",
			zap.String("walletSetID", walletSet.ID.String()),
			zap.String("circleWalletSetID", walletSet.CircleWalletSetID))
		return walletSet, nil
	}

	s.logger.Info("Creating new developer-controlled wallet set")

	// Create new wallet set in Circle using pre-registered Entity Secret Ciphertext
	setName := fmt.Sprintf("%s-%s", s.config.WalletSetNamePrefix, time.Now().Format("20060102"))
	circleResp, err := s.circleClient.CreateWalletSet(ctx, setName, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create Circle wallet set: %w", err)
	}

	// Generate entity secret ciphertext for the wallet set
	entitySecretCiphertext, err := s.entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret ciphertext: %w", err)
	}

	// Create wallet set record
	walletSet = &entities.WalletSet{
		ID:                     uuid.New(),
		Name:                   setName,
		CircleWalletSetID:      circleResp.WalletSet.ID,
		EntitySecretCiphertext: entitySecretCiphertext,
		Status:                 entities.WalletSetStatusActive,
		CreatedAt:              time.Now(),
		UpdatedAt:              time.Now(),
	}

	if err := s.walletSetRepo.Create(ctx, walletSet); err != nil {
		return nil, fmt.Errorf("failed to create wallet set record: %w", err)
	}

	s.logger.Info("Created new developer-controlled wallet set",
		zap.String("walletSetID", walletSet.ID.String()),
		zap.String("circleWalletSetID", walletSet.CircleWalletSetID))

	return walletSet, nil
}

func (s *Service) createWalletForChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain,
	walletSet *entities.WalletSet, job *entities.WalletProvisioningJob) error {

	s.logger.Info("Creating developer-controlled wallet for chain",
		zap.String("userID", userID.String()),
		zap.String("chain", string(chain)))

	// Check if wallet already exists for this chain
	existingWallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err == nil && existingWallet != nil {
		s.logger.Info("Wallet already exists for chain",
			zap.String("userID", userID.String()),
			zap.String("chain", string(chain)),
			zap.String("address", existingWallet.Address))
		return nil
	}

	// Determine account type based on chain following developer-controlled-wallet pattern
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

	// Add request to job log
	job.AddCircleRequest("create_wallet", circleReq, nil)

	// Create wallet in Circle using developer-controlled pattern
	circleResp, err := s.circleClient.CreateWallet(ctx, circleReq)
	if err != nil {
		// Add error response to job log
		job.AddCircleRequest("create_wallet_error", circleReq, map[string]any{"error": err.Error()})
		return fmt.Errorf("failed to create wallet in Circle: %w", err)
	}

	// Add successful response to job log
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
		return fmt.Errorf("no address found for chain %s in Circle response", string(chain))
	}

	// Create wallet record with Circle wallet ID for transaction operations
	wallet := &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         userID,
		Chain:          chain,
		Address:        address,
		CircleWalletID: circleResp.Wallet.ID, // Store Circle wallet ID for transactions
		WalletSetID:    walletSet.ID,
		AccountType:    accountType,
		Status:         entities.WalletStatusLive, // Circle API returns live wallets
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := wallet.Validate(); err != nil {
		return fmt.Errorf("wallet validation failed: %w", err)
	}

	if err := s.walletRepo.Create(ctx, wallet); err != nil {
		return fmt.Errorf("failed to create wallet record: %w", err)
	}

	// Log audit event
	if err := s.auditService.LogWalletEvent(ctx, userID, "developer_wallet_created", "wallet", nil, wallet); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Created developer-controlled wallet successfully",
		zap.String("userID", userID.String()),
		zap.String("chain", string(chain)),
		zap.String("address", address),
		zap.String("circleWalletID", circleResp.Wallet.ID))

	return nil
}

// HealthCheck performs health checks on the wallet service
func (s *Service) HealthCheck(ctx context.Context) error {
	s.logger.Debug("Performing wallet service health check")

	// Check Circle client health
	if err := s.circleClient.HealthCheck(ctx); err != nil {
		return fmt.Errorf("circle client health check failed: %w", err)
	}

	// Check if we can access the wallet set
	_, err := s.walletSetRepo.GetActive(ctx)
	if err != nil {
		s.logger.Warn("No active wallet set found", zap.Error(err))
		// This is not necessarily a failure for health check
	}

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

// GetWalletByUserAndChain retrieves a wallet for a specific user and chain
func (s *Service) GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	s.logger.Debug("Getting wallet for user and chain",
		zap.String("userID", userID.String()),
		zap.String("chain", string(chain)))

	wallet, err := s.walletRepo.GetByUserAndChain(ctx, userID, chain)
	if err != nil {
		s.logger.Error("Failed to get wallet for user and chain",
			zap.Error(err),
			zap.String("userID", userID.String()),
			zap.String("chain", string(chain)))
		return nil, fmt.Errorf("failed to get wallet: %w", err)
	}

	return wallet, nil
}

// GetMetrics returns service metrics for monitoring
func (s *Service) GetMetrics() map[string]interface{} {
	metrics := s.circleClient.GetMetrics()

	// Add service-specific metrics
	metrics["service"] = "wallet"
	metrics["timestamp"] = time.Now()

	return metrics
}

// SupportedChains returns the configured wallet chains
func (s *Service) SupportedChains() []entities.WalletChain {
	return append([]entities.WalletChain(nil), s.config.SupportedChains...)
}
