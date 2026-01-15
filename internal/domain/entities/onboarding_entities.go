package entities

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OnboardingStatus represents the overall onboarding status
type OnboardingStatus string

const (
	OnboardingStatusStarted        OnboardingStatus = "started"
	OnboardingStatusKYCPending     OnboardingStatus = "kyc_pending"
	OnboardingStatusKYCApproved    OnboardingStatus = "kyc_approved"
	OnboardingStatusKYCRejected    OnboardingStatus = "kyc_rejected"
	OnboardingStatusWalletsPending OnboardingStatus = "wallets_pending"
	OnboardingStatusCompleted      OnboardingStatus = "completed"
)

// IsValid checks if the onboarding status is valid
func (s OnboardingStatus) IsValid() bool {
	switch s {
	case OnboardingStatusStarted, OnboardingStatusKYCPending, OnboardingStatusKYCApproved,
		OnboardingStatusKYCRejected, OnboardingStatusWalletsPending, OnboardingStatusCompleted:
		return true
	default:
		return false
	}
}

// CanTransitionTo checks if transition to target status is allowed
func (s OnboardingStatus) CanTransitionTo(target OnboardingStatus) bool {
	transitions := map[OnboardingStatus][]OnboardingStatus{
		OnboardingStatusStarted:        {OnboardingStatusWalletsPending, OnboardingStatusKYCPending},
		OnboardingStatusKYCPending:     {OnboardingStatusKYCApproved, OnboardingStatusKYCRejected, OnboardingStatusWalletsPending},
		OnboardingStatusKYCApproved:    {OnboardingStatusWalletsPending},
		OnboardingStatusKYCRejected:    {OnboardingStatusKYCPending, OnboardingStatusWalletsPending}, // Allow retry or continue without KYC
		OnboardingStatusWalletsPending: {OnboardingStatusCompleted},
		OnboardingStatusCompleted:      {}, // Terminal state
	}

	allowedTargets, exists := transitions[s]
	if !exists {
		return false
	}

	for _, allowed := range allowedTargets {
		if allowed == target {
			return true
		}
	}
	return false
}

// KYCStatus represents KYC verification status
type KYCStatus string

const (
	KYCStatusPending    KYCStatus = "pending"
	KYCStatusProcessing KYCStatus = "processing"
	KYCStatusApproved   KYCStatus = "approved"
	KYCStatusRejected   KYCStatus = "rejected"
	KYCStatusExpired    KYCStatus = "expired"
)

// IsValid checks if KYC status is valid
func (s KYCStatus) IsValid() bool {
	switch s {
	case KYCStatusPending, KYCStatusProcessing, KYCStatusApproved, KYCStatusRejected, KYCStatusExpired:
		return true
	default:
		return false
	}
}

// OnboardingStepType represents different steps in the onboarding flow
type OnboardingStepType string

const (
	StepRegistration       OnboardingStepType = "registration"
	StepEmailVerification  OnboardingStepType = "email_verification"
	StepPhoneVerification  OnboardingStepType = "phone_verification"
	StepPasscodeCreation   OnboardingStepType = "passcode_creation"
	StepKYCSubmission      OnboardingStepType = "kyc_submission"
	StepKYCReview          OnboardingStepType = "kyc_review"
	StepWalletCreation     OnboardingStepType = "wallet_creation"
	StepOnboardingComplete OnboardingStepType = "completed"
)

// StepStatus represents the status of an individual onboarding step
type StepStatus string

const (
	StepStatusPending    StepStatus = "pending"
	StepStatusInProgress StepStatus = "in_progress"
	StepStatusCompleted  StepStatus = "completed"
	StepStatusFailed     StepStatus = "failed"
	StepStatusSkipped    StepStatus = "skipped"
)

