// Package errors provides standardized error types for the domain layer.
// These errors provide consistent error handling across all services
// and enable proper error categorization for HTTP responses.
package errors

import (
	"errors"
	"fmt"
)

// Standard error categories
var (
	// ErrNotFound indicates the requested resource was not found
	ErrNotFound = errors.New("resource not found")

	// ErrAlreadyExists indicates the resource already exists
	ErrAlreadyExists = errors.New("resource already exists")

	// ErrInvalidInput indicates invalid input was provided
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized indicates the request is not authorized
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the request is forbidden
	ErrForbidden = errors.New("forbidden")

	// ErrInternal indicates an internal server error
	ErrInternal = errors.New("internal error")

	// ErrConflict indicates a conflict with the current state
	ErrConflict = errors.New("conflict")

	// ErrRateLimit indicates rate limit exceeded
	ErrRateLimit = errors.New("rate limit exceeded")

	// ErrServiceUnavailable indicates the service is temporarily unavailable
	ErrServiceUnavailable = errors.New("service unavailable")
)

// DomainError represents a domain-specific error with additional context
type DomainError struct {
	Err       error
	Code      string
	Message   string
	Details   map[string]interface{}
	Retryable bool
}

// Error implements the error interface
func (e *DomainError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

// Unwrap returns the underlying error
func (e *DomainError) Unwrap() error {
	return e.Err
}

// Is checks if the error matches the target
func (e *DomainError) Is(target error) bool {
	if e.Err != nil {
		return errors.Is(e.Err, target)
	}
	return false
}

// NewDomainError creates a new domain error
func NewDomainError(err error, code, message string) *DomainError {
	return &DomainError{
		Err:     err,
		Code:    code,
		Message: message,
	}
}

// WithDetails adds details to the error
func (e *DomainError) WithDetails(details map[string]interface{}) *DomainError {
	e.Details = details
	return e
}

// WithRetryable marks the error as retryable
func (e *DomainError) WithRetryable(retryable bool) *DomainError {
	e.Retryable = retryable
	return e
}

// IsRetryable returns true if the error is retryable
func (e *DomainError) IsRetryable() bool {
	return e.Retryable
}

// NotFoundError creates a not found error
func NotFoundError(resource string) *DomainError {
	return &DomainError{
		Err:     ErrNotFound,
		Code:    fmt.Sprintf("%s_NOT_FOUND", resource),
		Message: fmt.Sprintf("%s not found", resource),
	}
}

// AlreadyExistsError creates an already exists error
func AlreadyExistsError(resource string) *DomainError {
	return &DomainError{
		Err:     ErrAlreadyExists,
		Code:    fmt.Sprintf("%s_ALREADY_EXISTS", resource),
		Message: fmt.Sprintf("%s already exists", resource),
	}
}

// ValidationError creates a validation error
func ValidationError(field, message string) *DomainError {
	return &DomainError{
		Err:     ErrInvalidInput,
		Code:    "VALIDATION_ERROR",
		Message: message,
		Details: map[string]interface{}{
			"field": field,
		},
	}
}

// UnauthorizedError creates an unauthorized error
func UnauthorizedError(message string) *DomainError {
	return &DomainError{
		Err:     ErrUnauthorized,
		Code:    "UNAUTHORIZED",
		Message: message,
	}
}

// ForbiddenError creates a forbidden error
func ForbiddenError(message string) *DomainError {
	return &DomainError{
		Err:     ErrForbidden,
		Code:    "FORBIDDEN",
		Message: message,
	}
}

// InternalError creates an internal error
func InternalError(message string, err error) *DomainError {
	return &DomainError{
		Err:     ErrInternal,
		Code:    "INTERNAL_ERROR",
		Message: message,
		Details: map[string]interface{}{
			"cause": err.Error(),
		},
	}
}

// ConflictError creates a conflict error
func ConflictError(resource, reason string) *DomainError {
	return &DomainError{
		Err:     ErrConflict,
		Code:    "CONFLICT",
		Message: fmt.Sprintf("conflict with %s: %s", resource, reason),
	}
}

// RateLimitError creates a rate limit error
func RateLimitError(limit int, window string) *DomainError {
	return &DomainError{
		Err:     ErrRateLimit,
		Code:    "RATE_LIMIT_EXCEEDED",
		Message: "rate limit exceeded",
		Details: map[string]interface{}{
			"limit":  limit,
			"window": window,
		},
	}
}

// ServiceUnavailableError creates a service unavailable error
func ServiceUnavailableError(service string, err error) *DomainError {
	de := &DomainError{
		Err:       ErrServiceUnavailable,
		Code:      "SERVICE_UNAVAILABLE",
		Message:   fmt.Sprintf("%s service is temporarily unavailable", service),
		Retryable: true,
	}
	if err != nil {
		de.Details = map[string]interface{}{
			"cause": err.Error(),
		}
	}
	return de
}

// Error helpers for common patterns

// IsNotFound checks if an error is a not found error
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsAlreadyExists checks if an error is an already exists error
func IsAlreadyExists(err error) bool {
	return errors.Is(err, ErrAlreadyExists)
}

// IsInvalidInput checks if an error is an invalid input error
func IsInvalidInput(err error) bool {
	return errors.Is(err, ErrInvalidInput)
}

// IsUnauthorized checks if an error is an unauthorized error
func IsUnauthorized(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}

// IsForbidden checks if an error is a forbidden error
func IsForbidden(err error) bool {
	return errors.Is(err, ErrForbidden)
}

// IsInternal checks if an error is an internal error
func IsInternal(err error) bool {
	return errors.Is(err, ErrInternal)
}

// IsConflict checks if an error is a conflict error
func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}

// IsRateLimit checks if an error is a rate limit error
func IsRateLimit(err error) bool {
	return errors.Is(err, ErrRateLimit)
}

// IsServiceUnavailable checks if an error is a service unavailable error
func IsServiceUnavailable(err error) bool {
	return errors.Is(err, ErrServiceUnavailable)
}

// GetErrorCode extracts the error code from a domain error
func GetErrorCode(err error) string {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return "UNKNOWN_ERROR"
}

// GetErrorDetails extracts details from a domain error
func GetErrorDetails(err error) map[string]interface{} {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Details
	}
	return nil
}

// Wrap wraps an error with additional context
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// WrapWithCode wraps an error with a code and message
func WrapWithCode(err error, code, message string) *DomainError {
	return &DomainError{
		Err:     err,
		Code:    code,
		Message: message,
	}
}
