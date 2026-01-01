package onboarding

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

var kycRequiredFeatures = []string{"virtual_account", "cards", "fiat_withdrawal"}

// Service handles onboarding operations - user creation, KYC flow, wallet provisioning
type Service struct {
	userRepo            UserRepository
	onboardingFlowRepo  OnboardingFlowRepository
	kycSubmissionRepo   KYCSubmissionRepository
	walletService       WalletService
	gridService         GridService
	emailService        EmailService
	auditService        AuditService
	alpacaAdapter       AlpacaAdapter
	allocationService   AllocationService
	logger              *zap.Logger
	defaultWalletChains []entities.WalletChain
}

// Repository interfaces
type UserRepository interface {
	Create(ctx context.Context, user *entities.UserProfile) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
	GetByAuthProviderID(ctx context.Context, authProviderID string) (*entities.UserProfile, error)
	Update(ctx context.Context, user *entities.UserProfile) error
	UpdateOnboardingStatus(ctx context.Context, userID uuid.UUID, status entities.OnboardingStatus) error
	UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status string, approvedAt *time.Time, rejectionReason *string) error
}

type OnboardingFlowRepository interface {
	Create(ctx context.Context, flow *entities.OnboardingFlow) error
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.OnboardingFlow, error)
	GetByUserAndStep(ctx context.Context, userID uuid.UUID, step entities.OnboardingStepType) (*entities.OnboardingFlow, error)
	Update(ctx context.Context, flow *entities.OnboardingFlow) error
	GetCompletedSteps(ctx context.Context, userID uuid.UUID) ([]entities.OnboardingStepType, error)
}

type KYCSubmissionRepository interface {
	Create(ctx context.Context, submission *entities.KYCSubmission) error
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.KYCSubmission, error)
	GetByProviderRef(ctx context.Context, providerRef string) (*entities.KYCSubmission, error)
	Update(ctx context.Context, submission *entities.KYCSubmission) error
	GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.KYCSubmission, error)
}

// External service interfaces
type WalletService interface {
	CreateWalletsForUser(ctx context.Context, userID uuid.UUID, chains []entities.WalletChain) error
	GetWalletStatus(ctx context.Context, userID uuid.UUID) (*entities.WalletStatusResponse, error)
	RegisterExternalWallet(ctx context.Context, userID uuid.UUID, wallet *entities.ExternalWallet) error
}

// GridService handles Grid account creation, KYC, and virtual accounts
type GridService interface {
	InitiateAccountCreation(ctx context.Context, userID uuid.UUID, email string) (*GridAccountCreationStatus, error)
	CompleteAccountCreation(ctx context.Context, email, otp string) (*GridAccountResult, error)
	GetAccount(ctx context.Context, userID uuid.UUID) (*entities.GridAccount, error)
	InitiateKYC(ctx context.Context, userID uuid.UUID) (*GridKYCInitiationResult, error)
	GetKYCStatus(ctx context.Context, userID uuid.UUID) (*GridKYCStatusResult, error)
}

// GridAccountCreationStatus represents Grid account creation status
type GridAccountCreationStatus struct {
	Email     string
	OTPSent   bool
	ExpiresAt time.Time
}

// GridAccountResult represents the result of Grid account creation
type GridAccountResult struct {
	ID          uuid.UUID
	Address     string // Solana address
	Email       string
	Status      string
}

// GridKYCInitiationResult represents the result of initiating Grid KYC
type GridKYCInitiationResult struct {
	URL       string
	ExpiresAt time.Time
}

// GridKYCStatusResult represents Grid KYC status
type GridKYCStatusResult struct {
	Status    string
	UpdatedAt time.Time
	Reasons   []string
}

type EmailService interface {
	SendVerificationEmail(ctx context.Context, email, verificationToken string) error
	SendKYCStatusEmail(ctx context.Context, email string, status entities.KYCStatus, rejectionReasons []string) error
	SendWelcomeEmail(ctx context.Context, email string) error
}

type AuditService interface {
	LogOnboardingEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error
}

type AlpacaAdapter interface {
	CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error)
}

// AllocationService interface for enabling 70/30 allocation mode
type AllocationService interface {
	EnableMode(ctx context.Context, userID uuid.UUID, ratios entities.AllocationRatios) error
}

// NewService creates a new onboarding service
func NewService(
	userRepo UserRepository,
	onboardingFlowRepo OnboardingFlowRepository,
	kycSubmissionRepo KYCSubmissionRepository,
	walletService WalletService,
	gridService GridService,
	emailService EmailService,
	auditService AuditService,
	alpacaAdapter AlpacaAdapter,
	allocationService AllocationService,
	logger *zap.Logger,
	defaultWalletChains []entities.WalletChain,
) *Service {
	normalizedChains := normalizeDefaultWalletChains(defaultWalletChains, logger)

	return &Service{
		userRepo:            userRepo,
		onboardingFlowRepo:  onboardingFlowRepo,
		kycSubmissionRepo:   kycSubmissionRepo,
		walletService:       walletService,
		gridService:         gridService,
		emailService:        emailService,
		auditService:        auditService,
		alpacaAdapter:       alpacaAdapter,
		allocationService:   allocationService,
		logger:              logger,
		defaultWalletChains: normalizedChains,
	}
}

// SetAllocationService sets the allocation service (used to resolve circular dependency)
func (s *Service) SetAllocationService(allocationService AllocationService) {
	s.allocationService = allocationService
}

