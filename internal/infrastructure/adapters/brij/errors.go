package brij

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ErrorDetail is the BRIJ error envelope. Code is a machine-readable string
// (e.g. intent_already_exists, booking_disabled), Message is human-readable,
// and Detail may carry extra fields like detail.intent_id or detail.expired_at.
type ErrorDetail struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

// Error is a non-2xx BRIJ response. It satisfies error and carries enough
// structure for the caller to branch on status codes and machine codes.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Body       string
	Detail     json.RawMessage
}

// Error implements error.
func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("brij %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("brij %d: %s", e.StatusCode, e.Message)
}

// IsNotFound reports whether the error is a 404 (intent or order not found).
func (e *Error) IsNotFound() bool { return e.StatusCode == http.StatusNotFound }

// IsConflict reports whether the error is a 409 (state machine rejection).
func (e *Error) IsConflict() bool { return e.StatusCode == http.StatusConflict }

// IsRetryable reports whether the request may succeed on a retry (5xx + 429).
func (e *Error) IsRetryable() bool {
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// DetailOf extracts a typed field from the error detail blob, e.g.
//
//	var d struct { IntentID string `json:"intent_id"` }
//	err.DetailOf(&d)
func (e *Error) DetailOf(v interface{}) bool {
	if len(e.Detail) == 0 {
		return false
	}
	return json.Unmarshal(e.Detail, v) == nil
}

// PaymentVerificationError is returned when the x402 payment was rejected by
// the BRIJ/sponsor side after the client submitted a signature. Callers should
// surface this to the user and avoid charging them again.
type PaymentVerificationError struct {
	Code    string
	Message string
}

func (e *PaymentVerificationError) Error() string {
	return fmt.Sprintf("brij payment verification failed: %s", e.Message)
}

// errPayload is the raw envelope we decode from non-2xx bodies.
type errPayload struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

// apiErrorFrom builds a *Error from a non-2xx response body.
func apiErrorFrom(statusCode int, body []byte) *Error {
	e := &Error{StatusCode: statusCode, Body: string(body)}
	var p errPayload
	if err := json.Unmarshal(body, &p); err == nil {
		e.Code = p.Code
		e.Message = p.Message
		e.Detail = p.Detail
	}
	if e.Message == "" {
		e.Message = "request failed"
	}
	return e
}
