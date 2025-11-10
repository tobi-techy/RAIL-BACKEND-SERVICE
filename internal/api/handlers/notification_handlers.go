package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.uber.org/zap"
)

type NotificationHandler struct {
	notificationService *services.NotificationService
	logger              *zap.Logger
}

func NewNotificationHandler(notificationService *services.NotificationService, logger *zap.Logger) *NotificationHandler {
	return &NotificationHandler{
		notificationService: notificationService,
		logger:              logger,
	}
}

func (h *NotificationHandler) GetPreferences(c *gin.Context) {
	userID := uuid.MustParse(c.GetString("user_id"))

	prefs := &entities.UserPreference{
		UserID:             userID,
		EmailNotifications: true,
		PushNotifications:  true,
		DepositAlerts:      true,
		WithdrawalAlerts:   true,
		SecurityAlerts:     true,
	}

	c.JSON(http.StatusOK, prefs)
}

func (h *NotificationHandler) UpdatePreferences(c *gin.Context) {
	var prefs entities.UserPreference
	if err := c.ShouldBindJSON(&prefs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := uuid.MustParse(c.GetString("user_id"))
	prefs.UserID = userID

	c.JSON(http.StatusOK, prefs)
}

func (h *NotificationHandler) GetNotifications(c *gin.Context) {
	userID := uuid.MustParse(c.GetString("user_id"))

	notifications := []entities.Notification{}

	h.logger.Info("Fetching notifications", zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, notifications)
}

func (h *NotificationHandler) MarkAsRead(c *gin.Context) {
	notificationID := c.Param("id")
	userID := uuid.MustParse(c.GetString("user_id"))

	h.logger.Info("Marking notification as read",
		zap.String("notification_id", notificationID),
		zap.String("user_id", userID.String()))

	c.JSON(http.StatusOK, gin.H{"message": "Notification marked as read"})
}