func normalizeDefaultWalletChains(chains []entities.WalletChain, logger *zap.Logger) []entities.WalletChain {
	if len(chains) == 0 {
		logger.Warn("No default wallet chains configured; falling back to SOL-DEVNET")
		return []entities.WalletChain{
			entities.WalletChainSOLDevnet,
		}
	}

	normalized := make([]entities.WalletChain, 0, len(chains))
	seen := make(map[entities.WalletChain]struct{})

	for _, chain := range chains {
		if !chain.IsValid() {
			logger.Warn("Ignoring invalid wallet chain configuration", zap.String("chain", string(chain)))
			continue
		}
		if _, ok := seen[chain]; ok {
			continue
		}
		seen[chain] = struct{}{}
		normalized = append(normalized, chain)
	}

	if len(normalized) == 0 {
		logger.Warn("Configured wallet chains invalid; falling back to SOL-DEVNET")
		return []entities.WalletChain{
			entities.WalletChainSOLDevnet,
		}
	}

	return normalized
}

// StartOnboarding initiates the onboarding process for a new user
func (s *Service) StartOnboarding(ctx context.Context, req *entities.OnboardingStartRequest) (*entities.OnboardingStartResponse, error) {
	s.logger.Info("Starting onboarding process", zap.String("email", req.Email))

	// Check if user already exists
	existingUser, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err == nil && existingUser != nil {
		s.logger.Info("User already exists, returning existing onboarding status",
			zap.String("email", req.Email),
			zap.String("userId", existingUser.ID.String()),
			zap.String("status", string(existingUser.OnboardingStatus)))

		return &entities.OnboardingStartResponse{
			UserID:           existingUser.ID,
			OnboardingStatus: existingUser.OnboardingStatus,
			NextStep:         s.determineNextStep(existingUser),
		}, nil
	}

	// Create new user
	user := &entities.UserProfile{
		ID:               uuid.New(),
		Email:            req.Email,
		Phone:            req.Phone,
		EmailVerified:    false,
		PhoneVerified:    false,
		OnboardingStatus: entities.OnboardingStatusStarted,
		KYCStatus:        string(entities.KYCStatusPending),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := user.Validate(); err != nil {
		return nil, fmt.Errorf("user validation failed: %w", err)
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		s.logger.Error("Failed to create user", zap.Error(err), zap.String("email", req.Email))
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	// Create initial onboarding flow steps
	if err := s.createInitialOnboardingSteps(ctx, user.ID); err != nil {
		s.logger.Error("Failed to create onboarding steps", zap.Error(err), zap.String("userId", user.ID.String()))
		return nil, fmt.Errorf("failed to create onboarding steps: %w", err)
	}

	// Initiate Grid account creation (sends OTP to user's email)
	_, err = s.gridService.InitiateAccountCreation(ctx, user.ID, req.Email)
	if err != nil {
		s.logger.Error("Failed to initiate Grid account", zap.Error(err), zap.String("email", req.Email))
		return nil, fmt.Errorf("failed to initiate account: %w", err)
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, user.ID, "onboarding_started", "user", nil, user); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding started successfully, OTP sent",
		zap.String("userId", user.ID.String()),
		zap.String("email", user.Email))

	return &entities.OnboardingStartResponse{
		UserID:           user.ID,
		OnboardingStatus: user.OnboardingStatus,
		NextStep:         entities.StepOTPVerification,
	}, nil
}

// GetOnboardingStatus returns the current onboarding status for a user
func (s *Service) GetOnboardingStatus(ctx context.Context, userID uuid.UUID) (*entities.OnboardingStatusResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	// Check if user is inactive
	if !user.IsActive {
		return nil, fmt.Errorf("user account is inactive")
	}

	// Get completed steps
	completedSteps, err := s.onboardingFlowRepo.GetCompletedSteps(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get completed steps", zap.Error(err), zap.String("userId", userID.String()))
		completedSteps = []entities.OnboardingStepType{}
	}
	completedSteps = s.normalizeCompletedSteps(user, completedSteps)

	// Get wallet status if KYC is approved
	var walletStatus *entities.WalletStatusSummary
	if user.OnboardingStatus == entities.OnboardingStatusKYCApproved ||
		user.OnboardingStatus == entities.OnboardingStatusWalletsPending ||
		user.OnboardingStatus == entities.OnboardingStatusCompleted {

		walletStatusResp, err := s.walletService.GetWalletStatus(ctx, userID)
		if err != nil {
			s.logger.Warn("Failed to get wallet status", zap.Error(err), zap.String("userId", userID.String()))
		} else {
			walletStatus = &entities.WalletStatusSummary{
				TotalWallets:    walletStatusResp.TotalWallets,
				CreatedWallets:  walletStatusResp.ReadyWallets,
				PendingWallets:  walletStatusResp.PendingWallets,
				FailedWallets:   walletStatusResp.FailedWallets,
				SupportedChains: []string{"ETH", "SOL", "APTOS"},
				WalletsByChain:  make(map[string]string),
			}

			for chain, status := range walletStatusResp.WalletsByChain {
				walletStatus.WalletsByChain[chain] = status.Status
			}
		}
	}

	// Determine current step and required actions
	currentStep := s.determineCurrentStep(user, completedSteps)
	requiredActions := s.determineRequiredActions(user, completedSteps)
	canProceed := s.canProceed(user, completedSteps)

	return &entities.OnboardingStatusResponse{
		UserID:           user.ID,
		OnboardingStatus: user.OnboardingStatus,
		KYCStatus:        user.KYCStatus,
		CurrentStep:      currentStep,
		CompletedSteps:   completedSteps,
		WalletStatus:     walletStatus,
		CanProceed:       canProceed,
		RequiredActions:  requiredActions,
	}, nil
}

// CompleteEmailVerification marks email verification as finished and advances onboarding without requiring KYC
// Deprecated: Use VerifyGridOTP instead for Grid-based onboarding
func (s *Service) CompleteEmailVerification(ctx context.Context, userID uuid.UUID) error {
	_, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	now := time.Now()
	if err := s.markStepCompleted(ctx, userID, entities.StepEmailVerification, map[string]any{
		"verified_at": now,
	}); err != nil {
		s.logger.Warn("Failed to mark email verification step as completed", zap.Error(err), zap.String("userId", userID.String()))
	}

	// Don't trigger wallet creation yet - wait for passcode creation
	// Just mark email verification as completed

	if err := s.auditService.LogOnboardingEvent(ctx, userID, "email_verified", "user", nil, map[string]any{
		"verified_at": now,
	}); err != nil {
		s.logger.Warn("Failed to log email verification event", zap.Error(err))
	}

	return nil
}

// VerifyGridOTP verifies the OTP sent by Grid and completes account creation
func (s *Service) VerifyGridOTP(ctx context.Context, userID uuid.UUID, otp string) error {
	s.logger.Info("Verifying Grid OTP", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Complete Grid account creation (verifies OTP and creates Solana wallet)
	gridAccount, err := s.gridService.CompleteAccountCreation(ctx, user.Email, otp)
	if err != nil {
		s.logger.Error("Grid OTP verification failed", zap.Error(err), zap.String("userId", userID.String()))
		return fmt.Errorf("OTP verification failed: %w", err)
	}

	// Mark email as verified (Grid verified it via OTP)
	now := time.Now()
	user.EmailVerified = true
	gridAccountID := gridAccount.ID.String()
	user.GridAccountID = &gridAccountID
	user.UpdatedAt = now

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// Mark OTP verification step as completed
	if err := s.markStepCompleted(ctx, userID, entities.StepOTPVerification, map[string]any{
		"grid_address": gridAccount.Address,
		"verified_at":  now,
	}); err != nil {
		s.logger.Warn("Failed to mark OTP verification step as completed", zap.Error(err))
	}

	// Also mark email verification as completed for backward compatibility
	if err := s.markStepCompleted(ctx, userID, entities.StepEmailVerification, map[string]any{
		"verified_at": now,
	}); err != nil {
		s.logger.Warn("Failed to mark email verification step as completed", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "otp_verified", "user", nil, map[string]any{
		"grid_address": gridAccount.Address,
		"verified_at":  now,
	}); err != nil {
		s.logger.Warn("Failed to log OTP verification event", zap.Error(err))
	}

	s.logger.Info("Grid OTP verified successfully",
		zap.String("userId", userID.String()),
		zap.String("gridAddress", gridAccount.Address))

	return nil
}

// CompleteOnboarding handles the completion of onboarding with personal info and account creation
func (s *Service) CompleteOnboarding(ctx context.Context, req *entities.OnboardingCompleteRequest) (*entities.OnboardingCompleteResponse, error) {
	s.logger.Info("Completing onboarding with account creation", zap.String("user_id", req.UserID.String()))

	// Get user
	user, err := s.userRepo.GetByID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !user.EmailVerified {
		return nil, fmt.Errorf("email must be verified before completing onboarding")
	}

	// Update user with personal information
	user.FirstName = &req.FirstName
	user.LastName = &req.LastName
	user.Phone = req.Phone
	user.DateOfBirth = req.DateOfBirth
	user.UpdatedAt = time.Now()

	// Create Alpaca account for brokerage
	alpacaReq := &entities.AlpacaCreateAccountRequest{
		Contact: entities.AlpacaContact{
			EmailAddress:  user.Email,
			PhoneNumber:   getStringValue(req.Phone),
			StreetAddress: []string{req.Address.Street},
			City:          req.Address.City,
			State:         req.Address.State,
			PostalCode:    req.Address.PostalCode,
			Country:       req.Country,
		},
		Identity: entities.AlpacaIdentity{
			GivenName:             req.FirstName,
			FamilyName:            req.LastName,
			DateOfBirth:           req.DateOfBirth.Format("2006-01-02"),
			CountryOfCitizenship:  req.Country,
			CountryOfBirth:        req.Country,
			CountryOfTaxResidence: req.Country,
			FundingSource:         []string{"employment_income"},
		},
		Disclosures: entities.AlpacaDisclosures{
			EmploymentStatus: "employed",
		},
		Agreements: []entities.AlpacaAgreement{
			{
				Agreement: "customer_agreement",
				SignedAt:  time.Now().Format(time.RFC3339),
				IPAddress: "127.0.0.1", // Should be passed from request context
			},
		},
	}

	alpacaResp, err := s.alpacaAdapter.CreateAccount(ctx, alpacaReq)
	if err != nil {
		s.logger.Error("Failed to create Alpaca account", zap.Error(err))
		return nil, fmt.Errorf("failed to create Alpaca account: %w", err)
	}

	// Update user with Alpaca account ID
	// Grid account ID is already stored in grid_accounts table and user.GridAccountID
	user.AlpacaAccountID = &alpacaResp.ID
	user.UpdatedAt = time.Now()

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user with account IDs: %w", err)
	}

	// Get Grid account address for response
	var gridAddress string
	if user.GridAccountID != nil {
		gridAccount, err := s.gridService.GetAccount(ctx, req.UserID)
		if err == nil && gridAccount != nil {
			gridAddress = gridAccount.Address
		}
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, req.UserID, "accounts_created", "user", nil, map[string]any{
		"alpaca_account_id": alpacaResp.ID,
		"grid_address":      gridAddress,
	}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding completed successfully",
		zap.String("user_id", req.UserID.String()),
		zap.String("alpaca_account_id", alpacaResp.ID),
		zap.String("grid_address", gridAddress))

	return &entities.OnboardingCompleteResponse{
		UserID:          req.UserID,
		GridAccountID:   gridAddress,
		AlpacaAccountID: alpacaResp.ID,
		Message:         "Accounts created successfully. Please create your passcode to continue.",
		NextSteps:       []string{"create_passcode"},
	}, nil
}

// CompletePasscodeCreation handles passcode creation completion and triggers wallet creation
func (s *Service) CompletePasscodeCreation(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Processing passcode creation completion", zap.String("userId", userID.String()))

	_, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Mark passcode creation step as completed
	if err := s.markStepCompleted(ctx, userID, entities.StepPasscodeCreation, map[string]any{
		"completed_at": time.Now(),
	}); err != nil {
		s.logger.Warn("Failed to mark passcode creation step as completed", zap.Error(err))
	}

	// Transition to wallet provisioning
	if err := s.userRepo.UpdateOnboardingStatus(ctx, userID, entities.OnboardingStatusWalletsPending); err != nil {
		return fmt.Errorf("failed to update onboarding status: %w", err)
	}

	// Check if user has Grid account (Solana wallet already created during OTP verification)
	gridAccount, err := s.gridService.GetAccount(ctx, userID)
	if err == nil && gridAccount != nil && gridAccount.Address != "" {
		// Register Grid wallet in managed_wallets for compatibility
		if err := s.walletService.RegisterExternalWallet(ctx, userID, &entities.ExternalWallet{
			Chain:    entities.WalletChainSolana,
			Address:  gridAccount.Address,
			Provider: entities.WalletProviderGrid,
		}); err != nil {
			s.logger.Warn("Failed to register Grid wallet", zap.Error(err))
		} else {
			s.logger.Info("Registered Grid Solana wallet",
				zap.String("userId", userID.String()),
				zap.String("address", gridAccount.Address))
		}
	}

	// Only create Circle wallets for non-Solana chains (Grid provides Solana wallet)
	nonSolanaChains := filterOutSolana(s.defaultWalletChains)
	if len(nonSolanaChains) > 0 {
		if err := s.walletService.CreateWalletsForUser(ctx, userID, nonSolanaChains); err != nil {
			s.logger.Warn("Failed to enqueue wallet provisioning for non-Solana chains",
				zap.Error(err),
				zap.String("userId", userID.String()))
		}
	}

	// Auto-enable 70/30 allocation mode (Rail MVP default - non-negotiable)
	// Per PRD: "This rule is system-defined, always on in MVP, not user-editable"
	if s.allocationService != nil {
		defaultRatios := entities.AllocationRatios{
			SpendingRatio: entities.DefaultSpendingRatio, // 0.70
			StashRatio:    entities.DefaultStashRatio,    // 0.30
		}
		if err := s.allocationService.EnableMode(ctx, userID, defaultRatios); err != nil {
			s.logger.Error("Failed to enable default 70/30 allocation mode",
				zap.Error(err),
				zap.String("userId", userID.String()))
			// Don't fail onboarding - allocation can be retried
		} else {
			s.logger.Info("Auto-enabled 70/30 allocation mode for user",
				zap.String("userId", userID.String()))
		}
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "passcode_created", "user", nil, map[string]any{
		"created_at": time.Now(),
	}); err != nil {
		s.logger.Warn("Failed to log passcode creation event", zap.Error(err))
	}

	s.logger.Info("Passcode creation completed and wallet provisioning initiated", zap.String("userId", userID.String()))
	return nil
}

// filterOutSolana removes Solana chains from the list (Grid provides Solana wallet)
func filterOutSolana(chains []entities.WalletChain) []entities.WalletChain {
	result := make([]entities.WalletChain, 0, len(chains))
	for _, chain := range chains {
		if chain != entities.WalletChainSolana && chain != entities.WalletChainSOLDevnet {
			result = append(result, chain)
		}
	}
	return result
}

// InitiateGridKYC initiates KYC through Grid and returns the KYC URL
func (s *Service) InitiateGridKYC(ctx context.Context, userID uuid.UUID) (*entities.KYCInitiationResponse, error) {
	s.logger.Info("Initiating Grid KYC", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if !user.CanStartKYC() {
		return nil, fmt.Errorf("user cannot start KYC process")
	}

	// Initiate KYC through Grid
	kycResp, err := s.gridService.InitiateKYC(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to initiate Grid KYC", zap.Error(err))
		return nil, fmt.Errorf("failed to initiate KYC: %w", err)
	}

	// Update user KYC status
	now := time.Now()
	user.KYCStatus = string(entities.KYCStatusProcessing)
	user.KYCSubmittedAt = &now
	user.UpdatedAt = now

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("failed to update user KYC status: %w", err)
	}

	// Mark KYC submission step as in progress
	if err := s.markStepCompleted(ctx, userID, entities.StepKYCSubmission, map[string]any{
		"initiated_at": now,
		"kyc_url":      kycResp.URL,
	}); err != nil {
		s.logger.Warn("Failed to mark KYC submission step", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "kyc_initiated", "user", nil, map[string]any{
		"initiated_at": now,
	}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Grid KYC initiated successfully", zap.String("userId", userID.String()))

	return &entities.KYCInitiationResponse{
		KYCURL:    kycResp.URL,
		ExpiresAt: kycResp.ExpiresAt,
	}, nil
}

// SubmitKYC handles KYC document submission
// Deprecated: Use InitiateGridKYC instead for Grid-based KYC
func (s *Service) SubmitKYC(ctx context.Context, userID uuid.UUID, req *entities.KYCSubmitRequest) error {
	s.logger.Info("SubmitKYC called - redirecting to InitiateGridKYC", zap.String("userId", userID.String()))
	
	// For backward compatibility, initiate Grid KYC
	_, err := s.InitiateGridKYC(ctx, userID)
	return err
}

// ProcessKYCCallback processes KYC provider callbacks (Grid KYC webhooks)
func (s *Service) ProcessKYCCallback(ctx context.Context, providerRef string, status entities.KYCStatus, rejectionReasons []string) error {
	s.logger.Info("Processing KYC callback",
		zap.String("providerRef", providerRef),
		zap.String("status", string(status)))

	// For Grid, providerRef is the Grid address
	// Try to find user by Grid address via the grid_accounts table
	// First, try the old way via KYC submission
	submission, err := s.kycSubmissionRepo.GetByProviderRef(ctx, providerRef)
	var user *entities.UserProfile
	
	if err != nil {
		// Try to find by Grid address - this is the new Grid flow
		s.logger.Info("KYC submission not found, trying Grid address lookup", zap.String("providerRef", providerRef))
		// We need to look up the user by their Grid account address
		// For now, log and return - the Grid service webhook handler should handle this
		return fmt.Errorf("KYC submission not found for provider ref: %s", providerRef)
	}

	// Get user from submission
	user, err = s.userRepo.GetByID(ctx, submission.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Update submission
	submission.MarkReviewed(status, rejectionReasons)
	if err := s.kycSubmissionRepo.Update(ctx, submission); err != nil {
		return fmt.Errorf("failed to update KYC submission: %w", err)
	}

	// Update user based on KYC result
	var kycApprovedAt *time.Time
	var kycRejectionReason *string

	switch status {
	case entities.KYCStatusApproved:
		now := time.Now()
		kycApprovedAt = &now

		// Mark KYC review step as completed
		if err := s.markStepCompleted(ctx, user.ID, entities.StepKYCReview, map[string]any{
			"status":      string(status),
			"approved_at": now,
		}); err != nil {
			s.logger.Warn("Failed to mark KYC review step as completed", zap.Error(err))
		}

	case entities.KYCStatusRejected:
		if len(rejectionReasons) > 0 {
			reason := fmt.Sprintf("KYC rejected: %v", rejectionReasons)
			kycRejectionReason = &reason
		}

		// Mark KYC review step as failed
		if err := s.markStepFailed(ctx, user.ID, entities.StepKYCReview, fmt.Sprintf("KYC rejected: %v", rejectionReasons)); err != nil {
			s.logger.Warn("Failed to mark KYC review step as failed", zap.Error(err))
		}

	default:
		// For processing status, no onboarding status change
		s.logger.Info("KYC still processing", zap.String("status", string(status)))
		return nil
	}

	// Update user status
	if err := s.userRepo.UpdateKYCStatus(ctx, user.ID, string(status), kycApprovedAt, kycRejectionReason); err != nil {
		return fmt.Errorf("failed to update user KYC status: %w", err)
	}

	// Send status email
	if err := s.emailService.SendKYCStatusEmail(ctx, user.Email, status, rejectionReasons); err != nil {
		s.logger.Warn("Failed to send KYC status email", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, user.ID, "kyc_reviewed", "kyc_submission",
		map[string]any{"status": "processing"},
		map[string]any{"status": string(status), "rejection_reasons": rejectionReasons}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("KYC callback processed successfully",
		zap.String("userId", user.ID.String()),
		zap.String("status", string(status)))

	return nil
}

// ProcessGridKYCWebhook processes Grid KYC webhook updates
func (s *Service) ProcessGridKYCWebhook(ctx context.Context, gridAddress string, status string, reasons []string) error {
	s.logger.Info("Processing Grid KYC webhook",
		zap.String("gridAddress", gridAddress),
		zap.String("status", status))

	// Get Grid account by address to find the user
	gridAccount, err := s.gridService.GetAccount(ctx, uuid.Nil) // We need to look up by address
	if err != nil {
		// The Grid service's ProcessKYCWebhook should handle this directly
		s.logger.Warn("Could not find Grid account, webhook should be handled by Grid service", zap.Error(err))
		return nil
	}

	// Map Grid KYC status to our KYC status
	var kycStatus entities.KYCStatus
	switch status {
	case "approved":
		kycStatus = entities.KYCStatusApproved
	case "rejected":
		kycStatus = entities.KYCStatusRejected
	case "pending":
		kycStatus = entities.KYCStatusProcessing
	default:
		kycStatus = entities.KYCStatusProcessing
	}

	// Get user
	user, err := s.userRepo.GetByID(ctx, gridAccount.UserID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	// Update user KYC status
	var kycApprovedAt *time.Time
	var kycRejectionReason *string

	if kycStatus == entities.KYCStatusApproved {
		now := time.Now()
		kycApprovedAt = &now

		if err := s.markStepCompleted(ctx, user.ID, entities.StepKYCReview, map[string]any{
			"status":      string(kycStatus),
			"approved_at": now,
		}); err != nil {
			s.logger.Warn("Failed to mark KYC review step as completed", zap.Error(err))
		}
	} else if kycStatus == entities.KYCStatusRejected {
		if len(reasons) > 0 {
			reason := fmt.Sprintf("KYC rejected: %v", reasons)
			kycRejectionReason = &reason
		}

		if err := s.markStepFailed(ctx, user.ID, entities.StepKYCReview, fmt.Sprintf("KYC rejected: %v", reasons)); err != nil {
			s.logger.Warn("Failed to mark KYC review step as failed", zap.Error(err))
		}
	}

	if err := s.userRepo.UpdateKYCStatus(ctx, user.ID, string(kycStatus), kycApprovedAt, kycRejectionReason); err != nil {
		return fmt.Errorf("failed to update user KYC status: %w", err)
	}

	// Send status email
	if err := s.emailService.SendKYCStatusEmail(ctx, user.Email, kycStatus, reasons); err != nil {
		s.logger.Warn("Failed to send KYC status email", zap.Error(err))
	}

	s.logger.Info("Grid KYC webhook processed successfully",
		zap.String("userId", user.ID.String()),
		zap.String("status", string(kycStatus)))

	return nil
}

// GetKYCStatus returns an aggregate view of the user's KYC standing
func (s *Service) GetKYCStatus(ctx context.Context, userID uuid.UUID) (*entities.KYCStatusResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	status := user.KYCStatus
	if status == "" {
		status = string(entities.KYCStatusPending)
	}

	requiredFor := append([]string(nil), kycRequiredFeatures...)
	kycStatus := entities.KYCStatus(status)
	nextSteps := []string{}

	switch kycStatus {
	case entities.KYCStatusPending:
		if user.KYCSubmittedAt == nil {
			nextSteps = append(nextSteps, "Submit your KYC documents to unlock advanced features")
		} else {
			nextSteps = append(nextSteps, "Your documents are queued for review")
		}
	case entities.KYCStatusProcessing:
		nextSteps = append(nextSteps, "Verification in progress with our compliance partner")
	case entities.KYCStatusRejected:
		nextSteps = append(nextSteps, "Review the rejection reasons and resubmit corrected documents")
	case entities.KYCStatusExpired:
		nextSteps = append(nextSteps, "Resubmit your KYC documents to refresh your verification")
	}

	response := &entities.KYCStatusResponse{
		UserID:            user.ID,
		Status:            status,
		Verified:          kycStatus == entities.KYCStatusApproved,
		HasSubmitted:      user.KYCSubmittedAt != nil,
		RequiresKYC:       len(requiredFor) > 0,
		RequiredFor:       requiredFor,
		LastSubmittedAt:   user.KYCSubmittedAt,
		ApprovedAt:        user.KYCApprovedAt,
		RejectionReason:   user.KYCRejectionReason,
		ProviderReference: user.KYCProviderRef,
		NextSteps:         nextSteps,
	}

	return response, nil
}

// GetOnboardingProgress returns a detailed progress view of the user's onboarding
func (s *Service) GetOnboardingProgress(ctx context.Context, userID uuid.UUID) (*entities.OnboardingProgressResponse, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	completedSteps, err := s.onboardingFlowRepo.GetCompletedSteps(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get completed steps", zap.Error(err))
		completedSteps = []entities.OnboardingStepType{}
	}
	completedSteps = s.normalizeCompletedSteps(user, completedSteps)

	completedMap := make(map[entities.OnboardingStepType]bool)
	for _, step := range completedSteps {
		completedMap[step] = true
	}

	// Define checklist items
	checklist := []entities.OnboardingCheckItem{
		{Step: entities.StepRegistration, Title: "Create Account", Description: "Sign up with email or phone", Required: true, Order: 1},
		{Step: entities.StepEmailVerification, Title: "Verify Email", Description: "Confirm your email address", Required: true, Order: 2},
		{Step: entities.StepPasscodeCreation, Title: "Set Passcode", Description: "Create a secure passcode", Required: true, Order: 3},
		{Step: entities.StepWalletCreation, Title: "Setup Wallet", Description: "Create your crypto wallet", Required: true, Order: 4},
		{Step: entities.StepKYCSubmission, Title: "Verify Identity", Description: "Complete KYC for full access", Required: false, Order: 5},
	}

	// Update status for each item
	completedCount := 0
	for i := range checklist {
		if completedMap[checklist[i].Step] {
			checklist[i].Status = entities.StepStatusCompleted
			completedCount++
		} else if i > 0 && checklist[i-1].Status != entities.StepStatusCompleted {
			checklist[i].Status = entities.StepStatusPending
		} else {
			checklist[i].Status = entities.StepStatusPending
		}
	}

	// Calculate progress percentage (only required steps)
	requiredCount := 0
	requiredCompleted := 0
	for _, item := range checklist {
		if item.Required {
			requiredCount++
			if item.Status == entities.StepStatusCompleted {
				requiredCompleted++
			}
		}
	}

	percentComplete := 0
	if requiredCount > 0 {
		percentComplete = (requiredCompleted * 100) / requiredCount
	}

	// Determine current step
	currentStep := s.determineCurrentStep(user, completedSteps)

	// Estimate remaining time
	estimatedTime := "Complete"
	if percentComplete < 100 {
		remainingSteps := requiredCount - requiredCompleted
		estimatedTime = fmt.Sprintf("%d min", remainingSteps*2)
	}

	// Determine capabilities
	kycApproved := entities.KYCStatus(user.KYCStatus) == entities.KYCStatusApproved
	canInvest := user.OnboardingStatus == entities.OnboardingStatusCompleted
	canWithdraw := canInvest && kycApproved

	return &entities.OnboardingProgressResponse{
		UserID:          user.ID,
		PercentComplete: percentComplete,
		Checklist:       checklist,
		CurrentStep:     currentStep,
		EstimatedTime:   estimatedTime,
		CanInvest:       canInvest,
		CanWithdraw:     canWithdraw,
	}, nil
}

// ProcessWalletCreationComplete handles wallet creation completion
func (s *Service) ProcessWalletCreationComplete(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Processing wallet creation completion", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if user.OnboardingStatus != entities.OnboardingStatusWalletsPending {
		s.logger.Warn("User is not in wallets pending status",
			zap.String("userId", userID.String()),
			zap.String("status", string(user.OnboardingStatus)))
		return nil
	}

	// Mark wallet creation step as completed
	if err := s.markStepCompleted(ctx, userID, entities.StepWalletCreation, map[string]any{
		"completed_at": time.Now(),
	}); err != nil {
		s.logger.Warn("Failed to mark wallet creation step as completed", zap.Error(err))
	}

	// Mark onboarding as completed
	if err := s.markStepCompleted(ctx, userID, entities.StepOnboardingComplete, map[string]any{
		"completed_at": time.Now(),
	}); err != nil {
		s.logger.Warn("Failed to mark onboarding complete step as completed", zap.Error(err))
	}

	// Update user status
	if err := s.userRepo.UpdateOnboardingStatus(ctx, userID, entities.OnboardingStatusCompleted); err != nil {
		return fmt.Errorf("failed to update onboarding status: %w", err)
	}

	// Send welcome email
	if err := s.emailService.SendWelcomeEmail(ctx, user.Email); err != nil {
		s.logger.Warn("Failed to send welcome email", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "onboarding_completed", "user",
		map[string]any{"status": string(entities.OnboardingStatusWalletsPending)},
		map[string]any{"status": string(entities.OnboardingStatusCompleted)}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding completed successfully", zap.String("userId", userID.String()))

	return nil
}

// Helper methods

func (s *Service) createInitialOnboardingSteps(ctx context.Context, userID uuid.UUID) error {
	steps := []entities.OnboardingStepType{
		entities.StepRegistration,
		entities.StepOTPVerification,
		entities.StepEmailVerification, // Keep for backward compatibility
		entities.StepPasscodeCreation,
		entities.StepKYCSubmission,
		entities.StepKYCReview,
		entities.StepWalletCreation,
		entities.StepOnboardingComplete,
	}

	for _, step := range steps {
		flow := &entities.OnboardingFlow{
			ID:        uuid.New(),
			UserID:    userID,
			Step:      step,
			Status:    entities.StepStatusPending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Mark registration as completed since user was just created
		if step == entities.StepRegistration {
			flow.MarkCompleted(map[string]any{
				"registration_completed": true,
			})
		}

		if err := s.onboardingFlowRepo.Create(ctx, flow); err != nil {
			return fmt.Errorf("failed to create step %s: %w", step, err)
		}
	}

	return nil
}

func (s *Service) normalizeCompletedSteps(user *entities.UserProfile, steps []entities.OnboardingStepType) []entities.OnboardingStepType {
	if steps == nil {
		steps = make([]entities.OnboardingStepType, 0)
	}

	completed := make(map[entities.OnboardingStepType]bool, len(steps))
	for _, step := range steps {
		completed[step] = true
	}

	completed[entities.StepRegistration] = true

	if user.EmailVerified {
		completed[entities.StepOTPVerification] = true
		completed[entities.StepEmailVerification] = true // Backward compatibility
	}

	// Check if passcode is created by checking if user has a passcode hash
	// This would need to be implemented in the user repository
	// For now, we'll assume it's completed if onboarding status is wallets_pending or completed
	if user.OnboardingStatus == entities.OnboardingStatusWalletsPending ||
		user.OnboardingStatus == entities.OnboardingStatusCompleted {
		completed[entities.StepPasscodeCreation] = true
	}

	kycStatus := entities.KYCStatus(user.KYCStatus)
	if user.KYCSubmittedAt != nil ||
		kycStatus == entities.KYCStatusProcessing ||
		kycStatus == entities.KYCStatusRejected ||
		kycStatus == entities.KYCStatusApproved {
		completed[entities.StepKYCSubmission] = true
	}
	if kycStatus == entities.KYCStatusApproved {
		completed[entities.StepKYCReview] = true
	}

	if user.OnboardingStatus == entities.OnboardingStatusCompleted {
		completed[entities.StepWalletCreation] = true
		completed[entities.StepOnboardingComplete] = true
	}

	canonical := []entities.OnboardingStepType{
		entities.StepRegistration,
		entities.StepOTPVerification,
		entities.StepEmailVerification,
		entities.StepPasscodeCreation,
		entities.StepKYCSubmission,
		entities.StepKYCReview,
		entities.StepWalletCreation,
		entities.StepOnboardingComplete,
	}

	normalized := make([]entities.OnboardingStepType, 0, len(completed))
	for _, step := range canonical {
		if completed[step] {
			normalized = append(normalized, step)
			delete(completed, step)
		}
	}

	if len(completed) > 0 {
		extraSteps := make([]string, 0, len(completed))
		for step := range completed {
			extraSteps = append(extraSteps, string(step))
		}
		sort.Strings(extraSteps)
		for _, step := range extraSteps {
			normalized = append(normalized, entities.OnboardingStepType(step))
		}
	}

	return normalized
}

func (s *Service) markStepCompleted(ctx context.Context, userID uuid.UUID, step entities.OnboardingStepType, data map[string]any) error {
	flow, err := s.onboardingFlowRepo.GetByUserAndStep(ctx, userID, step)
	if err != nil {
		// If onboarding flow doesn't exist, create initial steps and try again
		s.logger.Warn("Onboarding flow not found, creating initial steps",
			zap.Error(err),
			zap.String("userId", userID.String()),
			zap.String("step", string(step)))
		
		if createErr := s.createInitialOnboardingSteps(ctx, userID); createErr != nil {
			return fmt.Errorf("failed to create initial onboarding steps: %w", createErr)
		}
		
		// Retry getting the flow
		flow, err = s.onboardingFlowRepo.GetByUserAndStep(ctx, userID, step)
		if err != nil {
			return fmt.Errorf("failed to get onboarding flow step after creation: %w", err)
		}
	}

	flow.MarkCompleted(data)
	return s.onboardingFlowRepo.Update(ctx, flow)
}

func (s *Service) markStepFailed(ctx context.Context, userID uuid.UUID, step entities.OnboardingStepType, errorMsg string) error {
	flow, err := s.onboardingFlowRepo.GetByUserAndStep(ctx, userID, step)
	if err != nil {
		return fmt.Errorf("failed to get onboarding flow step: %w", err)
	}

	flow.MarkFailed(errorMsg)
	return s.onboardingFlowRepo.Update(ctx, flow)
}

func (s *Service) triggerWalletCreation(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Triggering wallet creation for user", zap.String("userId", userID.String()))

	// Update user status to wallets pending
	if err := s.userRepo.UpdateOnboardingStatus(ctx, userID, entities.OnboardingStatusWalletsPending); err != nil {
		return fmt.Errorf("failed to update status to wallets pending: %w", err)
	}

	// This now enqueues a job instead of processing immediately
	// The worker scheduler will pick it up and process with retries and audit logging
	if err := s.walletService.CreateWalletsForUser(ctx, userID, s.defaultWalletChains); err != nil {
		s.logger.Error("Failed to enqueue wallet provisioning job",
			zap.Error(err),
			zap.String("userId", userID.String()))
		return fmt.Errorf("failed to enqueue wallet provisioning: %w", err)
	}

	s.logger.Info("Wallet provisioning job enqueued successfully",
		zap.String("userId", userID.String()),
		zap.Int("chains_count", len(s.defaultWalletChains)))

	return nil
}

func (s *Service) determineNextStep(user *entities.UserProfile) entities.OnboardingStepType {
	if !user.EmailVerified {
		return entities.StepOTPVerification
	}

	if user.OnboardingStatus == entities.OnboardingStatusCompleted {
		return entities.StepOnboardingComplete
	}

	if user.OnboardingStatus == entities.OnboardingStatusWalletsPending {
		return entities.StepWalletCreation
	}

	kycStatus := entities.KYCStatus(user.KYCStatus)
	switch kycStatus {
	case entities.KYCStatusRejected:
		return entities.StepKYCSubmission
	case entities.KYCStatusProcessing:
		return entities.StepKYCReview
	}

	return entities.StepWalletCreation
}

func (s *Service) determineCurrentStep(user *entities.UserProfile, completedSteps []entities.OnboardingStepType) *entities.OnboardingStepType {
	// Find the first uncompleted step
	allSteps := []entities.OnboardingStepType{
		entities.StepRegistration,
		entities.StepEmailVerification,
	}

	// Only include KYC steps if the user has started or completed KYC
	if user.KYCSubmittedAt != nil ||
		entities.KYCStatus(user.KYCStatus) == entities.KYCStatusProcessing ||
		entities.KYCStatus(user.KYCStatus) == entities.KYCStatusRejected ||
		entities.KYCStatus(user.KYCStatus) == entities.KYCStatusApproved {
		allSteps = append(allSteps, entities.StepKYCSubmission, entities.StepKYCReview)
	}

	allSteps = append(allSteps,
		entities.StepWalletCreation,
		entities.StepOnboardingComplete,
	)

	completedMap := make(map[entities.OnboardingStepType]bool)
	for _, step := range completedSteps {
		completedMap[step] = true
	}

	for _, step := range allSteps {
		if !completedMap[step] {
			return &step
		}
	}

	// All steps completed
	step := entities.StepOnboardingComplete
	return &step
}

func (s *Service) determineRequiredActions(user *entities.UserProfile, completedSteps []entities.OnboardingStepType) []string {
	var actions []string

	completedMap := make(map[entities.OnboardingStepType]bool)
	for _, step := range completedSteps {
		completedMap[step] = true
	}

	if !user.EmailVerified && !completedMap[entities.StepEmailVerification] {
		actions = append(actions, "Verify your email address")
	}

	if user.OnboardingStatus == entities.OnboardingStatusKYCRejected {
		actions = append(actions, "Resubmit KYC documents to unlock advanced features")
	}

	return actions
}

func (s *Service) canProceed(user *entities.UserProfile, completedSteps []entities.OnboardingStepType) bool {
	if !user.EmailVerified {
		return false
	}

	switch user.OnboardingStatus {
	case entities.OnboardingStatusWalletsPending:
		return false // Wallet provisioning still in progress
	case entities.OnboardingStatusCompleted:
		return true
	default:
		return true
	}
}

// Helper function to safely convert *string to string
func getStringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
