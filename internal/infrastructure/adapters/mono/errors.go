package mono

import "fmt"

// ErrorResponse represents a Mono API error.
type ErrorResponse struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("Mono API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}

func (e *ErrorResponse) IsNotFound() bool      { return e.StatusCode == 404 }
func (e *ErrorResponse) IsUnauthorized() bool   { return e.StatusCode == 401 }
func (e *ErrorResponse) IsRateLimited() bool     { return e.StatusCode == 429 }
func (e *ErrorResponse) IsRetryable() bool       { return e.StatusCode >= 500 || e.StatusCode == 429 }
func (e *ErrorResponse) IsBadRequest() bool      { return e.StatusCode == 400 }
