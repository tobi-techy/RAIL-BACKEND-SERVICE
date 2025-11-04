package recovery

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles fund recovery operations with multi-factor verification
type Service struct {
	recoveryRepo    RecoveryRepository
	userRepo        UserRepository
	verificationSvc VerificationService
	emailSvc        EmailService
	auditSvc        AuditService
	logger          *logger.Logger
}

// RecoveryRepository interface for recovery operations
type RecoveryRepository interface {
	CreateRecoveryRequest(ctx context.Context, req *entities.RecoveryRequest) error
	GetRecoveryRequest(ctx context.Context, id uuid.UUID) (*entities.RecoveryRequest, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.RecoveryRequest, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.RecoveryStatus, processedBy *uuid.UUID, notes *string) error
	CreateRecoveryAction(ctx context.Context, action *entities.RecoveryAction) error
	GetRecoveryActions(ctx context.Context, recoveryID uuid.UUID) ([]*entities.RecoveryAction, error)
}

// UserRepository interface for user operations
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
	GetByEmail(ctx context.Context, email string) (*entities.User, error)
	UpdateRecoveryCodes(ctx context.Context, userID uuid.UUID, codes []string) error
}

// VerificationService interface for multi-factor verification
type VerificationService interface {
	SendVerificationCode(ctx context.Context, identifier, method string) error
	VerifyCode(ctx context.Context, identifier, code string) (bool, error)
	GenerateRecoveryCodes(ctx context.Context, count int) ([]string, error)
}

// EmailService interface for email notifications
type EmailService interface {
	SendRecoveryNotification(ctx context.Context, email, subject, body string) error
}

// AuditService interface for audit logging
type AuditService interface {
	LogRecoveryEvent(ctx context.Context, eventType, userID, recoveryID string, details map[string]interface{}) error
}

// NewService creates a new recovery service
func NewService(
	recoveryRepo RecoveryRepository,
	userRepo UserRepository,
	verificationSvc VerificationService,
	emailSvc EmailService,
	auditSvc AuditService,
	logger *logger.Logger,
) *Service {
	return &Service{
		recoveryRepo:    recoveryRepo,
		userRepo:        userRepo,
		verificationSvc: verificationSvc,
		emailSvc:        emailSvc,
		auditSvc:        auditSvc,
		logger:          logger,
	}
}

