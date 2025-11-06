package errors

import (
	"fmt"
	"net/http"
)

// ErrorCode represents standardized error codes
type ErrorCode string

const (
	// Authentication errors
	ErrCodeUnauthorized     ErrorCode = "UNAUTHORIZED"
	ErrCodeForbidden        ErrorCode = "FORBIDDEN"
	ErrCodeInvalidToken     ErrorCode = "INVALID_TOKEN"
	
	// Validation errors
	ErrCodeValidation       ErrorCode = "VALIDATION_ERROR"
	ErrCodeInvalidInput     ErrorCode = "INVALID_INPUT"
	ErrCodeMissingField     ErrorCode = "MISSING_FIELD"
	
	// Business logic errors
	ErrCodeInsufficientFunds ErrorCode = "INSUFFICIENT_FUNDS"
	ErrCodeWalletNotFound    ErrorCode = "WALLET_NOT_FOUND"
	ErrCodeUserNotFound      ErrorCode = "USER_NOT_FOUND"
	ErrCodeDuplicateEntry    ErrorCode = "DUPLICATE_ENTRY"
	
	// System errors
	ErrCodeInternal         ErrorCode = "INTERNAL_ERROR"
	ErrCodeServiceUnavailable ErrorCode = "SERVICE_UNAVAILABLE"
	ErrCodeTimeout          ErrorCode = "TIMEOUT"
	ErrCodeRateLimit        ErrorCode = "RATE_LIMIT_EXCEEDED"
)

// StackError represents a standardized error
type StackError struct {
	Code       ErrorCode              `json:"code"`
	Message    string                 `json:"message"`
	Details    map[string]interface{} `json:"details,omitempty"`
	StatusCode int                    `json:"-"`
}

func (e StackError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// New creates a new StackError
func New(code ErrorCode, message string) *StackError {
	return &StackError{
		Code:       code,
		Message:    message,
		StatusCode: getHTTPStatusCode(code),
		Details:    make(map[string]interface{}),
	}
}

// NewWithDetails creates a new StackError with details
func NewWithDetails(code ErrorCode, message string, details map[string]interface{}) *StackError {
	return &StackError{
		Code:       code,
		Message:    message,
		StatusCode: getHTTPStatusCode(code),
		Details:    details,
	}
}

// Wrap wraps an existing error with StackError
func Wrap(err error, code ErrorCode, message string) *StackError {
	details := map[string]interface{}{
		"original_error": err.Error(),
	}
	return NewWithDetails(code, message, details)
}

// AddDetail adds a detail to the error
func (e *StackError) AddDetail(key string, value interface{}) *StackError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// getHTTPStatusCode maps error codes to HTTP status codes
func getHTTPStatusCode(code ErrorCode) int {
	switch code {
	case ErrCodeUnauthorized, ErrCodeInvalidToken:
		return http.StatusUnauthorized
	case ErrCodeForbidden:
		return http.StatusForbidden
	case ErrCodeValidation, ErrCodeInvalidInput, ErrCodeMissingField:
		return http.StatusBadRequest
	case ErrCodeUserNotFound, ErrCodeWalletNotFound:
		return http.StatusNotFound
	case ErrCodeDuplicateEntry:
		return http.StatusConflict
	case ErrCodeRateLimit:
		return http.StatusTooManyRequests
	case ErrCodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case ErrCodeTimeout:
		return http.StatusRequestTimeout
	case ErrCodeInsufficientFunds:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// Common error constructors
func Unauthorized(message string) *StackError {
	return New(ErrCodeUnauthorized, message)
}

func Forbidden(message string) *StackError {
	return New(ErrCodeForbidden, message)
}

func ValidationError(message string) *StackError {
	return New(ErrCodeValidation, message)
}

func NotFound(resource string) *StackError {
	return New(ErrCodeUserNotFound, fmt.Sprintf("%s not found", resource))
}

func InsufficientFunds(message string) *StackError {
	return New(ErrCodeInsufficientFunds, message)
}

func Internal(message string) *StackError {
	return New(ErrCodeInternal, message)
}

func ServiceUnavailable(service string) *StackError {
	return New(ErrCodeServiceUnavailable, fmt.Sprintf("%s service unavailable", service))
}
