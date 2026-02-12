package withdrawal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/circuitbreaker"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/queue"
)

// AllocationService interface for spending enforcement
type AllocationService interface {
	CanSpend(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
	LogDeclinedSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error
}

// AllocationNotificationManager interface for sending allocation notifications
type AllocationNotificationManager interface {
	NotifyTransactionDeclined(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, transactionType string) error
}

// WithdrawalLimitsService interface for withdrawal limit validation
type WithdrawalLimitsService interface {
	ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error)
	RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// WithdrawalAuditService interface for compliance audit logging
type WithdrawalAuditService interface {
	LogWithdrawal(ctx context.Context, userID uuid.UUID, withdrawalID uuid.UUID, amount string, status string) error
}

// WithdrawalNotificationService interface for sending withdrawal-related notifications
type WithdrawalNotificationService interface {
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount, destinationAddress string) error
	NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount, reason string) error
	NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error
}

// WithdrawalService handles USD to USDC withdrawal operations
type WithdrawalService struct {
	withdrawalRepo        WithdrawalRepository
	alpacaAPI             AlpacaAdapter
	withdrawalProvider    WithdrawalProviderAdapter
	allocationService     AllocationService
	allocationNotifier    AllocationNotificationManager
	limitsService         WithdrawalLimitsService
	auditService          WithdrawalAuditService
	notificationService   WithdrawalNotificationService
	logger                *logger.Logger
	alpacaBreaker         *circuitbreaker.CircuitBreaker
	providerBreaker       *circuitbreaker.CircuitBreaker
	queuePublisher        queue.Publisher
}

// WithdrawalRepository interface for withdrawal persistence
type WithdrawalRepository interface {
	Create(ctx context.Context, withdrawal *entities.Withdrawal) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error
	UpdateAlpacaJournal(ctx context.Context, id uuid.UUID, journalID string) error
	UpdateBridgeTransfer(ctx context.Context, id uuid.UUID, transferID, recipientID string) error
	UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error
	MarkCompleted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error
	GetPendingWithdrawalsTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// AlpacaAdapter interface for Alpaca operations
type AlpacaAdapter interface {
	GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)
}

// WithdrawalProviderAdapter interface for withdrawal/off-ramp operations (Bridge)
type WithdrawalProviderAdapter interface {
	ProcessWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*ProcessWithdrawalResponse, error)
	GetTransferStatus(ctx context.Context, transferID string) (*OnRampTransferResponse, error)
}

// ProcessWithdrawalResponse contains withdrawal processing result
type ProcessWithdrawalResponse struct {
	TransferID     string
	RecipientID    string
	FundingAddress string
	SourceAmount   string
	DestAmount     string
	Status         string
}

// OnRampTransferResponse contains transfer status
type OnRampTransferResponse struct {
	ID     string
	Status string
}

// NewWithdrawalService creates a new withdrawal service
func NewWithdrawalService(
	withdrawalRepo WithdrawalRepository,
	alpacaAPI AlpacaAdapter,
	withdrawalProvider WithdrawalProviderAdapter,
	allocationService AllocationService,
	allocationNotifier AllocationNotificationManager,
	logger *logger.Logger,
	queuePublisher queue.Publisher,
) *WithdrawalService {
	cfg := circuitbreaker.Config{
		MaxRequests:      10,
		Interval:         60 * time.Second,
		Timeout:          60 * time.Second,
		FailureThreshold: 5,
		SuccessThreshold: 2,
	}
	if queuePublisher == nil {
		queuePublisher = queue.NewMockPublisher()
	}
	return &WithdrawalService{
		withdrawalRepo:     withdrawalRepo,
		alpacaAPI:          alpacaAPI,
		withdrawalProvider: withdrawalProvider,
		allocationService:  allocationService,
		allocationNotifier: allocationNotifier,
		logger:             logger,
		alpacaBreaker:      circuitbreaker.New(cfg),
		providerBreaker:    circuitbreaker.New(cfg),
		queuePublisher:     queuePublisher,
	}
}

// SetLimitsService sets the limits service for withdrawal validation (optional)
func (s *WithdrawalService) SetLimitsService(ls WithdrawalLimitsService) {
	s.limitsService = ls
}

// SetAuditService sets the audit service for compliance logging (optional)
func (s *WithdrawalService) SetAuditService(as WithdrawalAuditService) {
	s.auditService = as
}

// SetNotificationService sets the notification service (optional)
func (s *WithdrawalService) SetNotificationService(ns WithdrawalNotificationService) {
	s.notificationService = ns
}

