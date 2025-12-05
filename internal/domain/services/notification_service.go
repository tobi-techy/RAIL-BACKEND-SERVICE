package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
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

func (s *NotificationService) NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error {
	s.logger.Info("Sending transaction declined notification",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("type", transactionType))
	return nil
}

// NotifyDepositConfirmed sends notification when a deposit is confirmed
func (s *NotificationService) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	s.logger.Info("Sending deposit confirmed notification",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount),
		zap.String("chain", chain),
		zap.String("tx_hash", txHash))
	return nil
}

// NotifyWithdrawalCompleted sends notification when a withdrawal is completed
func (s *NotificationService) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error {
	s.logger.Info("Sending withdrawal completed notification",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount),
		zap.String("destination", destinationAddress))
	return nil
}

// NotifyWithdrawalFailed sends notification when a withdrawal fails
func (s *NotificationService) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error {
	s.logger.Warn("Sending withdrawal failed notification",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount),
		zap.String("reason", reason))
	return nil
}

// NotifyLargeBalanceChange sends notification for significant balance changes
func (s *NotificationService) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	s.logger.Info("Sending large balance change notification",
		zap.String("user_id", userID.String()),
		zap.String("change_type", changeType),
		zap.String("amount", amount.String()),
		zap.String("new_balance", newBalance.String()))
	return nil
}


// SendGenericNotification sends a generic notification with title and message
func (s *NotificationService) SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	s.logger.Info("Sending generic notification",
		zap.String("user_id", userID.String()),
		zap.String("title", title),
		zap.String("message", message))
	return nil
}