// UserProfile represents enhanced user information for onboarding
type UserProfile struct {
	ID                 uuid.UUID        `json:"id" db:"id"`
	AuthProviderID     *string          `json:"auth_provider_id" db:"auth_provider_id"`
	Email              string           `json:"email" db:"email" validate:"required,email"`
	FirstName          *string          `json:"first_name" db:"first_name"`
	LastName           *string          `json:"last_name" db:"last_name"`
	DateOfBirth        *time.Time       `json:"date_of_birth" db:"date_of_birth"`
	Phone              *string          `json:"phone" db:"phone" validate:"omitempty,e164"`
	PhoneVerified      bool             `json:"phone_verified" db:"phone_verified"`
	EmailVerified      bool             `json:"email_verified" db:"email_verified"`
	OnboardingStatus   OnboardingStatus `json:"onboarding_status" db:"onboarding_status"`
	KYCStatus          string           `json:"kyc_status" db:"kyc_status"`
	KYCProviderRef     *string          `json:"kyc_provider_ref" db:"kyc_provider_ref"`
	KYCSubmittedAt     *time.Time       `json:"kyc_submitted_at" db:"kyc_submitted_at"`
	KYCApprovedAt      *time.Time       `json:"kyc_approved_at" db:"kyc_approved_at"`
	KYCRejectionReason *string          `json:"kyc_rejection_reason" db:"kyc_rejection_reason"`
	BridgeCustomerID   *string          `json:"bridge_customer_id" db:"bridge_customer_id"`
	AlpacaAccountID    *string          `json:"alpaca_account_id" db:"alpaca_account_id"`
	IsActive           bool             `json:"is_active" db:"is_active"`
	CreatedAt          time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at" db:"updated_at"`
}

// Validate performs business rule validation on user profile
func (u *UserProfile) Validate() error {
	if u.Email == "" {
		return fmt.Errorf("email is required")
	}

	if !strings.Contains(u.Email, "@") {
		return fmt.Errorf("invalid email format")
	}

	// Validate names if provided
	if u.FirstName != nil && *u.FirstName == "" {
		return fmt.Errorf("first name cannot be empty if provided")
	}

	if u.LastName != nil && *u.LastName == "" {
		return fmt.Errorf("last name cannot be empty if provided")
	}

	// Validate date of birth if provided
	if u.DateOfBirth != nil {
		// Must be at least 18 years old
		eighteenYearsAgo := time.Now().AddDate(-18, 0, 0)
		if u.DateOfBirth.After(eighteenYearsAgo) {
			return fmt.Errorf("user must be at least 18 years old")
		}

		// Cannot be more than 120 years ago
		oneHundredTwentyYearsAgo := time.Now().AddDate(-120, 0, 0)
		if u.DateOfBirth.Before(oneHundredTwentyYearsAgo) {
			return fmt.Errorf("invalid date of birth: too far in the past")
		}
	}

	if !u.OnboardingStatus.IsValid() {
		return fmt.Errorf("invalid onboarding status: %s", u.OnboardingStatus)
	}

	return nil
}

// CanStartKYC checks if user can start KYC process
func (u *UserProfile) CanStartKYC() bool {
	if !u.EmailVerified {
		return false
	}

	switch u.OnboardingStatus {
	case OnboardingStatusStarted, OnboardingStatusKYCRejected, OnboardingStatusWalletsPending, OnboardingStatusCompleted:
		return true
	case OnboardingStatusKYCPending:
		// Allow initial submission when we're in the pending state but nothing was sent yet
		return u.KYCSubmittedAt == nil
	default:
		return false
	}
}

// CanCreateWallets checks if user can proceed to wallet creation
func (u *UserProfile) CanCreateWallets() bool {
	if !u.EmailVerified {
		return false
	}

	// Wallet provisioning is allowed once identity basics are verified, even if KYC is optional
	switch u.OnboardingStatus {
	case OnboardingStatusWalletsPending, OnboardingStatusCompleted:
		return true
	case OnboardingStatusStarted, OnboardingStatusKYCPending, OnboardingStatusKYCApproved, OnboardingStatusKYCRejected:
		return true
	default:
		return false
	}
}

// GetFullName returns the user's full name if available
func (u *UserProfile) GetFullName() string {
	if u.FirstName != nil && u.LastName != nil {
		return fmt.Sprintf("%s %s", *u.FirstName, *u.LastName)
	}
	if u.FirstName != nil {
		return *u.FirstName
	}
	if u.LastName != nil {
		return *u.LastName
	}
	return ""
}

// HasPersonalInfo checks if user has provided personal information
func (u *UserProfile) HasPersonalInfo() bool {
	return u.FirstName != nil && u.LastName != nil && u.DateOfBirth != nil
}

// GetAge calculates user's age if date of birth is provided
func (u *UserProfile) GetAge() *int {
	if u.DateOfBirth == nil {
		return nil
	}
	now := time.Now()
	age := now.Year() - u.DateOfBirth.Year()
	if now.YearDay() < u.DateOfBirth.YearDay() {
		age--
	}
	return &age
}

