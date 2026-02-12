// Package circuitbreaker provides a wrapper around sony/gobreaker for circuit breaker pattern
package circuitbreaker

import (
	"context"
	"time"

	"github.com/sony/gobreaker"
)

// State represents the circuit breaker state
type State gobreaker.State

// String returns the string representation of the state
func (s State) String() string {
	return gobreaker.State(s).String()
}

// State constants
const (
	StateClosed   State = State(gobreaker.StateClosed)
	StateHalfOpen State = State(gobreaker.StateHalfOpen)
	StateOpen     State = State(gobreaker.StateOpen)
)

// Config holds circuit breaker configuration
type Config struct {
	MaxRequests      uint32
	Interval         time.Duration
	Timeout          time.Duration
	FailureThreshold uint32
	SuccessThreshold uint32
	OnStateChange    func(from, to State)
}

// CircuitBreaker wraps gobreaker.CircuitBreaker
type CircuitBreaker struct {
	cb *gobreaker.CircuitBreaker
}

// New creates a new CircuitBreaker with the given config
func New(cfg Config) *CircuitBreaker {
	settings := gobreaker.Settings{
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.FailureThreshold
		},
	}
	if cfg.OnStateChange != nil {
		settings.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
			cfg.OnStateChange(State(from), State(to))
		}
	}
	return &CircuitBreaker{cb: gobreaker.NewCircuitBreaker(settings)}
}

// Execute runs the given function through the circuit breaker (context-aware, error-only)
func (c *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	_, err := c.cb.Execute(func() (interface{}, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, fn()
		}
	})
	return err
}

// Call runs the given function through the circuit breaker (error-only, no context)
func (c *CircuitBreaker) Call(fn func() error) error {
	_, err := c.cb.Execute(func() (interface{}, error) {
		return nil, fn()
	})
	return err
}

// State returns the current state of the circuit breaker
func (c *CircuitBreaker) State() State {
	return State(c.cb.State())
}
