package allocation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// NotificationThresholds defines spending percentage thresholds for notifications
type NotificationThresholds struct {
	Warning  decimal.Decimal // 80% threshold
	Critical decimal.Decimal // 95% threshold
	Depleted decimal.Decimal // 100% threshold
}

// DefaultNotificationThresholds returns the standard threshold configuration
func DefaultNotificationThresholds() NotificationThresholds {
	return NotificationThresholds{
		Warning:  decimal.NewFromFloat(0.80),
		Critical: decimal.NewFromFloat(0.95),
		Depleted: decimal.NewFromInt(1),
	}
}

// NotificationService interface for sending notifications
type NotificationService interface {
	Send(ctx context.Context, notification *entities.Notification, prefs *entities.UserPreference) error
}

// NotificationManager handles allocation-related notifications with deduplication.
type NotificationManager struct {
	notificationService NotificationService
	thresholds          NotificationThresholds
	logger              *logger.Logger

	// dedup prevents sending the same threshold notification repeatedly.
	// Key: "userID:threshold", Value: time the notification was last sent.
	mu    sync.Mutex
	dedup map[string]time.Time
	// dedupTTL is how long to suppress duplicate threshold notifications.
	dedupTTL time.Duration
}

// NewNotificationManager creates a new notification manager for allocations
func NewNotificationManager(
	notificationService NotificationService,
	logger *logger.Logger,
) *NotificationManager {
	return &NotificationManager{
		notificationService: notificationService,
		thresholds:          DefaultNotificationThresholds(),
		logger:              logger,
		dedup:               make(map[string]time.Time),
		dedupTTL:            1 * time.Hour,
	}
}

// dedupKey returns a deduplication key for a user + threshold level.
func dedupKey(userID uuid.UUID, threshold string) string {
	return userID.String() + ":" + threshold
}

// shouldNotify returns true if this threshold notification hasn't been sent recently.
func (nm *NotificationManager) shouldNotify(userID uuid.UUID, threshold string) bool {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	key := dedupKey(userID, threshold)
	if lastSent, ok := nm.dedup[key]; ok && time.Since(lastSent) < nm.dedupTTL {
		return false
	}
	nm.dedup[key] = time.Now()
	return true
}

// resetDedup clears dedup state for a user (e.g., after a new deposit resets spending).
func (nm *NotificationManager) ResetDedup(userID uuid.UUID) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	for _, t := range []string{"warning", "critical", "depleted"} {
		delete(nm.dedup, dedupKey(userID, t))
	}
}

// CheckAndNotifyThresholds checks spending balance and sends notifications if thresholds are crossed
func (nm *NotificationManager) CheckAndNotifyThresholds(
	ctx context.Context,
	userID uuid.UUID,
	spendingBalance decimal.Decimal,
	spendingUsed decimal.Decimal,
	totalSpending decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.CheckAndNotifyThresholds",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("spending_balance", spendingBalance.String()),
			attribute.String("spending_used", spendingUsed.String()),
		))
	defer span.End()

	if totalSpending.IsZero() || totalSpending.IsNegative() {
		return nil
	}

	spendingPercentage := spendingUsed.Div(totalSpending)

	if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Depleted) {
		return nm.notifySpendingDepleted(ctx, userID, spendingBalance, totalSpending)
	} else if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Critical) {
		return nm.notifySpendingCritical(ctx, userID, spendingBalance, spendingPercentage, totalSpending)
	} else if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Warning) {
		return nm.notifySpendingWarning(ctx, userID, spendingBalance, spendingPercentage, totalSpending)
	}

	return nil
}

func (nm *NotificationManager) notifySpendingWarning(
	ctx context.Context, userID uuid.UUID,
	remainingBalance, percentage, totalSpending decimal.Decimal,
) error {
	if !nm.shouldNotify(userID, "warning") {
		return nil
	}
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingWarning")
	defer span.End()

	pct := percentage.Mul(decimal.NewFromInt(100)).StringFixed(0)
	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityMedium,
		Title:    "Spending Limit Warning",
		Body:    fmt.Sprintf("You're nearing your spending limit (%s%% used). $%s remaining in your spending balance.", pct, remainingBalance.StringFixed(2)),
		Data: map[string]interface{}{
			"type": "spending_warning", "threshold": "warning", "percentage": percentage.String(),
			"remaining_balance": remainingBalance.String(), "total_spending": totalSpending.String(), "threshold_type": "80_percent",
		},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send warning notification: %w", err)
	}
	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

