package funding_webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

const (
	maxFundingRetries = 3
	fundingRetryDelay = 2 * time.Second
)

// AlpacaFundingOrchestrator handles Alpaca brokerage funding after off-ramp completion
type AlpacaFundingOrchestrator struct {
	depositRepo        DepositRepository
	balanceRepo        BalanceRepository
	virtualAccountRepo VirtualAccountRepository
	alpacaAdapter      AlpacaAdapter
	notificationSvc    NotificationService
	logger             *logger.Logger
}

// DepositRepository interface for deposit operations
type DepositRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Deposit, error)
	Update(ctx context.Context, deposit *entities.Deposit) error
}

// BalanceRepository interface for balance operations
type BalanceRepository interface {
	UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// VirtualAccountRepository interface for virtual account operations
type VirtualAccountRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.VirtualAccount, error)
}

// AlpacaAdapter interface for Alpaca API operations
type AlpacaAdapter interface {
	GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
	InitiateInstantFunding(ctx context.Context, req *entities.AlpacaInstantFundingRequest) (*entities.AlpacaInstantFundingResponse, error)
	GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
}

// NotificationService interface for user notifications
type NotificationService interface {
	NotifyFundingSuccess(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	NotifyFundingFailure(ctx context.Context, userID uuid.UUID, depositID uuid.UUID, reason string) error
}

// NewAlpacaFundingOrchestrator creates a new Alpaca funding orchestrator
func NewAlpacaFundingOrchestrator(
	depositRepo DepositRepository,
	balanceRepo BalanceRepository,
	virtualAccountRepo VirtualAccountRepository,
	alpacaAdapter AlpacaAdapter,
	notificationSvc NotificationService,
	logger *logger.Logger,
) *AlpacaFundingOrchestrator {
	return &AlpacaFundingOrchestrator{
		depositRepo:        depositRepo,
		balanceRepo:        balanceRepo,
		virtualAccountRepo: virtualAccountRepo,
		alpacaAdapter:      alpacaAdapter,
		notificationSvc:    notificationSvc,
		logger:             logger,
	}
}

// ProcessOffRampCompletion handles off-ramp completion and initiates Alpaca funding
func (o *AlpacaFundingOrchestrator) ProcessOffRampCompletion(ctx context.Context, depositID uuid.UUID) error {
	o.logger.Info("Processing off-ramp completion for Alpaca funding",
		"deposit_id", depositID.String())

	// Retrieve deposit
	deposit, err := o.depositRepo.GetByID(ctx, depositID)
	if err != nil {
		o.logger.Error("Failed to retrieve deposit", "error", err, "deposit_id", depositID.String())
		return fmt.Errorf("failed to retrieve deposit: %w", err)
	}

	// Validate deposit status
	if deposit.Status != "off_ramp_completed" {
		o.logger.Warn("Deposit not in off_ramp_completed status",
			"deposit_id", depositID.String(),
			"status", deposit.Status)
		return fmt.Errorf("deposit status is %s, expected off_ramp_completed", deposit.Status)
	}

	// Get virtual account to retrieve Alpaca account ID
	if deposit.VirtualAccountID == nil {
		o.logger.Error("Deposit missing virtual account ID", "deposit_id", depositID.String())
		return fmt.Errorf("deposit missing virtual account ID")
	}

	virtualAccount, err := o.virtualAccountRepo.GetByID(ctx, *deposit.VirtualAccountID)
	if err != nil {
		o.logger.Error("Failed to retrieve virtual account",
			"error", err,
			"virtual_account_id", deposit.VirtualAccountID.String())
		return fmt.Errorf("failed to retrieve virtual account: %w", err)
	}

	// Initiate Alpaca funding with retry logic
	var lastErr error
	for attempt := 0; attempt < maxFundingRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(fundingRetryDelay * time.Duration(attempt))
		}
		err = o.initiateFunding(ctx, deposit, virtualAccount.AlpacaAccountID)
		if err == nil {
			break
		}
		lastErr = err
		o.logger.Warn("Alpaca funding attempt failed, retrying",
			"attempt", attempt+1,
			"error", err.Error())
	}
	err = lastErr

	if err != nil {
		o.logger.Error("Failed to initiate Alpaca funding after retries",
			"error", err,
			"deposit_id", depositID.String(),
			"retries", maxFundingRetries)

		// Notify user of failure
		if notifyErr := o.notificationSvc.NotifyFundingFailure(ctx, deposit.UserID, depositID, err.Error()); notifyErr != nil {
			o.logger.Error("Failed to send funding failure notification",
				"error", notifyErr,
				"user_id", deposit.UserID.String())
		}

		return fmt.Errorf("failed to initiate Alpaca funding: %w", err)
	}

	// Update balance with new buying power
	if err := o.balanceRepo.UpdateBuyingPower(ctx, deposit.UserID, deposit.Amount); err != nil {
		o.logger.Error("Failed to update buying power",
			"error", err,
			"user_id", deposit.UserID.String(),
			"amount", deposit.Amount.String())
		return fmt.Errorf("failed to update buying power: %w", err)
	}

	// Notify user of success
	if err := o.notificationSvc.NotifyFundingSuccess(ctx, deposit.UserID, deposit.Amount); err != nil {
		o.logger.Error("Failed to send funding success notification",
			"error", err,
			"user_id", deposit.UserID.String())
		// Don't fail the operation if notification fails
	}

	o.logger.Info("Alpaca funding completed successfully",
		"deposit_id", depositID.String(),
		"user_id", deposit.UserID.String(),
		"amount", deposit.Amount.String())

	return nil
}

// initiateFunding performs the actual funding initiation
func (o *AlpacaFundingOrchestrator) initiateFunding(ctx context.Context, deposit *entities.Deposit, alpacaAccountID string) error {
	// Verify Alpaca account is active
	alpacaAccount, err := o.alpacaAdapter.GetAccount(ctx, alpacaAccountID)
	if err != nil {
		return fmt.Errorf("failed to get Alpaca account: %w", err)
	}

	if alpacaAccount.Status != entities.AlpacaAccountStatusActive {
		return fmt.Errorf("Alpaca account not active: %s", alpacaAccount.Status)
	}

	// Create instant funding request
	fundingReq := &entities.AlpacaInstantFundingRequest{
		AccountNo:       alpacaAccount.AccountNumber,
		SourceAccountNo: "SI",
		Amount:          deposit.Amount,
	}

	// Initiate instant funding
	fundingResp, err := o.alpacaAdapter.InitiateInstantFunding(ctx, fundingReq)
	if err != nil {
		return fmt.Errorf("failed to initiate instant funding: %w", err)
	}

	// Update deposit with funding details
	now := time.Now()
	txID := fundingResp.ID
	deposit.AlpacaFundingTxID = &txID
	deposit.AlpacaFundedAt = &now
	deposit.Status = "broker_funded"

	if err := o.depositRepo.Update(ctx, deposit); err != nil {
		return fmt.Errorf("failed to update deposit status: %w", err)
	}

	o.logger.Info("Instant funding initiated",
		"transfer_id", fundingResp.ID,
		"status", fundingResp.Status,
		"alpaca_account_id", alpacaAccountID)

	return nil
}
