package wallet

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// BridgeWalletLister fetches wallets from Bridge for a given customer.
type BridgeWalletLister interface {
	CreateWalletForCustomer(ctx context.Context, customerID string, chain string) (*entities.ManagedWallet, error)
	ListWallets(ctx context.Context, customerID string) ([]*entities.ManagedWallet, error)
}

// UserProfileProvider retrieves user profile data needed during provisioning.
type UserProfileProvider interface {
	GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service handles wallet operations
type Service struct {
	walletRepo          WalletRepository
	provisioningJobRepo WalletProvisioningJobRepository
	auditService        AuditService
	onboardingService   OnboardingService
	bridgeWallets       BridgeWalletLister
	userProfiles        UserProfileProvider
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
	GetOrCreateForUser(ctx context.Context, userID uuid.UUID, chains []string) (job *entities.WalletProvisioningJob, created bool, err error)
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
	bridgeWallets BridgeWalletLister,
	userProfiles UserProfileProvider,
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
		bridgeWallets:       bridgeWallets,
		userProfiles:        userProfiles,
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
			entities.WalletChainSolana,
			entities.WalletChainPolygon,
			entities.WalletChainCelo,
			entities.WalletChainTron,
			entities.WalletChainBase,
			entities.WalletChainAvalanche,
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
			entities.WalletChainSolana,
			entities.WalletChainPolygon,
			entities.WalletChainCelo,
			entities.WalletChainTron,
		}
	}

	return normalized
}

// CreateWalletsForUser creates developer-controlled wallets for a user across specified chains
// This follows the developer-controlled-wallet pattern where we use a pre-registered Entity Secret Ciphertext
// Uses a transactional approach to prevent race conditions when concurrent requests create wallets
func (s *Service) CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error {
	s.logger.Info("Creating developer-controlled wallets for user",
		zap.String("userID", userID.String()),
		zap.Any("chains", chains))

	if len(chains) == 0 {
		chains = s.config.SupportedChains
	}

	chainStrings := make([]string, len(chains))
	for i, chain := range chains {
		chainStrings[i] = string(chain)
	}

	job, created, err := s.provisioningJobRepo.GetOrCreateForUser(ctx, userID, chainStrings)
	if err != nil {
		return fmt.Errorf("failed to get or create provisioning job: %w", err)
	}

	if !created {
		s.logger.Info("User already has an active provisioning job",
			zap.String("userID", userID.String()),
			zap.String("jobID", job.ID.String()),
			zap.String("status", string(job.Status)))
		return nil
	}

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

		if job, getErr := s.provisioningJobRepo.GetByID(bgCtx, jobID); getErr == nil {
			job.MarkFailed(err.Error(), 30*time.Second)
			if updateErr := s.provisioningJobRepo.Update(bgCtx, job); updateErr != nil {
				s.logger.Error("Failed to update job status after async failure",
					zap.Error(updateErr),
					zap.String("jobID", jobID.String()))
			}
		}
	}
}

// ProcessWalletProvisioningJob creates wallets on Bridge and saves them locally.
func (s *Service) ProcessWalletProvisioningJob(ctx context.Context, jobID uuid.UUID) error {
	job, err := s.provisioningJobRepo.GetByID(ctx, jobID)
	if err != nil {
		return fmt.Errorf("failed to get provisioning job: %w", err)
	}

	job.MarkStarted()
	if err := s.provisioningJobRepo.Update(ctx, job); err != nil {
		s.logger.Error("Failed to update job status to started", zap.Error(err), zap.String("job_id", jobID.String()))
		return fmt.Errorf("failed to update job status: %w", err)
	}

	customerID, err := s.userProfiles.GetBridgeCustomerID(ctx, job.UserID)
	if err != nil || customerID == "" {
		msg := "bridge customer ID not found"
		job.MarkFailed(msg, 30*time.Second)
		_ = s.provisioningJobRepo.Update(ctx, job)
		return fmt.Errorf("%s for user %s", msg, job.UserID)
	}

	saved := 0
	for _, chain := range job.Chains {
		existing, _ := s.walletRepo.GetByUserAndChain(ctx, job.UserID, entities.WalletChain(chain))
		if existing != nil {
			saved++
			continue
		}

		mw, err := s.bridgeWallets.CreateWalletForCustomer(ctx, customerID, chain)
		if err != nil {
			s.logger.Warn("Failed to create wallet on Bridge, checking if already exists remotely",
				zap.Error(err), zap.String("chain", chain), zap.String("customerID", customerID))

			// Wallet may already exist on Bridge (e.g. stale idempotency key after >24h).
			// Recover by listing remote wallets and importing the match.
			mw = s.recoverWalletFromBridge(ctx, customerID, chain)
			if mw == nil {
				continue
			}
		}

		mw.UserID = job.UserID
		if err := s.walletRepo.Create(ctx, mw); err != nil {
			s.logger.Error("Failed to save wallet", zap.Error(err), zap.String("chain", chain))
			continue
		}
		saved++
		s.logger.Info("Wallet created and saved",
			zap.String("userID", job.UserID.String()),
			zap.String("chain", chain),
			zap.String("address", mw.Address),
			zap.String("bridgeWalletID", mw.BridgeWalletID))
	}

	if saved == 0 {
		job.MarkFailed("failed to create any wallets", 30*time.Second)
		_ = s.provisioningJobRepo.Update(ctx, job)
		return fmt.Errorf("failed to create any wallets")
	}

	job.MarkCompleted()
	_ = s.provisioningJobRepo.Update(ctx, job)

	s.logger.Info("Wallet provisioning completed",
		zap.String("userID", job.UserID.String()),
		zap.Int("saved", saved),
		zap.Int("total", len(job.Chains)))

	return nil
}

