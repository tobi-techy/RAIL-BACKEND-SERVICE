package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/version"
)

// CoreHandlers contains health, version, and metrics handlers
type CoreHandlers struct {
	db     *sql.DB
	logger *logger.Logger
}

// NewCoreHandlers creates a new core handlers instance
func NewCoreHandlers(db *sql.DB, logger *logger.Logger) *CoreHandlers {
	return &CoreHandlers{
		db:     db,
		logger: logger,
	}
}

var startTime = time.Now()

// HealthCheck represents a health check result
type HealthCheck struct {
	Service   string        `json:"service"`
	Status    string        `json:"status"`
	Latency   time.Duration `json:"latency"`
	Error     string        `json:"error,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
}

// HealthResponse represents the overall health response
type HealthResponse struct {
	Status    string                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Version   string                 `json:"version"`
	Uptime    time.Duration          `json:"uptime"`
	Checks    map[string]HealthCheck `json:"checks"`
}

// Health performs comprehensive health checks
func (h *CoreHandlers) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	checks := make(map[string]HealthCheck)
	overallStatus := "healthy"

	dbCheck := h.checkDatabase(ctx)
	checks["database"] = dbCheck
	if dbCheck.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	response := HealthResponse{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    time.Since(startTime),
		Checks:    checks,
	}

	statusCode := http.StatusOK
	if overallStatus == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, response)
}

// Ready checks if the application is ready to serve traffic
func (h *CoreHandlers) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	dbCheck := h.checkDatabase(ctx)
	ready := dbCheck.Status == "healthy"
	status := "ready"
	if !ready {
		status = "not_ready"
	}

	response := map[string]interface{}{
		"status":    status,
		"timestamp": time.Now(),
		"checks": map[string]interface{}{
			"database": dbCheck,
		},
	}

	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, response)
}

// Live checks if the application is alive
func (h *CoreHandlers) Live(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"status":    "alive",
		"timestamp": time.Now(),
		"uptime":    time.Since(startTime),
	})
}

// checkDatabase performs database health check
func (h *CoreHandlers) checkDatabase(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Service:   "database",
		Timestamp: start,
	}

	err := h.db.PingContext(ctx)
	check.Latency = time.Since(start)

	if err != nil {
		check.Status = "unhealthy"
		check.Error = err.Error()
	} else {
		check.Status = "healthy"
	}

	return check
}

// Version returns the application version
func (h *CoreHandlers) Version(c *gin.Context) {
	c.JSON(http.StatusOK, version.Get())
}

// Metrics exposes Prometheus metrics
func (h *CoreHandlers) Metrics(c *gin.Context) {
	handler := promhttp.Handler()
	handler.ServeHTTP(c.Writer, c.Request)
}

// BasicHealthCheck returns the basic health status
func BasicHealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "ok",
			"service":   "stack_service",
			"version":   "1.0.0",
			"timestamp": time.Now().Unix(),
		})
	}
}

// VersionHandler returns version information
func VersionHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, version.Get())
	}
}

// Metrics handler function
func Metrics() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
