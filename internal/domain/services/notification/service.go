package notification

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// EmailSenderService defines email sending operations
type EmailSenderService interface {
	SendGenericEmail(ctx context.Context, to, subject, body string) error
}

// NotificationPersister persists notifications to the database
type NotificationPersister interface {
	Create(ctx context.Context, userID uuid.UUID, notifType, priority, title, body string, data map[string]interface{}) error
}

// PushSender sends push notifications
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// UserEmailLookup resolves a user's email address from their ID
type UserEmailLookup interface {
	GetEmailByUserID(ctx context.Context, userID uuid.UUID) (string, error)
}

// emailWorthy notification types that should also trigger an email
var emailWorthyTypes = map[string]bool{
	"deposit_confirmed":    true,
	"allocation_complete":  true,
	"allocation_failed":    true,
	"withdrawal_completed": true,
	"withdrawal_failed":    true,
	"yield_credited":       true,
	"offramp_success":      true,
	"offramp_failure":      true,
	"stash_window_open":    true,
}

// Metrics tracks notification delivery counts.
type Metrics struct {
	mu          sync.Mutex
	Sent        map[string]int64 // keyed by "channel:type"
	Failed      map[string]int64
	Persisted   int64
	PersistFail int64
}

func newMetrics() *Metrics {
	return &Metrics{Sent: make(map[string]int64), Failed: make(map[string]int64)}
}

func (m *Metrics) incSent(channel, notifType string) {
	m.mu.Lock()
	m.Sent[channel+":"+notifType]++
	m.mu.Unlock()
}

func (m *Metrics) incFailed(channel, notifType string) {
	m.mu.Lock()
	m.Failed[channel+":"+notifType]++
	m.mu.Unlock()
}

// Snapshot returns a copy of current metrics.
func (m *Metrics) Snapshot() (sent, failed map[string]int64, persisted, persistFail int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sent = make(map[string]int64, len(m.Sent))
	for k, v := range m.Sent {
		sent[k] = v
	}
	failed = make(map[string]int64, len(m.Failed))
	for k, v := range m.Failed {
		failed[k] = v
	}
	return sent, failed, m.Persisted, m.PersistFail
}

// NotificationService orchestrates notification delivery via push, email, and in-app persistence.
//
// Required dependencies (must be set before use):
//   - Persister: in-app notification storage (SetPersister)
//   - PushSender: Expo push delivery (SetPushSender)
//
// Optional dependencies:
//   - EmailSender + UserEmailLookup: email delivery for important events (SetEmailSender, SetUserEmailLookup)
type NotificationService struct {
	logger          *zap.Logger
	emailSender     EmailSenderService
	persister       NotificationPersister
	pushSender      PushSender
	userEmailLookup UserEmailLookup
	metrics         *Metrics

	// wg tracks in-flight background sends for graceful shutdown.
	wg sync.WaitGroup
}

func NewNotificationService(logger *zap.Logger) *NotificationService {
	return &NotificationService{logger: logger, metrics: newMetrics()}
}

// SetEmailSender sets the email sender
func (s *NotificationService) SetEmailSender(sender EmailSenderService) { s.emailSender = sender }

// SetPersister sets the notification persister for in-app notifications
func (s *NotificationService) SetPersister(p NotificationPersister) { s.persister = p }

// SetPushSender sets the push notification sender (Expo Push)
func (s *NotificationService) SetPushSender(sender PushSender) { s.pushSender = sender }

// SetUserEmailLookup sets the user email resolver for email notifications
func (s *NotificationService) SetUserEmailLookup(lookup UserEmailLookup) { s.userEmailLookup = lookup }

// GetMetrics returns the internal metrics tracker.
func (s *NotificationService) GetMetrics() *Metrics { return s.metrics }

// Shutdown waits for in-flight background sends to complete (with timeout).
func (s *NotificationService) Shutdown(timeout time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		s.logger.Warn("notification service shutdown timed out, some sends may be lost")
	}
}

