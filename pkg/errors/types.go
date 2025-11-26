package errors

import (
	"errors"
	"fmt"
)

// ErrorType represents the category of error
type ErrorType string

const (
	// ErrorTypeInternal represents internal server errors
	ErrorTypeInternal ErrorType = "internal"
	
	// ErrorTypeValidation represents input validation errors
	ErrorTypeValidation ErrorType = "validation"
	
	// ErrorTypeNotFound represents resource not found errors
	ErrorTypeNotFound ErrorType = "not_found"
	
	// ErrorTypeConflict represents resource conflict errors
	ErrorTypeConflict ErrorType = "conflict"
	
	// ErrorTypeUnauthorized represents authentication errors
	ErrorTypeUnauthorized ErrorType = "unauthorized"
	
	// ErrorTypeForbidden represents authorization errors
	ErrorTypeForbidden ErrorType = "forbidden"
	
	// ErrorTypeRateLimit represents rate limiting errors
	ErrorTypeRateLimit ErrorType = "rate_limit"
	
	// ErrorTypeTimeout represents timeout errors
	ErrorTypeTimeout ErrorType = "timeout"
	
	// ErrorTypeExternal represents external service errors
	ErrorTypeExternal ErrorType = "external"
	
	// ErrorTypeTransient represents transient errors that can be retried
	ErrorTypeTransient ErrorType = "transient"
)

// AppError represents an application error with additional context
type AppError struct {
	Type       ErrorType         `json:"type"`
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Details    map[string]string `json:"details,omitempty"`
	Err        error             `json:"-"`
	Retryable  bool              `json:"retryable"`
	StatusCode int               `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the wrapped error
func (e *AppError) Unwrap() error {
	return e.Err
}

// Is implements error comparison
func (e *AppError) Is(target error) bool {
	t, ok := target.(*AppError)
	if !ok {
		return false
	}
	return e.Code == t.Code && e.Type == t.Type
}

// WithDetail adds a detail to the error
func (e *AppError) WithDetail(key, value string) *AppError {
	if e.Details == nil {
		e.Details = make(map[string]string)
	}
	e.Details[key] = value
	return e
}

// Common error instances
var (
	// ErrInternalServer represents a generic internal server error
	ErrInternalServer = &AppError{
		Type:       ErrorTypeInternal,
		Code:       "INTERNAL_ERROR",
		Message:    "An internal server error occurred",
		StatusCode: 500,
		Retryable:  false,
	}
	
	// ErrValidation represents a generic validation error
	ErrValidation = &AppError{
		Type:       ErrorTypeValidation,
		Code:       "VALIDATION_ERROR",
		Message:    "Validation failed",
		StatusCode: 400,
		Retryable:  false,
	}
	
	// ErrNotFound represents a generic not found error
	ErrNotFound = &AppError{
		Type:       ErrorTypeNotFound,
		Code:       "NOT_FOUND",
		Message:    "Resource not found",
		StatusCode: 404,
		Retryable:  false,
	}
	
	// ErrConflict represents a generic conflict error
	ErrConflict = &AppError{
		Type:       ErrorTypeConflict,
		Code:       "CONFLICT",
		Message:    "Resource conflict",
		StatusCode: 409,
		Retryable:  false,
	}
	
	// ErrUnauthorized represents a generic unauthorized error
	ErrUnauthorized = &AppError{
		Type:       ErrorTypeUnauthorized,
		Code:       "UNAUTHORIZED",
		Message:    "Authentication required",
		StatusCode: 401,
		Retryable:  false,
	}
	
	// ErrForbidden represents a generic forbidden error
	ErrForbidden = &AppError{
		Type:       ErrorTypeForbidden,
		Code:       "FORBIDDEN",
		Message:    "Access denied",
		StatusCode: 403,
		Retryable:  false,
	}
	
	// ErrRateLimit represents a rate limit error
	ErrRateLimit = &AppError{
		Type:       ErrorTypeRateLimit,
		Code:       "RATE_LIMIT_EXCEEDED",
		Message:    "Rate limit exceeded",
		StatusCode: 429,
		Retryable:  true,
	}
	
	// ErrTimeout represents a timeout error
	ErrTimeout = &AppError{
		Type:       ErrorTypeTimeout,
		Code:       "TIMEOUT",
		Message:    "Request timeout",
		StatusCode: 504,
		Retryable:  true,
	}
	
	// ErrExternalService represents an external service error
	ErrExternalService = &AppError{
		Type:       ErrorTypeExternal,
		Code:       "EXTERNAL_SERVICE_ERROR",
		Message:    "External service error",
		StatusCode: 502,
		Retryable:  true,
	}
	
	// ErrInsufficientFunds represents insufficient funds error
	ErrInsufficientFunds = &AppError{
		Type:       ErrorTypeValidation,
		Code:       "INSUFFICIENT_FUNDS",
		Message:    "Insufficient funds",
		StatusCode: 400,
		Retryable:  false,
	}
	
	// ErrDuplicateEntry represents a duplicate entry error
	ErrDuplicateEntry = &AppError{
		Type:       ErrorTypeConflict,
		Code:       "DUPLICATE_ENTRY",
		Message:    "Duplicate entry",
		StatusCode: 409,
		Retryable:  false,
	}
	
	// ErrInvalidToken represents an invalid token error
	ErrInvalidToken = &AppError{
		Type:       ErrorTypeUnauthorized,
		Code:       "INVALID_TOKEN",
		Message:    "Invalid or expired token",
		StatusCode: 401,
		Retryable:  false,
	}
	
	// ErrKYCNotApproved represents KYC not approved error
	ErrKYCNotApproved = &AppError{
		Type:       ErrorTypeForbidden,
		Code:       "KYC_NOT_APPROVED",
		Message:    "KYC verification not approved",
		StatusCode: 403,
		Retryable:  false,
	}
)

// New creates a new AppError
func New(errType ErrorType, code, message string) *AppError {
	return &AppError{
		Type:    errType,
		Code:    code,
		Message: message,
	}
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Retryable
	}
	return false
}

// GetType returns the error type
func GetType(err error) ErrorType {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Type
	}
	return ErrorTypeInternal
}

// GetCode returns the error code
func GetCode(err error) string {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return "UNKNOWN_ERROR"
}

// GetStatusCode returns the HTTP status code for an error
func GetStatusCode(err error) int {
	var appErr *AppError
	if errors.As(err, &appErr) {
		if appErr.StatusCode != 0 {
			return appErr.StatusCode
		}
	}
	return 500
}
