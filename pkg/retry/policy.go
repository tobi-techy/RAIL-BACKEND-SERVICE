package retry

import (
	"errors"
	"time"
)

var (
	// ErrMaxRetriesExceeded is returned when max retries are exceeded
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")
	
	// ErrInvalidMaxRetries is returned when max retries is negative
	ErrInvalidMaxRetries = errors.New("max retries must be non-negative")
	
	// ErrInvalidInitialBackoff is returned when initial backoff is negative
	ErrInvalidInitialBackoff = errors.New("initial backoff must be non-negative")
	
	// ErrInvalidMaxBackoff is returned when max backoff is less than initial
	ErrInvalidMaxBackoff = errors.New("max backoff must be greater than initial backoff")
	
	// ErrInvalidMultiplier is returned when multiplier is less than 1
	ErrInvalidMultiplier = errors.New("multiplier must be at least 1.0")
	
	// ErrInvalidJitter is returned when jitter is out of range
	ErrInvalidJitter = errors.New("jitter must be between 0 and 1")
)

// Policy defines retry behavior
type Policy struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	Jitter         float64
	RetryableFunc  func(error) bool
}

// Predefined retry policies for common scenarios
var (
	// PolicyDefault is the default retry policy
	PolicyDefault = Policy{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.1,
		RetryableFunc:  nil, // Will use default error classification
	}
	
	// PolicyDatabaseTransient is for transient database errors
	PolicyDatabaseTransient = Policy{
		MaxRetries:     3,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.1,
		RetryableFunc:  nil,
	}
	
	// PolicyExternalAPI is for external API calls
	PolicyExternalAPI = Policy{
		MaxRetries:     5,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     60 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.2,
		RetryableFunc:  nil,
	}
	
	// PolicyRateLimit is for rate limit errors (longer backoff)
	PolicyRateLimit = Policy{
		MaxRetries:     3,
		InitialBackoff: 30 * time.Second,
		MaxBackoff:     5 * time.Minute,
		Multiplier:     2.0,
		Jitter:         0.1,
		RetryableFunc:  nil,
	}
	
	// PolicyTimeout is for timeout errors
	PolicyTimeout = Policy{
		MaxRetries:     2,
		InitialBackoff: 5 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
		Jitter:         0.15,
		RetryableFunc:  nil,
	}
	
	// PolicyQuick is for fast retries with minimal backoff
	PolicyQuick = Policy{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		Multiplier:     1.5,
		Jitter:         0.1,
		RetryableFunc:  nil,
	}
	
	// PolicyAggressive is for critical operations (more retries)
	PolicyAggressive = Policy{
		MaxRetries:     10,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     60 * time.Second,
		Multiplier:     1.5,
		Jitter:         0.1,
		RetryableFunc:  nil,
	}
	
	// PolicyNoRetry disables retries
	PolicyNoRetry = Policy{
		MaxRetries:     0,
		InitialBackoff: 0,
		MaxBackoff:     0,
		Multiplier:     0,
		Jitter:         0,
		RetryableFunc:  nil,
	}
)

// WithMaxRetries creates a new policy with custom max retries
func (p Policy) WithMaxRetries(maxRetries int) Policy {
	p.MaxRetries = maxRetries
	return p
}

// WithInitialBackoff creates a new policy with custom initial backoff
func (p Policy) WithInitialBackoff(duration time.Duration) Policy {
	p.InitialBackoff = duration
	return p
}

// WithMaxBackoff creates a new policy with custom max backoff
func (p Policy) WithMaxBackoff(duration time.Duration) Policy {
	p.MaxBackoff = duration
	return p
}

// WithMultiplier creates a new policy with custom multiplier
func (p Policy) WithMultiplier(multiplier float64) Policy {
	p.Multiplier = multiplier
	return p
}

// WithJitter creates a new policy with custom jitter
func (p Policy) WithJitter(jitter float64) Policy {
	p.Jitter = jitter
	return p
}

// WithRetryableFunc creates a new policy with custom retryable function
func (p Policy) WithRetryableFunc(fn func(error) bool) Policy {
	p.RetryableFunc = fn
	return p
}

// NewPolicy creates a custom retry policy
func NewPolicy(maxRetries int, initialBackoff, maxBackoff time.Duration) Policy {
	return Policy{
		MaxRetries:     maxRetries,
		InitialBackoff: initialBackoff,
		MaxBackoff:     maxBackoff,
		Multiplier:     2.0,
		Jitter:         0.1,
		RetryableFunc:  nil,
	}
}

// Validate checks if the policy is valid
func (p Policy) Validate() error {
	if p.MaxRetries < 0 {
		return ErrInvalidMaxRetries
	}
	if p.InitialBackoff < 0 {
		return ErrInvalidInitialBackoff
	}
	if p.MaxBackoff < p.InitialBackoff && p.MaxBackoff != 0 {
		return ErrInvalidMaxBackoff
	}
	if p.Multiplier < 1.0 {
		return ErrInvalidMultiplier
	}
	if p.Jitter < 0 || p.Jitter > 1.0 {
		return ErrInvalidJitter
	}
	return nil
}
