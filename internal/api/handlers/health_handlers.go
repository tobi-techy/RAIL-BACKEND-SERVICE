package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/pkg/logger"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	db     *sql.DB
	logger *logger.Logger
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(db *sql.DB, logger *logger.Logger) *HealthHandler {
	return &HealthHandler{
		db:     db,
		logger: logger,
	}
}

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

var startTime = time.Now()

// Health performs comprehensive health checks
// @Summary Get application health status
// @Description Performs health checks on all critical services
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	checks := make(map[string]HealthCheck)
	overallStatus := "healthy"

	// Database health check
	dbCheck := h.checkDatabase(ctx)
	checks["database"] = dbCheck
	if dbCheck.Status != "healthy" {
		overallStatus = "unhealthy"
	}



	response := HealthResponse{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   "1.0.0", // Should come from build info
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
// @Summary Get application readiness status
// @Description Checks if critical services are available for serving requests
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Check only critical services for readiness
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
// @Summary Get application liveness status
// @Description Simple liveness check for container orchestration
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /live [get]
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"status":    "alive",
		"timestamp": time.Now(),
		"uptime":    time.Since(startTime),
	})
}

// checkDatabase performs database health check
func (h *HealthHandler) checkDatabase(ctx context.Context) HealthCheck {
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


