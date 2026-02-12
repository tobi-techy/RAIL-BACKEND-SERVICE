package cctp

import "fmt"

// ErrorResponse represents a CCTP API error response
type ErrorResponse struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("CCTP API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
}

func (e *ErrorResponse) IsNotFound() bool {
	return e.StatusCode == 404
}

func (e *ErrorResponse) IsRateLimited() bool {
	return e.StatusCode == 429
}

// ErrAttestationPending indicates the attestation is not yet complete
var ErrAttestationPending = fmt.Errorf("attestation pending")

// ErrNoMessages indicates no messages found for the transaction
var ErrNoMessages = fmt.Errorf("no messages found for transaction")