// OnboardingFlow represents a step in the onboarding process
type OnboardingFlow struct {
	ID           uuid.UUID          `json:"id" db:"id"`
	UserID       uuid.UUID          `json:"user_id" db:"user_id"`
	Step         OnboardingStepType `json:"step" db:"step"`
	Status       StepStatus         `json:"status" db:"status"`
	Data         map[string]any     `json:"data" db:"data"` // JSON data specific to the step
	ErrorMessage *string            `json:"error_message" db:"error_message"`
	StartedAt    *time.Time         `json:"started_at" db:"started_at"`
	CompletedAt  *time.Time         `json:"completed_at" db:"completed_at"`
	CreatedAt    time.Time          `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at" db:"updated_at"`
}

// MarkStarted marks the step as started
func (o *OnboardingFlow) MarkStarted() {
	now := time.Now()
	o.Status = StepStatusInProgress
	o.StartedAt = &now
	o.UpdatedAt = now
}

// MarkCompleted marks the step as completed
func (o *OnboardingFlow) MarkCompleted(data map[string]any) {
	now := time.Now()
	o.Status = StepStatusCompleted
	o.CompletedAt = &now
	if data != nil {
		o.Data = data
	}
	o.UpdatedAt = now
}

// MarkFailed marks the step as failed
func (o *OnboardingFlow) MarkFailed(errorMsg string) {
	now := time.Now()
	o.Status = StepStatusFailed
	o.ErrorMessage = &errorMsg
	o.UpdatedAt = now
}

// KYCSubmission represents a KYC submission
type KYCSubmission struct {
	ID               uuid.UUID      `json:"id" db:"id"`
	UserID           uuid.UUID      `json:"user_id" db:"user_id"`
	Provider         string         `json:"provider" db:"provider"`
	ProviderRef      string         `json:"provider_ref" db:"provider_ref"`
	SubmissionType   string         `json:"submission_type" db:"submission_type"`
	Status           KYCStatus      `json:"status" db:"status"`
	VerificationData map[string]any `json:"verification_data" db:"verification_data"`
	RejectionReasons []string       `json:"rejection_reasons" db:"rejection_reasons"`
	SubmittedAt      time.Time      `json:"submitted_at" db:"submitted_at"`
	ReviewedAt       *time.Time     `json:"reviewed_at" db:"reviewed_at"`
	ExpiresAt        *time.Time     `json:"expires_at" db:"expires_at"`
	CreatedAt        time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at" db:"updated_at"`
}

// IsExpired checks if the KYC submission has expired
func (k *KYCSubmission) IsExpired() bool {
	return k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt)
}

// CanRetry checks if the KYC submission can be retried
func (k *KYCSubmission) CanRetry() bool {
	return k.Status == KYCStatusRejected || k.Status == KYCStatusExpired
}

// MarkReviewed marks the KYC as reviewed with a status
func (k *KYCSubmission) MarkReviewed(status KYCStatus, rejectionReasons []string) {
	now := time.Now()
	k.Status = status
	k.ReviewedAt = &now
	k.UpdatedAt = now

	if status == KYCStatusRejected && len(rejectionReasons) > 0 {
		k.RejectionReasons = rejectionReasons
	}
}

// === API Request/Response Models ===

// OnboardingStartRequest represents the request to start onboarding
type OnboardingStartRequest struct {
	Email string  `json:"email" validate:"required,email"`
	Phone *string `json:"phone,omitempty" validate:"omitempty,e164"`
}

// OnboardingStartResponse represents the response after starting onboarding
type OnboardingStartResponse struct {
	UserID           uuid.UUID          `json:"userId"`
	OnboardingStatus OnboardingStatus   `json:"onboardingStatus"`
	NextStep         OnboardingStepType `json:"nextStep"`
	SessionToken     string             `json:"sessionToken,omitempty"`
}

// OnboardingStatusResponse represents the current onboarding status
type OnboardingStatusResponse struct {
	UserID           uuid.UUID            `json:"userId"`
	OnboardingStatus OnboardingStatus     `json:"onboardingStatus"`
	KYCStatus        string               `json:"kycStatus"`
	CurrentStep      *OnboardingStepType  `json:"currentStep,omitempty"`
	CompletedSteps   []OnboardingStepType `json:"completedSteps"`
	WalletStatus     *WalletStatusSummary `json:"walletStatus,omitempty"`
	CanProceed       bool                 `json:"canProceed"`
	RequiredActions  []string             `json:"requiredActions,omitempty"`
}

