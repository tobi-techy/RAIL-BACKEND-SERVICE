package ramphub

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrAccountResolveFailed is returned when RampHub cannot resolve a bank
// account to an account holder name (wrong number, unsupported bank code, or
// provider directory miss).
var ErrAccountResolveFailed = errors.New("ramphub: bank account could not be resolved")

// APIError is returned when RampHub responds with a non-success HTTP status.
type APIError struct {
	StatusCode int
	Body       string
	Path       string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("ramphub returned %d: %s", e.StatusCode, e.Body)
}

// IsUnauthorized reports whether err wraps a RampHub 401/403 response.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr != nil && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
}

// IsActiveIntentConflict reports whether err is a RampHub active payment-window
// conflict (PAYCHAIN_ACTIVE_INTENT_CONFLICT). Callers can retry the order with
// OverrideActiveIntent=true to take over the existing window.
func IsActiveIntentConflict(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(apiErr.Body, "PAYCHAIN_ACTIVE_INTENT_CONFLICT")
}

// extractErrorCode pulls the machine error code out of a RampHub error body so
// it can be logged without exposing the full payload (which may carry PII such
// as account numbers). Checks the common shapes: {"code": "..."},
// {"error": "..."} and {"error": {"code": "..."}}.
func extractErrorCode(body []byte) string {
	var envelope struct {
		Code  string          `json:"code"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if envelope.Code != "" {
		return envelope.Code
	}
	if len(envelope.Error) > 0 {
		var errStr string
		if json.Unmarshal(envelope.Error, &errStr) == nil {
			return truncateCode(errStr)
		}
		var errObj struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(envelope.Error, &errObj) == nil {
			return errObj.Code
		}
	}
	return ""
}

// truncateCode keeps error strings log-safe: short and single-line.
func truncateCode(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
