package retry

import (
	"context"
	"fmt"
	"math"
	"time"
)

// RetryConfig holds configuration for retry behavior
type RetryConfig struct {
	MaxAttempts int           // Maximum number of retry attempts
	BaseDelay   time.Duration // Base delay between retries
	MaxDelay    time.Duration // Maximum delay between retries
	Multiplier  float64       // Backoff multiplier
}

// DefaultConfig returns a default retry configuration
func DefaultConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    30 * time.Second,
		Multiplier:  2.0,
	}
}

// RetryableFunc represents a function that can be retried
type RetryableFunc func() error

// IsRetryableFunc determines if an error should trigger a retry
type IsRetryableFunc func(error) bool

// WithExponentialBackoff retries a function with exponential backoff
func WithExponentialBackoff(
	ctx context.Context,
	config RetryConfig,
	fn RetryableFunc,
	isRetryable IsRetryableFunc,
) error {
	var lastErr error

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Execute the function
		err := fn()
		if err == nil {
			return nil // Success
		}

		lastErr = err

		// Check if we should retry
		if !isRetryable(err) {
			return fmt.Errorf("non-retryable error: %w", err)
		}

		// Don't wait after the last attempt
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate delay with exponential backoff
		delay := time.Duration(float64(config.BaseDelay) * math.Pow(config.Multiplier, float64(attempt)))
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}

		// Wait for the calculated delay or context cancellation
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled by context: %w", ctx.Err())
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return fmt.Errorf("max retry attempts (%d) exceeded: %w", config.MaxAttempts, lastErr)
}

// IsTemporaryError is a common retry predicate for temporary/transient errors
func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}

	// Add logic to identify temporary errors
	// This could be based on error types, messages, HTTP status codes, etc.
	errorStr := err.Error()

	// Common temporary error patterns
	temporaryPatterns := []string{
		"connection refused",
		"timeout",
		"temporary failure",
		"service unavailable",
		"internal server error",
		"too many requests",
		"rate limited",
		"network is unreachable",
		"no route to host",
		"connection reset",
	}

	for _, pattern := range temporaryPatterns {
		if contains(errorStr, pattern) {
			return true
		}
	}

	return false
}

// contains is a simple substring check helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			(len(s) > len(substr) &&
				(s[:len(substr)] == substr ||
					s[len(s)-len(substr):] == substr ||
					indexOf(s, substr) != -1)))
}

// indexOf returns the index of substr in s, or -1 if not found
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
