package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	"github.com/rail-service/rail_service/pkg/logger"
)

// NewsHandlers handles news endpoints
type NewsHandlers struct {
	newsService *newsservice.Service
	logger      *logger.Logger
}

// NewNewsHandlers creates new news handlers
func NewNewsHandlers(newsService *newsservice.Service, logger *logger.Logger) *NewsHandlers {
	return &NewsHandlers{newsService: newsService, logger: logger}
}

// GetFeed handles GET /api/v1/news/feed
func (h *NewsHandlers) GetFeed(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	news, err := h.newsService.GetFeed(c.Request.Context(), userID, limit)
	if err != nil {
		h.logger.Error("Failed to get news feed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get news"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"news": news, "count": len(news)})
}

// GetWeeklyNews handles GET /api/v1/news/weekly
func (h *NewsHandlers) GetWeeklyNews(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	news, err := h.newsService.GetWeeklyNews(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get weekly news", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get news"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"news": news, "count": len(news)})
}

// MarkAsRead handles POST /api/v1/news/read
func (h *NewsHandlers) MarkAsRead(c *gin.Context) {
	_, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		NewsIDs []string `json:"news_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	newsIDs := make([]uuid.UUID, 0, len(req.NewsIDs))
	for _, id := range req.NewsIDs {
		if parsed, err := uuid.Parse(id); err == nil {
			newsIDs = append(newsIDs, parsed)
		}
	}

	if len(newsIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid news IDs"})
		return
	}

	if err := h.newsService.MarkAsRead(c.Request.Context(), newsIDs); err != nil {
		h.logger.Error("Failed to mark news as read", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark as read"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"marked": len(newsIDs)})
}

// GetUnreadCount handles GET /api/v1/news/unread-count
func (h *NewsHandlers) GetUnreadCount(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	count, err := h.newsService.GetUnreadCount(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get unread count", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get count"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

// RefreshNews handles POST /api/v1/news/refresh
func (h *NewsHandlers) RefreshNews(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.newsService.FetchAndStoreNews(c.Request.Context(), userID); err != nil {
		h.logger.Error("Failed to refresh news", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh news"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "refreshed"})
}