// WalletStatusSummary provides a summary of wallet provisioning status
type WalletStatusSummary struct {
	TotalWallets    int               `json:"totalWallets"`
	CreatedWallets  int               `json:"createdWallets"`
	PendingWallets  int               `json:"pendingWallets"`
	FailedWallets   int               `json:"failedWallets"`
	SupportedChains []string          `json:"supportedChains"`
	WalletsByChain  map[string]string `json:"walletsByChain"` // chain -> status
}

// KYCStatusResponse captures a user's verification state with contextual guidance
type KYCStatusResponse struct {
	UserID            uuid.UUID  `json:"userId"`
	Status            string     `json:"status"`
	Verified          bool       `json:"verified"`
	HasSubmitted      bool       `json:"hasSubmitted"`
	RequiresKYC       bool       `json:"requiresKyc"`
	RequiredFor       []string   `json:"requiredFor"`
	LastSubmittedAt   *time.Time `json:"lastSubmittedAt,omitempty"`
	ApprovedAt        *time.Time `json:"approvedAt,omitempty"`
	RejectionReason   *string    `json:"rejectionReason,omitempty"`
	ProviderReference *string    `json:"providerReference,omitempty"`
	NextSteps         []string   `json:"nextSteps,omitempty"`
}

// KYCSubmitRequest represents KYC submission request
type KYCSubmitRequest struct {
	DocumentType string                 `json:"documentType" validate:"required"`
	Documents    []KYCDocumentUpload    `json:"documents" validate:"required,min=1"`
	PersonalInfo *KYCPersonalInfo       `json:"personalInfo,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// KYCDocumentUpload represents a document uploaded for KYC
type KYCDocumentUpload struct {
	Type        string `json:"type" validate:"required"` // passport, drivers_license, etc.
	FileURL     string `json:"fileUrl" validate:"required,url"`
	ContentType string `json:"contentType" validate:"required"`
}

// KYCPersonalInfo represents personal information for KYC
type KYCPersonalInfo struct {
	FirstName   string     `json:"firstName" validate:"required"`
	LastName    string     `json:"lastName" validate:"required"`
	DateOfBirth *time.Time `json:"dateOfBirth,omitempty"`
	Country     string     `json:"country" validate:"required,len=2"`
	Address     *Address   `json:"address,omitempty"`
}

// Address represents a physical address
type Address struct {
	Street     string `json:"street" validate:"required"`
	City       string `json:"city" validate:"required"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postalCode" validate:"required"`
	Country    string `json:"country" validate:"required,len=2"`
}

// OnboardingCompleteRequest represents the request to complete onboarding
type OnboardingCompleteRequest struct {
	UserID      uuid.UUID  `json:"-" validate:"-"` // Set from auth context
	FirstName   string     `json:"firstName" validate:"required"`
	LastName    string     `json:"lastName" validate:"required"`
	DateOfBirth *time.Time `json:"dateOfBirth" validate:"required"`
	Country     string     `json:"country" validate:"required,len=2"`
	Address     Address    `json:"address" validate:"required"`
	Phone       *string    `json:"phone,omitempty" validate:"omitempty,e164"`
}

// OnboardingCompleteResponse represents the response after completing onboarding
type OnboardingCompleteResponse struct {
	UserID           uuid.UUID `json:"userId"`
	BridgeCustomerID string    `json:"bridgeCustomerId"`
	AlpacaAccountID  string    `json:"alpacaAccountId"`
	Message          string    `json:"message"`
	NextSteps        []string  `json:"nextSteps"`
}

// OnboardingProgressResponse represents the user's onboarding progress
type OnboardingProgressResponse struct {
	UserID          uuid.UUID              `json:"userId"`
	PercentComplete int                    `json:"percentComplete"`
	Checklist       []OnboardingCheckItem  `json:"checklist"`
	CurrentStep     *OnboardingStepType    `json:"currentStep,omitempty"`
	EstimatedTime   string                 `json:"estimatedTime"`
	CanInvest       bool                   `json:"canInvest"`
	CanWithdraw     bool                   `json:"canWithdraw"`
}

// OnboardingCheckItem represents a single item in the onboarding checklist
type OnboardingCheckItem struct {
	Step        OnboardingStepType `json:"step"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Status      StepStatus         `json:"status"`
	Required    bool               `json:"required"`
	Order       int                `json:"order"`
}


