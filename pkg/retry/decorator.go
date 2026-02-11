package retry

import (
	"context"
	"fmt"
	"time"

	apperrors "github.com/rail-service/rail_service/pkg/errors"
	"go.uber.org/zap"
)

// Retrier handles retry logic
type Retrier struct {
	policy  Policy
	backoff *Backoff
	logger  *zap.Logger
}

// NewRetrier creates a new retrier
func NewRetrier(policy Policy, logger *zap.Logger) (*Retrier, error) {
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid retry policy: %w", err)
	}

	return &Retrier{
		policy:  policy,
		backoff: NewBackoff(policy),
		logger:  logger,
	}, nil
}

// Do executes a function with retry logic
func (r *Retrier) Do(ctx context.Context, operation func() error) error {
	return r.DoWithData(ctx, func() (interface{}, error) {
		return nil, operation()
	})
}

// DoWithData executes a function that returns data with retry logic
func (r *Retrier) DoWithData(ctx context.Context, operation func() (interface{}, error)) error {
	_, err := r.DoWithResult(ctx, operation)
	return err
}

// DoWithResult executes a function with retry logic and returns the result
func (r *Retrier) DoWithResult(ctx context.Context, operation func() (interface{}, error)) (interface{}, error) {
	var lastErr error
	var result interface{}

	for attempt := 0; attempt <= r.policy.MaxRetries; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Execute operation
		result, lastErr = operation()

		// Success
		if lastErr == nil {
			if attempt > 0 {
				r.logger.Info("Operation succeeded after retries",
					zap.Int("attempt", attempt),
					zap.Int("max_retries", r.policy.MaxRetries))
			}
			return result, nil
		}

		// Check if error is retryable
		if !r.isRetryable(lastErr) {
			r.logger.Debug("Error is not retryable",
				zap.Error(lastErr),
				zap.Int("attempt", attempt))
			return nil, lastErr
		}

		// No more retries
		if attempt >= r.policy.MaxRetries {
			r.logger.Warn("Max retries exceeded",
				zap.Error(lastErr),
				zap.Int("attempts", attempt+1),
				zap.Int("max_retries", r.policy.MaxRetries))
			return nil, fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
		}

		// Calculate backoff
		backoffDuration := r.backoff.Calculate(attempt + 1)

		r.logger.Debug("Retrying operation",
			zap.Error(lastErr),
			zap.Int("attempt", attempt+1),
			zap.Int("max_retries", r.policy.MaxRetries),
			zap.Duration("backoff", backoffDuration))

		// Wait for backoff
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoffDuration):
			// Continue to next attempt
		}
	}

	return nil, fmt.Errorf("%w: %v", ErrMaxRetriesExceeded, lastErr)
}

// isRetryable checks if an error should be retried
func (r *Retrier) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Use custom retryable function if provided
	if r.policy.RetryableFunc != nil {
		return r.policy.RetryableFunc(err)
	}

	// Use default error classification
	return apperrors.ShouldRetry(err)
}

// Do is a package-level helper for one-off retries
func Do(ctx context.Context, policy Policy, logger *zap.Logger, operation func() error) error {
	retrier, err := NewRetrier(policy, logger)
	if err != nil {
		return err
	}
	return retrier.Do(ctx, operation)
}

// DoWithResult is a package-level helper for one-off retries with results
func DoWithResult(ctx context.Context, policy Policy, logger *zap.Logger, operation func() (interface{}, error)) (interface{}, error) {
	retrier, err := NewRetrier(policy, logger)
	if err != nil {
		return nil, err
	}
	return retrier.DoWithResult(ctx, operation)
}

// Decorator wraps a function with retry logic
type Decorator struct {
	retrier *Retrier
}

// NewDecorator creates a new retry decorator
func NewDecorator(policy Policy, logger *zap.Logger) (*Decorator, error) {
	retrier, err := NewRetrier(policy, logger)
	if err != nil {
		return nil, err
	}
	return &Decorator{
		retrier: retrier,
	}, nil
}

// Decorate wraps a function with retry logic
func (d *Decorator) Decorate(fn func() error) func(context.Context) error {
	return func(ctx context.Context) error {
		return d.retrier.Do(ctx, fn)
	}
}

// DecorateWithResult wraps a function that returns a result with retry logic
func (d *Decorator) DecorateWithResult(fn func() (interface{}, error)) func(context.Context) (interface{}, error) {
	return func(ctx context.Context) (interface{}, error) {
		return d.retrier.DoWithResult(ctx, fn)
	}
}