// Send dispatches a notification respecting user preferences.
// If prefs is nil, the notification is always sent (preferences not loaded).
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
	// If preferences are not loaded, default to sending.
	if prefs == nil {
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
	if s.emailSender == nil || s.userEmailLookup == nil {
		s.logger.Debug("Email sender or lookup not configured, skipping email", zap.String("user_id", notification.UserID.String()))
		return nil
	}
	email, err := s.userEmailLookup.GetEmailByUserID(ctx, notification.UserID)
	if err != nil || email == "" {
		s.logger.Debug("No email for user, skipping", zap.String("user_id", notification.UserID.String()))
		return nil
	}
	return s.emailSender.SendGenericEmail(ctx, email, notification.Title, notification.Body)
}

func (s *NotificationService) sendPush(ctx context.Context, notification *entities.Notification) error {
	return s.queueNotification(ctx, notification.UserID, "push", notification.Title, notification.Body, notification.Data)
}

func (s *NotificationService) sendSMS(ctx context.Context, notification *entities.Notification) error {
	// SMS channel is not currently wired; persist as in-app instead.
	s.logger.Debug("SMS channel not supported, falling back to in-app", zap.String("user_id", notification.UserID.String()))
	return s.sendInApp(ctx, notification)
}

func (s *NotificationService) sendInApp(ctx context.Context, notification *entities.Notification) error {
	if s.persister != nil {
		return s.persister.Create(ctx, notification.UserID, string(notification.Type), string(notification.Priority), notification.Title, notification.Body, notification.Data)
	}
	s.logger.Info("No persister configured, in-app notification dropped", zap.String("user_id", notification.UserID.String()))
	return nil
}

// queueNotification is the core delivery path for convenience methods.
// It persists in-app, sends push via Expo, and emails for important event types.
func (s *NotificationService) queueNotification(ctx context.Context, userID uuid.UUID, notifType, title, body string, data map[string]interface{}) error {
	// 1. Always persist to in-app notification center
	if s.persister != nil {
		if err := s.persister.Create(ctx, userID, notifType, "medium", title, body, data); err != nil {
			s.logger.Warn("Failed to persist notification", zap.Error(err))
			s.metrics.mu.Lock()
			s.metrics.PersistFail++
			s.metrics.mu.Unlock()
		} else {
			s.metrics.mu.Lock()
			s.metrics.Persisted++
			s.metrics.mu.Unlock()
		}
	}

	// 2. Send push notification via Expo
	if notifType == "push" && s.pushSender != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.pushSender.SendToUser(bgCtx, userID, title, body, data); err != nil {
				s.logger.Error("Failed to send push notification",
					zap.Error(err), zap.String("user_id", userID.String()))
				s.metrics.incFailed("push", notifType)
			} else {
				s.metrics.incSent("push", notifType)
			}
		}()
	}

	// 3. Send email for important event types
	eventType, _ := data["type"].(string)
	if s.emailSender != nil && s.userEmailLookup != nil && emailWorthyTypes[eventType] {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			email, err := s.userEmailLookup.GetEmailByUserID(bgCtx, userID)
			if err != nil || email == "" {
				s.logger.Debug("No email for user, skipping email notification", zap.String("user_id", userID.String()))
				return
			}
			if err := s.emailSender.SendGenericEmail(bgCtx, email, title, body); err != nil {
				s.logger.Error("Failed to send email notification",
					zap.Error(err), zap.String("user_id", userID.String()))
				s.metrics.incFailed("email", eventType)
			} else {
				s.metrics.incSent("email", eventType)
			}
		}()
	}

	return nil
}

// --- Convenience notification methods ---

func (s *NotificationService) SendWeeklySummary(ctx context.Context, userID uuid.UUID, weekStart time.Time) error {
	title := "Miriam's weekly recap"
	body := fmt.Sprintf("Your week of %s is ready. Miriam kept the receipts.", weekStart.Format("Jan 2, 2006"))
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "weekly_summary"})
}

func (s *NotificationService) NotifyOffRampSuccess(ctx context.Context, userID uuid.UUID, amount string) error {
	title := "Withdrawal landed"
	body := fmt.Sprintf("$%s is processed. Miriam can stop staring at the progress bar.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "offramp_success", "amount": amount})
}

func (s *NotificationService) NotifyOffRampFailure(ctx context.Context, userID uuid.UUID, reason string) error {
	title := "Withdrawal needs attention"
	body := fmt.Sprintf("Miriam hit a snag: %s", reason)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "offramp_failure"})
}

