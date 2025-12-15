package errors

import "errors"

// Authentication and authorization errors
var (
	// User errors
	ErrUserNotFound          = errors.New("user not found")
	ErrUserAlreadyExists     = errors.New("user already exists")
	ErrUserInactive          = errors.New("user is inactive")
	ErrUserSuspended         = errors.New("user is suspended")

	// Authentication errors
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrInvalidToken          = errors.New("invalid token")
	ErrTokenExpired          = errors.New("token expired")
	ErrTokenBlacklisted      = errors.New("token has been revoked")
	ErrSessionExpired        = errors.New("session expired")
	ErrSessionInvalid        = errors.New("session invalid")

	// Password errors
	ErrWeakPassword          = errors.New("password does not meet requirements")
	ErrPasswordMismatch      = errors.New("passwords do not match")
	ErrPasswordHashFailed    = errors.New("failed to hash password")
	ErrPasswordSameAsOld     = errors.New("new password cannot be same as old")
	ErrPasswordExpired       = errors.New("password has expired")
	ErrPasswordBreached      = errors.New("password found in data breach")

	// Passcode errors
	ErrPasscodeNotSet        = errors.New("passcode not set")
	ErrPasscodeAlreadySet    = errors.New("passcode already set")
	ErrPasscodeMismatch      = errors.New("incorrect passcode")
	ErrPasscodeLocked        = errors.New("passcode locked")
	ErrInvalidPasscodeFormat = errors.New("invalid passcode format")

	// 2FA errors
	ErrTwoFANotEnabled       = errors.New("2FA not enabled")
	ErrTwoFAAlreadyEnabled   = errors.New("2FA already enabled")
	ErrTwoFAInvalidCode      = errors.New("invalid 2FA code")
	ErrTwoFASetupRequired    = errors.New("2FA setup required")
	ErrBackupCodeInvalid     = errors.New("invalid backup code")
	ErrBackupCodeUsed        = errors.New("backup code already used")

	// API Key errors
	ErrAPIKeyNotFound        = errors.New("API key not found")
	ErrAPIKeyInvalid         = errors.New("invalid API key")
	ErrAPIKeyExpired         = errors.New("API key expired")
	ErrAPIKeyRevoked         = errors.New("API key revoked")
	ErrAPIKeyLimitExceeded   = errors.New("API key limit exceeded")

	// Rate limiting
	ErrTooManyAttempts       = errors.New("too many attempts")
	ErrLoginLocked           = errors.New("login temporarily locked")

	// Account verification
	ErrEmailNotVerified      = errors.New("email not verified")
	ErrPhoneNotVerified      = errors.New("phone not verified")
	ErrVerificationExpired   = errors.New("verification code expired")
	ErrVerificationInvalid   = errors.New("invalid verification code")
)

// UserNotFoundError creates a user not found error
func UserNotFoundError(identifier string) *DomainError {
	return &DomainError{
		Err:     ErrUserNotFound,
		Code:    "USER_NOT_FOUND",
		Message: "user not found",
		Details: map[string]interface{}{
			"identifier": identifier,
		},
	}
}

// UserAlreadyExistsError creates a user already exists error
func UserAlreadyExistsError(email string) *DomainError {
	return &DomainError{
		Err:     ErrUserAlreadyExists,
		Code:    "USER_ALREADY_EXISTS",
		Message: "a user with this email already exists",
		Details: map[string]interface{}{
			"email": email,
		},
	}
}

// InvalidCredentialsError creates an invalid credentials error
func InvalidCredentialsError() *DomainError {
	return &DomainError{
		Err:     ErrInvalidCredentials,
		Code:    "INVALID_CREDENTIALS",
		Message: "invalid email or password",
	}
}

// WeakPasswordError creates a weak password error with requirements
func WeakPasswordError(requirements []string) *DomainError {
	return &DomainError{
		Err:     ErrWeakPassword,
		Code:    "WEAK_PASSWORD",
		Message: "password does not meet requirements",
		Details: map[string]interface{}{
			"requirements": requirements,
		},
	}
}

