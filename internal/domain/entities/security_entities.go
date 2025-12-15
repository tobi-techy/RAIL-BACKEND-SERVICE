package entities

import (
	"time"

	"github.com/google/uuid"
)

// PasscodeSetupRequest represents a request to create an access passcode
type PasscodeSetupRequest struct {
	Passcode        string `json:"passcode" validate:"required"`
	ConfirmPasscode string `json:"confirmPasscode" validate:"required"`
}

// PasscodeUpdateRequest represents a request to change an existing passcode
type PasscodeUpdateRequest struct {
	CurrentPasscode string `json:"currentPasscode" validate:"required"`
	NewPasscode     string `json:"newPasscode" validate:"required"`
	ConfirmPasscode string `json:"confirmPasscode" validate:"required"`
}

// PasscodeVerifyRequest represents a passcode verification attempt
type PasscodeVerifyRequest struct {
	Passcode string `json:"passcode" validate:"required"`
}

// PasscodeRemoveRequest represents a request to disable the passcode
type PasscodeRemoveRequest struct {
	Passcode string `json:"passcode" validate:"required"`
}

// PasscodeStatusResponse exposes passcode configuration status to API clients
type PasscodeStatusResponse struct {
	Enabled           bool       `json:"enabled"`
	Locked            bool       `json:"locked"`
	FailedAttempts    int        `json:"failedAttempts"`
	RemainingAttempts int        `json:"remainingAttempts"`
	LockedUntil       *time.Time `json:"lockedUntil,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
}

// PasscodeVerificationResponse is returned after a successful verification
type PasscodeVerificationResponse struct {
	Verified                 bool      `json:"verified"`
	AccessToken              string    `json:"accessToken"`
	RefreshToken             string    `json:"refreshToken"`
	ExpiresAt                time.Time `json:"expiresAt"`
	PasscodeSessionToken     string    `json:"passcodeSessionToken"`     // Short-lived token for sensitive operations
	PasscodeSessionExpiresAt time.Time `json:"passcodeSessionExpiresAt"` // Expiration for passcode session
}

// PasscodeMetadata captures persisted passcode security information
type PasscodeMetadata struct {
	HashedPasscode *string    `json:"-"`
	FailedAttempts int        `json:"failedAttempts"`
	LockedUntil    *time.Time `json:"lockedUntil,omitempty"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
}

// PasscodeSession holds information about a verified passcode session
type PasscodeSession struct {
	UserID    uuid.UUID `json:"userId"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}