func (s *NotificationService) NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error {
	title := "Miriam blocked a spend"
	body := fmt.Sprintf("That %s for $%s crossed your spending limits.", transactionType, amount.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "transaction_declined"})
}

func (s *NotificationService) NotifyDepositConfirmed(ctx context.Context, userID uuid.UUID, amount, chain, txHash string) error {
	title := "Deposit made it in"
	body := fmt.Sprintf("%s arrived. Miriam is already sorting the money lanes.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "deposit_confirmed", "tx_hash": txHash, "chain": chain})
}

func (s *NotificationService) NotifyDepositDetected(ctx context.Context, userID uuid.UUID, chain string) error {
	title := "Miriam sees money inbound"
	body := "Incoming deposit spotted. Confirmation is the next little checkpoint."
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "deposit_detected", "chain": chain})
}

func (s *NotificationService) NotifyWithdrawalSubmitted(ctx context.Context, userID uuid.UUID, amount string) error {
	title := "Withdrawal is moving"
	body := fmt.Sprintf("$%s is submitted. Miriam is watching the handoff.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_submitted"})
}

func (s *NotificationService) NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error {
	title := "Withdrawal sent"
	body := fmt.Sprintf("$%s is on its way. Clean exit.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_completed"})
}

func (s *NotificationService) NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error {
	title := "Withdrawal needs a retry"
	body := fmt.Sprintf("$%s could not be processed. Miriam saved the context.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "withdrawal_failed"})
}

func (s *NotificationService) NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	title := "Balance changed"
	body := fmt.Sprintf("%s of $%s processed. New balance is $%s.", changeType, amount.String(), newBalance.StringFixed(2))
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "balance_change"})
}

func (s *NotificationService) NotifyAllocationFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, depositID uuid.UUID, reason string) error {
	s.logger.Error("Allocation failed notification",
		zap.String("user_id", userID.String()),
		zap.String("deposit_id", depositID.String()),
		zap.String("amount", amount.String()),
		zap.String("failure_reason", reason))

	title := "Miriam needs backup"
	body := fmt.Sprintf("$%s arrived, but the automatic split could not complete. Contact support.", amount.String())
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{
		"type":       "allocation_failed",
		"deposit_id": depositID.String(),
		"amount":     amount.String(),
	})
}

func (s *NotificationService) NotifyKYCApproved(ctx context.Context, userID uuid.UUID) error {
	title := "Miriam unlocked the doors"
	body := "Identity verified. Deposits, withdrawals, and the real Rail flow are open."
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "kyc_approved"})
}

func (s *NotificationService) NotifyKYCRejected(ctx context.Context, userID uuid.UUID) error {
	title := "Verification needs a redo"
	body := "Miriam could not verify this round. Check the documents and try again."
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "kyc_rejected"})
}

func (s *NotificationService) NotifyAllocationComplete(ctx context.Context, userID uuid.UUID, spendAmount, investAmount string) error {
	title := "Split engine did it"
	body := fmt.Sprintf("$%s to Spend, $%s to Invest. Miriam loves clean automation.", spendAmount, investAmount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "allocation_complete"})
}

func (s *NotificationService) NotifyInvestmentComplete(ctx context.Context, userID uuid.UUID, amount string) error {
	title := "Investment placed"
	body := fmt.Sprintf("$%s invested automatically. Miriam handled the decision fatigue.", amount)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "investment_complete"})
}

func (s *NotificationService) NotifyCardTransaction(ctx context.Context, userID uuid.UUID, amount, merchant string) error {
	title := "Miriam saw the swipe"
	body := fmt.Sprintf("$%s at %s. Receipt energy logged.", amount, merchant)
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "card_transaction"})
}

func (s *NotificationService) NotifyYieldCredited(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	title := "Tiny Stash win"
	body := fmt.Sprintf("$%s in yield landed in Stash. Quiet compounding moment.", amount.StringFixed(2))
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "yield_credited", "amount": amount.String()})
}

func (s *NotificationService) NotifyStashWindowOpen(ctx context.Context, userID uuid.UUID, windowEnd time.Time) error {
	title := "Stash window is open"
	daysLeft := int(time.Until(windowEnd).Hours()/24) + 1
	body := fmt.Sprintf("%d days to move Stash money before it locks again on %s.", daysLeft, windowEnd.Format("Jan 2"))
	return s.queueNotification(ctx, userID, "push", title, body, map[string]interface{}{"type": "stash_window_open", "window_end": windowEnd.Format(time.RFC3339)})
}

