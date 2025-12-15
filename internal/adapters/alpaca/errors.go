package alpaca

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// AlpacaError represents different types of Alpaca API errors
type AlpacaError interface {
	error
	IsRetryable() bool
	RetryAfter() time.Duration
}

// RateLimitError represents a rate limit error (429)
type RateLimitError struct {
	Message         string
	RetryAfterDuration time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded: %s (retry after %v)", e.Message, e.RetryAfterDuration)
}

func (e *RateLimitError) IsRetryable() bool { return true }
func (e *RateLimitError) RetryAfter() time.Duration { return e.RetryAfterDuration }

// ValidationError represents validation errors (422)
type ValidationError struct {
	Message string
	Details []ValidationDetail
}

type ValidationDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string {
	if len(e.Details) > 0 {
		var details []string
		for _, d := range e.Details {
			details = append(details, fmt.Sprintf("%s: %s", d.Field, d.Message))
		}
		return fmt.Sprintf("validation error: %s", strings.Join(details, ", "))
	}
	return fmt.Sprintf("validation error: %s", e.Message)
}

func (e *ValidationError) IsRetryable() bool { return false }
func (e *ValidationError) RetryAfter() time.Duration { return 0 }

// ServerError represents server errors (5xx)
type ServerError struct {
	Message    string
	StatusCode int
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error (%d): %s", e.StatusCode, e.Message)
}

func (e *ServerError) IsRetryable() bool { return true }
func (e *ServerError) RetryAfter() time.Duration { return 5 * time.Second }

// ClientError represents client errors (4xx, except 422 and 429)
type ClientError struct {
	Message    string
	StatusCode int
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("client error (%d): %s", e.StatusCode, e.Message)
}

func (e *ClientError) IsRetryable() bool { return false }
func (e *ClientError) RetryAfter() time.Duration { return 0 }

// parseAlpacaError creates appropriate error types from HTTP response
func parseAlpacaError(statusCode int, body []byte) AlpacaError {
	var errorResp struct {
		Message string             `json:"message"`
		Details []ValidationDetail `json:"details"`
	}

	// Try to parse JSON error response
	json.Unmarshal(body, &errorResp)
	message := errorResp.Message
	if message == "" {
		message = string(body)
	}

	switch statusCode {
	case http.StatusTooManyRequests:
		retryAfter := extractRetryAfter(body)
		return &RateLimitError{
			Message:            message,
			RetryAfterDuration: retryAfter,
		}

	case http.StatusUnprocessableEntity:
		return &ValidationError{
			Message: message,
			Details: errorResp.Details,
		}

	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return &ServerError{
			Message:    message,
			StatusCode: statusCode,
		}

	default:
		if statusCode >= 400 && statusCode < 500 {
			return &ClientError{
				Message:    message,
				StatusCode: statusCode,
			}
		}
		return &ServerError{
			Message:    message,
			StatusCode: statusCode,
		}
	}
}

// extractRetryAfter extracts retry-after duration from response
func extractRetryAfter(body []byte) time.Duration {
	// Try to extract from Retry-After header or response body
	var resp struct {
		RetryAfter string `json:"retry_after"`
	}

	if err := json.Unmarshal(body, &resp); err == nil && resp.RetryAfter != "" {
		if seconds, err := strconv.Atoi(resp.RetryAfter); err == nil {
			return time.Duration(seconds) * time.Second
		}
	}

	return 60 * time.Second // Default retry after 1 minute
}
