package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/pkg/health"
	"go.uber.org/zap"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	livenessChecker  *health.HealthChecker
	readinessChecker *health.HealthChecker
	startupChecker   *health.HealthChecker
	logger           *zap.Logger
	version          string
	startTime        time.Time
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(
	livenessChecker *health.HealthChecker,
	readinessChecker *health.HealthChecker,
	startupChecker *health.HealthChecker,
	logger *zap.Logger,
	version string,
) *HealthHandler {
	return &HealthHandler{
		livenessChecker:  livenessChecker,
		readinessChecker: readinessChecker,
		startupChecker:   startupChecker,
		logger:           logger,
		version:          version,
		startTime:        time.Now(),
	}
}

// Liveness handles the liveness probe
// @Summary Liveness check
// @Description Returns 200 if the service is alive
// @Tags health
// @Produce json
// @Success 200 {object} health.HealthResponse
// @Failure 503 {object} health.HealthResponse
// @Router /health/liveness [get]
func (h *HealthHandler) Liveness(c *gin.Context) {
	status, checks := h.livenessChecker.Check(c.Request.Context())
	
	response := health.HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Version:   h.version,
		Checks:    checks,
	}
	
	statusCode := http.StatusOK
	if status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	}
	
	h.logger.Debug("Liveness check",
		zap.String("status", string(status)),
		zap.Int("status_code", statusCode))
	
	c.JSON(statusCode, response)
}

// Readiness handles the readiness probe
// @Summary Readiness check
// @Description Returns 200 if the service is ready to accept traffic
// @Tags health
// @Produce json
// @Success 200 {object} health.HealthResponse
// @Failure 503 {object} health.HealthResponse
// @Router /health/readiness [get]
func (h *HealthHandler) Readiness(c *gin.Context) {
	status, checks := h.readinessChecker.Check(c.Request.Context())
	
	response := health.HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Version:   h.version,
		Checks:    checks,
	}
	
	statusCode := http.StatusOK
	switch status {
	case health.StatusUnhealthy:
		statusCode = http.StatusServiceUnavailable
		h.logger.Warn("Readiness check failed",
			zap.String("status", string(status)),
			zap.Any("checks", checks))
	case health.StatusDegraded:
		// Still return 200 for degraded, but log it
		h.logger.Warn("Service degraded",
			zap.String("status", string(status)),
			zap.Any("checks", checks))
	}
	
	c.JSON(statusCode, response)
}

// Startup handles the startup probe
// @Summary Startup check
// @Description Returns 200 if the service has completed startup
// @Tags health
// @Produce json
// @Success 200 {object} health.HealthResponse
// @Failure 503 {object} health.HealthResponse
// @Router /health/startup [get]
func (h *HealthHandler) Startup(c *gin.Context) {
	status, checks := h.startupChecker.Check(c.Request.Context())
	
	response := health.HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Version:   h.version,
		Checks:    checks,
	}
	
	statusCode := http.StatusOK
	if status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
		h.logger.Warn("Startup check failed",
			zap.String("status", string(status)),
			zap.Any("checks", checks))
	}
	
	c.JSON(statusCode, response)
}

// Health handles the general health endpoint (combines all checks)
// @Summary General health check
// @Description Returns overall service health with detailed component status
// @Tags health
// @Produce json
// @Success 200 {object} health.HealthResponse
// @Failure 503 {object} health.HealthResponse
// @Router /health [get]
func (h *HealthHandler) Health(c *gin.Context) {
	status, checks := h.readinessChecker.Check(c.Request.Context())
	
	uptime := time.Since(h.startTime)
	
	response := health.HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Version:   h.version,
		Checks:    checks,
	}
	
	// Add global metadata
	for name, check := range response.Checks {
		check = check.WithMetadata("uptime_seconds", int(uptime.Seconds()))
		response.Checks[name] = check
	}
	
	statusCode := http.StatusOK
	if status == health.StatusUnhealthy {
		statusCode = http.StatusServiceUnavailable
	}
	
	c.JSON(statusCode, response)
}

// Ping handles simple ping endpoint (no checks, always returns 200)
// @Summary Ping
// @Description Simple ping endpoint that always returns 200 OK
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string
// @Router /ping [get]
func (h *HealthHandler) Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"time":    time.Now().Unix(),
		"version": h.version,
	})
}
