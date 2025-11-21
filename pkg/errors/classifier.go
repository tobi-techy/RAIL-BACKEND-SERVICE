package errors

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
)

// ClassifyError classifies an error for retry and circuit breaker logic
func ClassifyError(err error) ErrorType {
	if err == nil {
		return ""
	}

	// Check if it's already an AppError
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Type
	}

	// Context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTypeTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrorTypeInternal // Canceled is typically not retryable
	}

	// Database errors
	if errors.Is(err, sql.ErrNoRows) {
		return ErrorTypeNotFound
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) {
		return ErrorTypeInternal
	}

	// Network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ErrorTypeTimeout
		}
		if netErr.Temporary() {
			return ErrorTypeTransient
		}
	}

	// System call errors
	var syscallErr syscall.Errno
	if errors.As(err, &syscallErr) {
		switch syscallErr {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ECONNABORTED:
			return ErrorTypeTransient
		case syscall.ETIMEDOUT:
			return ErrorTypeTimeout
		}
	}

	// Check error message for common patterns
	errMsg := strings.ToLower(err.Error())
	
	// Timeout patterns
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded") {
		return ErrorTypeTimeout
	}

	// Connection patterns
	if strings.Contains(errMsg, "connection refused") || 
	   strings.Contains(errMsg, "connection reset") ||
	   strings.Contains(errMsg, "broken pipe") {
		return ErrorTypeTransient
	}

	// Rate limiting patterns
	if strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "too many requests") {
		return ErrorTypeRateLimit
	}

	// Validation patterns
	if strings.Contains(errMsg, "invalid") || 
	   strings.Contains(errMsg, "malformed") ||
	   strings.Contains(errMsg, "bad request") {
		return ErrorTypeValidation
	}

	// Duplicate/conflict patterns
	if strings.Contains(errMsg, "duplicate") || 
	   strings.Contains(errMsg, "already exists") ||
	   strings.Contains(errMsg, "conflict") {
		return ErrorTypeConflict
	}

	// Authorization patterns
	if strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "unauthenticated") {
		return ErrorTypeUnauthorized
	}
	if strings.Contains(errMsg, "forbidden") || strings.Contains(errMsg, "permission denied") {
		return ErrorTypeForbidden
	}

	// Not found patterns
	if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no such") {
		return ErrorTypeNotFound
	}

	// Default to internal error
	return ErrorTypeInternal
}

// ClassifyHTTPError classifies HTTP response errors
func ClassifyHTTPError(statusCode int) ErrorType {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return "" // Success
	case statusCode == http.StatusBadRequest:
		return ErrorTypeValidation
	case statusCode == http.StatusUnauthorized:
		return ErrorTypeUnauthorized
	case statusCode == http.StatusForbidden:
		return ErrorTypeForbidden
	case statusCode == http.StatusNotFound:
		return ErrorTypeNotFound
	case statusCode == http.StatusConflict:
		return ErrorTypeConflict
	case statusCode == http.StatusTooManyRequests:
		return ErrorTypeRateLimit
	case statusCode >= 400 && statusCode < 500:
		return ErrorTypeValidation // Client error
	case statusCode == http.StatusBadGateway || statusCode == http.StatusServiceUnavailable:
		return ErrorTypeTransient
	case statusCode == http.StatusGatewayTimeout:
		return ErrorTypeTimeout
	case statusCode >= 500:
		return ErrorTypeExternal // Server error from external service
	default:
		return ErrorTypeInternal
	}
}

// ShouldRetry determines if an error should be retried
func ShouldRetry(err error) bool {
	errType := ClassifyError(err)
	return IsTransient(errType)
}

// IsCircuitBreakerError determines if an error should trip the circuit breaker
func IsCircuitBreakerError(err error) bool {
	errType := ClassifyError(err)
	switch errType {
	case ErrorTypeTimeout, ErrorTypeTransient, ErrorTypeExternal:
		return true
	default:
		return false
	}
}

// GetRetryDelay returns the recommended retry delay for an error
func GetRetryDelay(err error, attempt int) int {
	errType := ClassifyError(err)
	
	// Base delays in seconds
	baseDelays := map[ErrorType]int{
		ErrorTypeTimeout:    5,
		ErrorTypeTransient:  2,
		ErrorTypeRateLimit:  30, // Longer delay for rate limits
		ErrorTypeExternal:   3,
	}

	baseDelay, ok := baseDelays[errType]
	if !ok {
		return 0 // Don't retry
	}

	// Exponential backoff: baseDelay * 2^(attempt-1)
	// Capped at 60 seconds
	delay := baseDelay * (1 << uint(attempt-1))
	if delay > 60 {
		delay = 60
	}

	return delay
}
