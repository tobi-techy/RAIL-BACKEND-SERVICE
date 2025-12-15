package allocation

import (
	"context"
	"fmt"
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
		Warning:  decimal.NewFromFloat(0.80), // 80%
		Critical: decimal.NewFromFloat(0.95), // 95%
		Depleted: decimal.NewFromInt(1),      // 100%
	}
}

// NotificationService interface for sending notifications
type NotificationService interface {
	Send(ctx context.Context, notification *entities.Notification, prefs *entities.UserPreference) error
}

// NotificationManager handles allocation-related notifications
type NotificationManager struct {
	notificationService NotificationService
	thresholds          NotificationThresholds
	logger              *logger.Logger
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

	// Calculate spending percentage
	if totalSpending.IsZero() || totalSpending.IsNegative() {
		nm.logger.Debug("No spending allocation to check",
			"user_id", userID,
			"total_spending", totalSpending)
		return nil
	}

	spendingPercentage := spendingUsed.Div(totalSpending)
	
	nm.logger.Debug("Checking spending thresholds",
		"user_id", userID,
		"spending_used", spendingUsed,
		"total_spending", totalSpending,
		"percentage", spendingPercentage)

	// Check thresholds in order (100% -> 95% -> 80%)
	if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Depleted) {
		return nm.notifySpendingDepleted(ctx, userID, spendingBalance, totalSpending)
	} else if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Critical) {
		return nm.notifySpendingCritical(ctx, userID, spendingBalance, spendingPercentage, totalSpending)
	} else if spendingPercentage.GreaterThanOrEqual(nm.thresholds.Warning) {
		return nm.notifySpendingWarning(ctx, userID, spendingBalance, spendingPercentage, totalSpending)
	}

	return nil
}

