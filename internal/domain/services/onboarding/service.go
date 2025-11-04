package onboarding

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
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
}

type KYCProvider interface {
	SubmitKYC(ctx context.Context, userID uuid.UUID, documents []entities.KYCDocument, personalInfo *entities.KYCPersonalInfo) (string, error)
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

// NewService creates a new onboarding service
func NewService(
	userRepo UserRepository,
	onboardingFlowRepo OnboardingFlowRepository,
	kycSubmissionRepo KYCSubmissionRepository,
	walletService WalletService,
	kycProvider KYCProvider,
	emailService EmailService,
	auditService AuditService,
	logger *zap.Logger,
	defaultWalletChains []entities.WalletChain,
) *Service {
	normalizedChains := normalizeDefaultWalletChains(defaultWalletChains, logger)

	return &Service{
		userRepo:            userRepo,
		onboardingFlowRepo:  onboardingFlowRepo,
		kycSubmissionRepo:   kycSubmissionRepo,
		walletService:       walletService,
		kycProvider:         kycProvider,
		emailService:        emailService,
		auditService:        auditService,
		logger:              logger,
		defaultWalletChains: normalizedChains,
	}
}

func normalizeDefaultWalletChains(chains []entities.WalletChain, logger *zap.Logger) []entities.WalletChain {
	if len(chains) == 0 {
		logger.Warn("No default wallet chains configured; falling back to SOL-DEVNET")
		return []entities.WalletChain{
			entities.ChainSOLDevnet,
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
			entities.ChainSOLDevnet,
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

	// Calculate completion percentage
	completionPercentage := s.calculateCompletionPercentage(user, completedSteps)

	return &entities.OnboardingStatusResponse{
		UserID:               user.ID,
		OnboardingStatus:     user.OnboardingStatus,
		KYCStatus:            user.KYCStatus,
		CurrentStep:          currentStep,
		CompletedSteps:       completedSteps,
		WalletStatus:         walletStatus,
		CanProceed:           canProceed,
		RequiredActions:      requiredActions,
		CompletionPercentage: completionPercentage,
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
		Provider:       "jumio", // TODO: make configurable
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

		// Complete onboarding since KYC is approved
		if err := s.completeOnboardingAfterKYC(ctx, user.ID); err != nil {
			s.logger.Warn("Failed to complete onboarding after KYC approval", zap.Error(err))
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

// InitiateKYC allows users to manually initiate or re-initiate the KYC process
// This provides a recovery mechanism for users stuck in KYCPending status
func (s *Service) InitiateKYC(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Manually initiating KYC process", zap.String("userId", userID.String()))

	// Verify user exists and is active
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if !user.IsActive {
		return fmt.Errorf("user account is inactive")
	}

	// Check if user is in a state where KYC initiation makes sense
	if user.OnboardingStatus == entities.OnboardingStatusCompleted {
		return fmt.Errorf("onboarding is already completed")
	}

	// Initiate KYC process (this method handles checking if already in progress)
	if err := s.initiateKYCProcess(ctx, userID); err != nil {
		return fmt.Errorf("failed to initiate KYC process: %w", err)
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "kyc_manual_initiation", "user", nil, map[string]any{
		"trigger": "manual_request",
	}); err != nil {
		s.logger.Warn("Failed to log manual KYC initiation audit event", zap.Error(err))
	}

	s.logger.Info("KYC process manually initiated successfully", zap.String("userId", userID.String()))
	return nil
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

	// Update user status to KYC pending (not completed yet)
	if err := s.userRepo.UpdateOnboardingStatus(ctx, userID, entities.OnboardingStatusKYCPending); err != nil {
	return fmt.Errorf("failed to update onboarding status: %w", err)
	}

	// Initiate KYC process
	if err := s.initiateKYCProcess(ctx, userID); err != nil {
		s.logger.Warn("Failed to initiate KYC process after wallet creation", zap.Error(err), zap.String("userId", userID.String()))
		// Don't fail the wallet creation completion, just log the warning
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

// completeOnboardingAfterKYC completes the onboarding process after successful KYC approval
func (s *Service) completeOnboardingAfterKYC(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Completing onboarding after KYC approval", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for onboarding completion: %w", err)
	}

	// Mark onboarding complete step as completed
	if err := s.markStepCompleted(ctx, userID, entities.StepOnboardingComplete, map[string]any{
		"completed_at": time.Now(),
		"kyc_approved": true,
	}); err != nil {
		s.logger.Warn("Failed to mark onboarding complete step as completed", zap.Error(err))
	}

	// Update user status to completed
	if err := s.userRepo.UpdateOnboardingStatus(ctx, userID, entities.OnboardingStatusCompleted); err != nil {
		return fmt.Errorf("failed to update onboarding status to completed: %w", err)
	}

	// Send welcome email with full access notification
	if err := s.emailService.SendWelcomeEmail(ctx, user.Email); err != nil {
		s.logger.Warn("Failed to send welcome email after KYC completion", zap.Error(err))
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "onboarding_completed", "user",
		map[string]any{"status": string(entities.OnboardingStatusKYCApproved)},
		map[string]any{"status": string(entities.OnboardingStatusCompleted), "kyc_approved": true}); err != nil {
		s.logger.Warn("Failed to log onboarding completion audit event", zap.Error(err))
	}

	s.logger.Info("Onboarding completed successfully after KYC approval", zap.String("userId", userID.String()))
	return nil
}

// calculateCompletionPercentage calculates the onboarding completion percentage based on completed steps
func (s *Service) calculateCompletionPercentage(user *entities.UserProfile, completedSteps []entities.OnboardingStepType) int {
	// Define all required steps for completion
	allSteps := []entities.OnboardingStepType{
		entities.StepRegistration,
		entities.StepEmailVerification,
		entities.StepPasscodeCreation,
		entities.StepWalletCreation,
		entities.StepKYCSubmission,
		entities.StepKYCReview,
		entities.StepOnboardingComplete,
	}

	completedMap := make(map[entities.OnboardingStepType]bool)
	for _, step := range completedSteps {
		completedMap[step] = true
	}

	// Add inferred completed steps based on user status
	if user.OnboardingStatus == entities.OnboardingStatusCompleted {
		for _, step := range allSteps {
			completedMap[step] = true
		}
	}

	// Count completed steps
	completedCount := 0
	for _, step := range allSteps {
		if completedMap[step] {
			completedCount++
		}
	}

	// Calculate percentage
	if len(allSteps) == 0 {
		return 100
	}

	percentage := (completedCount * 100) / len(allSteps)
	if percentage > 100 {
		percentage = 100
	}

	return percentage
}

// initiateKYCProcess automatically initiates KYC verification for users after wallet creation
func (s *Service) initiateKYCProcess(ctx context.Context, userID uuid.UUID) error {
	s.logger.Info("Initiating KYC process after wallet creation", zap.String("userId", userID.String()))

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user for KYC initiation: %w", err)
	}

	// Check if user already has KYC in progress or completed
	kycStatus := entities.KYCStatus(user.KYCStatus)
	if kycStatus == entities.KYCStatusProcessing || kycStatus == entities.KYCStatusApproved {
		s.logger.Info("KYC already initiated or completed", zap.String("userId", userID.String()), zap.String("status", string(kycStatus)))
		return nil
	}

	// Generate KYC URL for the user
	kycURL, err := s.kycProvider.GenerateKYCURL(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to generate KYC URL: %w", err)
	}

	// Send KYC initiation email
	if err := s.emailService.SendKYCStatusEmail(ctx, user.Email, entities.KYCStatusPending, nil); err != nil {
		s.logger.Warn("Failed to send KYC initiation email", zap.Error(err), zap.String("userId", userID.String()))
		// Don't fail the KYC initiation for email failure
	}

	// Log audit event
	if err := s.auditService.LogOnboardingEvent(ctx, userID, "kyc_auto_initiated", "user", nil, map[string]any{
		"kyc_url": kycURL,
		"trigger": "wallet_creation_complete",
	}); err != nil {
		s.logger.Warn("Failed to log KYC initiation audit event", zap.Error(err))
	}

	s.logger.Info("KYC process initiated automatically", zap.String("userId", userID.String()))
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

	if user.OnboardingStatus == entities.OnboardingStatusKYCPending {
		kycStatus := entities.KYCStatus(user.KYCStatus)
		switch kycStatus {
		case entities.KYCStatusPending, entities.KYCStatusProcessing:
			return entities.StepKYCReview
		case entities.KYCStatusRejected:
			return entities.StepKYCSubmission
		case entities.KYCStatusApproved:
			return entities.StepOnboardingComplete
		default:
			return entities.StepKYCSubmission
		}
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