// recoverWalletFromBridge attempts to find an existing wallet on Bridge when
// creation fails (e.g. stale idempotency key after >24h). Returns nil if no
// matching wallet is found.
func (s *Service) recoverWalletFromBridge(ctx context.Context, customerID, chain string) *entities.ManagedWallet {
	remoteWallets, err := s.bridgeWallets.ListWallets(ctx, customerID)
	if err != nil {
		s.logger.Error("Failed to list Bridge wallets for recovery",
			zap.Error(err), zap.String("customerID", customerID))
		return nil
	}

	targetRail := entities.WalletChain(chain).ToBridgeWalletChain()
	for _, rw := range remoteWallets {
		rwRail := rw.Chain.ToBridgeWalletChain()
		if rwRail == targetRail {
			s.logger.Info("Recovered existing wallet from Bridge",
				zap.String("chain", chain),
				zap.String("bridgeWalletID", rw.BridgeWalletID),
				zap.String("address", rw.Address))
			return rw
		}
	}

	s.logger.Warn("No matching wallet found on Bridge for recovery",
		zap.String("chain", chain), zap.String("customerID", customerID))
	return nil
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

// CreateWalletForCustomer delegates wallet creation to Bridge.
func (s *Service) CreateWalletForCustomer(ctx context.Context, customerID string, chain string) (*entities.ManagedWallet, error) {
	return s.bridgeWallets.CreateWalletForCustomer(ctx, customerID, chain)
}

// SaveWallet persists a managed wallet to the database.
func (s *Service) SaveWallet(ctx context.Context, wallet *entities.ManagedWallet) error {
	return s.walletRepo.Create(ctx, wallet)
}

// ListBridgeWallets fetches wallets from Bridge for a given customer.
func (s *Service) ListBridgeWallets(ctx context.Context, customerID string) ([]*entities.ManagedWallet, error) {
	return s.bridgeWallets.ListWallets(ctx, customerID)
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

// SyncWalletStatus updates a wallet's status from Bridge webhook events
func (s *Service) SyncWalletStatus(ctx context.Context, bridgeWalletID string, bridgeStatus string) error {
	wallet, err := s.walletRepo.GetByBridgeWalletID(ctx, bridgeWalletID)
	if err != nil {
		return fmt.Errorf("wallet not found for Bridge ID %s: %w", bridgeWalletID, err)
	}

	localStatus := mapBridgeStatusToWalletStatus(bridgeStatus)
	if wallet.Status == localStatus {
		s.logger.Debug("Wallet status unchanged",
			zap.String("wallet_id", wallet.ID.String()),
			zap.String("bridge_wallet_id", bridgeWalletID),
			zap.String("status", string(wallet.Status)))
		return nil
	}

	wallet.Status = localStatus
	if err := s.walletRepo.Update(ctx, wallet); err != nil {
		return fmt.Errorf("failed to update wallet status: %w", err)
	}

	s.logger.Info("Wallet status synced from Bridge",
		zap.String("wallet_id", wallet.ID.String()),
		zap.String("bridge_wallet_id", bridgeWalletID),
		zap.String("old_status", string(wallet.Status)),
		zap.String("new_status", string(localStatus)))

	return nil
}

// mapBridgeStatusToWalletStatus converts Bridge wallet status to local wallet status
func mapBridgeStatusToWalletStatus(bridgeStatus string) entities.WalletStatus {
	switch bridgeStatus {
	case "active", "live", "ready":
		return entities.WalletStatusLive
	case "failed", "error", "rejected":
		return entities.WalletStatusFailed
	case "creating", "pending", "processing", "initializing":
		return entities.WalletStatusCreating
	default:
		return entities.WalletStatusCreating
	}
}
