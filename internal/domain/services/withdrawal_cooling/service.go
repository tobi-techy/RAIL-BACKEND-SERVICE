package withdrawal_cooling

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// PendingWithdrawalRepository manages pending withdrawal records
type PendingWithdrawalRepository interface {
	Create(ctx context.Context, pw *entities.PendingWithdrawal) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.PendingWithdrawal, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.PendingWithdrawal, error)
	GetPendingByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.PendingWithdrawal, error)
	GetReadyToExecute(ctx context.Context) ([]*entities.PendingWithdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.PendingWithdrawalStatus, timestamp *time.Time) error
	Cancel(ctx context.Context, id uuid.UUID) error
}

// InvestmentRulesRepository retrieves user cooling config
type InvestmentRulesRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
}

// NotificationService sends cooling period notifications
type NotificationService interface {
	SendWithdrawalCoolingStarted(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, executeAfter time.Time) error
	SendWithdrawalCoolingReminder(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, timeRemaining time.Duration) error
	SendWithdrawalReady(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	SendWithdrawalCancelled(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// Service handles withdrawal cooling-off periods
type Service struct {
	pendingRepo   PendingWithdrawalRepository
	rulesRepo     InvestmentRulesRepository
	notifier      NotificationService
	logger        *zap.Logger
}

// NewService creates a new withdrawal cooling service
func NewService(
	pendingRepo PendingWithdrawalRepository,
	rulesRepo InvestmentRulesRepository,
	notifier NotificationService,
	logger *zap.Logger,
) *Service {
	return &Service{
		pendingRepo: pendingRepo,
		rulesRepo:   rulesRepo,
		notifier:    notifier,
		logger:      logger,
	}
}

// CoolingCheckResult contains the result of a cooling period check
type CoolingCheckResult struct {
	RequiresCooling bool                       `json:"requires_cooling"`
	PendingID       *uuid.UUID                 `json:"pending_id,omitempty"`
	ExecuteAfter    *time.Time                 `json:"execute_after,omitempty"`
	TimeRemaining   *time.Duration             `json:"time_remaining,omitempty"`
	Reason          string                     `json:"reason,omitempty"`
	BypassReason    string                     `json:"bypass_reason,omitempty"`
}

// CheckWithdrawalCooling checks if a withdrawal requires a cooling-off period
func (s *Service) CheckWithdrawalCooling(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*CoolingCheckResult, error) {
	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get investment rules, bypassing cooling",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return &CoolingCheckResult{
			RequiresCooling: false,
			BypassReason:    "rules_unavailable",
		}, nil
	}

	// Check if cooling is enabled
	if rules == nil || rules.WithdrawalCooling == nil || !rules.WithdrawalCooling.Enabled {
		return &CoolingCheckResult{
			RequiresCooling: false,
			BypassReason:    "cooling_disabled",
		}, nil
	}

	cooling := rules.WithdrawalCooling

	// Check if amount is below small threshold (bypass)
	if cooling.BypassForSmall && amount.LessThanOrEqual(cooling.SmallThreshold) {
		return &CoolingCheckResult{
			RequiresCooling: false,
			BypassReason:    fmt.Sprintf("amount_below_threshold_%s", cooling.SmallThreshold.String()),
		}, nil
	}

	// Cooling is required
	executeAfter := time.Now().Add(cooling.CoolingPeriod)
	timeRemaining := cooling.CoolingPeriod

	return &CoolingCheckResult{
		RequiresCooling: true,
		ExecuteAfter:    &executeAfter,
		TimeRemaining:   &timeRemaining,
		Reason:          "24_hour_rule",
	}, nil
}

// InitiateCoolingPeriod creates a pending withdrawal with cooling period
func (s *Service) InitiateCoolingPeriod(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.PendingWithdrawal, error) {
	check, err := s.CheckWithdrawalCooling(ctx, userID, amount)
	if err != nil {
		return nil, err
	}

	if !check.RequiresCooling {
		return nil, nil // No cooling needed, proceed immediately
	}

	pending := &entities.PendingWithdrawal{
		ID:           uuid.New(),
		UserID:       userID,
		Amount:       amount,
		RequestedAt:  time.Now(),
		ExecuteAfter: *check.ExecuteAfter,
		Status:       entities.PendingWithdrawalStatusPending,
	}

	if err := s.pendingRepo.Create(ctx, pending); err != nil {
		return nil, fmt.Errorf("failed to create pending withdrawal: %w", err)
	}

	// Send notification
	if s.notifier != nil {
		_ = s.notifier.SendWithdrawalCoolingStarted(ctx, userID, amount, pending.ExecuteAfter)
	}

	s.logger.Info("Withdrawal cooling period initiated",
		zap.String("user_id", userID.String()),
		zap.String("pending_id", pending.ID.String()),
		zap.String("amount", amount.String()),
		zap.Time("execute_after", pending.ExecuteAfter))

	return pending, nil
}

// CancelPendingWithdrawal cancels a pending withdrawal during cooling period
func (s *Service) CancelPendingWithdrawal(ctx context.Context, userID uuid.UUID, pendingID uuid.UUID) error {
	pending, err := s.pendingRepo.GetByID(ctx, pendingID)
	if err != nil {
		return fmt.Errorf("failed to get pending withdrawal: %w", err)
	}

	if pending == nil {
		return fmt.Errorf("pending withdrawal not found")
	}

	if pending.UserID != userID {
		return fmt.Errorf("unauthorized: withdrawal belongs to different user")
	}

	if pending.Status != entities.PendingWithdrawalStatusPending {
		return fmt.Errorf("withdrawal cannot be cancelled: status is %s", pending.Status)
	}

	if err := s.pendingRepo.Cancel(ctx, pendingID); err != nil {
		return fmt.Errorf("failed to cancel withdrawal: %w", err)
	}

	// Send notification
	if s.notifier != nil {
		_ = s.notifier.SendWithdrawalCancelled(ctx, userID, pending.Amount)
	}

	s.logger.Info("Pending withdrawal cancelled",
		zap.String("user_id", userID.String()),
		zap.String("pending_id", pendingID.String()),
		zap.String("amount", pending.Amount.String()))

	return nil
}

// GetPendingWithdrawals returns all pending withdrawals for a user
func (s *Service) GetPendingWithdrawals(ctx context.Context, userID uuid.UUID) ([]*entities.PendingWithdrawal, error) {
	return s.pendingRepo.GetPendingByUserID(ctx, userID)
}

// GetReadyWithdrawals returns all withdrawals ready to execute
func (s *Service) GetReadyWithdrawals(ctx context.Context) ([]*entities.PendingWithdrawal, error) {
	return s.pendingRepo.GetReadyToExecute(ctx)
}

// MarkExecuted marks a pending withdrawal as executed
func (s *Service) MarkExecuted(ctx context.Context, pendingID uuid.UUID) error {
	now := time.Now()
	return s.pendingRepo.UpdateStatus(ctx, pendingID, entities.PendingWithdrawalStatusExecuted, &now)
}

// GetWithdrawalStatus returns the status of a pending withdrawal
func (s *Service) GetWithdrawalStatus(ctx context.Context, pendingID uuid.UUID) (*WithdrawalStatusResponse, error) {
	pending, err := s.pendingRepo.GetByID(ctx, pendingID)
	if err != nil {
		return nil, err
	}

	if pending == nil {
		return nil, fmt.Errorf("pending withdrawal not found")
	}

	response := &WithdrawalStatusResponse{
		ID:           pending.ID,
		Amount:       pending.Amount,
		Status:       pending.Status,
		RequestedAt:  pending.RequestedAt,
		ExecuteAfter: pending.ExecuteAfter,
		CanExecute:   pending.CanExecute(),
	}

	if !pending.CanExecute() {
		remaining := pending.TimeRemaining()
		response.TimeRemaining = &remaining
	}

	return response, nil
}

// WithdrawalStatusResponse contains withdrawal status information
type WithdrawalStatusResponse struct {
	ID            uuid.UUID                       `json:"id"`
	Amount        decimal.Decimal                 `json:"amount"`
	Status        entities.PendingWithdrawalStatus `json:"status"`
	RequestedAt   time.Time                       `json:"requested_at"`
	ExecuteAfter  time.Time                       `json:"execute_after"`
	CanExecute    bool                            `json:"can_execute"`
	TimeRemaining *time.Duration                  `json:"time_remaining,omitempty"`
}

// ProcessReadyWithdrawals processes all withdrawals that have passed cooling period
// This should be called by a worker
func (s *Service) ProcessReadyWithdrawals(ctx context.Context, executor func(ctx context.Context, pw *entities.PendingWithdrawal) error) error {
	ready, err := s.pendingRepo.GetReadyToExecute(ctx)
	if err != nil {
		return fmt.Errorf("failed to get ready withdrawals: %w", err)
	}

	for _, pw := range ready {
		if err := executor(ctx, pw); err != nil {
			s.logger.Error("Failed to execute withdrawal",
				zap.String("pending_id", pw.ID.String()),
				zap.Error(err))
			continue
		}

		// Mark as executed
		now := time.Now()
		if err := s.pendingRepo.UpdateStatus(ctx, pw.ID, entities.PendingWithdrawalStatusExecuted, &now); err != nil {
			s.logger.Error("Failed to mark withdrawal as executed",
				zap.String("pending_id", pw.ID.String()),
				zap.Error(err))
		}

		// Send notification
		if s.notifier != nil {
			_ = s.notifier.SendWithdrawalReady(ctx, pw.UserID, pw.Amount)
		}
	}

	return nil
}
