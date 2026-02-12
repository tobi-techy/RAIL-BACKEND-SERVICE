package onboarding

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/crypto"
	"go.uber.org/zap"
)

var kycRequiredFeatures = []string{"virtual_account", "cards", "fiat_withdrawal"}

// Service handles onboarding operations - user creation, KYC flow, wallet provisioning
type Service struct {
	userRepo            UserRepository
	onboardingFlowRepo  OnboardingFlowRepository
	kycSubmissionRepo   KYCSubmissionRepository
	walletService       WalletService
	kycProvider         KYCProvider
	emailService        EmailService
	auditService        AuditService
	bridgeAdapter       BridgeAdapter
	alpacaAdapter       AlpacaAdapter
	allocationService   AllocationService
	logger              *zap.Logger
	defaultWalletChains []entities.WalletChain
	kycProviderName     string
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
	UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error
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
}

type KYCProvider interface {
	SubmitKYC(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocumentUpload, personalInfo *entities.KYCPersonalInfo) (string, error)
	GetKYCStatus(ctx context.Context, providerRef string) (*entities.KYCSubmission, error)
	GenerateKYCURL(ctx context.Context, userID uuid.UUID) (string, error)
}

type EmailService interface {
	SendVerificationEmail(ctx context.Context, email, verificationToken string) error
	SendKYCStatusEmail(ctx context.Context, email string, status entities.KYCStatus, rejectionReasons []string) error
	SendWelcomeEmail(ctx context.Context, email string) error
}

type AuditService interface {
	LogOnboardingEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error
}

