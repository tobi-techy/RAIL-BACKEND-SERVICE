package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

const maxNotificationLimit = 100

// DeviceTokenRepo interface for device token operations
type DeviceTokenRepo interface {
	RegisterToken(ctx context.Context, userID uuid.UUID, token, platform string, appVersion, deviceModel, osVersion *string) (*repositories.DeviceToken, error)
	DeleteUserToken(ctx context.Context, userID uuid.UUID, token string) error
}

// NotificationRepo interface for notification operations
type NotificationRepo interface {
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*repositories.Notification, error)
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	MarkAsRead(ctx context.Context, userID, notificationID uuid.UUID) (bool, error)
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
}

// NotificationHandlers handles notification-related endpoints
type NotificationHandlers struct {
	deviceTokenRepo  DeviceTokenRepo
	notificationRepo NotificationRepo
	logger           *zap.Logger
}

// NewNotificationHandlers creates new notification handlers
func NewNotificationHandlers(deviceTokenRepo DeviceTokenRepo, notificationRepo NotificationRepo, logger *zap.Logger) *NotificationHandlers {
	return &NotificationHandlers{
		deviceTokenRepo:  deviceTokenRepo,
		notificationRepo: notificationRepo,
		logger:           logger,
	}
}

// RegisterDeviceTokenRequest represents the request to register a device token
type RegisterDeviceTokenRequest struct {
	Token       string  `json:"token" binding:"required"`
	Platform    string  `json:"platform" binding:"required,oneof=ios android web"`
	AppVersion  *string `json:"app_version,omitempty"`
	DeviceModel *string `json:"device_model,omitempty"`
	OSVersion   *string `json:"os_version,omitempty"`
}

// RegisterDeviceToken registers a push notification token
// POST /api/v1/devices/token
func (h *NotificationHandlers) RegisterDeviceToken(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req RegisterDeviceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dt, err := h.deviceTokenRepo.RegisterToken(c.Request.Context(), userID.(uuid.UUID), req.Token, req.Platform, req.AppVersion, req.DeviceModel, req.OSVersion)
	if err != nil {
		h.logger.Error("Failed to register device token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": dt.ID, "message": "token registered"})
}

// UnregisterDeviceTokenRequest represents the request to unregister a device token
type UnregisterDeviceTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// UnregisterDeviceToken removes a push notification token
// DELETE /api/v1/devices/token
func (h *NotificationHandlers) UnregisterDeviceToken(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req UnregisterDeviceTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.deviceTokenRepo.DeleteUserToken(c.Request.Context(), userID.(uuid.UUID), req.Token); err != nil {
		h.logger.Error("Failed to unregister device token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unregister token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "token unregistered"})
}

// GetNotifications returns paginated notifications for the user
// GET /api/v1/notifications
func (h *NotificationHandlers) GetNotifications(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 {
		limit = 20
	}
	if limit > maxNotificationLimit {
		limit = maxNotificationLimit
	}
	if offset < 0 {
		offset = 0
	}

	notifications, err := h.notificationRepo.GetByUserID(c.Request.Context(), userID.(uuid.UUID), limit, offset)
	if err != nil {
		h.logger.Error("Failed to get notifications", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get notifications"})
		return
	}

	if notifications == nil {
		notifications = []*repositories.Notification{}
	}

	c.JSON(http.StatusOK, gin.H{
		"notifications": notifications,
		"limit":         limit,
		"offset":        offset,
	})
}

// GetUnreadCount returns the count of unread notifications
// GET /api/v1/notifications/unread-count
func (h *NotificationHandlers) GetUnreadCount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	count, err := h.notificationRepo.GetUnreadCount(c.Request.Context(), userID.(uuid.UUID))
	if err != nil {
		h.logger.Error("Failed to get unread count", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get unread count"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

// MarkAsRead marks a single notification as read
// POST /api/v1/notifications/:id/read
func (h *NotificationHandlers) MarkAsRead(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	notificationID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification id"})
		return
	}

	found, err := h.notificationRepo.MarkAsRead(c.Request.Context(), userID.(uuid.UUID), notificationID)
	if err != nil {
		h.logger.Error("Failed to mark notification as read", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark as read"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "notification not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "marked as read"})
}

// MarkAllAsRead marks all notifications as read
// POST /api/v1/notifications/read-all
func (h *NotificationHandlers) MarkAllAsRead(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.notificationRepo.MarkAllAsRead(c.Request.Context(), userID.(uuid.UUID)); err != nil {
		h.logger.Error("Failed to mark all notifications as read", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark all as read"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "all marked as read"})
}
