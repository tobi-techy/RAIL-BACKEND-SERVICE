package grid

import "fmt"

// ErrorResponse represents a Grid API error response
type ErrorResponse struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// Error implements the error interface
func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("Grid API error [%d]: %s (code: %s)", e.StatusCode, e.Message, e.Code)
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

// IsOTPExpired returns true if the OTP has expired
func (e *ErrorResponse) IsOTPExpired() bool {
	return e.Code == "OTP_EXPIRED"
}

// IsInvalidOTP returns true if the OTP is invalid
func (e *ErrorResponse) IsInvalidOTP() bool {
	return e.Code == "INVALID_OTP"
}
