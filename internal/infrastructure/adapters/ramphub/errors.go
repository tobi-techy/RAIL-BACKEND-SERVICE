package ramphub

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// digitRun matches runs of 6+ digits (e.g. bank account numbers) so they can be
// masked out of anything we log.
var digitRun = regexp.MustCompile(`\d{6,}`)

// extractErrorMessage pulls RampHub's human-readable `message` (the validation
// reason — e.g. "Name can only contain alphanumeric characters …" or "property
// name should not exist") so 4xx failures are debuggable, while masking long
// digit runs like account numbers to keep logs PII-safe. Handles both the
// string and []string ("message": ["…"]) shapes RampHub returns.
func extractErrorMessage(body []byte) string {
	var envelope struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Message) == 0 {
		return ""
	}
	var msg string
	if json.Unmarshal(envelope.Message, &msg) != nil {
		var many []string
		if json.Unmarshal(envelope.Message, &many) != nil {
			return ""
		}
		msg = strings.Join(many, "; ")
	}
	return truncateCode(digitRun.ReplaceAllString(msg, "***"))
}

// DetailItem represents one field-level validation error from RampHub.
type DetailItem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// extractErrorDetails returns a comma-separated summary of field-level
// validation errors from the RampHub error body. The body shape is:
//
//	{"code":"Unprocessable Entity","message":"Validation failed","details":[{"field":"fiatAmount","message":"must be a positive number"}]}
//
// Returns "" when there are no details or the shape doesn't match.
func extractErrorDetails(body []byte) string {
	var envelope struct {
		Details []DetailItem `json:"details"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Details) == 0 {
		return ""
	}
	var parts []string
	for _, d := range envelope.Details {
		field := strings.TrimSpace(d.Field)
		msg := strings.TrimSpace(d.Message)
		if field == "" && msg == "" {
			continue
		}
		parts = append(parts, field+":"+truncateCode(digitRun.ReplaceAllString(msg, "***")))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
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
