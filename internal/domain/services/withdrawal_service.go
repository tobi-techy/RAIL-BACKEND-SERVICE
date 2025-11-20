package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/circuitbreaker"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stack-service/stack_service/pkg/queue"
)

// WithdrawalService handles USD to USDC withdrawal operations
type WithdrawalService struct {
	withdrawalRepo  WithdrawalRepository
	alpacaAPI       AlpacaAdapter
	dueAPI          DueWithdrawalAdapter
	logger          *logger.Logger
	alpacaBreaker   *circuitbreaker.CircuitBreaker
	dueBreaker      *circuitbreaker.CircuitBreaker
	queuePublisher  queue.Publisher
}

// WithdrawalRepository interface for withdrawal persistence
type WithdrawalRepository interface {
	Create(ctx context.Context, withdrawal *entities.Withdrawal) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error
	UpdateAlpacaJournal(ctx context.Context, id uuid.UUID, journalID string) error
	UpdateDueTransfer(ctx context.Context, id uuid.UUID, transferID, recipientID string) error
	UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error
	MarkCompleted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error
}

// AlpacaAdapter interface for Alpaca operations
type AlpacaAdapter interface {
	GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error)
}

// DueWithdrawalAdapter interface for Due on-ramp operations
type DueWithdrawalAdapter interface {
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
	dueAPI DueWithdrawalAdapter,
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
		withdrawalRepo:  withdrawalRepo,
		alpacaAPI:       alpacaAPI,
		dueAPI:          dueAPI,
		logger:          logger,
		alpacaBreaker:   circuitbreaker.New(cfg),
		dueBreaker:      circuitbreaker.New(cfg),
		queuePublisher:  queuePublisher,
	}
}

// InitiateWithdrawal initiates a USD to USDC withdrawal
func (s *WithdrawalService) InitiateWithdrawal(ctx context.Context, req *entities.InitiateWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	s.logger.Info("Initiating withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"chain", req.DestinationChain,
		"address", req.DestinationAddress)

	// Step 1: Validate Alpaca account and buying power
	var alpacaAccount *entities.AlpacaAccountResponse
	var getAccountErr error
	err := s.alpacaBreaker.Execute(ctx, func() error {
		alpacaAccount, getAccountErr = s.alpacaAPI.GetAccount(ctx, req.AlpacaAccountID)
		return getAccountErr
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		return nil, fmt.Errorf("Alpaca account not active: %s", alpacaAccount.Status)
	}

	if alpacaAccount.BuyingPower.LessThan(req.Amount) {
		return nil, fmt.Errorf("insufficient buying power: have %s, need %s",
			alpacaAccount.BuyingPower.String(), req.Amount.String())
	}

	// Step 2: Create withdrawal record
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

	// Step 3: Enqueue withdrawal processing to SQS
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
	if err := s.processDueOnRamp(ctx, withdrawal); err != nil {
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

// processDueOnRamp processes the Due on-ramp (USD → USDC)
func (s *WithdrawalService) processDueOnRamp(ctx context.Context, withdrawal *entities.Withdrawal) error {
	s.logger.Info("Processing Due on-ramp",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", withdrawal.Amount.String())

	req := &entities.InitiateWithdrawalRequest{
		UserID:             withdrawal.UserID,
		AlpacaAccountID:    withdrawal.AlpacaAccountID,
		Amount:             withdrawal.Amount,
		DestinationChain:   withdrawal.DestinationChain,
		DestinationAddress: withdrawal.DestinationAddress,
	}

	var dueResp *ProcessWithdrawalResponse
	var processErr error
	err := s.dueBreaker.Execute(ctx, func() error {
		dueResp, processErr = s.dueAPI.ProcessWithdrawal(ctx, req)
		return processErr
	})
	if err != nil {
		return fmt.Errorf("failed to process Due withdrawal: %w", err)
	}

	// Update withdrawal with Due transfer details
	if err := s.withdrawalRepo.UpdateDueTransfer(ctx, withdrawal.ID, dueResp.TransferID, dueResp.RecipientID); err != nil {
		return fmt.Errorf("failed to update Due transfer: %w", err)
	}

	s.logger.Info("Due on-ramp initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"transfer_id", dueResp.TransferID)

	return nil
}

// monitorTransferCompletion monitors the Due transfer until completion
func (s *WithdrawalService) monitorTransferCompletion(ctx context.Context, withdrawal *entities.Withdrawal) error {
	s.logger.Info("Monitoring transfer completion", "withdrawal_id", withdrawal.ID.String())

	// Reload withdrawal to get Due transfer ID
	w, err := s.withdrawalRepo.GetByID(ctx, withdrawal.ID)
	if err != nil {
		return fmt.Errorf("failed to get withdrawal: %w", err)
	}

	if w.DueTransferID == nil {
		return fmt.Errorf("no Due transfer ID found")
	}

	// Poll for transfer status (max 30 attempts, 10 seconds apart = 5 minutes)
	maxAttempts := 30
	pollInterval := 10 * time.Second

	for attempt := 0; attempt < maxAttempts; attempt++ {
		time.Sleep(pollInterval)

		var status *OnRampTransferResponse
		var statusErr error
		err := s.dueBreaker.Execute(ctx, func() error {
			status, statusErr = s.dueAPI.GetTransferStatus(ctx, *w.DueTransferID)
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
			return nil

		case "failed":
			return fmt.Errorf("Due transfer failed")

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
