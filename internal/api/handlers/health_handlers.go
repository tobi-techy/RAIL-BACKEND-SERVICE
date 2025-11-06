package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stack-service/stack_service/internal/infrastructure/cache"
	"github.com/stack-service/stack_service/internal/infrastructure/circle"
	"github.com/stack-service/stack_service/internal/infrastructure/zerog"
	"github.com/stack-service/stack_service/pkg/logger"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	db           *sql.DB
	redis        cache.RedisClient
	circleClient *circle.Client
	zeroGClient  *zerog.StorageClient
	logger       *logger.Logger
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(db *sql.DB, redis cache.RedisClient, circleClient *circle.Client, zeroGClient *zerog.StorageClient, logger *logger.Logger) *HealthHandler {
	return &HealthHandler{
		db:           db,
		redis:        redis,
		circleClient: circleClient,
		zeroGClient:  zeroGClient,
		logger:       logger,
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

// GetHealth performs comprehensive health checks
// @Summary Get application health status
// @Description Performs health checks on all critical services
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} HealthResponse
// @Router /health [get]
func (h *HealthHandler) GetHealth(c *gin.Context) {
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

	// Redis health check
	redisCheck := h.checkRedis(ctx)
	checks["redis"] = redisCheck
	if redisCheck.Status != "healthy" {
		overallStatus = "degraded"
	}

	// Circle API health check
	circleCheck := h.checkCircleAPI(ctx)
	checks["circle_api"] = circleCheck
	if circleCheck.Status != "healthy" {
		overallStatus = "degraded"
	}

	// ZeroG health check
	zeroGCheck := h.checkZeroG(ctx)
	checks["zerog"] = zeroGCheck
	if zeroGCheck.Status != "healthy" {
		overallStatus = "degraded"
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

// GetReadiness checks if the application is ready to serve traffic
// @Summary Get application readiness status
// @Description Checks if critical services are available for serving requests
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func (h *HealthHandler) GetReadiness(c *gin.Context) {
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

// GetLiveness checks if the application is alive
// @Summary Get application liveness status
// @Description Simple liveness check for container orchestration
// @Tags health
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /live [get]
func (h *HealthHandler) GetLiveness(c *gin.Context) {
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

// checkRedis performs Redis health check
func (h *HealthHandler) checkRedis(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Service:   "redis",
		Timestamp: start,
	}

	err := h.redis.Ping(ctx)
	check.Latency = time.Since(start)

	if err != nil {
		check.Status = "unhealthy"
		check.Error = err.Error()
	} else {
		check.Status = "healthy"
	}

	return check
}

// checkCircleAPI performs Circle API health check
func (h *HealthHandler) checkCircleAPI(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Service:   "circle_api",
		Timestamp: start,
	}

	// Simple ping to Circle API - implement based on Circle client capabilities
	// For now, just check if client is configured
	if h.circleClient == nil {
		check.Status = "unhealthy"
		check.Error = "Circle client not configured"
	} else {
		check.Status = "healthy"
	}
	
	check.Latency = time.Since(start)
	return check
}

// checkZeroG performs ZeroG health check
func (h *HealthHandler) checkZeroG(ctx context.Context) HealthCheck {
	start := time.Now()
	check := HealthCheck{
		Service:   "zerog",
		Timestamp: start,
	}

	// Simple check for ZeroG client
	if h.zeroGClient == nil {
		check.Status = "unhealthy"
		check.Error = "ZeroG client not configured"
	} else {
		check.Status = "healthy"
	}
	
	check.Latency = time.Since(start)
	return check
}