func (nm *NotificationManager) notifySpendingCritical(
	ctx context.Context, userID uuid.UUID,
	remainingBalance, percentage, totalSpending decimal.Decimal,
) error {
	if !nm.shouldNotify(userID, "critical") {
		return nil
	}
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingCritical")
	defer span.End()

	pct := percentage.Mul(decimal.NewFromInt(100)).StringFixed(0)
	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityHigh,
		Title:    "Spending Limit Critical",
		Body:    fmt.Sprintf("You're very close to your spending limit (%s%% used). Only $%s remaining.", pct, remainingBalance.StringFixed(2)),
		Data: map[string]interface{}{
			"type": "spending_critical", "threshold": "critical", "percentage": percentage.String(),
			"remaining_balance": remainingBalance.String(), "total_spending": totalSpending.String(), "threshold_type": "95_percent",
		},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send critical notification: %w", err)
	}
	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

func (nm *NotificationManager) notifySpendingDepleted(
	ctx context.Context, userID uuid.UUID,
	remainingBalance, totalSpending decimal.Decimal,
) error {
	if !nm.shouldNotify(userID, "depleted") {
		return nil
	}
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingDepleted")
	defer span.End()

	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityCritical,
		Title:    "Spending Limit Reached",
		Body:    "You've reached your 70% spending limit. Your 30% savings remain protected.",
		Data: map[string]interface{}{
			"type": "spending_depleted", "threshold": "depleted", "percentage": "100",
			"remaining_balance": remainingBalance.String(), "total_spending": totalSpending.String(), "threshold_type": "100_percent",
		},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send depleted notification: %w", err)
	}
	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// NotifyTransactionDeclined sends notification when transaction is declined due to spending limit
func (nm *NotificationManager) NotifyTransactionDeclined(
	ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string,
) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyTransactionDeclined",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
			attribute.String("transaction_type", transactionType),
		))
	defer span.End()

	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityCritical,
		Title:    "Transaction Declined",
		Body:    fmt.Sprintf("Your %s of $%s was declined. You've reached your spending limit. Your stash is safe.", transactionType, amount.StringFixed(2)),
		Data: map[string]interface{}{
			"declined_amount": amount.String(), "transaction_type": transactionType, "reason": "spending_limit_reached",
		},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send declined notification: %w", err)
	}
	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// NotifyModeEnabled sends notification when allocation mode is enabled
func (nm *NotificationManager) NotifyModeEnabled(
	ctx context.Context, userID uuid.UUID, spendingRatio, stashRatio decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyModeEnabled")
	defer span.End()

	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityMedium,
		Title:    "Smart Allocation Enabled",
		Body: fmt.Sprintf("Your funds will now be split: %s%% for spending, %s%% saved automatically.",
			spendingRatio.Mul(decimal.NewFromInt(100)).StringFixed(0),
			stashRatio.Mul(decimal.NewFromInt(100)).StringFixed(0)),
		Data: map[string]interface{}{
			"spending_ratio": spendingRatio.String(), "stash_ratio": stashRatio.String(), "mode_status": "enabled",
		},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send mode enabled notification: %w", err)
	}
	return nil
}

// NotifyModePaused sends notification when allocation mode is paused
func (nm *NotificationManager) NotifyModePaused(ctx context.Context, userID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyModePaused")
	defer span.End()

	notification := &entities.Notification{
		ID: uuid.New(), UserID: userID,
		Type: entities.NotificationTypePortfolio, Channel: entities.ChannelPush,
		Priority: entities.PriorityLow,
		Title:    "Smart Allocation Paused",
		Body:    "Your allocation mode has been paused. New deposits won't be split automatically.",
		Data:     map[string]interface{}{"mode_status": "paused"},
		CreatedAt: time.Now(),
	}

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send mode paused notification: %w", err)
	}
	return nil
}
