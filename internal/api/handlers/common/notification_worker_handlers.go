package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	walletprovisioning "github.com/rail-service/rail_service/internal/workers/wallet_provisioning"
	"go.uber.org/zap"
)

// WorkerAdminHandlers handles admin endpoints for background worker management.
// Notification preference and CRUD handlers have been removed — use NotificationHandlers instead.
type WorkerAdminHandlers struct {
	scheduler *walletprovisioning.Scheduler
	logger    *zap.Logger
}

// NewWorkerAdminHandlers creates a new instance of worker admin handlers
func NewWorkerAdminHandlers(
	scheduler *walletprovisioning.Scheduler,
	logger *zap.Logger,
) *WorkerAdminHandlers {
	return &WorkerAdminHandlers{
		scheduler: scheduler,
		logger:    logger,
	}
}

// GetWorkerStatus handles GET /admin/workers/status
func (h *WorkerAdminHandlers) GetWorkerStatus(c *gin.Context) {
	if h.scheduler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SCHEDULER_UNAVAILABLE", "message": "Worker scheduler is not available"})
		return
	}
	status := h.scheduler.GetStatus()
	c.JSON(http.StatusOK, gin.H{
		"worker":    gin.H{"type": "wallet_provisioning", "status": "operational"},
		"scheduler": gin.H{"is_running": status.IsRunning, "poll_interval": status.PollInterval.String(), "max_concurrency": status.MaxConcurrency, "active_jobs": status.ActiveJobs},
		"metrics": gin.H{
			"total_jobs_processed": status.WorkerMetrics.TotalJobsProcessed,
			"successful_jobs":      status.WorkerMetrics.SuccessfulJobs,
			"failed_jobs":          status.WorkerMetrics.FailedJobs,
			"total_retries":        status.WorkerMetrics.TotalRetries,
			"average_duration_ms":  status.WorkerMetrics.AverageDuration.Milliseconds(),
			"last_processed_at":    status.WorkerMetrics.LastProcessedAt,
			"errors_by_type":       status.WorkerMetrics.ErrorsByType,
		},
	})
}

// GetWorkerMetrics handles GET /admin/workers/metrics
func (h *WorkerAdminHandlers) GetWorkerMetrics(c *gin.Context) {
	if h.scheduler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SCHEDULER_UNAVAILABLE", "message": "Worker scheduler is not available"})
		return
	}
	status := h.scheduler.GetStatus()
	metrics := status.WorkerMetrics
	var successRate float64
	if metrics.TotalJobsProcessed > 0 {
		successRate = float64(metrics.SuccessfulJobs) / float64(metrics.TotalJobsProcessed) * 100
	}
	c.JSON(http.StatusOK, gin.H{
		"total_jobs_processed": metrics.TotalJobsProcessed,
		"successful_jobs":      metrics.SuccessfulJobs,
		"failed_jobs":          metrics.FailedJobs,
		"success_rate":         successRate,
		"total_retries":        metrics.TotalRetries,
		"average_duration":     gin.H{"milliseconds": metrics.AverageDuration.Milliseconds(), "seconds": metrics.AverageDuration.Seconds()},
		"last_processed_at":    metrics.LastProcessedAt,
		"errors_by_type":       metrics.ErrorsByType,
		"active_jobs":          status.ActiveJobs,
	})
}

// TriggerJobRequest represents a request to trigger job processing
type TriggerJobRequest struct {
	JobID string `json:"job_id" validate:"required,uuid"`
}

// TriggerJobProcessing handles POST /admin/workers/trigger
func (h *WorkerAdminHandlers) TriggerJobProcessing(c *gin.Context) {
	var req TriggerJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "Invalid request payload"})
		return
	}
	jobID, err := uuid.Parse(req.JobID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_JOB_ID", "message": "Invalid job ID format"})
		return
	}
	h.logger.Info("Triggering job processing", zap.String("job_id", jobID.String()))
	c.JSON(http.StatusAccepted, gin.H{"message": "Job will be processed by the scheduler", "job_id": jobID.String()})
}

// GetWorkerHealth handles GET /admin/workers/health
func (h *WorkerAdminHandlers) GetWorkerHealth(c *gin.Context) {
	if h.scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": "SCHEDULER_UNAVAILABLE"})
		return
	}
	if !h.scheduler.IsRunning() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "reason": "scheduler_not_running"})
		return
	}
	status := h.scheduler.GetStatus()
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy", "scheduler": "running", "active_jobs": status.ActiveJobs,
		"metrics": gin.H{"total_processed": status.WorkerMetrics.TotalJobsProcessed, "last_activity": status.WorkerMetrics.LastProcessedAt},
	})
}

// RestartScheduler handles POST /admin/workers/restart
func (h *WorkerAdminHandlers) RestartScheduler(c *gin.Context) {
	if h.scheduler == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "SCHEDULER_UNAVAILABLE"})
		return
	}
	if err := h.scheduler.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "STOP_FAILED", "message": err.Error()})
		return
	}
	if err := h.scheduler.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "START_FAILED", "message": err.Error()})
		return
	}
	h.logger.Info("Scheduler restarted successfully")
	c.JSON(http.StatusOK, gin.H{"message": "Scheduler restarted successfully", "status": "running"})
}
