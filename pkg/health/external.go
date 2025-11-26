package health

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ExternalAPIChecker checks external API connectivity
type ExternalAPIChecker struct {
	name       string
	healthURL  string
	httpClient *http.Client
	timeout    time.Duration
}

// NewExternalAPIChecker creates a new external API health checker
func NewExternalAPIChecker(name, healthURL string, timeout time.Duration) *ExternalAPIChecker {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	
	return &ExternalAPIChecker{
		name:      name,
		healthURL: healthURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Check performs the external API health check
func (c *ExternalAPIChecker) Check(ctx context.Context) CheckResult {
	start := time.Now()
	
	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	
	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthURL, nil)
	if err != nil {
		return NewUnhealthyResult(c.name, err).WithDuration(time.Since(start))
	}
	
	// Make request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return NewUnhealthyResult(c.name, err).WithDuration(time.Since(start))
	}
	defer resp.Body.Close()
	
	duration := time.Since(start)
	
	// Check status code
	result := CheckResult{
		Component: c.name,
		Timestamp: time.Now(),
		Duration:  duration,
		Metadata:  make(map[string]interface{}),
	}
	
	result = result.
		WithMetadata("status_code", resp.StatusCode).
		WithMetadata("endpoint", c.healthURL)
	
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = StatusHealthy
		result.Message = "api reachable"
	} else if resp.StatusCode >= 500 {
		result.Status = StatusUnhealthy
		result.Message = fmt.Sprintf("api returned %d", resp.StatusCode)
	} else {
		result.Status = StatusDegraded
		result.Message = fmt.Sprintf("api returned %d", resp.StatusCode)
	}
	
	// Check response time
	if duration > 5*time.Second {
		result.Status = StatusDegraded
		result.Message = "slow response time"
	}
	
	return result
}

// Name returns the checker name
func (c *ExternalAPIChecker) Name() string {
	return c.name
}

// CircuitBreakerChecker checks circuit breaker state
type CircuitBreakerChecker struct {
	name         string
	stateGetter  func() string
	countsGetter func() map[string]interface{}
}

// NewCircuitBreakerChecker creates a circuit breaker health checker
func NewCircuitBreakerChecker(name string, stateGetter func() string, countsGetter func() map[string]interface{}) *CircuitBreakerChecker {
	return &CircuitBreakerChecker{
		name:         name,
		stateGetter:  stateGetter,
		countsGetter: countsGetter,
	}
}

// Check performs the circuit breaker health check
func (c *CircuitBreakerChecker) Check(ctx context.Context) CheckResult {
	start := time.Now()
	
	state := c.stateGetter()
	counts := c.countsGetter()
	
	result := CheckResult{
		Component: c.name,
		Timestamp: time.Now(),
		Duration:  time.Since(start),
		Metadata:  counts,
	}
	
	result = result.WithMetadata("circuit_state", state)
	
	switch state {
	case "closed":
		result.Status = StatusHealthy
		result.Message = "circuit closed"
	case "half-open":
		result.Status = StatusDegraded
		result.Message = "circuit half-open"
	case "open":
		result.Status = StatusUnhealthy
		result.Message = "circuit open"
	default:
		result.Status = StatusUnhealthy
		result.Message = "unknown circuit state"
	}
	
	return result
}

// Name returns the checker name
func (c *CircuitBreakerChecker) Name() string {
	return c.name
}

// WorkerChecker checks background worker health
type WorkerChecker struct {
	name          string
	isRunning     func() bool
	getStatus     func() map[string]interface{}
}

// NewWorkerChecker creates a worker health checker
func NewWorkerChecker(name string, isRunning func() bool, getStatus func() map[string]interface{}) *WorkerChecker {
	return &WorkerChecker{
		name:      name,
		isRunning: isRunning,
		getStatus: getStatus,
	}
}

// Check performs the worker health check
func (c *WorkerChecker) Check(ctx context.Context) CheckResult {
	start := time.Now()
	
	running := c.isRunning()
	status := c.getStatus()
	
	result := CheckResult{
		Component: c.name,
		Timestamp: time.Now(),
		Duration:  time.Since(start),
		Metadata:  status,
	}
	
	result = result.WithMetadata("running", running)
	
	if running {
		result.Status = StatusHealthy
		result.Message = "worker running"
	} else {
		result.Status = StatusUnhealthy
		result.Message = "worker not running"
	}
	
	return result
}

// Name returns the checker name
func (c *WorkerChecker) Name() string {
	return c.name
}
