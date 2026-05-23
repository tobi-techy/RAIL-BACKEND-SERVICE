package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/growthengine"
	"go.uber.org/zap"
)

type GrowthEngineHandlers struct {
	service *growthengine.Service
	logger  *zap.Logger
}

func NewGrowthEngineHandlers(service *growthengine.Service, logger *zap.Logger) *GrowthEngineHandlers {
	return &GrowthEngineHandlers{service: service, logger: logger}
}

type trackGrowthEventRequest struct {
	UserID    uuid.UUID      `json:"user_id" binding:"required"`
	EventName string         `json:"event_name" binding:"required"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (h *GrowthEngineHandlers) TrackEvent(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "growth engine not configured"})
		return
	}

	var req trackGrowthEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}

	if err := h.service.Track(c.Request.Context(), req.UserID, entities.GrowthEventName(req.EventName), req.Metadata); err != nil {
		h.logger.Error("failed to track growth event", zap.Error(err), zap.String("user_id", req.UserID.String()), zap.String("event_name", req.EventName))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to track event"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}

func (h *GrowthEngineHandlers) RunSegmentation(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "growth engine not configured"})
		return
	}
	segmented, queued, err := h.service.RunSegmentation(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to run growth segmentation", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to run segmentation"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"segmented": segmented, "queued": queued})
}

func (h *GrowthEngineHandlers) ManualWhatsAppExport(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "growth engine not configured"})
		return
	}

	limit := 200
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	leads, err := h.service.ListManualWhatsAppLeads(c.Request.Context(), limit)
	if err != nil {
		h.logger.Error("failed to export manual whatsapp growth leads", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to export leads"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": len(leads), "leads": leads})
}
