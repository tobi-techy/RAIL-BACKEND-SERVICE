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

// NotificationQueue defines async notification queueing
type NotificationQueue interface {
	QueueNotification(ctx context.Context, msg *QueuedNotification) error
}

// QueuedNotification represents a notification to be queued
type QueuedNotification struct {
	UserID    uuid.UUID              `json:"user_id"`
	Type      string                 `json:"type"`
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Priority  string            `json:"priority"`
	Recipient string            `json:"recipient,omitempty"`
}

// SMSSender defines SMS sending operations
type SMSSender interface {
	SendSMS(ctx context.Context, phone, message string) error
}

// EmailSenderService defines email sending operations
type EmailSenderService interface {
	SendGenericEmail(ctx context.Context, to, subject, body string) error
}

type NotificationService struct {
	logger      *zap.Logger
	queue       NotificationQueue
	smsSender   SMSSender
	emailSender EmailSenderService
}

func NewNotificationService(logger *zap.Logger) *NotificationService {
	return &NotificationService{logger: logger}
}

// SetQueue sets the notification queue (SNS/SQS)
func (s *NotificationService) SetQueue(q NotificationQueue) {
	s.queue = q
}

// SetSMSSender sets the SMS sender
func (s *NotificationService) SetSMSSender(sender SMSSender) {
	s.smsSender = sender
}

// SetEmailSender sets the email sender
func (s *NotificationService) SetEmailSender(sender EmailSenderService) {
	s.emailSender = sender
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
	if s.emailSender != nil {
		return s.emailSender.SendGenericEmail(ctx, "", notification.Title, notification.Message)
	}
	return s.queueNotification(ctx, notification.UserID, "email", notification.Title, notification.Message, nil)
}

func (s *NotificationService) sendPush(ctx context.Context, notification *entities.Notification) error {
	return s.queueNotification(ctx, notification.UserID, "push", notification.Title, notification.Message, notification.Data)
}

func (s *NotificationService) sendSMS(ctx context.Context, notification *entities.Notification) error {
	if s.smsSender != nil {
		// Direct SMS for critical notifications
		if notification.Priority == entities.PriorityCritical {
			return s.smsSender.SendSMS(ctx, "", notification.Message)
		}
	}
	return s.queueNotification(ctx, notification.UserID, "sms", "", notification.Message, nil)
}

func (s *NotificationService) sendInApp(ctx context.Context, notification *entities.Notification) error {
	s.logger.Info("Sending in-app notification", zap.String("user_id", notification.UserID.String()))
	return nil
}

func (s *NotificationService) queueNotification(ctx context.Context, userID uuid.UUID, notifType, title, body string, data map[string]interface{}) error {
	if s.queue == nil {
		s.logger.Debug("Notification queue not configured, logging only",
			zap.String("type", notifType),
			zap.String("user_id", userID.String()))
		return nil
	}

	return s.queue.QueueNotification(ctx, &QueuedNotification{
		UserID:   userID,
		Type:     notifType,
		Title:    title,
		Body:     body,
		Data:     data,
		Priority: "normal",
	})
}

func (s *NotificationService) SendWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) error {
	title := "Your Weekly Investment Summary"
	body := fmt.Sprintf("Here's your investment summary for the week of %s", weekStart.Format("Jan 2, 2006"))
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "weekly_summary"})
}

func (s *NotificationService) NotifyOffRampSuccess(ctx context.Context, userID uuid.UUID, amount string) error {
	title := "Withdrawal Complete"
	body := fmt.Sprintf("Your withdrawal of $%s has been processed successfully.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "offramp_success", "amount": amount})
}

func (s *NotificationService) NotifyOffRampFailure(ctx context.Context, userID uuid.UUID, reason string) error {
	title := "Withdrawal Failed"
	body := fmt.Sprintf("Your withdrawal could not be processed: %s", reason)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "offramp_failure"})
}

func (s *NotificationService) NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error {
	title := "Transaction Declined"
	body := fmt.Sprintf("Your %s of $%s was declined due to spending limits.", transactionType, amount.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "transaction_declined"})
}

func (s *NotificationService) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	title := "Deposit Confirmed"
	body := fmt.Sprintf("Your deposit of %s on %s has been confirmed.", amount, chain)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "deposit_confirmed", "tx_hash": txHash})
}

func (s *NotificationService) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error {
	title := "Withdrawal Complete"
	body := fmt.Sprintf("Your withdrawal of $%s has been sent to %s...%s", amount, destinationAddress[:6], destinationAddress[len(destinationAddress)-4:])
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_completed"})
}

func (s *NotificationService) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error {
	title := "Withdrawal Failed"
	body := fmt.Sprintf("Your withdrawal of $%s failed: %s", amount, reason)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_failed"})
}

func (s *NotificationService) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	title := "Large Balance Change"
	body := fmt.Sprintf("A %s of $%s has been processed. New balance: $%s", changeType, amount.String(), newBalance.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "balance_change"})
}

func (s *NotificationService) SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	return s.queueNotification(ctx, userID, "push", title, message, nil)
}
