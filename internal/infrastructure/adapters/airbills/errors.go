package airbills

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// APIError is returned when Airbills responds with a non-2xx HTTP status.
type APIError struct {
	StatusCode int
	Body       string
	Path       string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("airbills returned %d: %s", e.StatusCode, e.Body)
}

// IsUnauthorized reports whether err wraps an Airbills 401/403 response
// (missing or invalid secret key).
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr != nil && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
}

// StatusError is returned when Airbills responds 2xx but with a non-success
// business status code in the body (anything other than "00"/"06").
type StatusError struct {
	Status  string
	Message string
}

func (e *StatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("airbills status %s: %s", e.Status, e.Message)
}

// digitRun matches runs of 6+ digits (phone/meter/card numbers) so they can be
// masked out of anything we log.
var digitRun = regexp.MustCompile(`\d{6,}`)

// maskDigits replaces long digit runs with *** to keep logs PII-safe.
func maskDigits(s string) string {
	return digitRun.ReplaceAllString(s, "***")
}

// extractErrorMessage pulls Airbills' human-readable `message` from an error
// body, masking long digit runs. Handles both the string and []string shapes.
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
	return truncate(maskDigits(msg))
}

// truncate keeps error strings log-safe: short and single-line.
func truncate(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
