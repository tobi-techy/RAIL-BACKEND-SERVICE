package paj

import (
	"errors"
	"fmt"
)

// APIError is returned when Paj responds with a non-success HTTP status.
type APIError struct {
	StatusCode int
	Body       string
	Path       string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("paj returned %d: %s", e.StatusCode, e.Body)
}

// IsUnauthorized reports whether err wraps a Paj 401 response.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 401
}
