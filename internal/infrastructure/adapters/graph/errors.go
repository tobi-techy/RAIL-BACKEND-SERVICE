package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNotConfigured is returned by the service layer when Graph is not enabled.
var ErrNotConfigured = errors.New("graph: not configured")

// APIError is returned when Graph responds with a non-success HTTP status.
type APIError struct {
	StatusCode int
	Body       string
	Path       string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	masked := truncate(maskPII(e.Body))
	return fmt.Sprintf("graph returned %d: %s", e.StatusCode, masked)
}

// IsUnauthorized reports whether err wraps a Graph 401/403 response.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr != nil && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
}

// IsConflict reports whether err wraps a Graph 409 (already exists) response.
func IsConflict(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr != nil && apiErr.StatusCode == 409
}

// digitRun matches runs of 6+ digits (e.g. BVN, account numbers) so they can be
// masked out of anything we log.
var digitRun = regexp.MustCompile(`\d{6,}`)

// emailRun matches email addresses in JSON bodies.
var emailRun = regexp.MustCompile(`"email"\s*:\s*"[^"]*"`)

// nameRun matches name fields in JSON bodies.
var nameRun = regexp.MustCompile(`"(name_first|name_last|name_other|phone)"\s*:\s*"[^"]*"`)

// urlRun matches document URLs that may contain tokens or presigned paths.
var urlRun = regexp.MustCompile(`"(url|document_url|id_document_url)"\s*:\s*"[^"]*"`)

// maskPII applies all PII masking patterns to a string for safe logging.
func maskPII(s string) string {
	s = digitRun.ReplaceAllString(s, "***")
	s = emailRun.ReplaceAllString(s, `"email":"***"`)
	s = nameRun.ReplaceAllString(s, `"${1}":"***"`)
	s = urlRun.ReplaceAllString(s, `"${1}":"***"`)
	return s
}

// extractErrorMessage pulls Graph's human-readable `message` so 4xx failures are
// debuggable, while masking long digit runs (BVN/account numbers) to keep logs
// PII-safe. Handles both the string and []string message shapes.
func extractErrorMessage(body []byte) string {
	var env struct {
		Message json.RawMessage `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	raw := env.Message
	if len(raw) == 0 {
		raw = env.Error
	}
	if len(raw) == 0 {
		return ""
	}
	var msg string
	if json.Unmarshal(raw, &msg) != nil {
		var many []string
		if json.Unmarshal(raw, &many) != nil {
			return ""
		}
		msg = strings.Join(many, "; ")
	}
	return truncate(maskPII(msg))
}

// truncate keeps error strings log-safe: short and single-line.
func truncate(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
