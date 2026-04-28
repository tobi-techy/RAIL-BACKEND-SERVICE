package circle

import "fmt"

// ErrorResponse represents a Circle API error.
type ErrorResponse struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("Circle API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}

func (e *ErrorResponse) IsNotFound() bool         { return e.StatusCode == 404 }
func (e *ErrorResponse) IsUnauthorized() bool      { return e.StatusCode == 401 }
func (e *ErrorResponse) IsRateLimited() bool       { return e.StatusCode == 429 }
func (e *ErrorResponse) IsRetryable() bool         { return e.StatusCode >= 500 || e.StatusCode == 429 }
func (e *ErrorResponse) IsBadCiphertext() bool     { return e.Code == "invalid_entity_secret_ciphertext" }
func (e *ErrorResponse) IsInsufficientFunds() bool { return e.Code == "insufficient_funds" }
