package entities

import (
	"time"

	"github.com/google/uuid"
)

// SocialProvider represents supported OAuth providers
type SocialProvider string

const (
	SocialProviderGoogle SocialProvider = "google"
	SocialProviderApple  SocialProvider = "apple"
)

// SocialAccount represents a linked social account
type SocialAccount struct {
	ID           uuid.UUID      `json:"id" db:"id"`
	UserID       uuid.UUID      `json:"userId" db:"user_id"`
	Provider     SocialProvider `json:"provider" db:"provider"`
	ProviderID   string         `json:"providerId" db:"provider_id"`
	Email        string         `json:"email" db:"email"`
	Name         string         `json:"name,omitempty" db:"name"`
	AvatarURL    string         `json:"avatarUrl,omitempty" db:"avatar_url"`
	AccessToken  string         `json:"-" db:"access_token"`
	RefreshToken string         `json:"-" db:"refresh_token"`
	ExpiresAt    *time.Time     `json:"expiresAt,omitempty" db:"expires_at"`
	CreatedAt    time.Time      `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time      `json:"updatedAt" db:"updated_at"`
}

// SocialLoginRequest represents a social login request
type SocialLoginRequest struct {
	Provider    SocialProvider `json:"provider" validate:"required"`
	IDToken     string         `json:"idToken,omitempty"`
	AccessToken string         `json:"accessToken,omitempty"`
	Code        string         `json:"code,omitempty"`
	RedirectURI string         `json:"redirectUri,omitempty"`
	// Apple Sign-In specific fields (name only sent on first sign-in)
	Name      string `json:"name,omitempty"`
	GivenName string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

// SocialLoginResponse represents the response after social login
type SocialLoginResponse struct {
	User         *UserInfo `json:"user"`
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	IsNewUser    bool      `json:"isNewUser"`
}

// SocialAuthURLRequest represents a request for OAuth URL
type SocialAuthURLRequest struct {
	Provider    SocialProvider `json:"provider" validate:"required"`
	RedirectURI string         `json:"redirectUri" validate:"required"`
	State       string         `json:"state,omitempty"`
}

// SocialAuthURLResponse represents the OAuth URL response
type SocialAuthURLResponse struct {
	URL   string `json:"url"`
	State string `json:"state"`
}

// LinkedAccountsResponse represents linked social accounts
type LinkedAccountsResponse struct {
	Accounts []LinkedAccount `json:"accounts"`
}

// LinkedAccount represents a simplified linked account view
type LinkedAccount struct {
	Provider  SocialProvider `json:"provider"`
	Email     string         `json:"email"`
	Name      string         `json:"name,omitempty"`
	LinkedAt  time.Time      `json:"linkedAt"`
}

// WebAuthnCredential represents a stored WebAuthn credential
type WebAuthnCredential struct {
	ID              uuid.UUID `json:"id" db:"id"`
	UserID          uuid.UUID `json:"userId" db:"user_id"`
	CredentialID    []byte    `json:"-" db:"credential_id"`
	PublicKey       []byte    `json:"-" db:"public_key"`
	AttestationType string    `json:"attestationType" db:"attestation_type"`
	AAGUID          []byte    `json:"-" db:"aaguid"`
	SignCount       uint32    `json:"signCount" db:"sign_count"`
	Name            string    `json:"name" db:"name"`
	CreatedAt       time.Time `json:"createdAt" db:"created_at"`
	LastUsedAt      *time.Time `json:"lastUsedAt,omitempty" db:"last_used_at"`
}

// WebAuthnRegisterRequest represents a WebAuthn registration request
type WebAuthnRegisterRequest struct {
	Name string `json:"name" validate:"required"`
}

// WebAuthnRegisterResponse represents registration options
type WebAuthnRegisterResponse struct {
	Options interface{} `json:"options"`
}

// WebAuthnRegisterFinishRequest represents the finish registration request
type WebAuthnRegisterFinishRequest struct {
	Response interface{} `json:"response"`
}

// WebAuthnLoginRequest represents a WebAuthn login request
type WebAuthnLoginRequest struct {
	Email string `json:"email,omitempty"`
}

// WebAuthnLoginResponse represents login options
type WebAuthnLoginResponse struct {
	Options interface{} `json:"options"`
}

// WebAuthnLoginFinishRequest represents the finish login request
type WebAuthnLoginFinishRequest struct {
	Response interface{} `json:"response"`
}

// WebAuthnCredentialsResponse represents user's WebAuthn credentials
type WebAuthnCredentialsResponse struct {
	Credentials []WebAuthnCredentialInfo `json:"credentials"`
}

// WebAuthnCredentialInfo represents credential info for display
type WebAuthnCredentialInfo struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}