// PasscodeLockedError creates a passcode locked error
func PasscodeLockedError(unlockAt string) *DomainError {
	return &DomainError{
		Err:     ErrPasscodeLocked,
		Code:    "PASSCODE_LOCKED",
		Message: "passcode is locked due to too many failed attempts",
		Details: map[string]interface{}{
			"unlock_at": unlockAt,
		},
	}
}

// TwoFAInvalidCodeError creates a 2FA invalid code error
func TwoFAInvalidCodeError() *DomainError {
	return &DomainError{
		Err:     ErrTwoFAInvalidCode,
		Code:    "2FA_INVALID_CODE",
		Message: "invalid or expired 2FA code",
	}
}

// TooManyAttemptsError creates a rate limit error for login
func TooManyAttemptsError(retryAfter int) *DomainError {
	return &DomainError{
		Err:     ErrTooManyAttempts,
		Code:    "TOO_MANY_ATTEMPTS",
		Message: "too many failed attempts, please try again later",
		Details: map[string]interface{}{
			"retry_after_seconds": retryAfter,
		},
	}
}

// TokenExpiredError creates a token expired error
func TokenExpiredError() *DomainError {
	return &DomainError{
		Err:     ErrTokenExpired,
		Code:    "TOKEN_EXPIRED",
		Message: "authentication token has expired",
	}
}

// SessionExpiredError creates a session expired error
func SessionExpiredError() *DomainError {
	return &DomainError{
		Err:     ErrSessionExpired,
		Code:    "SESSION_EXPIRED",
		Message: "session has expired, please log in again",
	}
}

// VerificationCodeError creates a verification code error
func VerificationCodeError(codeType string, expired bool) *DomainError {
	var err error
	var code, message string

	if expired {
		err = ErrVerificationExpired
		code = "VERIFICATION_EXPIRED"
		message = "verification code has expired"
	} else {
		err = ErrVerificationInvalid
		code = "VERIFICATION_INVALID"
		message = "invalid verification code"
	}

	return &DomainError{
		Err:     err,
		Code:    code,
		Message: message,
		Details: map[string]interface{}{
			"type": codeType,
		},
	}
}

// APIKeyError creates an API key error
func APIKeyError(reason string) *DomainError {
	var err error
	var code string

	switch reason {
	case "not_found":
		err = ErrAPIKeyNotFound
		code = "API_KEY_NOT_FOUND"
	case "invalid":
		err = ErrAPIKeyInvalid
		code = "API_KEY_INVALID"
	case "expired":
		err = ErrAPIKeyExpired
		code = "API_KEY_EXPIRED"
	case "revoked":
		err = ErrAPIKeyRevoked
		code = "API_KEY_REVOKED"
	default:
		err = ErrAPIKeyInvalid
		code = "API_KEY_ERROR"
	}

	return &DomainError{
		Err:     err,
		Code:    code,
		Message: "API key error: " + reason,
	}
}

// Error checking helpers

// IsUserNotFound checks if error is user not found
func IsUserNotFound(err error) bool {
	return errors.Is(err, ErrUserNotFound)
}

// IsInvalidCredentials checks if error is invalid credentials
func IsInvalidCredentials(err error) bool {
	return errors.Is(err, ErrInvalidCredentials)
}

// IsTokenExpired checks if error is token expired
func IsTokenExpired(err error) bool {
	return errors.Is(err, ErrTokenExpired)
}

// IsPasscodeLocked checks if passcode is locked
func IsPasscodeLocked(err error) bool {
	return errors.Is(err, ErrPasscodeLocked)
}

// IsTooManyAttempts checks if too many attempts
func IsTooManyAttempts(err error) bool {
	return errors.Is(err, ErrTooManyAttempts)
}

// IsTwoFARequired checks if 2FA is required
func IsTwoFARequired(err error) bool {
	return errors.Is(err, ErrTwoFASetupRequired)
}