// InitiateWithdrawal initiates a USD to USDC withdrawal
func (s *WithdrawalService) InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	s.logger.Info("Initiating withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"chain", req.DestinationChain,
		"address", req.DestinationAddress)

	// Step 1: Check for pending withdrawals to prevent race conditions
	pendingTotal, err := s.withdrawalRepo.GetPendingWithdrawalsTotal(ctx, req.UserID)
	if err != nil {
		s.logger.Error("Failed to check pending withdrawals", "error", err, "user_id", req.UserID.String())
		return nil, fmt.Errorf("failed to check pending withdrawals: %w", err)
	}

	// Step 2: Validate against withdrawal limits (KYC tier-based)
	if s.limitsService != nil {
		result, err := s.limitsService.ValidateWithdrawal(ctx, req.UserID, req.Amount)
		if err != nil {
			s.logger.Warn("Withdrawal limit validation failed",
				"user_id", req.UserID.String(),
				"amount", req.Amount.String(),
				"error", err.Error(),
			)
			if result != nil {
				return nil, fmt.Errorf("withdrawal limit exceeded (%s): %s remaining until %v",
					result.LimitType, result.RemainingCapacity.String(), result.ResetsAt)
			}
			return nil, fmt.Errorf("withdrawal limit exceeded: %w", err)
		}
	}

	// Step 2: Check 70/30 allocation mode spending limit
	if s.allocationService != nil {
		canSpend, err := s.allocationService.CanSpend(ctx, req.UserID, req.Amount)
		if err != nil {
			s.logger.Error("Failed to check spending limit", "error", err, "user_id", req.UserID.String())
			return nil, fmt.Errorf("failed to check spending limit: %w", err)
		}

		if !canSpend {
			s.logger.Warn("Withdrawal declined - spending limit reached",
				"user_id", req.UserID.String(),
				"amount", req.Amount.String())
			
			// Log declined spending event
			_ = s.allocationService.LogDeclinedSpending(ctx, req.UserID, req.Amount, "withdrawal")
			
			// Send notification to user
			if s.allocationNotifier != nil {
				_ = s.allocationNotifier.NotifyTransactionDeclined(ctx, req.UserID, req.Amount, "withdrawal")
			}
			
			return nil, entities.ErrSpendingLimitReached
		}
	}

	// Step 4: Validate Alpaca account and buying power (accounting for pending withdrawals)
	var alpacaAccount *entities.AlpacaAccountResponse
	var getAccountErr error
	err = s.alpacaBreaker.Execute(ctx, func() error {
		alpacaAccount, getAccountErr = s.alpacaAPI.GetAccount(ctx, req.AlpacaAccountID)
		return getAccountErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		return nil, fmt.Errorf("Alpaca account not active: %s", alpacaAccount.Status)
	}

	// Calculate effective available balance (buying power minus pending withdrawals)
	effectiveBalance := alpacaAccount.BuyingPower.Sub(pendingTotal)
	if effectiveBalance.LessThan(req.Amount) {
		s.logger.Warn("Insufficient balance after accounting for pending withdrawals",
			"user_id", req.UserID.String(),
			"buying_power", alpacaAccount.BuyingPower.String(),
			"pending_withdrawals", pendingTotal.String(),
			"effective_balance", effectiveBalance.String(),
			"requested", req.Amount.String())
		return nil, fmt.Errorf("insufficient buying power: have %s (with %s pending), need %s",
			effectiveBalance.String(), pendingTotal.String(), req.Amount.String())
	}

	// Step 5: Create withdrawal record
	withdrawal := &entities.Withdrawal{
		ID:                 uuid.New(),
		UserID:             req.UserID,
		AlpacaAccountID:    req.AlpacaAccountID,
		Amount:             req.Amount,
		DestinationChain:   req.DestinationChain,
		DestinationAddress: req.DestinationAddress,
		Status:             entities.WithdrawalStatusPending,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create withdrawal record", "error", err, "user_id", req.UserID.String())
		return nil, fmt.Errorf("failed to create withdrawal record: %w", err)
	}

	// Record withdrawal usage against limits
	if s.limitsService != nil {
		if err := s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount); err != nil {
			s.logger.Warn("Failed to record withdrawal usage", "error", err, "user_id", req.UserID.String())
			// Don't fail the withdrawal, just log the warning
		}
	}

	// Create audit log entry for compliance
	if s.auditService != nil {
		if err := s.auditService.LogWithdrawal(ctx, req.UserID, withdrawal.ID, req.Amount.String(), string(withdrawal.Status)); err != nil {
			s.logger.Warn("Failed to create audit log for withdrawal", "error", err, "withdrawal_id", withdrawal.ID.String())
			// Don't fail the withdrawal, audit logging is non-critical
		}
	}

	// Step 6: Enqueue withdrawal processing to SQS
	msg := queue.WithdrawalMessage{
		WithdrawalID: withdrawal.ID.String(),
		Step:         "debit_alpaca",
	}
	if err := s.queuePublisher.Publish(ctx, "withdrawal-processing", msg); err != nil {
		s.logger.Error("Failed to enqueue withdrawal", "error", err)
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "failed to enqueue processing")
		return nil, fmt.Errorf("failed to enqueue withdrawal: %w", err)
	}

	s.logger.Info("Withdrawal initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"status", withdrawal.Status)

	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: withdrawal.ID,
		Status:       withdrawal.Status,
		Message:      "Withdrawal initiated successfully",
	}, nil
}

