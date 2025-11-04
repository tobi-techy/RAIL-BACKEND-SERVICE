package withdrawal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
)

// Service handles withdrawal operations with enhanced security
type Service struct {
	withdrawalRepo WithdrawalRepository
	approvalRepo   ApprovalRepository
	limitsRepo     LimitsRepository
	trackingRepo   TrackingRepository
	fundingService FundingService
	userRepo       UserRepository
	auditService   AuditService
	logger         *logger.Logger
}

// WithdrawalRepository interface for withdrawal operations
type WithdrawalRepository interface {
	Create(ctx context.Context, req *entities.WithdrawalRequest) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.WithdrawalRequest, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.WithdrawalRequest, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus, approvedBy *uuid.UUID, approvedAt *time.Time) error
	Reject(ctx context.Context, id uuid.UUID, reason string, rejectedBy uuid.UUID) error
	GetPendingApprovals(ctx context.Context, limit, offset int) ([]*entities.WithdrawalRequest, error)
	ExpireOldRequests(ctx context.Context) error
}

// ApprovalRepository interface for approval operations
type ApprovalRepository interface {
	Create(ctx context.Context, approval *entities.WithdrawalApproval) error
	GetByWithdrawalID(ctx context.Context, withdrawalID uuid.UUID) ([]*entities.WithdrawalApproval, error)
	GetApprovalCount(ctx context.Context, withdrawalID uuid.UUID) (int, error)
}

// LimitsRepository interface for limits operations
type LimitsRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.WithdrawalLimits, error)
	Upsert(ctx context.Context, limits *entities.WithdrawalLimits) error
}

// TrackingRepository interface for tracking operations
type TrackingRepository interface {
	GetByUserIDAndDate(ctx context.Context, userID uuid.UUID, date time.Time) (*entities.WithdrawalTracking, error)
	Upsert(ctx context.Context, tracking *entities.WithdrawalTracking) error
}

// FundingService interface for funding operations
type FundingService interface {
	ProcessWithdrawal(ctx context.Context, userID, walletID uuid.UUID, amount decimal.Decimal, destinationAddress, blockchain string) error
}

// UserRepository interface for user operations
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
}

// AuditService interface for audit logging
type AuditService interface {
	LogWithdrawalEvent(ctx context.Context, eventType, userID, withdrawalID string, details map[string]interface{}) error
}

// NewService creates a new withdrawal service
func NewService(
	withdrawalRepo WithdrawalRepository,
	approvalRepo ApprovalRepository,
	limitsRepo LimitsRepository,
	trackingRepo TrackingRepository,
	fundingService FundingService,
	userRepo UserRepository,
	auditService AuditService,
	logger *logger.Logger,
) *Service {
	return &Service{
		withdrawalRepo: withdrawalRepo,
		approvalRepo:   approvalRepo,
		limitsRepo:     limitsRepo,
		trackingRepo:   trackingRepo,
		fundingService: fundingService,
		userRepo:       userRepo,
		auditService:   auditService,
		logger:         logger,
	}
}

// RequestWithdrawal creates a new withdrawal request with limit checking
func (s *Service) RequestWithdrawal(ctx context.Context, userID uuid.UUID, req *entities.CreateWithdrawalRequest) (*entities.WithdrawalResponse, error) {
	// Parse and validate amount
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid amount: %w", err)
	}

	// Check withdrawal limits
	limitsCheck, err := s.checkWithdrawalLimits(ctx, userID, amount)
	if err != nil {
		return nil, fmt.Errorf("limit check failed: %w", err)
	}

	// Determine if dual authorization is required
	requireApproval := limitsCheck.RequireDualAuth

	// Create withdrawal request
	expiresAt := time.Now().Add(24 * time.Hour) // 24 hour expiration
	withdrawal := &entities.WithdrawalRequest{
		ID:                 uuid.New(),
		UserID:             userID,
		WalletID:           req.WalletID,
		Amount:             amount,
		Currency:           req.Currency,
		DestinationAddress: req.DestinationAddress,
		Blockchain:         req.Blockchain,
		Status:             entities.WithdrawalStatusPending,
		ApprovalRequired:   requireApproval,
		ExpiresAt:          expiresAt,
		IdempotencyKey:     req.IdempotencyKey,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := withdrawal.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// If no approval required, auto-approve
	if !requireApproval {
		withdrawal.Status = entities.WithdrawalStatusApproved
		now := time.Now()
		withdrawal.ApprovedAt = &now
		withdrawal.ApprovedBy = &userID
	}

	// Save withdrawal request
	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		return nil, fmt.Errorf("failed to create withdrawal request: %w", err)
	}

	// Audit log
	s.auditService.LogWithdrawalEvent(ctx, "withdrawal_requested", userID.String(), withdrawal.ID.String(), map[string]interface{}{
		"amount":              amount.String(),
		"currency":            req.Currency,
		"approval_required":   requireApproval,
		"destination_address": req.DestinationAddress,
		"blockchain":          req.Blockchain,
	})

	s.logger.Infow("Withdrawal request created",
		"user_id", userID.String(),
		"withdrawal_id", withdrawal.ID.String(),
		"amount", amount.String(),
		"approval_required", requireApproval,
	)

	return s.toWithdrawalResponse(withdrawal), nil
}

