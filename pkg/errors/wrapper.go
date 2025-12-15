package errors

import (
	"fmt"
)

// Wrap wraps an error with additional context
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// Wrapf wraps an error with formatted context
func Wrapf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

// WrapWithType wraps an error with a specific error type
func WrapWithType(err error, errType ErrorType, code, message string) *AppError {
	return &AppError{
		Type:      errType,
		Code:      code,
		Message:   message,
		Err:       err,
		Retryable: IsTransient(errType),
	}
}

// WrapInternal wraps an internal error
func WrapInternal(err error, message string) *AppError {
	return WrapWithType(err, ErrorTypeInternal, "INTERNAL_ERROR", message)
}

// WrapValidation wraps a validation error
func WrapValidation(err error, message string) *AppError {
	return WrapWithType(err, ErrorTypeValidation, "VALIDATION_ERROR", message)
}

// WrapNotFound wraps a not found error
func WrapNotFound(err error, message string) *AppError {
	return WrapWithType(err, ErrorTypeNotFound, "NOT_FOUND", message)
}

// WrapExternal wraps an external service error
func WrapExternal(err error, service, message string) *AppError {
	appErr := WrapWithType(err, ErrorTypeExternal, "EXTERNAL_SERVICE_ERROR", message)
	appErr.WithDetail("service", service)
	return appErr
}

// WrapTimeout wraps a timeout error
func WrapTimeout(err error, operation string) *AppError {
	appErr := WrapWithType(err, ErrorTypeTimeout, "TIMEOUT", "Operation timeout")
	appErr.WithDetail("operation", operation)
	appErr.Retryable = true
	return appErr
}

// IsTransient determines if an error type is transient
func IsTransient(errType ErrorType) bool {
	switch errType {
	case ErrorTypeTransient, ErrorTypeTimeout, ErrorTypeRateLimit:
		return true
	case ErrorTypeExternal:
		return true // External errors are often transient
	default:
		return false
	}
}

// NewValidationError creates a new validation error
func NewValidationError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeValidation,
		Code:       "VALIDATION_ERROR",
		Message:    message,
		StatusCode: 400,
		Retryable:  false,
	}
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(resource string) *AppError {
	return &AppError{
		Type:       ErrorTypeNotFound,
		Code:       "NOT_FOUND",
		Message:    fmt.Sprintf("%s not found", resource),
		StatusCode: 404,
		Retryable:  false,
	}
}

// NewConflictError creates a new conflict error
func NewConflictError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeConflict,
		Code:       "CONFLICT",
		Message:    message,
		StatusCode: 409,
		Retryable:  false,
	}
}

// NewInternalError creates a new internal error
func NewInternalError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeInternal,
		Code:       "INTERNAL_ERROR",
		Message:    message,
		StatusCode: 500,
		Retryable:  false,
	}
}

// NewUnauthorizedError creates a new unauthorized error
func NewUnauthorizedError(message string) *AppError {
	return &AppError{
		Type:       ErrorTypeUnauthorized,
		Code:       "UNAUTHORIZED",
		Message:    message,
		StatusCode: 401,
		Retryable:  false,
	}
}

// NewForbiddenError creates a new forbidden error
func NewForbiddenError(message string) *AppError{
	return &AppError{
		Type:       ErrorTypeForbidden,
		Code:       "FORBIDDEN",
		Message:    message,
		StatusCode: 403,
		Retryable:  false,
	}
}
