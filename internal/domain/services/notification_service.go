package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.uber.org/zap"
)

type NotificationService struct {
	logger *zap.Logger
}

func NewNotificationService(logger *zap.Logger) *NotificationService {
	return &NotificationService{logger: logger}
}

func (s *NotificationService) Send(ctx context.Context, notification *entities.Notification, prefs *entities.UserPreference) error {
	if !s.shouldSend(notification, prefs) {
		s.logger.Debug("Notification skipped due to user preferences", zap.String("type", string(notification.Type)))
		return nil
	}

	switch notification.Channel {
	case entities.ChannelEmail:
		return s.sendEmail(ctx, notification)
	case entities.ChannelPush:
		return s.sendPush(ctx, notification)
	case entities.ChannelSMS:
		return s.sendSMS(ctx, notification)
	case entities.ChannelInApp:
		return s.sendInApp(ctx, notification)
	default:
		return fmt.Errorf("unsupported notification channel: %s", notification.Channel)
	}
}

func (s *NotificationService) shouldSend(notification *entities.Notification, prefs *entities.UserPreference) bool {
	if notification.Priority == entities.PriorityCritical {
		return true
	}

	switch notification.Channel {
	case entities.ChannelEmail:
		return prefs.EmailNotifications
	case entities.ChannelPush:
		return prefs.PushNotifications
	case entities.ChannelSMS:
		return prefs.SMSNotifications
	default:
		return true
	}
}

func (s *NotificationService) sendEmail(ctx context.Context, notification *entities.Notification) error {
	s.logger.Info("Sending email notification", zap.String("user_id", notification.UserID.String()))
	return nil
}

func (s *NotificationService) sendPush(ctx context.Context, notification *entities.Notification) error {
	s.logger.Info("Sending push notification", zap.String("user_id", notification.UserID.String()))
	return nil
}

func (s *NotificationService) sendSMS(ctx context.Context, notification *entities.Notification) error {
	s.logger.Info("Sending SMS notification", zap.String("user_id", notification.UserID.String()))
	return nil
}

func (s *NotificationService) sendInApp(ctx context.Context, notification *entities.Notification) error {
	s.logger.Info("Sending in-app notification", zap.String("user_id", notification.UserID.String()))
	return nil
}

func (s *NotificationService) SendWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) error {
	s.logger.Info("Sending weekly summary notification",
		zap.String("user_id", userID.String()),
		zap.String("week_start", weekStart.Format("2006-01-02")))
	return nil
}

func (s *NotificationService) NotifyOffRampSuccess(ctx context.Context, userID uuid.UUID, amount string) error {
	s.logger.Info("Sending off-ramp success notification",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount))
	return nil
}

func (s *NotificationService) NotifyOffRampFailure(ctx context.Context, userID uuid.UUID, reason string) error {
	s.logger.Warn("Sending off-ramp failure notification",
		zap.String("user_id", userID.String()),
		zap.String("reason", reason))
	return nil
}