// processWithdrawalAsync processes the withdrawal in the background
func (s *WithdrawalService) processWithdrawalAsync(ctx context.Context, withdrawal *entities.Withdrawal) {
	s.logger.Info("Processing withdrawal async", "withdrawal_id", withdrawal.ID.String())

	// Step 1: Debit USD from Alpaca account
	if err := s.debitAlpacaAccount(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to debit Alpaca account", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return
	}

	// Step 2: Process Due on-ramp (USD → USDC)
	if err := s.processBridgeTransfer(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to process Due on-ramp", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		// Compensation: Credit back Alpaca account
		if compErr := s.compensateAlpacaDebit(ctx, withdrawal); compErr != nil {
			s.logger.Error("Compensation failed", "error", compErr, "withdrawal_id", withdrawal.ID.String())
		}
		return
	}

	// Step 3: Monitor transfer completion
	if err := s.monitorTransferCompletion(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to monitor transfer", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return
	}

	s.logger.Info("Withdrawal completed successfully", "withdrawal_id", withdrawal.ID.String())
}

// debitAlpacaAccount debits USD from Alpaca brokerage account
func (s *WithdrawalService) debitAlpacaAccount(ctx context.Context, withdrawal *entities.Withdrawal) error {
	s.logger.Info("Debiting Alpaca account",
		"withdrawal_id", withdrawal.ID.String(),
		"alpaca_account_id", withdrawal.AlpacaAccountID,
		"amount", withdrawal.Amount.String())

	// Create journal entry to debit USD from user's account to virtual account
	journalReq := &entities.AlpacaJournalRequest{
		FromAccount: withdrawal.AlpacaAccountID,
		ToAccount:   "SI", // System/virtual account
		EntryType:   "JNLC",
		Amount:      withdrawal.Amount,
		Description: fmt.Sprintf("Withdrawal to USDC - %s", withdrawal.ID.String()),
	}

	var journalResp *entities.AlpacaJournalResponse
	var createJournalErr error
	err := s.alpacaBreaker.Execute(ctx, func() error {
		journalResp, createJournalErr = s.alpacaAPI.CreateJournal(ctx, journalReq)
		return createJournalErr
	})
	if err != nil {
		return fmt.Errorf("failed to create journal: %w", err)
	}

	// Update withdrawal with journal ID
	if err := s.withdrawalRepo.UpdateAlpacaJournal(ctx, withdrawal.ID, journalResp.ID); err != nil {
		return fmt.Errorf("failed to update journal ID: %w", err)
	}

	s.logger.Info("Alpaca account debited",
		"withdrawal_id", withdrawal.ID.String(),
		"journal_id", journalResp.ID)

	return nil
}

