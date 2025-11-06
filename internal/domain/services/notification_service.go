package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/pkg/logger"
)

// NotificationService handles user notifications
type NotificationService struct {
	logger *logger.Logger
}

// NewNotificationService creates a new notification service
func NewNotificationService(logger *logger.Logger) *NotificationService {
	return &NotificationService{
		logger: logger,
	}
}

// NotifyOffRampSuccess sends success notification
func (s *NotificationService) NotifyOffRampSuccess(ctx context.Context, userID uuid.UUID, amount string) error {
	s.logger.Info("Off-ramp completed successfully",
		"user_id", userID.String(),
		"amount", amount,
		"notification_type", "off_ramp_success")
	
	// Notification logged - actual delivery (push, email, SMS) would be implemented here
	// Integration points: SendGrid, Twilio, Firebase Cloud Messaging, etc.
	return nil
}

// NotifyOffRampFailure sends failure notification
func (s *NotificationService) NotifyOffRampFailure(ctx context.Context, userID uuid.UUID, reason string) error {
	s.logger.Warn("Off-ramp failed",
		"user_id", userID.String(),
		"reason", reason,
		"notification_type", "off_ramp_failure")
	
	// Notification logged - actual delivery (push, email, SMS) would be implemented here
	// Integration points: SendGrid, Twilio, Firebase Cloud Messaging, etc.
	return nil
}

// NotifyFundingSuccess notifies user of successful Alpaca funding
func (s *NotificationService) NotifyFundingSuccess(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	s.logger.Info("Alpaca funding completed successfully",
		"user_id", userID.String(),
		"amount", amount.StringFixed(2),
		"notification_type", "funding_success")
	
	// Notification logged - actual delivery would be implemented here
	return nil
}

// NotifyFundingFailure notifies user of failed Alpaca funding
func (s *NotificationService) NotifyFundingFailure(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, reason string) error {
	s.logger.Error("Alpaca funding failed",
		"user_id", userID.String(),
		"deposit_id", depositID.String(),
		"reason", reason,
		"notification_type", "funding_failure")
	
	message := fmt.Sprintf("Deposit %s funding failed: %s", depositID.String(), reason)
	s.logger.Info("Funding failure notification queued", "message", message)
	
	// Notification logged - actual delivery would be implemented here
	return nil
}