// InitiateRecovery starts the fund recovery process
func (s *Service) InitiateRecovery(ctx context.Context, email string, reason string) (*entities.RecoveryInitiationResponse, error) {
	// Get user by email
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	// Check if user has any active recovery requests
	existingRequests, err := s.recoveryRepo.GetByUserID(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing requests: %w", err)
	}

	for _, req := range existingRequests {
		if req.Status == entities.RecoveryStatusPending || req.Status == entities.RecoveryStatusInReview {
			return nil, fmt.Errorf("active recovery request already exists")
		}
	}

	// Generate recovery request ID
	requestID := uuid.New()

	// Create recovery request
	recoveryReq := &entities.RecoveryRequest{
		ID:                    requestID,
		UserID:                user.ID,
		RecoveryType:          entities.RecoveryTypeWalletAccess,
		Status:                entities.RecoveryStatusPending,
		Priority:              entities.RecoveryPriorityHigh,
		Reason:                reason,
		ExpiresAt:             time.Now().Add(24 * time.Hour), // 24 hours to complete
		RequiredVerifications: []string{"email", "phone", "document"},
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	if err := s.recoveryRepo.CreateRecoveryRequest(ctx, recoveryReq); err != nil {
		return nil, fmt.Errorf("failed to create recovery request: %w", err)
	}

	// Send initial verification code to email
	if err := s.verificationSvc.SendVerificationCode(ctx, email, "email"); err != nil {
		s.logger.Warnw("Failed to send verification code", "error", err, "email", email)
	}

	// Send notification email
	subject := "STACK Fund Recovery Request Initiated"
	body := fmt.Sprintf(`
Hello,

A fund recovery request has been initiated for your STACK account.

Recovery Request ID: %s
Reason: %s

To proceed with recovery, you will need to complete the following verification steps:
1. Email verification
2. Phone verification (if applicable)
3. Document verification

Please complete these steps within 24 hours.

If you did not initiate this request, please contact support immediately.

Best regards,
STACK Support Team
`, requestID.String(), reason)

	if err := s.emailSvc.SendRecoveryNotification(ctx, email, subject, body); err != nil {
		s.logger.Warnw("Failed to send recovery notification", "error", err, "email", email)
	}

	// Audit log
	s.auditSvc.LogRecoveryEvent(ctx, "recovery_initiated", user.ID.String(), requestID.String(), map[string]interface{}{
		"reason": reason,
		"email":  email,
	})

	s.logger.Infow("Recovery request initiated",
		"user_id", user.ID.String(),
		"recovery_id", requestID.String(),
		"reason", reason,
	)

	return &entities.RecoveryInitiationResponse{
		RecoveryID: requestID,
		Message:    "Recovery request initiated. Check your email for verification instructions.",
		ExpiresAt:  recoveryReq.ExpiresAt,
		NextSteps:  []string{"Verify email address", "Verify phone number", "Submit identity documents"},
	}, nil
}

// VerifyRecoveryStep verifies a single step in the recovery process
func (s *Service) VerifyRecoveryStep(ctx context.Context, recoveryID uuid.UUID, step, code string) error {
	// Get recovery request
	recoveryReq, err := s.recoveryRepo.GetRecoveryRequest(ctx, recoveryID)
	if err != nil {
		return fmt.Errorf("recovery request not found: %w", err)
	}

	if recoveryReq.Status != entities.RecoveryStatusPending {
		return fmt.Errorf("recovery request is not in pending status")
	}

	if recoveryReq.IsExpired() {
		return fmt.Errorf("recovery request has expired")
	}

	// Get user
	user, err := s.userRepo.GetByID(ctx, recoveryReq.UserID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// Verify the step
	var identifier string
	switch step {
	case "email":
		identifier = user.Email
	case "phone":
		if user.Phone == nil {
			return fmt.Errorf("phone verification not available")
		}
		identifier = *user.Phone
	default:
		return fmt.Errorf("invalid verification step: %s", step)
	}

	valid, err := s.verificationSvc.VerifyCode(ctx, identifier, code)
	if err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	if !valid {
		return fmt.Errorf("invalid verification code")
	}

	// Record the verification action
	action := &entities.RecoveryAction{
		ID:          uuid.New(),
		RecoveryID:  recoveryID,
		ActionType:  entities.RecoveryActionVerification,
		Step:        step,
		Status:      entities.RecoveryActionStatusCompleted,
		Details:     map[string]interface{}{"verified": true},
		PerformedAt: time.Now(),
		PerformedBy: &recoveryReq.UserID,
	}

	if err := s.recoveryRepo.CreateRecoveryAction(ctx, action); err != nil {
		return fmt.Errorf("failed to record verification action: %w", err)
	}

	// Update recovery request progress
	// In a real implementation, you'd check if all required verifications are complete

	s.logger.Infow("Recovery step verified",
		"recovery_id", recoveryID.String(),
		"step", step,
		"user_id", user.ID.String(),
	)

	return nil
}

// CompleteRecovery finalizes the recovery process
func (s *Service) CompleteRecovery(ctx context.Context, recoveryID uuid.UUID, adminID uuid.UUID) error {
	// Get recovery request
	recoveryReq, err := s.recoveryRepo.GetRecoveryRequest(ctx, recoveryID)
	if err != nil {
		return fmt.Errorf("recovery request not found: %w", err)
	}

	if recoveryReq.Status != entities.RecoveryStatusInReview {
		return fmt.Errorf("recovery request is not ready for completion")
	}

	// Get admin user
	admin, err := s.userRepo.GetByID(ctx, adminID)
	if err != nil {
		return fmt.Errorf("admin not found: %w", err)
	}

	if !admin.Role.HasPermission(entities.RoleAdmin) {
		return fmt.Errorf("insufficient permissions to complete recovery")
	}

	// Get user
	user, err := s.userRepo.GetByID(ctx, recoveryReq.UserID)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	// Generate new recovery codes
	recoveryCodes, err := s.verificationSvc.GenerateRecoveryCodes(ctx, 10)
	if err != nil {
		return fmt.Errorf("failed to generate recovery codes: %w", err)
	}

	// Update user with new recovery codes
	if err := s.userRepo.UpdateRecoveryCodes(ctx, user.ID, recoveryCodes); err != nil {
		return fmt.Errorf("failed to update recovery codes: %w", err)
	}

	// Update recovery request status
	notes := "Recovery completed successfully. New recovery codes generated and sent to user."
	if err := s.recoveryRepo.UpdateStatus(ctx, recoveryID, entities.RecoveryStatusCompleted, &adminID, &notes); err != nil {
		return fmt.Errorf("failed to update recovery status: %w", err)
	}

	// Record completion action
	action := &entities.RecoveryAction{
		ID:          uuid.New(),
		RecoveryID:  recoveryID,
		ActionType:  entities.RecoveryActionRecovery,
		Step:        "completion",
		Status:      entities.RecoveryActionStatusCompleted,
		Details:     map[string]interface{}{"recovery_codes_generated": len(recoveryCodes)},
		PerformedAt: time.Now(),
		PerformedBy: &adminID,
	}

	if err := s.recoveryRepo.CreateRecoveryAction(ctx, action); err != nil {
		s.logger.Warnw("Failed to record completion action", "error", err)
	}

	// Send recovery completion email with new codes
	subject := "STACK Fund Recovery Completed"
	codesList := ""
	for i, code := range recoveryCodes {
		codesList += fmt.Sprintf("%d. %s\n", i+1, code)
	}

	body := fmt.Sprintf(`
Hello,

Your fund recovery request has been completed successfully.

Your new recovery codes are:

%s

IMPORTANT SECURITY REMINDER:
- Store these codes in a safe place
- Each code can only be used once
- If you lose access again, you'll need these codes
- Never share these codes with anyone

If you did not request this recovery, please contact support immediately.

Best regards,
STACK Support Team
`, codesList)

	if err := s.emailSvc.SendRecoveryNotification(ctx, user.Email, subject, body); err != nil {
		s.logger.Errorw("Failed to send recovery completion email", "error", err)
	}

	// Audit log
	s.auditSvc.LogRecoveryEvent(ctx, "recovery_completed", user.ID.String(), recoveryID.String(), map[string]interface{}{
		"admin_id":             adminID.String(),
		"recovery_codes_count": len(recoveryCodes),
	})

	s.logger.Infow("Recovery completed",
		"recovery_id", recoveryID.String(),
		"user_id", user.ID.String(),
		"admin_id", adminID.String(),
	)

	return nil
}

// CancelRecovery cancels a recovery request
func (s *Service) CancelRecovery(ctx context.Context, recoveryID uuid.UUID, userID uuid.UUID) error {
	// Get recovery request
	recoveryReq, err := s.recoveryRepo.GetRecoveryRequest(ctx, recoveryID)
	if err != nil {
		return fmt.Errorf("recovery request not found: %w", err)
	}

	if recoveryReq.UserID != userID {
		return fmt.Errorf("unauthorized to cancel this recovery request")
	}

	if recoveryReq.Status != entities.RecoveryStatusPending {
		return fmt.Errorf("recovery request cannot be cancelled")
	}

	// Update status to cancelled
	notes := "Cancelled by user"
	if err := s.recoveryRepo.UpdateStatus(ctx, recoveryID, entities.RecoveryStatusCancelled, &userID, &notes); err != nil {
		return fmt.Errorf("failed to cancel recovery: %w", err)
	}

	s.logger.Infow("Recovery cancelled",
		"recovery_id", recoveryID.String(),
		"user_id", userID.String(),
	)

	return nil
}

// GetRecoveryStatus returns the status of a recovery request
func (s *Service) GetRecoveryStatus(ctx context.Context, recoveryID uuid.UUID, userID uuid.UUID) (*entities.RecoveryStatusResponse, error) {
	// Get recovery request
	recoveryReq, err := s.recoveryRepo.GetRecoveryRequest(ctx, recoveryID)
	if err != nil {
		return nil, fmt.Errorf("recovery request not found: %w", err)
	}

	if recoveryReq.UserID != userID {
		return nil, fmt.Errorf("unauthorized to view this recovery request")
	}

	// Get recovery actions
	actions, err := s.recoveryRepo.GetRecoveryActions(ctx, recoveryID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recovery actions: %w", err)
	}

	// Calculate progress
	completedSteps := 0
	totalSteps := len(recoveryReq.RequiredVerifications)

	for _, action := range actions {
		if action.Status == entities.RecoveryActionStatusCompleted {
			completedSteps++
		}
	}

	return &entities.RecoveryStatusResponse{
		RecoveryID: recoveryID,
		Status:     recoveryReq.Status,
		Priority:   recoveryReq.Priority,
		Progress:   fmt.Sprintf("%d/%d steps completed", completedSteps, totalSteps),
		ExpiresAt:  recoveryReq.ExpiresAt,
		CreatedAt:  recoveryReq.CreatedAt,
		NextSteps:  s.getNextSteps(recoveryReq, actions),
	}, nil
}

// getNextSteps determines what steps remain for the user
func (s *Service) getNextSteps(req *entities.RecoveryRequest, actions []*entities.RecoveryAction) []string {
	completedSteps := make(map[string]bool)
	for _, action := range actions {
		if action.Status == entities.RecoveryActionStatusCompleted {
			completedSteps[action.Step] = true
		}
	}

	var nextSteps []string
	for _, required := range req.RequiredVerifications {
		if !completedSteps[required] {
			switch required {
			case "email":
				nextSteps = append(nextSteps, "Verify your email address")
			case "phone":
				nextSteps = append(nextSteps, "Verify your phone number")
			case "document":
				nextSteps = append(nextSteps, "Submit identity documents")
			}
		}
	}

	if len(nextSteps) == 0 {
		nextSteps = append(nextSteps, "Wait for admin review")
	}

	return nextSteps
}
