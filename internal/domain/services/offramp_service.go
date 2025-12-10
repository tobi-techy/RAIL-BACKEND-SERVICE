package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/rail-service/rail_service/pkg/retry"
)

// OffRampService handles off-ramp operations
type OffRampService struct {
	dueAdapter         *due.OffRampAdapter
	alpacaAdapter      *alpaca.FundingAdapter
	depositRepo        repositories.DepositRepository
	virtualAccountRepo repositories.VirtualAccountRepository
	balanceService     *BalanceService
	notificationSvc    *NotificationService
	circuitBreaker     *gobreaker.CircuitBreaker
	logger             *logger.Logger
}

// NewOffRampService creates a new off-ramp service
func NewOffRampService(
	dueAdapter *due.OffRampAdapter,
	alpacaAdapter *alpaca.FundingAdapter,
	depositRepo repositories.DepositRepository,
	virtualAccountRepo repositories.VirtualAccountRepository,
	balanceService *BalanceService,
	notificationSvc *NotificationService,
	logger *logger.Logger,
) *OffRampService {
	// Configure circuit breaker
	st := gobreaker.Settings{
		Name:        "OffRampService",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
	}

	return &OffRampService{
		dueAdapter:         dueAdapter,
		alpacaAdapter:      alpacaAdapter,
		depositRepo:        depositRepo,
		virtualAccountRepo: virtualAccountRepo,
		balanceService:     balanceService,
		notificationSvc:    notificationSvc,
		circuitBreaker:     gobreaker.NewCircuitBreaker(st),
		logger:             logger,
	}
}

// InitiateOffRamp initiates the off-ramp process for a deposit
func (s *OffRampService) InitiateOffRamp(ctx context.Context, virtualAccountID, amount string) error {
	start := time.Now()
	defer func() {
		metrics.OffRampDuration.WithLabelValues("initiated").Observe(time.Since(start).Seconds())
	}()

	s.logger.Info("Initiating off-ramp",
		"virtual_account_id", virtualAccountID,
		"amount", amount)

	// Get virtual account details
	virtualAccount, err := s.virtualAccountRepo.GetByDueAccountID(ctx, virtualAccountID)
	if err != nil {
		return fmt.Errorf("failed to get virtual account: %w", err)
	}

	// Parse amount
	amountDecimal, err := decimal.NewFromString(amount)
	if err != nil {
		return fmt.Errorf("invalid amount: %w", err)
	}

	// Create deposit record
	deposit := &entities.Deposit{
		ID:               uuid.New(),
		UserID:           virtualAccount.UserID,
		VirtualAccountID: &virtualAccount.ID,
		Amount:           amountDecimal,
		Status:           "off_ramp_initiated",
		CreatedAt:        time.Now(),
	}

	if err := s.depositRepo.Create(ctx, deposit); err != nil {
		return fmt.Errorf("failed to create deposit: %w", err)
	}

	// Create Due recipient for Alpaca account if not exists
	recipientID, err := s.ensureRecipient(ctx, virtualAccount.AlpacaAccountID)
	if err != nil {
		return fmt.Errorf("failed to ensure recipient: %w", err)
	}

	// Initiate off-ramp with retry
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
	}

	var offRampResp *due.OffRampResponse
	retryFunc := func() error {
		req := &due.OffRampRequest{
			VirtualAccountID: virtualAccountID,
			RecipientID:      recipientID,
			Amount:           amountDecimal,
			Reference:        fmt.Sprintf("deposit_%s", deposit.ID.String()),
		}

		resp, err := s.dueAdapter.InitiateOffRamp(ctx, req)
		if err != nil {
			return err
		}
		offRampResp = resp
		return nil
	}

	isRetryable := func(err error) bool {
		// Retry on transient errors
		return err != nil
	}

	if err := retry.WithExponentialBackoff(ctx, retryConfig, retryFunc, isRetryable); err != nil {
		// Update deposit status to failed
		deposit.Status = "failed"
		_ = s.depositRepo.Update(ctx, deposit)

		// Track metrics
		metrics.OffRampTotal.WithLabelValues("failed").Inc()
		metrics.OffRampRetries.WithLabelValues("max_attempts").Inc()

		// Notify user of failure
		_ = s.notificationSvc.NotifyOffRampFailure(ctx, virtualAccount.UserID, err.Error())

		return fmt.Errorf("off-ramp initiation failed: %w", err)
	}

	// Update deposit with off-ramp details
	now := time.Now()
	deposit.OffRampTxID = &offRampResp.TransferID
	deposit.OffRampInitiatedAt = &now

	if err := s.depositRepo.Update(ctx, deposit); err != nil {
		s.logger.Error("Failed to update deposit with off-ramp details",
			"deposit_id", deposit.ID.String(),
			"error", err)
	}

	// Track metrics
	metrics.OffRampTotal.WithLabelValues("success").Inc()
	amountFloat, _ := amountDecimal.Float64()
	metrics.OffRampAmount.WithLabelValues("initiated").Observe(amountFloat)

	s.logger.Info("Off-ramp initiated successfully",
		"deposit_id", deposit.ID.String(),
		"transfer_id", offRampResp.TransferID)

	return nil
}

