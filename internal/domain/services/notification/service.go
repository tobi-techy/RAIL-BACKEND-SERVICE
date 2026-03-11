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

// PushSender sends push notifications
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

type NotificationService struct {
	logger      *zap.Logger
	queue       NotificationQueue
	smsSender   SMSSender
	emailSender EmailSenderService
	persister   NotificationPersister
	pushSender  PushSender
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

// SetPushSender sets the push notification sender (Expo Push)
func (s *NotificationService) SetPushSender(sender PushSender) {
	s.pushSender = sender
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
	// Always persist to in-app notification center
	if s.persister != nil {
		if err := s.persister.Create(ctx, userID, notifType, title, body, data); err != nil {
			s.logger.Warn("Failed to persist notification", zap.Error(err))
		}
	}

	// Send push notification via Expo Push (preferred)
	if notifType == "push" && s.pushSender != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.pushSender.SendToUser(bgCtx, userID, title, body, data); err != nil {
				s.logger.Warn("Failed to send push notification", zap.Error(err), zap.String("user_id", userID.String()))
			}
		}()
		return nil
	}

	// Fallback to queue if configured
	if s.queue != nil {
		return s.queue.QueueNotification(ctx, &QueuedNotification{
			UserID:   userID,
			Type:     notifType,
			Title:    title,
			Body:     body,
			Data:     data,
			Priority: "normal",
		})
	}

	s.logger.Debug("No push sender or queue configured",
		zap.String("type", notifType),
		zap.String("user_id", userID.String()))
	return nil
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
	title := "Deposit confirmed"
	body := fmt.Sprintf("Your deposit of %s has arrived and your money is being put to work.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "deposit_confirmed", "tx_hash": txHash, "chain": chain})
}

func (s *NotificationService) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error {
	title := "Withdrawal sent"
	body := fmt.Sprintf("Your withdrawal of $%s is on its way.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_completed"})
}

func (s *NotificationService) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error {
	title := "Withdrawal failed"
	body := fmt.Sprintf("Your withdrawal of $%s could not be processed. Please try again.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_failed"})
}

func (s *NotificationService) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	title := "Balance updated"
	body := fmt.Sprintf("A %s of $%s has been processed.", changeType, amount.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "balance_change"})
}

func (s *NotificationService) NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error {
	s.logger.Error("Allocation failed notification",
		zap.String("user_id", userID.String()),
		zap.String("deposit_id", depositID.String()),
		zap.String("amount", amount.String()),
		zap.String("failure_reason", reason))

	title := "Action needed"
	body := fmt.Sprintf("Your deposit of $%s arrived but the automatic split could not complete. Contact support.", amount.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{
		"type":       "allocation_failed",
		"deposit_id": depositID.String(),
		"amount":     amount.String(),
	})
}

func (s *NotificationService) NotifyKYCApproved(ctx context.Context, userID uuid.UUID) error {
	title := "Identity verified"
	body := "Your identity has been verified. You can now deposit and start investing."
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "kyc_approved"})
}

func (s *NotificationService) NotifyKYCRejected(ctx context.Context, userID uuid.UUID) error {
	title := "Verification unsuccessful"
	body := "We could not verify your identity. Please check your documents and try again."
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "kyc_rejected"})
}

func (s *NotificationService) NotifyAllocationComplete(ctx context.Context, userID uuid.UUID, spendAmount, investAmount string) error {
	title := "Money deployed"
	body := fmt.Sprintf("$%s to spending, $%s to investing. Your money is working.", spendAmount, investAmount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "allocation_complete"})
}

func (s *NotificationService) NotifyInvestmentComplete(ctx context.Context, userID uuid.UUID, amount string) error {
	title := "Investment placed"
	body := fmt.Sprintf("$%s has been automatically invested on your behalf.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "investment_complete"})
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
			return "First $100 invested", "You've hit your first $100 invested. This is just the beginning."
		case amount.Equal(decimal.NewFromInt(500)):
			return "$500 milestone", "Half a thousand dollars working for you. Keep it up."
		case amount.Equal(decimal.NewFromInt(1000)):
			return "$1,000 invested", "Welcome to the four-figure club. Your money is growing."
		case amount.Equal(decimal.NewFromInt(5000)):
			return "$5,000 invested", "Five thousand dollars invested. You're building real wealth."
		case amount.Equal(decimal.NewFromInt(10000)):
			return "$10,000 milestone", "Five figures. You're in the top tier of young investors."
		default:
			return fmt.Sprintf("$%s milestone", amountStr), fmt.Sprintf("You've reached $%s invested.", amountStr)
		}
	case entities.MilestoneTypeContribution:
		return fmt.Sprintf("$%s contributed", amountStr), fmt.Sprintf("You've contributed $%s total. Consistency wins.", amountStr)
	case entities.MilestoneTypeGain:
		return fmt.Sprintf("$%s in gains", amountStr), fmt.Sprintf("Your investments have earned $%s. Your money is working.", amountStr)
	default:
		return "Milestone reached", fmt.Sprintf("You've reached a $%s milestone.", amountStr)
	}
}
