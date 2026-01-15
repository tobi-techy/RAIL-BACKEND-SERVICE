package entities

import (
	"time"

	"github.com/google/uuid"
)

// === Authentication Request/Response Models ===

// RegisterRequest represents a user registration request
type RegisterRequest struct {
	Email    string  `json:"email" validate:"required,email"`
	Phone    *string `json:"phone,omitempty" validate:"omitempty,e164"`
	Password string  `json:"password" validate:"required,min=8"`
}

// LoginRequest represents a user login request
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// AuthResponse represents the response after successful authentication
type AuthResponse struct {
	User         *UserInfo `json:"user"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// UserInfo represents basic user information returned in auth responses
type UserInfo struct {
	ID               uuid.UUID        `json:"id"`
	Email            string           `json:"email"`
	Phone            *string          `json:"phone,omitempty"`
	EmailVerified    bool             `json:"emailVerified"`
	PhoneVerified    bool             `json:"phoneVerified"`
	OnboardingStatus OnboardingStatus `json:"onboardingStatus"`
	KYCStatus        string           `json:"kycStatus"`
	CreatedAt        time.Time        `json:"createdAt"`
}

// RefreshTokenRequest represents a token refresh request
type RefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken" validate:"required"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// ForgotPasswordRequest represents a forgot password request
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest represents a reset password request
type ResetPasswordRequest struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
}

// VerifyEmailRequest represents an email verification request
type VerifyEmailRequest struct {
	Token string `json:"token" validate:"required"`
}

// ChangePasswordRequest represents a change password request
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" validate:"required"`
	NewPassword     string `json:"newPassword" validate:"required,min=8"`
}

// SignUpRequest represents a user signup request with email OR phone
type SignUpRequest struct {
	Email    *string `json:"email,omitempty" validate:"omitempty,email"`
	Phone    *string `json:"phone,omitempty" validate:"omitempty,e164"`
	Password string  `json:"password" validate:"required,min=8"`
}

// VerifyCodeRequest represents a verification code request
type VerifyCodeRequest struct {
	Email *string `json:"email,omitempty" validate:"omitempty,email"`
	Phone *string `json:"phone,omitempty" validate:"omitempty,e164"`
	Code  string  `json:"code" validate:"required,len=6"`
}

// ResendCodeRequest represents a resend verification code request
type ResendCodeRequest struct {
	Email *string `json:"email,omitempty" validate:"omitempty,email"`
	Phone *string `json:"phone,omitempty" validate:"omitempty,e164"`
}

// VerificationCodeData represents verification code data stored in Redis
type VerificationCodeData struct {
	Code      string    `json:"code"`
	Attempts  int       `json:"attempts"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// PendingRegistration stores registration data in Redis until email/phone is verified
type PendingRegistration struct {
	Email        string    `json:"email,omitempty"`
	Phone        string    `json:"phone,omitempty"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// SignUpResponse represents the response after successful signup
type SignUpResponse struct {
	Message    string `json:"message"`
	Identifier string `json:"identifier"`
}

// VerifyCodeResponse represents the response after successful verification
type VerifyCodeResponse struct {
	User         *UserInfo `json:"user"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// User represents a complete user entity for database operations
type User struct {
	ID                 uuid.UUID        `json:"id" db:"id"`
	Email              string           `json:"email" db:"email"`
	Phone              *string          `json:"phone" db:"phone"`
	PasswordHash       string           `json:"-" db:"password_hash"`
	AuthProviderID     *string          `json:"authProviderId" db:"auth_provider_id"`
	EmailVerified      bool             `json:"emailVerified" db:"email_verified"`
	PhoneVerified      bool             `json:"phoneVerified" db:"phone_verified"`
	OnboardingStatus   OnboardingStatus `json:"onboardingStatus" db:"onboarding_status"`
	KYCStatus          string           `json:"kycStatus" db:"kyc_status"`
	KYCProviderRef     *string          `json:"kycProviderRef" db:"kyc_provider_ref"`
	KYCSubmittedAt     *time.Time       `json:"kycSubmittedAt" db:"kyc_submitted_at"`
	KYCApprovedAt      *time.Time       `json:"kycApprovedAt" db:"kyc_approved_at"`
	KYCRejectionReason *string          `json:"kycRejectionReason" db:"kyc_rejection_reason"`
	BridgeCustomerID   *string          `json:"bridgeCustomerId" db:"bridge_customer_id"`
	BridgeKYCStatus    *string          `json:"bridgeKycStatus" db:"bridge_kyc_status"`
	BridgeKYCLink      *string          `json:"bridgeKycLink" db:"bridge_kyc_link"`
	Role               string           `json:"role" db:"role"`
	IsActive           bool             `json:"isActive" db:"is_active"`
	LastLoginAt        *time.Time       `json:"lastLoginAt" db:"last_login_at"`
	CreatedAt          time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt          time.Time        `json:"updatedAt" db:"updated_at"`
}

// ToUserInfo converts User to UserInfo for public responses
func (u *User) ToUserInfo() *UserInfo {
	return &UserInfo{
		ID:               u.ID,
		Email:            u.Email,
		Phone:            u.Phone,
		EmailVerified:    u.EmailVerified,
		PhoneVerified:    u.PhoneVerified,
		OnboardingStatus: u.OnboardingStatus,
		KYCStatus:        u.KYCStatus,
		CreatedAt:        u.CreatedAt,
	}
}

// ToUserProfile converts User to UserProfile for onboarding operations
func (u *User) ToUserProfile() *UserProfile {
	return &UserProfile{
		ID:                 u.ID,
		AuthProviderID:     u.AuthProviderID,
		Email:              u.Email,
		Phone:              u.Phone,
		EmailVerified:      u.EmailVerified,
		PhoneVerified:      u.PhoneVerified,
		OnboardingStatus:   u.OnboardingStatus,
		KYCStatus:          u.KYCStatus,
		KYCProviderRef:     u.KYCProviderRef,
		KYCSubmittedAt:     u.KYCSubmittedAt,
		KYCApprovedAt:      u.KYCApprovedAt,
		KYCRejectionReason: u.KYCRejectionReason,
		IsActive:           u.IsActive,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
	}
}