// ApproveWithdrawal approves a withdrawal request
func (s *Service) ApproveWithdrawal(ctx context.Context, withdrawalID uuid.UUID, approverID uuid.UUID, notes *string) error {
	// Get withdrawal request
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	if withdrawal.Status != entities.WithdrawalStatusPending {
		return fmt.Errorf("withdrawal is not in pending status")
	}

	if withdrawal.IsExpired() {
		return fmt.Errorf("withdrawal request has expired")
	}

	// Get approver user
	approver, err := s.userRepo.GetByID(ctx, approverID)
	if err != nil {
		return fmt.Errorf("failed to get approver: %w", err)
	}

	// Check if approver has permission (admin or higher)
	if !approver.Role.HasPermission(entities.RoleAdmin) {
		return fmt.Errorf("insufficient permissions to approve withdrawal")
	}

	// Create approval record
	approval := &entities.WithdrawalApproval{
		ID:                  uuid.New(),
		WithdrawalRequestID: withdrawalID,
		ApproverID:          approverID,
		ApprovalLevel:       1, // Primary approval
		Notes:               notes,
		CreatedAt:           time.Now(),
	}

	if err := s.approvalRepo.Create(ctx, approval); err != nil {
		return fmt.Errorf("failed to create approval: %w", err)
	}

	// Update withdrawal status
	now := time.Now()
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusApproved, &approverID, &now); err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}

	// Audit log
	s.auditService.LogWithdrawalEvent(ctx, "withdrawal_approved", withdrawal.UserID.String(), withdrawalID.String(), map[string]interface{}{
		"approver_id": approverID.String(),
		"notes":       notes,
	})

	s.logger.Infow("Withdrawal approved",
		"withdrawal_id", withdrawalID.String(),
		"approver_id", approverID.String(),
		"user_id", withdrawal.UserID.String(),
	)

	return nil
}

// RejectWithdrawal rejects a withdrawal request
func (s *Service) RejectWithdrawal(ctx context.Context, withdrawalID uuid.UUID, rejectorID uuid.UUID, reason, notes string) error {
	// Get withdrawal request
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	if withdrawal.Status != entities.WithdrawalStatusPending {
		return fmt.Errorf("withdrawal is not in pending status")
	}

	// Get rejector user
	rejector, err := s.userRepo.GetByID(ctx, rejectorID)
	if err != nil {
		return fmt.Errorf("failed to get rejector: %w", err)
	}

	// Check if rejector has permission
	if !rejector.Role.HasPermission(entities.RoleAdmin) {
		return fmt.Errorf("insufficient permissions to reject withdrawal")
	}

	// Reject withdrawal
	if err := s.withdrawalRepo.Reject(ctx, withdrawalID, reason, rejectorID); err != nil {
		return fmt.Errorf("failed to reject withdrawal: %w", err)
	}

	// Audit log
	s.auditService.LogWithdrawalEvent(ctx, "withdrawal_rejected", withdrawal.UserID.String(), withdrawalID.String(), map[string]interface{}{
		"rejector_id": rejectorID.String(),
		"reason":      reason,
		"notes":       notes,
	})

	s.logger.Infow("Withdrawal rejected",
		"withdrawal_id", withdrawalID.String(),
		"rejector_id", rejectorID.String(),
		"user_id", withdrawal.UserID.String(),
		"reason", reason,
	)

	return nil
}