// processBridgeTransfer processes the Due on-ramp (USD → USDC)
func (s *WithdrawalService) processBridgeTransfer(ctx context.Context, withdrawal *entities.Withdrawal) error {
	s.logger.Info("Processing Bridge transfer",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", withdrawal.Amount.String())

	req := &entities.InitiateWithdrawalRequest{
		UserID:             withdrawal.UserID,
		AlpacaAccountID:    withdrawal.AlpacaAccountID,
		Amount:             withdrawal.Amount,
		DestinationChain:   withdrawal.DestinationChain,
		DestinationAddress: withdrawal.DestinationAddress,
	}

	var providerResp *ProcessWithdrawalResponse
	var processErr error
	err := s.providerBreaker.Execute(ctx, func() error {
		providerResp, processErr = s.withdrawalProvider.ProcessWithdrawal(ctx, req)
		return processErr
	})
	if err != nil {
		return fmt.Errorf("failed to process withdrawal: %w", err)
	}

	// Update withdrawal with transfer details
	if err := s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, providerResp.TransferID, providerResp.RecipientID); err != nil {
		return fmt.Errorf("failed to update transfer: %w", err)
	}

	s.logger.Info("Withdrawal transfer initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"transfer_id", providerResp.TransferID)

	return nil
}

// monitorTransferCompletion monitors the transfer until completion
func (s *WithdrawalService) monitorTransferCompletion(ctx context.Context, withdrawal *entities.Withdrawal) error {
	s.logger.Info("Monitoring transfer completion", "withdrawal_id", withdrawal.ID.String())

	// Reload withdrawal to get transfer ID
	w, err := s.withdrawalRepo.GetByID(ctx, withdrawal.ID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	if w.BridgeTransferID == nil {
		return fmt.Errorf("no transfer ID found")
	}

	// Poll for transfer status (max 30 attempts, 10 seconds apart = 5 minutes)
	maxAttempts := 30
	pollInterval := 10 * time.Second

	for attempt := 0; attempt < maxAttempts; attempt++ {
		time.Sleep(pollInterval)

		var status *OnRampTransferResponse
		var statusErr error
		err := s.providerBreaker.Execute(ctx, func() error {
			status, statusErr = s.withdrawalProvider.GetTransferStatus(ctx, *w.BridgeTransferID)
			return statusErr
		})
		if err != nil {
			s.logger.Warn("Failed to get transfer status", "error", err, "attempt", attempt)
			continue
		}

		s.logger.Info("Transfer status",
			"withdrawal_id", withdrawal.ID.String(),
			"status", status.Status,
			"attempt", attempt)

		switch status.Status {
		case "completed":
			// Mark withdrawal as completed
			if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
				return fmt.Errorf("failed to mark completed: %w", err)
			}
			// Send withdrawal completed notification
			if s.notificationService != nil {
				_ = s.notificationService.NotifyWithdrawalCompleted(ctx, withdrawal.UserID, withdrawal.Amount.String(), withdrawal.DestinationAddress)
				// Notify for large withdrawals (>= $1000)
				largeWithdrawalThreshold := decimal.NewFromInt(1000)
				if withdrawal.Amount.GreaterThanOrEqual(largeWithdrawalThreshold) {
					_ = s.notificationService.NotifyLargeBalanceChange(ctx, withdrawal.UserID, "withdrawal", withdrawal.Amount, decimal.Zero)
				}
			}
			return nil

		case "failed":
			// Send withdrawal failed notification
			if s.notificationService != nil {
				_ = s.notificationService.NotifyWithdrawalFailed(ctx, withdrawal.UserID, withdrawal.Amount.String(), "Bridge transfer failed")
			}
			return fmt.Errorf("Bridge transfer failed")

		default:
			// Continue polling
			continue
		}
	}

	return fmt.Errorf("transfer monitoring timeout after %d attempts", maxAttempts)
}

// GetWithdrawal retrieves a withdrawal by ID
func (s *WithdrawalService) GetWithdrawal(ctx context.Context, withdrawalID uuid.UUID) (*entities.Withdrawal, error) {
	return s.withdrawalRepo.GetByID(ctx, withdrawalID)
}

// GetUserWithdrawals retrieves withdrawals for a user
func (s *WithdrawalService) GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error) {
	return s.withdrawalRepo.GetByUserID(ctx, userID, limit, offset)
}

// CancelWithdrawal cancels a pending withdrawal
func (s *WithdrawalService) CancelWithdrawal(ctx context.Context, withdrawalID uuid.UUID, userID uuid.UUID) error {
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("not found: %w", err)
	}

	if withdrawal.UserID != userID {
		return fmt.Errorf("not found")
	}

	// Only allow cancellation of initiated/pending withdrawals
	if withdrawal.Status != entities.WithdrawalStatusInitiated && withdrawal.Status != entities.WithdrawalStatusPending {
		return fmt.Errorf("cannot cancel withdrawal in %s status", withdrawal.Status)
	}

	return s.withdrawalRepo.MarkFailed(ctx, withdrawalID, "cancelled by user")
}

// compensateAlpacaDebit reverses the Alpaca journal entry on failure
func (s *WithdrawalService) compensateAlpacaDebit(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if withdrawal.AlpacaJournalID == nil {
		return nil
	}

	s.logger.Info("Compensating Alpaca debit",
		"withdrawal_id", withdrawal.ID.String(),
		"journal_id", *withdrawal.AlpacaJournalID)

	journalReq := &entities.AlpacaJournalRequest{
		FromAccount: "SI",
		ToAccount:   withdrawal.AlpacaAccountID,
		EntryType:   "JNLC",
		Amount:      withdrawal.Amount,
		Description: fmt.Sprintf("Withdrawal reversal - %s", withdrawal.ID.String()),
	}

	var reversalJournal *entities.AlpacaJournalResponse
	var reversalErr error
	err := s.alpacaBreaker.Execute(ctx, func() error {
		reversalJournal, reversalErr = s.alpacaAPI.CreateJournal(ctx, journalReq)
		return reversalErr
	})
	if err != nil {
		return fmt.Errorf("failed to reverse journal: %w", err)
	}
	s.logger.Info("Alpaca debit compensated",
		"withdrawal_id", withdrawal.ID.String(),
		"reversal_journal_id", reversalJournal.ID)

	return nil
}

