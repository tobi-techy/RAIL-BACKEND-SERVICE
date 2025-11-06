package services

import (
	"context"

	"github.com/google/uuid"
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
