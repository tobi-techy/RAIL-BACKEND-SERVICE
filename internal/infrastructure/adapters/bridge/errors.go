package bridge

import (
	"encoding/json"
	"fmt"
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