type BridgeAdapter interface {
	CreateCustomer(ctx context.Context, req *entities.CreateAccountRequest) (*entities.CreateAccountResponse, error)
	GetCustomerByEmail(ctx context.Context, email string) (*entities.CreateAccountResponse, error)
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
	kycProvider KYCProvider,
	emailService EmailService,
	auditService AuditService,
	bridgeAdapter BridgeAdapter,
	alpacaAdapter AlpacaAdapter,
	allocationService AllocationService,
	logger *zap.Logger,
	defaultWalletChains []entities.WalletChain,
	kycProviderName string,
) *Service {
	normalizedChains := normalizeDefaultWalletChains(defaultWalletChains, logger)

	if kycProviderName == "" {
		kycProviderName = "sumsub" // Default KYC provider
	}

	return &Service{
		userRepo:            userRepo,
		onboardingFlowRepo:  onboardingFlowRepo,
		kycSubmissionRepo:   kycSubmissionRepo,
		walletService:       walletService,
		kycProvider:         kycProvider,
		emailService:        emailService,
		auditService:        auditService,
		bridgeAdapter:       bridgeAdapter,
		alpacaAdapter:       alpacaAdapter,
		allocationService:   allocationService,
		logger:              logger,
		defaultWalletChains: normalizedChains,
		kycProviderName:     kycProviderName,
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

	// Send verification email
	if err := s.emailService.SendVerificationEmail(ctx, user.Email, user.ID.String()); err != nil {
		s.logger.Warn("Failed to send verification email", zap.Error(err), zap.String("email", user.Email))
		// Don't fail onboarding start if email fails
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, user.ID, "onboarding_started", "user", nil, user); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding started successfully",
		zap.String("userId", user.ID.String()),
		zap.String("email", user.Email))

	return &entities.OnboardingStartResponse{
		UserID:           user.ID,
		OnboardingStatus: user.OnboardingStatus,
		NextStep:         entities.StepEmailVerification,
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

// CompleteOnboarding handles the completion of onboarding with personal info, password, and Bridge customer creation
// Alpaca account creation is now handled separately via the KYC flow
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

	// Hash and set password
	passwordHash, err := crypto.HashPassword(req.Password)
	if err != nil {
		s.logger.Error("Failed to hash password", zap.Error(err))
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.userRepo.UpdatePassword(ctx, req.UserID, passwordHash); err != nil {
		return nil, fmt.Errorf("failed to set password: %w", err)
	}

	// Update user with personal information
	user.FirstName = &req.FirstName
	user.LastName = &req.LastName
	user.Phone = req.Phone
	user.DateOfBirth = req.DateOfBirth
	user.UpdatedAt = time.Now()

	// Create Bridge customer with minimal data (no KYC yet)
	if user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		bridgeReq := &entities.CreateAccountRequest{
			Type:              "individual",
			FirstName:         req.FirstName,
			LastName:          req.LastName,
			Email:             user.Email,
			Country:           req.Country,
			Address:           &req.Address,
			DateOfBirth:       req.DateOfBirth,
			Phone:             req.Phone,
			SignedAgreementID: req.SignedAgreementID,
			// NOTE: No SSN/tax_id here - that's collected in KYC flow
		}

		bridgeResp, err := s.bridgeAdapter.CreateCustomer(ctx, bridgeReq)
		if err != nil {
			// Check if customer already exists in Bridge (email uniqueness)
			if strings.Contains(err.Error(), "already exists") {
				existingCustomer, lookupErr := s.bridgeAdapter.GetCustomerByEmail(ctx, user.Email)
				if lookupErr != nil || existingCustomer == nil {
					s.logger.Error("Failed to create Bridge customer and lookup failed", zap.Error(err), zap.Error(lookupErr))
					return nil, fmt.Errorf("failed to create Bridge customer: %w", err)
				}
				s.logger.Info("Found existing Bridge customer by email", zap.String("bridge_customer_id", existingCustomer.AccountID))
				user.BridgeCustomerID = &existingCustomer.AccountID
			} else {
				s.logger.Error("Failed to create Bridge customer", zap.Error(err))
				return nil, fmt.Errorf("failed to create Bridge customer: %w", err)
			}
		} else {
			user.BridgeCustomerID = &bridgeResp.AccountID
		}

		user.UpdatedAt = time.Now()
		if err := s.userRepo.Update(ctx, user); err != nil {
			return nil, fmt.Errorf("failed to persist Bridge customer information: %w", err)
		}
	}

	// NOTE: Alpaca account creation removed - now handled in KYC flow via POST /kyc/submit

	// Mark passcode creation step as completed and trigger wallet creation
	stepData := map[string]any{
		"completed_at": time.Now(),
	}
	if req.EmploymentStatus != nil {
		if trimmed := strings.TrimSpace(*req.EmploymentStatus); trimmed != "" {
			stepData["employment_status"] = trimmed
		}
	}
	if req.YearlyIncome != nil {
		stepData["yearly_income"] = *req.YearlyIncome
	}
	if req.UserExperience != nil {
		if trimmed := strings.TrimSpace(*req.UserExperience); trimmed != "" {
			stepData["user_experience"] = trimmed
		}
	}
	if len(req.InvestmentGoals) > 0 {
		stepData["investment_goals"] = req.InvestmentGoals
	}

	if err := s.markStepCompleted(ctx, req.UserID, entities.StepPasscodeCreation, stepData); err != nil {
		s.logger.Warn("Failed to mark passcode creation step as completed", zap.Error(err))
	}

	// Transition to wallet provisioning
	if err := s.userRepo.UpdateOnboardingStatus(ctx, req.UserID, entities.OnboardingStatusWalletsPending); err != nil {
		return nil, fmt.Errorf("failed to update onboarding status: %w", err)
	}

	// Trigger wallet provisioning
	if err := s.walletService.CreateWalletsForUser(ctx, req.UserID, s.defaultWalletChains); err != nil {
		s.logger.Error("Failed to trigger wallet provisioning", zap.Error(err), zap.String("userId", req.UserID.String()))
		return nil, fmt.Errorf("failed to create wallets: %w", err)
	}

	// Auto-enable 70/30 allocation mode (Rail MVP default - non-negotiable)
	if s.allocationService != nil {
		defaultRatios := entities.AllocationRatios{
			SpendingRatio: entities.DefaultSpendingRatio,
			StashRatio:    entities.DefaultStashRatio,
		}
		if err := s.allocationService.EnableMode(ctx, req.UserID, defaultRatios); err != nil {
			s.logger.Error("Failed to enable default 70/30 allocation mode", zap.Error(err), zap.String("userId", req.UserID.String()))
		}
	}

	// Get final IDs from user (may have been set in this request or previously)

	bridgeCustomerID := ""
	if user.BridgeCustomerID != nil {
		bridgeCustomerID = *user.BridgeCustomerID
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, req.UserID, "signup_completed", "user", nil, map[string]any{
		"bridge_customer_id": bridgeCustomerID,
		"password_set":       true,
		"wallets_queued":     true,
	}); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding completed successfully",
		zap.String("user_id", req.UserID.String()),
		zap.String("bridge_customer_id", bridgeCustomerID),
		zap.Bool("wallets_queued", true))

	return &entities.OnboardingCompleteResponse{
		UserID:           req.UserID,
		BridgeCustomerID: bridgeCustomerID,
		Message:          "Signup completed successfully. Complete KYC to unlock all features.",
		NextSteps: []string{
			"Complete KYC verification to unlock fiat deposits, cards, and investing",
			"You can deposit crypto immediately",
		},
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

	// Kick off wallet provisioning after passcode creation
	if err := s.walletService.CreateWalletsForUser(ctx, userID, s.defaultWalletChains); err != nil {
		s.logger.Warn("Failed to enqueue wallet provisioning after passcode creation",
			zap.Error(err),
			zap.String("userId", userID.String()))
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

// SubmitKYC handles KYC document submission
func (s *Service) SubmitKYC(ctx context.Context, userID uuid.UUID, req *entities.KYCSubmitRequest) error {
	s.logger.Info("Submitting KYC documents", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if !user.CanStartKYC() {
		return fmt.Errorf("user cannot start KYC process")
	}

	// Submit to KYC provider
	providerRef, err := s.kycProvider.SubmitKYC(ctx, userID, req.Documents, req.PersonalInfo)
	if err != nil {
		return fmt.Errorf("failed to submit KYC to provider: %w", err)
	}

	// Create KYC submission record
	submission := &entities.KYCSubmission{
		ID:             uuid.New(),
		UserID:         userID,
		Provider:       s.kycProviderName,
		ProviderRef:    providerRef,
		SubmissionType: req.DocumentType,
		Status:         entities.KYCStatusProcessing,
		VerificationData: map[string]any{
			"document_type": req.DocumentType,
			"documents":     req.Documents,
			"personal_info": req.PersonalInfo,
			"metadata":      req.Metadata,
		},
		SubmittedAt: time.Now(),
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.kycSubmissionRepo.Create(ctx, submission); err != nil {
		return fmt.Errorf("failed to create KYC submission record: %w", err)
	}

	// Update user KYC tracking fields
	now := time.Now()
	user.KYCStatus = string(entities.KYCStatusProcessing)
	user.KYCProviderRef = &providerRef
	user.KYCSubmittedAt = &now
	user.UpdatedAt = now

	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("failed to update user KYC status: %w", err)
	}

	// Update onboarding flow
	if err := s.markStepCompleted(ctx, userID, entities.StepKYCSubmission, map[string]any{
		"provider_ref": providerRef,
		"submitted_at": now,
	}); err != nil {
		s.logger.Warn("Failed to mark KYC submission step as completed", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "kyc_submitted", "kyc_submission", nil, submission); err != nil {
		s.logger.Warn("Failed to log audit event", zap.Error(err))
	}

	s.logger.Info("KYC submitted successfully",
		zap.String("userId", userID.String()),
		zap.String("providerRef", providerRef))

	return nil
}

// ProcessKYCCallback processes KYC provider callbacks
func (s *Service) ProcessKYCCallback(ctx context.Context, providerRef string, status entities.KYCStatus, rejectionReasons []string) error {
	s.logger.Info("Processing KYC callback",
		zap.String("providerRef", providerRef),
		zap.String("status", string(status)))

	// Get KYC submission
	submission, err := s.kycSubmissionRepo.GetByProviderRef(ctx, providerRef)
	if err != nil {
		return fmt.Errorf("failed to get KYC submission: %w", err)
	}

	// Get user
	user, err := s.userRepo.GetByID(ctx, submission.UserID)
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
		entities.StepEmailVerification,
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
		completed[entities.StepEmailVerification] = true
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
		return entities.StepEmailVerification
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


// countryAlpha2ToAlpha3 converts ISO 3166-1 alpha-2 to alpha-3 country codes
func countryAlpha2ToAlpha3(alpha2 string) string {
	codes := map[string]string{
		"AF": "AFG", "AL": "ALB", "DZ": "DZA", "AS": "ASM", "AD": "AND",
		"AO": "AGO", "AI": "AIA", "AQ": "ATA", "AG": "ATG", "AR": "ARG",
		"AM": "ARM", "AW": "ABW", "AU": "AUS", "AT": "AUT", "AZ": "AZE",
		"BS": "BHS", "BH": "BHR", "BD": "BGD", "BB": "BRB", "BY": "BLR",
		"BE": "BEL", "BZ": "BLZ", "BJ": "BEN", "BM": "BMU", "BT": "BTN",
		"BO": "BOL", "BA": "BIH", "BW": "BWA", "BR": "BRA", "BN": "BRN",
		"BG": "BGR", "BF": "BFA", "BI": "BDI", "KH": "KHM", "CM": "CMR",
		"CA": "CAN", "CV": "CPV", "KY": "CYM", "CF": "CAF", "TD": "TCD",
		"CL": "CHL", "CN": "CHN", "CO": "COL", "KM": "COM", "CG": "COG",
		"CD": "COD", "CR": "CRI", "CI": "CIV", "HR": "HRV", "CU": "CUB",
		"CY": "CYP", "CZ": "CZE", "DK": "DNK", "DJ": "DJI", "DM": "DMA",
		"DO": "DOM", "EC": "ECU", "EG": "EGY", "SV": "SLV", "GQ": "GNQ",
		"ER": "ERI", "EE": "EST", "ET": "ETH", "FJ": "FJI", "FI": "FIN",
		"FR": "FRA", "GA": "GAB", "GM": "GMB", "GE": "GEO", "DE": "DEU",
		"GH": "GHA", "GR": "GRC", "GD": "GRD", "GT": "GTM", "GN": "GIN",
		"GW": "GNB", "GY": "GUY", "HT": "HTI", "HN": "HND", "HK": "HKG",
		"HU": "HUN", "IS": "ISL", "IN": "IND", "ID": "IDN", "IR": "IRN",
		"IQ": "IRQ", "IE": "IRL", "IL": "ISR", "IT": "ITA", "JM": "JAM",
		"JP": "JPN", "JO": "JOR", "KZ": "KAZ", "KE": "KEN", "KI": "KIR",
		"KP": "PRK", "KR": "KOR", "KW": "KWT", "KG": "KGZ", "LA": "LAO",
		"LV": "LVA", "LB": "LBN", "LS": "LSO", "LR": "LBR", "LY": "LBY",
		"LI": "LIE", "LT": "LTU", "LU": "LUX", "MO": "MAC", "MK": "MKD",
		"MG": "MDG", "MW": "MWI", "MY": "MYS", "MV": "MDV", "ML": "MLI",
		"MT": "MLT", "MH": "MHL", "MR": "MRT", "MU": "MUS", "MX": "MEX",
		"FM": "FSM", "MD": "MDA", "MC": "MCO", "MN": "MNG", "ME": "MNE",
		"MA": "MAR", "MZ": "MOZ", "MM": "MMR", "NA": "NAM", "NR": "NRU",
		"NP": "NPL", "NL": "NLD", "NZ": "NZL", "NI": "NIC", "NE": "NER",
		"NG": "NGA", "NO": "NOR", "OM": "OMN", "PK": "PAK", "PW": "PLW",
		"PA": "PAN", "PG": "PNG", "PY": "PRY", "PE": "PER", "PH": "PHL",
		"PL": "POL", "PT": "PRT", "PR": "PRI", "QA": "QAT", "RO": "ROU",
		"RU": "RUS", "RW": "RWA", "KN": "KNA", "LC": "LCA", "VC": "VCT",
		"WS": "WSM", "SM": "SMR", "ST": "STP", "SA": "SAU", "SN": "SEN",
		"RS": "SRB", "SC": "SYC", "SL": "SLE", "SG": "SGP", "SK": "SVK",
		"SI": "SVN", "SB": "SLB", "SO": "SOM", "ZA": "ZAF", "ES": "ESP",
		"LK": "LKA", "SD": "SDN", "SR": "SUR", "SZ": "SWZ", "SE": "SWE",
		"CH": "CHE", "SY": "SYR", "TW": "TWN", "TJ": "TJK", "TZ": "TZA",
		"TH": "THA", "TL": "TLS", "TG": "TGO", "TO": "TON", "TT": "TTO",
		"TN": "TUN", "TR": "TUR", "TM": "TKM", "TV": "TUV", "UG": "UGA",
		"UA": "UKR", "AE": "ARE", "GB": "GBR", "US": "USA", "UY": "URY",
		"UZ": "UZB", "VU": "VUT", "VE": "VEN", "VN": "VNM", "YE": "YEM",
		"ZM": "ZMB", "ZW": "ZWE",
	}
	if alpha3, ok := codes[alpha2]; ok {
		return alpha3
	}
	return alpha2 // Return as-is if not found (might already be alpha-3)
}
