package health

import (
	"context"
	"database/sql"
	"time"
)

// DatabaseChecker checks database connectivity
type DatabaseChecker struct {
	db      *sql.DB
	timeout time.Duration
}

// NewDatabaseChecker creates a new database health checker
func NewDatabaseChecker(db *sql.DB, timeout time.Duration) *DatabaseChecker {
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &DatabaseChecker{
		db:      db,
		timeout: timeout,
	}
}

// Check performs the database health check
func (c *DatabaseChecker) Check(ctx context.Context) CheckResult {
	start := time.Now()

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Check connection
	if err := c.db.PingContext(ctx); err != nil {
		return NewUnhealthyResult("database", err).WithDuration(time.Since(start))
	}

	// Execute a simple query
	var result int
	err := c.db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		return NewUnhealthyResult("database", err).WithDuration(time.Since(start))
	}

	if result != 1 {
		return NewUnhealthyResult("database", nil).
			WithDuration(time.Since(start)).
			WithMetadata("error", "unexpected query result")
	}

	// Get connection stats
	stats := c.db.Stats()

	checkResult := NewHealthyResult("database", "connected").
		WithDuration(time.Since(start)).
		WithMetadata("open_connections", stats.OpenConnections).
		WithMetadata("in_use", stats.InUse).
		WithMetadata("idle", stats.Idle).
		WithMetadata("max_open_connections", stats.MaxOpenConnections)

	// Check if connection pool is under stress
	if stats.MaxOpenConnections > 0 {
		utilization := float64(stats.OpenConnections) / float64(stats.MaxOpenConnections)
		checkResult = checkResult.WithMetadata("pool_utilization", utilization)

		// Mark as degraded if > 80% utilization
		if utilization > 0.8 {
			checkResult.Status = StatusDegraded
			checkResult.Message = "high connection pool utilization"
		}
	}

	return checkResult
}

// Name returns the checker name
func (c *DatabaseChecker) Name() string {
	return "database"
}