// ProcessApprovedWithdrawal processes an approved withdrawal
func (s *Service) ProcessApprovedWithdrawal(ctx context.Context, withdrawalID uuid.UUID) error {
	// Get withdrawal request
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	if withdrawal.Status != entities.WithdrawalStatusApproved {
		return fmt.Errorf("withdrawal is not approved")
	}

	// Update status to processing
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusProcessing, nil, nil); err != nil {
		return fmt.Errorf("failed to update status to processing: %w", err)
	}

	// Process the withdrawal through funding service
	err = s.fundingService.ProcessWithdrawal(ctx, withdrawal.UserID, withdrawal.WalletID, withdrawal.Amount,
		withdrawal.DestinationAddress, withdrawal.Blockchain)

	if err != nil {
		// Mark as failed
		if updateErr := s.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusFailed, nil, nil); updateErr != nil {
			s.logger.Errorw("Failed to update withdrawal status to failed", "error", updateErr, "withdrawal_id", withdrawalID)
		}
		return fmt.Errorf("withdrawal processing failed: %w", err)
	}

	// Update status to completed and track usage
	now := time.Now()
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawalID, entities.WithdrawalStatusCompleted, nil, &now); err != nil {
		return fmt.Errorf("failed to update status to completed: %w", err)
	}

	// Update withdrawal tracking
	if err := s.updateWithdrawalTracking(ctx, withdrawal.UserID, withdrawal.Amount); err != nil {
		s.logger.Warnw("Failed to update withdrawal tracking", "error", err, "user_id", withdrawal.UserID, "amount", withdrawal.Amount)
	}

	// Audit log
	s.auditService.LogWithdrawalEvent(ctx, "withdrawal_completed", withdrawal.UserID.String(), withdrawalID.String(), map[string]interface{}{
		"amount":              withdrawal.Amount.String(),
		"destination_address": withdrawal.DestinationAddress,
		"blockchain":          withdrawal.Blockchain,
	})

	s.logger.Infow("Withdrawal completed",
		"withdrawal_id", withdrawalID.String(),
		"user_id", withdrawal.UserID.String(),
		"amount", withdrawal.Amount.String(),
	)

	return nil
}

// GetWithdrawalLimits gets user's withdrawal limits
func (s *Service) GetWithdrawalLimits(ctx context.Context, userID uuid.UUID) (*entities.WithdrawalLimitsResponse, error) {
	limits, err := s.limitsRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get limits: %w", err)
	}

	// Get current usage
	tracking, err := s.trackingRepo.GetByUserIDAndDate(ctx, userID, time.Now())
	if err != nil && err.Error() != "tracking not found" {
		return nil, fmt.Errorf("failed to get tracking: %w", err)
	}

	dailyUsed := decimal.Zero
	weeklyUsed := decimal.Zero
	monthlyUsed := decimal.Zero
	if tracking != nil {
		dailyUsed = tracking.DailyTotal
		weeklyUsed = tracking.WeeklyTotal
		monthlyUsed = tracking.MonthlyTotal
	}

	return &entities.WithdrawalLimitsResponse{
		DailyLimit:           limits.DailyLimit.String(),
		WeeklyLimit:          limits.WeeklyLimit.String(),
		MonthlyLimit:         limits.MonthlyLimit.String(),
		RequireDualAuthAbove: limits.RequireDualAuthAbove.String(),
		DailyUsed:            dailyUsed.String(),
		WeeklyUsed:           weeklyUsed.String(),
		MonthlyUsed:          monthlyUsed.String(),
		DailyRemaining:       limits.DailyLimit.Sub(dailyUsed).String(),
		WeeklyRemaining:      limits.WeeklyLimit.Sub(weeklyUsed).String(),
		MonthlyRemaining:     limits.MonthlyLimit.Sub(monthlyUsed).String(),
	}, nil
}

// UpdateWithdrawalLimits updates user's withdrawal limits
func (s *Service) UpdateWithdrawalLimits(ctx context.Context, userID uuid.UUID, req *entities.WithdrawalLimitsRequest) error {
	limits, err := s.limitsRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get current limits: %w", err)
	}

	// Update fields if provided
	if req.DailyLimit != nil {
		if dailyLimit, err := decimal.NewFromString(*req.DailyLimit); err == nil {
			limits.DailyLimit = dailyLimit
		}
	}
	if req.WeeklyLimit != nil {
		if weeklyLimit, err := decimal.NewFromString(*req.WeeklyLimit); err == nil {
			limits.WeeklyLimit = weeklyLimit
		}
	}
	if req.MonthlyLimit != nil {
		if monthlyLimit, err := decimal.NewFromString(*req.MonthlyLimit); err == nil {
			limits.MonthlyLimit = monthlyLimit
		}
	}
	if req.RequireDualAuthAbove != nil {
		if dualAuthThreshold, err := decimal.NewFromString(*req.RequireDualAuthAbove); err == nil {
			limits.RequireDualAuthAbove = dualAuthThreshold
		}
	}

	if err := limits.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	if err := s.limitsRepo.Upsert(ctx, limits); err != nil {
		return fmt.Errorf("failed to update limits: %w", err)
	}

	s.logger.Infow("Withdrawal limits updated",
		"user_id", userID.String(),
		"daily_limit", limits.DailyLimit.String(),
		"weekly_limit", limits.WeeklyLimit.String(),
		"monthly_limit", limits.MonthlyLimit.String(),
	)

	return nil
}