// notifySpendingWarning sends 80% threshold warning notification
func (nm *NotificationManager) notifySpendingWarning(
	ctx context.Context,
	userID uuid.UUID,
	remainingBalance decimal.Decimal,
	percentage decimal.Decimal,
	totalSpending decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingWarning")
	defer span.End()

	percentageFormatted := percentage.Mul(decimal.NewFromInt(100)).StringFixed(0)
	
	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityMedium,
		Title:    "Spending Limit Warning",
		Message: fmt.Sprintf(
			"You're nearing your spending limit (%s%% used). $%s remaining in your spending balance.",
			percentageFormatted,
			remainingBalance.StringFixed(2),
		),
		Data: map[string]interface{}{
			"threshold":         "warning",
			"percentage":        percentage.String(),
			"remaining_balance": remainingBalance.String(),
			"total_spending":    totalSpending.String(),
			"threshold_type":    "80_percent",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Info("Sending 80% spending threshold notification",
		"user_id", userID,
		"percentage", percentageFormatted,
		"remaining", remainingBalance)

	// Send notification (will respect user preferences)
	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		nm.logger.Error("Failed to send warning notification",
			"error", err,
			"user_id", userID)
		return fmt.Errorf("failed to send warning notification: %w", err)
	}

	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// notifySpendingCritical sends 95% threshold critical notification
func (nm *NotificationManager) notifySpendingCritical(
	ctx context.Context,
	userID uuid.UUID,
	remainingBalance decimal.Decimal,
	percentage decimal.Decimal,
	totalSpending decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingCritical")
	defer span.End()

	percentageFormatted := percentage.Mul(decimal.NewFromInt(100)).StringFixed(0)
	
	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityHigh,
		Title:    "Spending Limit Critical",
		Message: fmt.Sprintf(
			"You're very close to your spending limit (%s%% used). Only $%s remaining.",
			percentageFormatted,
			remainingBalance.StringFixed(2),
		),
		Data: map[string]interface{}{
			"threshold":         "critical",
			"percentage":        percentage.String(),
			"remaining_balance": remainingBalance.String(),
			"total_spending":    totalSpending.String(),
			"threshold_type":    "95_percent",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Warn("Sending 95% spending threshold notification",
		"user_id", userID,
		"percentage", percentageFormatted,
		"remaining", remainingBalance)

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		nm.logger.Error("Failed to send critical notification",
			"error", err,
			"user_id", userID)
		return fmt.Errorf("failed to send critical notification: %w", err)
	}

	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// notifySpendingDepleted sends 100% threshold depleted notification
func (nm *NotificationManager) notifySpendingDepleted(
	ctx context.Context,
	userID uuid.UUID,
	remainingBalance decimal.Decimal,
	totalSpending decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.notifySpendingDepleted")
	defer span.End()

	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityCritical,
		Title:    "Spending Limit Reached",
		Message: "You've reached your 70% spending limit. Your 30% savings remain protected.",
		Data: map[string]interface{}{
			"threshold":         "depleted",
			"percentage":        "100",
			"remaining_balance": remainingBalance.String(),
			"total_spending":    totalSpending.String(),
			"threshold_type":    "100_percent",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Warn("Sending 100% spending threshold notification",
		"user_id", userID,
		"remaining", remainingBalance)

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		nm.logger.Error("Failed to send depleted notification",
			"error", err,
			"user_id", userID)
		return fmt.Errorf("failed to send depleted notification: %w", err)
	}

	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// NotifyTransactionDeclined sends notification when transaction is declined due to spending limit
func (nm *NotificationManager) NotifyTransactionDeclined(
	ctx context.Context,
	userID uuid.UUID,
	amount decimal.Decimal,
	transactionType string,
) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyTransactionDeclined",
		trace.WithAttributes(
			attribute.String("user_id", userID.String()),
			attribute.String("amount", amount.String()),
			attribute.String("transaction_type", transactionType),
		))
	defer span.End()

	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityCritical,
		Title:    "Transaction Declined",
		Message: fmt.Sprintf(
			"Your %s of $%s was declined. You've reached your spending limit. Your stash is safe.",
			transactionType,
			amount.StringFixed(2),
		),
		Data: map[string]interface{}{
			"declined_amount":    amount.String(),
			"transaction_type":   transactionType,
			"reason":             "spending_limit_reached",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Warn("Sending transaction declined notification",
		"user_id", userID,
		"amount", amount,
		"type", transactionType)

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		nm.logger.Error("Failed to send declined notification",
			"error", err,
			"user_id", userID)
		return fmt.Errorf("failed to send declined notification: %w", err)
	}

	span.SetAttributes(attribute.String("notification_id", notification.ID.String()))
	return nil
}

// NotifyModeEnabled sends notification when allocation mode is enabled
func (nm *NotificationManager) NotifyModeEnabled(
	ctx context.Context,
	userID uuid.UUID,
	spendingRatio decimal.Decimal,
	stashRatio decimal.Decimal,
) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyModeEnabled")
	defer span.End()

	spendingPercent := spendingRatio.Mul(decimal.NewFromInt(100)).StringFixed(0)
	stashPercent := stashRatio.Mul(decimal.NewFromInt(100)).StringFixed(0)

	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityMedium,
		Title:    "Smart Allocation Enabled",
		Message: fmt.Sprintf(
			"Your funds will now be split: %s%% for spending, %s%% saved automatically.",
			spendingPercent,
			stashPercent,
		),
		Data: map[string]interface{}{
			"spending_ratio": spendingRatio.String(),
			"stash_ratio":    stashRatio.String(),
			"mode_status":    "enabled",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Info("Sending mode enabled notification", "user_id", userID)

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send mode enabled notification: %w", err)
	}

	return nil
}

// NotifyModePaused sends notification when allocation mode is paused
func (nm *NotificationManager) NotifyModePaused(
	ctx context.Context,
	userID uuid.UUID,
) error {
	ctx, span := tracer.Start(ctx, "allocation.NotifyModePaused")
	defer span.End()

	notification := &entities.Notification{
		ID:       uuid.New(),
		UserID:   userID,
		Type:     entities.NotificationTypePortfolio,
		Channel:  entities.ChannelPush,
		Priority: entities.PriorityLow,
		Title:    "Smart Allocation Paused",
		Message:  "Your allocation mode has been paused. New deposits won't be split automatically.",
		Data: map[string]interface{}{
			"mode_status": "paused",
		},
		CreatedAt: time.Now(),
	}

	nm.logger.Info("Sending mode paused notification", "user_id", userID)

	if err := nm.notificationService.Send(ctx, notification, nil); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to send mode paused notification: %w", err)
	}

	return nil
}
