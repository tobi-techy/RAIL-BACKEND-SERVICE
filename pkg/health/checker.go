package health

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Status represents the health status
type Status string

const (
	// StatusHealthy indicates the component is healthy
	StatusHealthy Status = "healthy"
	
	// StatusUnhealthy indicates the component is unhealthy
	StatusUnhealthy Status = "unhealthy"
	
	// StatusDegraded indicates the component is partially healthy
	StatusDegraded Status = "degraded"
)

// CheckResult represents the result of a health check
type CheckResult struct {
	Status    Status                 `json:"status"`
	Component string                 `json:"component"`
	Message   string                 `json:"message,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Duration  time.Duration          `json:"duration"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Checker is an interface for health checkers
type Checker interface {
	Check(ctx context.Context) CheckResult
	Name() string
}

// HealthChecker aggregates multiple health checkers
type HealthChecker struct {
	checkers []Checker
	timeout  time.Duration
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(timeout time.Duration) *HealthChecker {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	
	return &HealthChecker{
		checkers: make([]Checker, 0),
		timeout:  timeout,
	}
}

// Register adds a checker to the health checker
func (h *HealthChecker) Register(checker Checker) {
	h.checkers = append(h.checkers, checker)
}

// CheckAll runs all registered health checks in parallel
func (h *HealthChecker) CheckAll(ctx context.Context) map[string]CheckResult {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	results := make(map[string]CheckResult)
	resultsMux := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, checker := range h.checkers {
		wg.Add(1)
		go func(c Checker) {
			defer wg.Done()
			
			result := c.Check(ctx)
			
			resultsMux.Lock()
			results[c.Name()] = result
			resultsMux.Unlock()
		}(checker)
	}

	wg.Wait()
	return results
}

// Check runs all health checks and returns overall status
func (h *HealthChecker) Check(ctx context.Context) (Status, map[string]CheckResult) {
	results := h.CheckAll(ctx)
	
	overallStatus := StatusHealthy
	hasUnhealthy := false
	hasDegraded := false
	
	for _, result := range results {
		switch result.Status {
		case StatusUnhealthy:
			hasUnhealthy = true
		case StatusDegraded:
			hasDegraded = true
		}
	}
	
	if hasUnhealthy {
		overallStatus = StatusUnhealthy
	} else if hasDegraded {
		overallStatus = StatusDegraded
	}
	
	return overallStatus, results
}

// IsHealthy returns true if all checks are healthy
func (h *HealthChecker) IsHealthy(ctx context.Context) bool {
	status, _ := h.Check(ctx)
	return status == StatusHealthy
}

// HealthResponse represents the JSON response for health checks
type HealthResponse struct {
	Status    Status                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Version   string                 `json:"version,omitempty"`
	Checks    map[string]CheckResult `json:"checks"`
}

// NewCheckResult creates a new check result
func NewCheckResult(component string, status Status, message string, err error) CheckResult {
	result := CheckResult{
		Component: component,
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
		Metadata:  make(map[string]interface{}),
	}
	
	if err != nil {
		result.Error = err.Error()
		result.Status = StatusUnhealthy
	}
	
	return result
}

// NewHealthyResult creates a healthy check result
func NewHealthyResult(component, message string) CheckResult {
	return NewCheckResult(component, StatusHealthy, message, nil)
}

// NewUnhealthyResult creates an unhealthy check result
func NewUnhealthyResult(component string, err error) CheckResult {
	return NewCheckResult(component, StatusUnhealthy, "", err)
}

// NewDegradedResult creates a degraded check result
func NewDegradedResult(component, message string) CheckResult {
	return NewCheckResult(component, StatusDegraded, message, nil)
}

// WithMetadata adds metadata to a check result
func (r CheckResult) WithMetadata(key string, value interface{}) CheckResult {
	if r.Metadata == nil {
		r.Metadata = make(map[string]interface{})
	}
	r.Metadata[key] = value
	return r
}

// WithDuration adds duration to a check result
func (r CheckResult) WithDuration(d time.Duration) CheckResult {
	r.Duration = d
	return r
}

// TimeoutChecker wraps a checker with a timeout
type TimeoutChecker struct {
	checker Checker
	timeout time.Duration
}

// NewTimeoutChecker creates a new timeout checker
func NewTimeoutChecker(checker Checker, timeout time.Duration) *TimeoutChecker {
	return &TimeoutChecker{
		checker: checker,
		timeout: timeout,
	}
}

// Check runs the wrapped checker with a timeout
func (t *TimeoutChecker) Check(ctx context.Context) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	
	resultChan := make(chan CheckResult, 1)
	
	go func() {
		resultChan <- t.checker.Check(ctx)
	}()
	
	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return NewUnhealthyResult(
			t.checker.Name(),
			fmt.Errorf("health check timed out after %v", t.timeout),
		)
	}
}

// Name returns the name of the wrapped checker
func (t *TimeoutChecker) Name() string {
	return t.checker.Name()
}