func (s *NotificationService) NotifyP2PClaimed(ctx context.Context, senderID uuid.UUID, recipientName, amount string) error {
	body := fmt.Sprintf("%s claimed your %s transfer. Miriam can close that loop.", recipientName, amount)
	return s.queueNotification(ctx, senderID, "push", "Transfer claimed", body, map[string]interface{}{"type": "p2p_claimed"})
}

// NotifyMiriamMovedFunds notifies the user when Miriam (the financial agent)
// executes a money-moving action on their behalf. Covers both confirmed
// chat-flow actions and voice-direct executions.
//
// emergency reflects params.impact.emergency_withdrawal so the user gets the
// "stash unlocked early" framing rather than a vanilla transfer message; PAJ
// withdrawals go through the regular NotifyWithdrawalSubmitted/Completed
// pipeline and are intentionally not handled here.
func (s *NotificationService) NotifyMiriamMovedFunds(ctx context.Context, userID uuid.UUID, action, from, to string, amount decimal.Decimal, emergency, succeeded bool, errMsg string) error {
	amountStr := amount.StringFixed(2)
	var title, body string

	switch {
	case !succeeded && action == "transfer_funds":
		title = "Miriam couldn't finish that"
		body = fmt.Sprintf("Tried to move $%s %s → %s but hit a snag. Tap to retry.", amountStr, friendlyAccountLabel(from), friendlyAccountLabel(to))
	case !succeeded:
		title = "Miriam couldn't finish that"
		body = "An action failed. Open the chat to see what happened."
	case action == "transfer_funds" && emergency:
		title = "Stash unlocked early"
		body = fmt.Sprintf("Miriam pulled $%s from stash to spend (early-withdrawal fee applies).", amountStr)
	case action == "transfer_funds":
		title = "Miriam moved your money"
		body = fmt.Sprintf("$%s went from %s to %s. Tap to see the receipt.", amountStr, friendlyAccountLabel(from), friendlyAccountLabel(to))
	default:
		title = "Miriam took action"
		body = "Tap to see what changed."
	}

	data := map[string]interface{}{
		"type":      "miriam_action",
		"action":    action,
		"amount":    amountStr,
		"from":      from,
		"to":        to,
		"emergency": emergency,
		"succeeded": succeeded,
	}
	if errMsg != "" {
		data["error_message"] = errMsg
	}
	return s.queueNotification(ctx, userID, "push", title, body, data)
}

func friendlyAccountLabel(s string) string {
	switch s {
	case "spend":
		return "Spend"
	case "stash":
		return "Stash"
	}
	if s == "" {
		return "Balance"
	}
	return s
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
			return "First $100 invested", "Miriam saw the first three digits. Tiny number, serious signal."
		case amount.Equal(decimal.NewFromInt(500)):
			return "$500 milestone", "Half a thousand invested. Miriam is doing the respectful nod."
		case amount.Equal(decimal.NewFromInt(1000)):
			return "$1,000 invested", "Four figures invested. That is not beginner energy anymore."
		case amount.Equal(decimal.NewFromInt(5000)):
			return "$5,000 invested", "Five thousand invested. Miriam is officially paying attention."
		case amount.Equal(decimal.NewFromInt(10000)):
			return "$10,000 milestone", "Five figures invested. Quiet flex, loud discipline."
		default:
			return fmt.Sprintf("$%s milestone", amountStr), fmt.Sprintf("$%s invested. Miriam marked the receipt.", amountStr)
		}
	case entities.MilestoneTypeContribution:
		return fmt.Sprintf("$%s contributed", amountStr), fmt.Sprintf("$%s contributed total. Consistency is doing the heavy lifting.", amountStr)
	case entities.MilestoneTypeGain:
		return fmt.Sprintf("$%s in gains", amountStr), fmt.Sprintf("$%s in gains. Miriam likes when the graph behaves.", amountStr)
	default:
		return "Milestone reached", fmt.Sprintf("$%s milestone reached. Miriam kept the confetti tasteful.", amountStr)
	}
}