// StuckWithdrawalRepository interface for stuck withdrawal queries
type StuckWithdrawalRepository interface {
	GetStuckWithdrawals(ctx context.Context, slaThreshold time.Duration) ([]*entities.Withdrawal, error)
	MarkTimeout(ctx context.Context, id uuid.UUID) error
}

// ReconcileStuckWithdrawals queries provider for actual status of stuck withdrawals
// This is a fallback when webhooks fail - implements status enquiry pattern
func (s *WithdrawalService) ReconcileStuckWithdrawals(ctx context.Context, slaThreshold time.Duration) error {
	s.logger.Info("Starting stuck withdrawal reconciliation", "sla_threshold", slaThreshold.String())

	// Type assert to get the extended interface
	stuckRepo, ok := s.withdrawalRepo.(StuckWithdrawalRepository)
	if !ok {
		return fmt.Errorf("withdrawal repository does not support GetStuckWithdrawals")
	}

	stuckWithdrawals, err := stuckRepo.GetStuckWithdrawals(ctx, slaThreshold)
	if err != nil {
		return fmt.Errorf("failed to get stuck withdrawals: %w", err)
	}

	s.logger.Info("Found stuck withdrawals", "count", len(stuckWithdrawals))

	for _, w := range stuckWithdrawals {
		if err := s.reconcileSingleWithdrawal(ctx, w, stuckRepo); err != nil {
			s.logger.Warn("Failed to reconcile withdrawal",
				"withdrawal_id", w.ID.String(),
				"error", err.Error())
			continue
		}
	}

	return nil
}

// reconcileSingleWithdrawal queries the provider for actual status and updates accordingly
func (s *WithdrawalService) reconcileSingleWithdrawal(ctx context.Context, w *entities.Withdrawal, stuckRepo StuckWithdrawalRepository) error {
	// Only query provider if we have a transfer ID
	if w.BridgeTransferID == nil {
		// No transfer ID means it's stuck before provider submission
		// Mark as timeout so it can be retried or manually resolved
		s.logger.Warn("Withdrawal stuck without transfer ID, marking timeout",
			"withdrawal_id", w.ID.String(),
			"status", w.Status)
		return stuckRepo.MarkTimeout(ctx, w.ID)
	}

	// Query provider for actual status
	var status *OnRampTransferResponse
	var statusErr error
	err := s.providerBreaker.Execute(ctx, func() error {
		status, statusErr = s.withdrawalProvider.GetTransferStatus(ctx, *w.BridgeTransferID)
		return statusErr
	})
	if err != nil {
		s.logger.Warn("Failed to get transfer status from provider",
			"withdrawal_id", w.ID.String(),
			"transfer_id", *w.BridgeTransferID,
			"error", err.Error())
		// Mark as timeout - provider unreachable
		return stuckRepo.MarkTimeout(ctx, w.ID)
	}

	s.logger.Info("Got status from provider for stuck withdrawal",
		"withdrawal_id", w.ID.String(),
		"provider_status", status.Status)

	// Update based on actual status from provider
	switch status.Status {
	case "completed":
		if err := s.withdrawalRepo.MarkCompleted(ctx, w.ID); err != nil {
			return fmt.Errorf("failed to mark completed: %w", err)
		}
		s.logger.Info("Reconciled stuck withdrawal as completed", "withdrawal_id", w.ID.String())

	case "failed":
		if err := s.withdrawalRepo.MarkFailed(ctx, w.ID, "Provider reported failure during reconciliation"); err != nil {
			return fmt.Errorf("failed to mark failed: %w", err)
		}
		s.logger.Info("Reconciled stuck withdrawal as failed", "withdrawal_id", w.ID.String())

	default:
		// Still processing - mark as timeout if beyond SLA
		if err := stuckRepo.MarkTimeout(ctx, w.ID); err != nil {
			return fmt.Errorf("failed to mark timeout: %w", err)
		}
		s.logger.Info("Marked stuck withdrawal as timeout", "withdrawal_id", w.ID.String())
	}

	return nil
}
