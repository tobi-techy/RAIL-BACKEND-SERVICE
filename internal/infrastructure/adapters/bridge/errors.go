package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrorResponse represents a Bridge API error response
type ErrorResponse struct {
	StatusCode int                    `json:"status_code"`
	Code       string                 `json:"code"`
	Message    string                 `json:"message"`
	Details    map[string]interface{} `json:"details,omitempty"`
	Source     *ErrorSource           `json:"source,omitempty"`
}

// ErrorSource contains the location and key of the error
type ErrorSource struct {
	Location string                 `json:"location,omitempty"`
	Key      map[string]interface{} `json:"key,omitempty"`
}

// Error implements the error interface
func (e *ErrorResponse) Error() string {
	if e.Source != nil && len(e.Source.Key) > 0 {
		keyJSON, _ := json.Marshal(e.Source.Key)
		return fmt.Sprintf("Bridge API error [%d]: %s (code: %s, source: %s)", e.StatusCode, e.Message, e.Code, string(keyJSON))
	}
	if len(e.Details) > 0 {
		return fmt.Sprintf("Bridge API error [%d]: %s (code: %s, details: %v)", e.StatusCode, e.Message, e.Code, e.Details)
	}
	return fmt.Sprintf("Bridge API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}

// IsNotFound returns true if the error is a 404 not found error
func (e *ErrorResponse) IsNotFound() bool {
	return e.StatusCode == 404
}

// IsUnauthorized returns true if the error is a 401 unauthorized error
func (e *ErrorResponse) IsUnauthorized() bool {
	return e.StatusCode == 401
}

// IsRateLimited returns true if the error is a 429 rate limit error
func (e *ErrorResponse) IsRateLimited() bool {
	return e.StatusCode == 429
}

// IsConflict returns true if the error is a 409 conflict error (e.g., customer already exists)
func (e *ErrorResponse) IsConflict() bool {
	return e.StatusCode == 409
}

// IsCustomerAlreadyExists returns true if the error indicates a customer already exists
func (e *ErrorResponse) IsCustomerAlreadyExists() bool {
	msg := strings.ToLower(e.Message)
	code := strings.ToLower(e.Code)
	errStr := strings.ToLower(e.Error())
	if e.IsConflict() ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(errStr, "already exists") {
		return true
	}
	return strings.Contains(code, "already_exists") ||
		strings.Contains(code, "duplicate")
}

// IsTerminalError reports whether err is a Bridge client error that will not
// succeed on retry with the same request: 4xx validation, not-found (e.g. a
// deleted customer), conflict, or unprocessable responses. Callers running in
// retry loops must treat terminal errors as give-up signals instead of
// re-issuing the request on a schedule.
// Rate limiting (429) and request timeouts (408) are NOT terminal — the same
// request may succeed later.
func IsTerminalError(err error) bool {
	var errResp *ErrorResponse
	if !errors.As(err, &errResp) {
		return false
	}
	switch errResp.StatusCode {
	case 408, 425, 429:
		return false
	}
	return errResp.StatusCode >= 400 && errResp.StatusCode < 500
}

// GetErrorType returns a standardized error type string for the error
func (e *ErrorResponse) GetErrorType() string {
	switch {
	case e.IsNotFound():
		return "not_found"
	case e.IsUnauthorized():
		return "unauthorized"
	case e.IsRateLimited():
		return "rate_limited"
	case e.IsCustomerAlreadyExists():
		return "already_exists"
	case e.StatusCode >= 500:
		return "server_error"
	case e.StatusCode >= 400:
		return "client_error"
	default:
		return "unknown"
	}
}
