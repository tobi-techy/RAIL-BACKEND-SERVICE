package glider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrProviderUnavailable means the provider could not be reached at all. Callers
// must treat a financial action as unconfirmed, never as failed-and-retryable
// without an idempotency anchor.
var ErrProviderUnavailable = errors.New("glider: provider unavailable")

// APIError is a structured error returned by the Glider V2 API envelope.
type APIError struct {
	StatusCode    int
	Code          string
	Message       string
	Details       []string
	RetryAfter    time.Duration
	CorrelationID string
}

func (e *APIError) Error() string {
	if len(e.Details) > 0 {
		if e.Code != "" {
			return fmt.Sprintf("glider: %d %s: %s (%s)", e.StatusCode, e.Code, e.Message, strings.Join(e.Details, "; "))
		}
		return fmt.Sprintf("glider: %d: %s (%s)", e.StatusCode, e.Message, strings.Join(e.Details, "; "))
	}
	if e.Code != "" {
		return fmt.Sprintf("glider: %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("glider: %d: %s", e.StatusCode, e.Message)
}

// IsUnauthorized reports a missing or invalid API key (401).
func (e *APIError) IsUnauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

// IsForbidden reports a missing scope (403).
func (e *APIError) IsForbidden() bool { return e.StatusCode == http.StatusForbidden }

// IsNotFound reports a missing or cross-tenant resource (404).
func (e *APIError) IsNotFound() bool { return e.StatusCode == http.StatusNotFound }

// IsConflict reports an in-flight duplicate or an idempotency mismatch (409).
// A conflict is never a reason to retry with a different body.
func (e *APIError) IsConflict() bool { return e.StatusCode == http.StatusConflict }

// IsCooldown reports a rebalance triggered too soon (429). The caller must wait
// for RetryAfter.
func (e *APIError) IsCooldown() bool { return e.StatusCode == http.StatusTooManyRequests }

// IsValidation reports a rejected request body or allocation (400).
func (e *APIError) IsValidation() bool { return e.StatusCode == http.StatusBadRequest }

// IsRetryable reports whether the same request (same idempotency anchor) may be
// safely retried later.
func (e *APIError) IsRetryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// AsAPIError extracts a structured provider error.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// IsRetryableError reports whether any error is a retryable provider error.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrProviderUnavailable) {
		return true
	}
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.IsRetryable()
	}
	return false
}
