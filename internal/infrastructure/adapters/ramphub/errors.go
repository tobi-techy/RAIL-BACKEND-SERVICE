package ramphub

import (
	"errors"
	"fmt"
	"strings"
)

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
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
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