// HandleTransferCompleted handles completed transfer events
func (s *OffRampService) HandleTransferCompleted(ctx context.Context, transferID string) error {
	s.logger.Info("Handling transfer completion", "transfer_id", transferID)

	// Find deposit by transfer ID
	deposit, err := s.depositRepo.GetByOffRampTxID(ctx, transferID)
	if err != nil {
		return fmt.Errorf("failed to get deposit: %w", err)
	}

	// Get transfer status
	transferStatus, err := s.dueAdapter.GetTransferStatus(ctx, transferID)
	if err != nil {
		return fmt.Errorf("failed to get transfer status: %w", err)
	}

	// Update deposit status
	now := time.Now()
	deposit.OffRampCompletedAt = &now
	deposit.Status = "off_ramp_completed"

	if err := s.depositRepo.Update(ctx, deposit); err != nil {
		return fmt.Errorf("failed to update deposit: %w", err)
	}

	// Initiate Alpaca funding
	if err := s.fundAlpacaAccount(ctx, deposit, transferStatus.DestAmount); err != nil {
		s.logger.Error("Failed to fund Alpaca account",
			"deposit_id", deposit.ID.String(),
			"error", err)
		return fmt.Errorf("alpaca funding failed: %w", err)
	}

	return nil
}

// fundAlpacaAccount funds the Alpaca brokerage account
func (s *OffRampService) fundAlpacaAccount(ctx context.Context, deposit *entities.Deposit, amount decimal.Decimal) error {
	s.logger.Info("Funding Alpaca account",
		"deposit_id", deposit.ID.String(),
		"amount", amount.String())

	// Get virtual account to get Alpaca account ID
	virtualAccount, err := s.virtualAccountRepo.GetByID(ctx, *deposit.VirtualAccountID)
	if err != nil {
		return fmt.Errorf("failed to get virtual account: %w", err)
	}

	// Initiate funding with retry
	retryConfig := retry.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
		Multiplier:  2.0,
	}

	var fundingResp *entities.AlpacaInstantFundingResponse
	retryFunc := func() error {
		req := &entities.AlpacaInstantFundingRequest{
			AccountNo:       virtualAccount.AlpacaAccountID,
			SourceAccountNo: "SI",
			Amount:          amount,
		}

		resp, err := s.alpacaAdapter.InitiateInstantFunding(ctx, req)
		if err != nil {
			return err
		}
		fundingResp = resp
		return nil
	}

	isRetryable := func(err error) bool {
		return err != nil
	}

	if err := retry.WithExponentialBackoff(ctx, retryConfig, retryFunc, isRetryable); err != nil {
		return fmt.Errorf("alpaca funding failed: %w", err)
	}

	// Update deposit with Alpaca funding details
	now := time.Now()
	fundingTxID := fundingResp.ID
	deposit.AlpacaFundingTxID = &fundingTxID
	deposit.AlpacaFundedAt = &now
	deposit.Status = "broker_funded"

	if err := s.depositRepo.Update(ctx, deposit); err != nil {
		return fmt.Errorf("failed to update deposit: %w", err)
	}

	// Update user balance
	if err := s.balanceService.UpdateBuyingPower(ctx, deposit.UserID, amount); err != nil {
		s.logger.Error("Failed to update buying power",
			"deposit_id", deposit.ID.String(),
			"error", err)
	}

	// Notify user of success
	_ = s.notificationSvc.NotifyOffRampSuccess(ctx, deposit.UserID, amount.String())

	s.logger.Info("Alpaca account funded successfully",
		"deposit_id", deposit.ID.String(),
		"account_id", virtualAccount.AlpacaAccountID)

	return nil
}

// ensureRecipient ensures a Due recipient exists for the Alpaca account
func (s *OffRampService) ensureRecipient(ctx context.Context, alpacaAccountID string) (string, error) {
	// In production, this would check if recipient exists and create if needed
	// For now, return the Alpaca account ID as recipient ID
	// This assumes recipients are pre-created during virtual account setup
	return alpacaAccountID, nil
}