// checkWithdrawalLimits checks if a withdrawal is within limits
func (s *Service) checkWithdrawalLimits(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*LimitsCheckResult, error) {
	limits, err := s.limitsRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get limits: %w", err)
	}

	// Get current usage
	tracking, err := s.trackingRepo.GetByUserIDAndDate(ctx, userID, time.Now())
	if err != nil && err.Error() != "tracking not found" {
		return nil, fmt.Errorf("failed to get tracking: %w", err)
	}

	dailyUsed := decimal.Zero
	weeklyUsed := decimal.Zero
	monthlyUsed := decimal.Zero
	if tracking != nil {
		dailyUsed = tracking.DailyTotal
		weeklyUsed = tracking.WeeklyTotal
		monthlyUsed = tracking.MonthlyTotal
	}

	// Check limits
	if amount.Add(dailyUsed).GreaterThan(limits.DailyLimit) {
		return nil, fmt.Errorf("daily withdrawal limit exceeded")
	}
	if amount.Add(weeklyUsed).GreaterThan(limits.WeeklyLimit) {
		return nil, fmt.Errorf("weekly withdrawal limit exceeded")
	}
	if amount.Add(monthlyUsed).GreaterThan(limits.MonthlyLimit) {
		return nil, fmt.Errorf("monthly withdrawal limit exceeded")
	}

	requireDualAuth := amount.GreaterThanOrEqual(limits.RequireDualAuthAbove)

	return &LimitsCheckResult{
		CanWithdraw:      true,
		RequireDualAuth:  requireDualAuth,
		DailyRemaining:   limits.DailyLimit.Sub(dailyUsed),
		WeeklyRemaining:  limits.WeeklyLimit.Sub(weeklyUsed),
		MonthlyRemaining: limits.MonthlyLimit.Sub(monthlyUsed),
	}, nil
}

// updateWithdrawalTracking updates the withdrawal tracking for a user
func (s *Service) updateWithdrawalTracking(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	today := time.Now()
	tracking, err := s.trackingRepo.GetByUserIDAndDate(ctx, userID, today)
	if err != nil && err.Error() != "tracking not found" {
		return err
	}

	if tracking == nil {
		tracking = &entities.WithdrawalTracking{
			ID:           uuid.New(),
			UserID:       userID,
			Date:         today,
			DailyTotal:   decimal.Zero,
			WeeklyTotal:  decimal.Zero,
			MonthlyTotal: decimal.Zero,
		}
	}

	tracking.DailyTotal = tracking.DailyTotal.Add(amount)
	tracking.WeeklyTotal = tracking.WeeklyTotal.Add(amount)
	tracking.MonthlyTotal = tracking.MonthlyTotal.Add(amount)
	now := time.Now()
	tracking.LastWithdrawalAt = &now
	tracking.UpdatedAt = now

	return s.trackingRepo.Upsert(ctx, tracking)
}

// LimitsCheckResult represents the result of a limits check
type LimitsCheckResult struct {
	CanWithdraw      bool
	RequireDualAuth  bool
	DailyRemaining   decimal.Decimal
	WeeklyRemaining  decimal.Decimal
	MonthlyRemaining decimal.Decimal
}

// toWithdrawalResponse converts a withdrawal request to response
func (s *Service) toWithdrawalResponse(req *entities.WithdrawalRequest) *entities.WithdrawalResponse {
	return &entities.WithdrawalResponse{
		ID:                 req.ID,
		UserID:             req.UserID,
		WalletID:           req.WalletID,
		Amount:             req.Amount.String(),
		Currency:           req.Currency,
		DestinationAddress: req.DestinationAddress,
		Blockchain:         req.Blockchain,
		Status:             req.Status,
		ApprovalRequired:   req.ApprovalRequired,
		ApprovedBy:         req.ApprovedBy,
		ApprovedAt:         req.ApprovedAt,
		ExpiresAt:          req.ExpiresAt,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
	}
}
