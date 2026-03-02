package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
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
	Priority  string                 `json:"priority"`
	Recipient string                 `json:"recipient,omitempty"`
}

// SMSSender defines SMS sending operations
type SMSSender interface {
	SendSMS(ctx context.Context, phone, message string) error
}

// EmailSenderService defines email sending operations
type EmailSenderService interface {
	SendGenericEmail(ctx context.Context, to, subject, body string) error
}

// NotificationPersister persists notifications to the database
type NotificationPersister interface {
	Create(ctx context.Context, userID uuid.UUID, notifType, title, body string, data map[string]interface{}) error
}

type NotificationService struct {
	logger      *zap.Logger
	queue       NotificationQueue
	smsSender   SMSSender
	emailSender EmailSenderService
	persister   NotificationPersister
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

// SetPersister sets the notification persister for in-app notifications
func (s *NotificationService) SetPersister(p NotificationPersister) {
	s.persister = p
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
	// Always persist to in-app notification center for push notifications
	if s.persister != nil && notifType == "push" {
		if err := s.persister.Create(ctx, userID, notifType, title, body, data); err != nil {
			s.logger.Warn("Failed to persist notification", zap.Error(err))
		}
	}

	if s.queue == nil {
		s.logger.Debug("Notification queue not configured, persisted only",
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

func (s *NotificationService) NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error {
	title := "Investment Allocation Requires Attention"
	body := fmt.Sprintf("Your deposit of $%s was received but the automatic 70/30 allocation split could not be completed: %s. Please contact support or try again later.", amount.String(), reason)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{
		"type":       "allocation_failed",
		"deposit_id": depositID.String(),
		"amount":     amount.String(),
	})
}

func (s *NotificationService) SendGenericNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	return s.queueNotification(ctx, userID, "push", title, message, nil)
}

// SendMilestoneNotification sends a celebration notification for investment milestones
func (s *NotificationService) SendMilestoneNotification(ctx context.Context, userID uuid.UUID, milestone *entities.InvestmentMilestone) error {
	title, body := getMilestoneMessage(milestone.Type, milestone.Amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{
		"type":           "milestone",
		"milestone_type": string(milestone.Type),
		"amount":         milestone.Amount.String(),
		"achieved_at":    milestone.AchievedAt.Format(time.RFC3339),
	})
}

// getMilestoneMessage returns celebration messages for different milestones
func getMilestoneMessage(milestoneType entities.MilestoneType, amount decimal.Decimal) (title, body string) {
	amountStr := amount.StringFixed(0)

	switch milestoneType {
	case entities.MilestoneTypeBalance:
		switch {
		case amount.Equal(decimal.NewFromInt(100)):
			return "🎉 First $100!", "You've hit your first $100 invested! This is just the beginning."
		case amount.Equal(decimal.NewFromInt(500)):
			return "🚀 $500 Milestone!", "Half a thousand dollars working for you. Keep it up!"
		case amount.Equal(decimal.NewFromInt(1000)):
			return "💰 $1,000 Club!", "Welcome to the four-figure club! Your money is growing."
		case amount.Equal(decimal.NewFromInt(5000)):
			return "⭐ $5,000 Achieved!", "Five thousand dollars invested. You're building real wealth."
		case amount.Equal(decimal.NewFromInt(10000)):
			return "🏆 $10,000 Milestone!", "Five figures! You're in the top tier of young investors."
		default:
			return fmt.Sprintf("🎯 $%s Milestone!", amountStr), fmt.Sprintf("You've reached $%s invested. Amazing progress!", amountStr)
		}
	case entities.MilestoneTypeContribution:
		return fmt.Sprintf("💪 $%s Contributed!", amountStr), fmt.Sprintf("You've contributed $%s total. Consistency wins!", amountStr)
	case entities.MilestoneTypeGain:
		return fmt.Sprintf("📈 $%s in Gains!", amountStr), fmt.Sprintf("Your investments have earned $%s. Your money is working!", amountStr)
	default:
		return "🎉 Milestone Achieved!", fmt.Sprintf("You've reached a $%s milestone!", amountStr)
	}
}
