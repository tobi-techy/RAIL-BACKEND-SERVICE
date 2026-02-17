package withdrawal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// Crypto withdrawal constants
const (
	CryptoWithdrawalMinAmount   = 10.00 // Minimum crypto withdrawal
	FiatWithdrawalMinAmountUSD  = 10.00 // Minimum USD fiat withdrawal
	FiatWithdrawalMinAmountEUR  = 10.00 // Minimum EUR fiat withdrawal
	CryptoWithdrawalFeePercent  = 0.0   // Circle transfers are free (network fees apply)
	FiatWithdrawalFeePercentUSD = 0.01  // 1% + $0.50 for USD
	FiatWithdrawalFeeFixedUSD   = 0.50
	FiatWithdrawalFeePercentEUR = 0.01 // 1% + €0.50 for EUR
	FiatWithdrawalFeeFixedEUR   = 0.50
)

// LedgerService interface for ledger operations
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
}

// BankAccountRepository interface for bank account persistence
type BankAccountRepository interface {
	Create(ctx context.Context, bankAccount *entities.BankAccount) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.BankAccount, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BankAccount, error)
	Update(ctx context.Context, bankAccount *entities.BankAccount) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetByBridgeRecipientID(ctx context.Context, recipientID string) (*entities.BankAccount, error)
}

// StashTransferRepository interface for stash transfer persistence
type StashTransferRepository interface {
	Create(ctx context.Context, transfer *entities.StashTransfer) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.StashTransfer, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.StashTransfer, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.StashTransferStatus) error
	MarkCompleted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID) error
}

// WithdrawalRepository interface for withdrawal persistence
type WithdrawalRepository interface {
	Create(ctx context.Context, withdrawal *entities.Withdrawal) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.Withdrawal, error)
	GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*entities.Withdrawal, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.WithdrawalStatus) error
	UpdateBridgeTransfer(ctx context.Context, id uuid.UUID, transferID string) error
	UpdateTxHash(ctx context.Context, id uuid.UUID, txHash string) error
	MarkCompleted(ctx context.Context, id uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, errorMsg string) error
	GetPendingWithdrawalsTotal(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// UserRepository interface for KYC/capability checks.
type UserRepository interface {
	GetUserEntityByID(ctx context.Context, id uuid.UUID) (*entities.User, error)
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
	NotifyWithdrawalCompleted(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error
	NotifyWithdrawalFailed(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error
	NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error
}

// CircleClient interface for Circle wallet operations
type CircleClient interface {
	TransferFunds(ctx context.Context, req entities.CircleTransferRequest) (map[string]interface{}, error)
	GetWallet(ctx context.Context, walletID string) (map[string]interface{}, error)
}

// BridgeAdapter interface for Bridge offramp operations
type BridgeAdapter interface {
	CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error)
	InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error)
	GetTransferStatus(ctx context.Context, transferID string) (map[string]interface{}, error)
}

// WithdrawalService handles crypto and fiat withdrawal operations
type WithdrawalService struct {
	withdrawalRepo      WithdrawalRepository
	userRepo            UserRepository
	bankAccountRepo     BankAccountRepository
	stashTransferRepo   StashTransferRepository
	ledgerService       LedgerService
	limitsService       WithdrawalLimitsService
	auditService        WithdrawalAuditService
	notificationService WithdrawalNotificationService
	circleClient        CircleClient
	bridgeAdapter       BridgeAdapter
	logger              *logger.Logger
}

// NewWithdrawalService creates a new withdrawal service
func NewWithdrawalService(
	withdrawalRepo WithdrawalRepository,
	userRepo UserRepository,
	ledgerService LedgerService,
	bankAccountRepo BankAccountRepository,
	limitsService WithdrawalLimitsService,
	auditService WithdrawalAuditService,
	notificationService WithdrawalNotificationService,
	circleClient CircleClient,
	bridgeAdapter BridgeAdapter,
	logger *logger.Logger,
) *WithdrawalService {
	return &WithdrawalService{
		withdrawalRepo:      withdrawalRepo,
		userRepo:            userRepo,
		ledgerService:       ledgerService,
		bankAccountRepo:     bankAccountRepo,
		limitsService:       limitsService,
		auditService:        auditService,
		notificationService: notificationService,
		circleClient:        circleClient,
		bridgeAdapter:       bridgeAdapter,
		logger:              logger,
	}
}

// InitiateCryptoWithdrawal initiates a crypto withdrawal (USDC to external wallet)
func (s *WithdrawalService) InitiateCryptoWithdrawal(ctx context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	s.logger.Info("Initiating crypto withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"destination", req.DestinationAddress,
		"source_account", req.SourceAccount)

	// Step 1: Validate request
	if err := req.Validate(); err != nil {
		s.logger.Warn("Invalid crypto withdrawal request", "error", err.Error())
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Step 2: Check idempotency
	if req.IdempotencyKey != "" {
		existing, err := s.withdrawalRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil {
			s.logger.Error("Failed to check idempotency key", "error", err)
			return nil, fmt.Errorf("failed to check idempotency: %w", err)
		}
		if existing != nil {
			s.logger.Info("Returning existing withdrawal for idempotency key", "withdrawal_id", existing.ID.String())
			return &entities.InitiateWithdrawalResponse{
				WithdrawalID: existing.ID,
				Status:       existing.Status,
				Message:      "Withdrawal already exists",
			}, nil
		}
	}

	// Step 3: Validate against withdrawal limits
	if s.limitsService != nil {
		result, err := s.limitsService.ValidateWithdrawal(ctx, req.UserID, req.Amount)
		if err != nil {
			s.logger.Warn("Withdrawal limit validation failed", "error", err.Error())
			if result != nil {
				return nil, fmt.Errorf("withdrawal limit exceeded (%s): %s remaining until %v",
					result.LimitType, result.RemainingCapacity.String(), result.ResetsAt)
			}
			return nil, fmt.Errorf("withdrawal limit exceeded: %w", err)
		}
	}

	// Step 4: Check balance based on source account
	balance, err := s.getSourceBalance(ctx, req.UserID, req.SourceAccount)
	if err != nil {
		s.logger.Error("Failed to get source balance", "error", err)
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	if balance.LessThan(req.Amount) {
		return nil, fmt.Errorf("insufficient balance: have %s, need %s", balance.String(), req.Amount.String())
	}

	// Step 5: Calculate fee
	fee := s.calculateCryptoWithdrawalFee(req.Amount)
	totalAmount := req.Amount.Add(fee)

	if balance.LessThan(totalAmount) {
		return nil, fmt.Errorf("insufficient balance for withdrawal + fee: have %s, need %s", balance.String(), totalAmount.String())
	}

	// Step 6: Create withdrawal record
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
	}

	withdrawal := &entities.Withdrawal{
		ID:                 uuid.New(),
		UserID:             req.UserID,
		WithdrawalType:     entities.WithdrawalTypeCrypto,
		Currency:           entities.WithdrawalCurrencyUSDC,
		Amount:             req.Amount,
		SourceAccount:      req.SourceAccount,
		CircleWalletID:     &req.CircleWalletID,
		DestinationType:    entities.DestinationTypeCryptoWallet,
		DestinationAddress: &req.DestinationAddress,
		FeeAmount:          fee,
		FeeCurrency:        entities.WithdrawalCurrencyUSDC,
		Status:             entities.WithdrawalStatusInitiated,
		IdempotencyKey:     &idempotencyKey,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create withdrawal", "error", err)
		return nil, fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Step 7: Execute Circle transfer
	txHash, err := s.executeCryptoTransfer(ctx, withdrawal, req.DestinationAddress, req.DestinationChain)
	if err != nil {
		s.logger.Error("Failed to execute crypto transfer", "error", err)
		// Mark withdrawal as failed
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, fmt.Errorf("failed to execute transfer: %w", err)
	}

	// Update tx hash
	if txHash != "" {
		if err := s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, txHash); err != nil {
			s.logger.Error("Failed to update tx hash", "error", err)
		}
	}

	// Step 8: Update status to completed (Circle transfers are instant)
	now := time.Now()
	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		s.logger.Error("Failed to mark withdrawal completed", "error", err)
		return nil, fmt.Errorf("failed to complete withdrawal: %w", err)
	}
	withdrawal.Status = entities.WithdrawalStatusCompleted
	withdrawal.CompletedAt = &now

	// Step 9: Record against limits
	if s.limitsService != nil {
		if err := s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount); err != nil {
			s.logger.Error("Failed to record withdrawal against limits", "error", err)
		}
	}

	// Step 10: Send notification
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalCompleted(ctx, req.UserID, req.Amount, req.DestinationAddress)
	}

	s.logger.Info("Crypto withdrawal completed",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", req.Amount.String(),
		"tx_hash", txHash)

	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: withdrawal.ID,
		Status:       withdrawal.Status,
		Message:      "Withdrawal completed successfully",
	}, nil
}

// InitiateFiatWithdrawal initiates a fiat withdrawal (USDC to fiat via Bridge)
func (s *WithdrawalService) InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	s.logger.Info("Initiating fiat withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"currency", req.Currency,
		"routing_number", req.RoutingNumber,
		"source_account", req.SourceAccount)

	// Step 1: Validate request
	if err := req.Validate(); err != nil {
		s.logger.Warn("Invalid fiat withdrawal request", "error", err.Error())
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Step 2: Ensure user is eligible for Bridge-based fiat withdrawal.
	if s.userRepo != nil {
		user, err := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify user KYC status: %w", err)
		}

		bridgeActive := user.BridgeKYCStatus != nil && *user.BridgeKYCStatus == "active"
		legacyApproved := user.KYCStatus == "approved"
		if !bridgeActive && !legacyApproved {
			return nil, fmt.Errorf("bridge kyc verification required for fiat withdrawals")
		}
	}

	// Step 3: Check idempotency
	if req.IdempotencyKey != "" {
		existing, err := s.withdrawalRepo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil {
			s.logger.Error("Failed to check idempotency key", "error", err)
			return nil, fmt.Errorf("failed to check idempotency: %w", err)
		}
		if existing != nil {
			s.logger.Info("Returning existing withdrawal for idempotency key", "withdrawal_id", existing.ID.String())
			return &entities.InitiateWithdrawalResponse{
				WithdrawalID: existing.ID,
				Status:       existing.Status,
				Message:      "Withdrawal already exists",
			}, nil
		}
	}

	// Step 4: Validate against withdrawal limits
	if s.limitsService != nil {
		result, err := s.limitsService.ValidateWithdrawal(ctx, req.UserID, req.Amount)
		if err != nil {
			s.logger.Warn("Withdrawal limit validation failed", "error", err.Error())
			if result != nil {
				return nil, fmt.Errorf("withdrawal limit exceeded (%s): %s remaining until %v",
					result.LimitType, result.RemainingCapacity.String(), result.ResetsAt)
			}
			return nil, fmt.Errorf("withdrawal limit exceeded: %w", err)
		}
	}

	// Step 5: Check balance based on source account
	balance, err := s.getSourceBalance(ctx, req.UserID, req.SourceAccount)
	if err != nil {
		s.logger.Error("Failed to get source balance", "error", err)
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	if balance.LessThan(req.Amount) {
		return nil, fmt.Errorf("insufficient balance: have %s, need %s", balance.String(), req.Amount.String())
	}

	// Step 6: Calculate fee
	fee := s.calculateFiatWithdrawalFee(req.Amount, req.Currency)
	totalAmount := req.Amount.Add(fee)

	if balance.LessThan(totalAmount) {
		return nil, fmt.Errorf("insufficient balance for withdrawal + fee: have %s, need %s", balance.String(), totalAmount.String())
	}

	// Step 7: Create or get bank account with routing number
	bankAccount, err := s.getOrCreateBankAccount(ctx, req.UserID, req.RoutingNumber, req.Currency)
	if err != nil {
		s.logger.Error("Failed to create bank account", "error", err)
		return nil, fmt.Errorf("failed to setup bank account: %w", err)
	}

	// Step 8: Create withdrawal record
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = uuid.New().String()
	}

	withdrawal := &entities.Withdrawal{
		ID:              uuid.New(),
		UserID:          req.UserID,
		WithdrawalType:  entities.WithdrawalTypeFiat,
		Currency:        req.Currency,
		Amount:          req.Amount,
		SourceAccount:   req.SourceAccount,
		DestinationType: entities.DestinationTypeBankAccount,
		BankAccountID:   &bankAccount.ID,
		FeeAmount:       fee,
		FeeCurrency:     entities.WithdrawalCurrencyUSDC, // Fees deducted in USDC
		Status:          entities.WithdrawalStatusInitiated,
		IdempotencyKey:  &idempotencyKey,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create withdrawal", "error", err)
		return nil, fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Step 9: Execute Bridge offramp transfer
	transferID, err := s.executeFiatTransfer(ctx, withdrawal, bankAccount)
	if err != nil {
		s.logger.Error("Failed to execute fiat transfer", "error", err)
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, fmt.Errorf("failed to execute transfer: %w", err)
	}

	// Update bridge transfer ID
	if err := s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, transferID); err != nil {
		s.logger.Error("Failed to update bridge transfer ID", "error", err)
	}

	// Update status to processing
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
		s.logger.Error("Failed to update withdrawal status", "error", err)
		return nil, fmt.Errorf("failed to update status: %w", err)
	}

	withdrawal.Status = entities.WithdrawalStatusProcessing

	s.logger.Info("Fiat withdrawal initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", req.Amount.String(),
		"transfer_id", transferID)

	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: withdrawal.ID,
		Status:       withdrawal.Status,
		Message:      "Fiat withdrawal initiated. Processing may take 1-3 business days.",
	}, nil
}

// getOrCreateBankAccount finds existing bank account by routing number or creates new one
func (s *WithdrawalService) getOrCreateBankAccount(ctx context.Context, userID uuid.UUID, routingNumber string, currency entities.WithdrawalCurrency) (*entities.BankAccount, error) {
	// Check if user already has a bank account with this routing number
	existingAccounts, err := s.bankAccountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing accounts: %w", err)
	}

	for _, acc := range existingAccounts {
		if acc.RoutingNumber != nil && *acc.RoutingNumber == routingNumber {
			s.logger.Info("Found existing bank account", "bank_account_id", acc.ID.String())
			return acc, nil
		}
	}

	// Create new bank account
	routingLast4 := routingNumber[len(routingNumber)-4:]
	bankCurrency := entities.BankAccountCurrencyUSD
	if currency == entities.WithdrawalCurrencyEUR {
		bankCurrency = entities.BankAccountCurrencyEUR
	}

	bankAccount := &entities.BankAccount{
		ID:                 uuid.New(),
		UserID:             userID,
		BankName:           "Bank", // Will be resolved by Bridge
		AccountNumberLast4: "0000", // Placeholder - Bridge handles full account
		RoutingNumber:      &routingNumber,
		RoutingNumberLast4: &routingLast4,
		Currency:           bankCurrency,
		IsVerified:         false,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	// Register with Bridge to get recipient ID
	if s.bridgeAdapter != nil {
		recipientReq := map[string]interface{}{
			"routing_number": routingNumber,
			"currency":       string(currency),
		}

		recipientID, err := s.bridgeAdapter.CreateRecipient(ctx, recipientReq)
		if err != nil {
			return nil, fmt.Errorf("failed to register with Bridge: %w", err)
		}

		bankAccount.BridgeRecipientID = &recipientID
		bankAccount.IsVerified = true
	}

	if err := s.bankAccountRepo.Create(ctx, bankAccount); err != nil {
		return nil, fmt.Errorf("failed to save bank account: %w", err)
	}

	s.logger.Info("Created new bank account",
		"bank_account_id", bankAccount.ID.String(),
		"routing_number_last4", routingLast4)

	return bankAccount, nil
}

// GetWithdrawalFee returns the fee for a withdrawal
func (s *WithdrawalService) GetWithdrawalFee(ctx context.Context, withdrawalType entities.WithdrawalType, amount decimal.Decimal, currency entities.WithdrawalCurrency) (*entities.WithdrawalFee, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}

	feeResponse := &entities.WithdrawalFee{
		Amount:   amount,
		Currency: currency,
	}

	switch withdrawalType {
	case entities.WithdrawalTypeCrypto:
		fee := s.calculateCryptoWithdrawalFee(amount)
		feeResponse.Amount = fee
		feeResponse.Currency = entities.WithdrawalCurrencyUSDC
		feeResponse.Breakdown.NetworkFee = fee
		feeResponse.Breakdown.ServiceFee = decimal.Zero
	case entities.WithdrawalTypeFiat:
		fee := s.calculateFiatWithdrawalFee(amount, currency)
		feeResponse.Amount = fee
		feeResponse.Currency = entities.WithdrawalCurrencyUSDC
		feeResponse.Breakdown.ServiceFee = fee
		feeResponse.Breakdown.NetworkFee = decimal.Zero
	default:
		return nil, fmt.Errorf("invalid withdrawal type: %s", withdrawalType)
	}

	return feeResponse, nil
}

// CancelWithdrawal cancels a pending withdrawal
func (s *WithdrawalService) CancelWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) error {
	s.logger.Info("Cancelling withdrawal",
		"user_id", userID.String(),
		"withdrawal_id", withdrawalID.String())

	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("withdrawal not found: %w", err)
	}

	if withdrawal.UserID != userID {
		return fmt.Errorf("withdrawal does not belong to user")
	}

	if !withdrawal.Status.IsPending() {
		return fmt.Errorf("cannot cancel withdrawal in status: %s", withdrawal.Status)
	}

	// For fiat withdrawals, we may need to cancel the Bridge transfer
	if withdrawal.IsFiat() && withdrawal.BridgeTransferID != nil {
		// TODO: Add Bridge cancellation logic if supported
		s.logger.Info("Fiat withdrawal cancellation - Bridge cancellation not implemented yet",
			"transfer_id", *withdrawal.BridgeTransferID)
	}

	if err := s.withdrawalRepo.MarkFailed(ctx, withdrawalID, "Cancelled by user"); err != nil {
		return fmt.Errorf("failed to cancel withdrawal: %w", err)
	}

	// Send notification
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalFailed(ctx, userID, withdrawal.Amount, "Cancelled by user")
	}

	return nil
}

// GetWithdrawal gets a withdrawal by ID
func (s *WithdrawalService) GetWithdrawal(ctx context.Context, userID, withdrawalID uuid.UUID) (*entities.Withdrawal, error) {
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return nil, fmt.Errorf("withdrawal not found: %w", err)
	}

	if withdrawal.UserID != userID {
		return nil, fmt.Errorf("withdrawal does not belong to user")
	}

	return withdrawal, nil
}

// GetUserWithdrawals gets all withdrawals for a user
func (s *WithdrawalService) GetUserWithdrawals(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	return s.withdrawalRepo.GetByUserID(ctx, userID, limit, offset)
}

// getSourceBalance gets the balance for the specified source account
func (s *WithdrawalService) getSourceBalance(ctx context.Context, userID uuid.UUID, sourceAccount entities.WithdrawalSourceAccount) (decimal.Decimal, error) {
	var accountType entities.AccountType
	switch sourceAccount {
	case entities.WithdrawalSourceSpendingBalance:
		accountType = entities.AccountTypeSpendingBalance
	case entities.WithdrawalSourceStashBalance:
		accountType = entities.AccountTypeStashBalance
	default:
		return decimal.Zero, fmt.Errorf("invalid source account: %s", sourceAccount)
	}

	return s.ledgerService.GetAccountBalance(ctx, userID, accountType)
}

// calculateCryptoWithdrawalFee calculates the fee for a crypto withdrawal
func (s *WithdrawalService) calculateCryptoWithdrawalFee(amount decimal.Decimal) decimal.Decimal {
	// Circle transfers are free for internal transfers
	// Network fees may apply for external transfers (minimal for USDC on Solana/Ethereum)
	return decimal.Zero
}

// calculateFiatWithdrawalFee calculates the fee for a fiat withdrawal
func (s *WithdrawalService) calculateFiatWithdrawalFee(amount decimal.Decimal, currency entities.WithdrawalCurrency) decimal.Decimal {
	var fee decimal.Decimal
	switch currency {
	case entities.WithdrawalCurrencyUSD:
		percentFee := amount.Mul(decimal.NewFromFloat(FiatWithdrawalFeePercentUSD))
		fixedFee := decimal.NewFromFloat(FiatWithdrawalFeeFixedUSD)
		fee = percentFee.Add(fixedFee)
	case entities.WithdrawalCurrencyEUR:
		percentFee := amount.Mul(decimal.NewFromFloat(FiatWithdrawalFeePercentEUR))
		fixedFee := decimal.NewFromFloat(FiatWithdrawalFeeFixedEUR)
		fee = percentFee.Add(fixedFee)
	default:
		fee = decimal.Zero
	}
	return fee
}

// executeCryptoTransfer executes a crypto transfer via Circle
func (s *WithdrawalService) executeCryptoTransfer(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, destinationChain string) (string, error) {
	if s.circleClient == nil {
		return "", fmt.Errorf("circle client not configured")
	}

	walletID := withdrawal.CircleWalletID
	if walletID == nil || *walletID == "" {
		return "", fmt.Errorf("circle wallet ID not provided")
	}

	// Format amount for Circle API (USDC uses 6 decimals)
	amountStr := withdrawal.Amount.StringFixed(6)

	req := entities.CircleTransferRequest{
		WalletID:           *walletID,
		TokenID:            "USDC", // USDC token
		Amounts:            []string{amountStr},
		DestinationAddress: destinationAddress,
		IDempotencyKey:     *withdrawal.IdempotencyKey,
	}

	// Add destination chain if specified
	if destinationChain != "" {
		// Circle handles chain routing automatically for most cases
		s.logger.Debug("Destination chain specified", "chain", destinationChain)
	}

	response, err := s.circleClient.TransferFunds(ctx, req)
	if err != nil {
		return "", fmt.Errorf("circle transfer failed: %w", err)
	}

	// Extract transaction hash from response
	// The response format depends on Circle's API
	var txHash string
	if txHashVal, ok := response["transactionHash"].(string); ok {
		txHash = txHashVal
	} else if txHashVal, ok := response["txHash"].(string); ok {
		txHash = txHashVal
	}

	s.logger.Info("Crypto transfer executed",
		"withdrawal_id", withdrawal.ID.String(),
		"tx_hash", txHash)

	return txHash, nil
}

// executeFiatTransfer executes a fiat transfer via Bridge offramp
func (s *WithdrawalService) executeFiatTransfer(ctx context.Context, withdrawal *entities.Withdrawal, bankAccount *entities.BankAccount) (string, error) {
	if s.bridgeAdapter == nil {
		return "", fmt.Errorf("bridge adapter not configured")
	}

	if bankAccount.BridgeRecipientID == nil || *bankAccount.BridgeRecipientID == "" {
		return "", fmt.Errorf("bank account not registered with Bridge")
	}

	// Format amount
	amountStr := withdrawal.Amount.StringFixed(2)

	// Create transfer request
	req := map[string]interface{}{
		"source":          "USDC",
		"amount":          amountStr,
		"currency":        string(withdrawal.Currency),
		"recipient_id":    *bankAccount.BridgeRecipientID,
		"idempotency_key": *withdrawal.IdempotencyKey,
	}

	response, err := s.bridgeAdapter.InitiateTransfer(ctx, req)
	if err != nil {
		return "", fmt.Errorf("bridge transfer failed: %w", err)
	}

	// Extract transfer ID from response
	transferID, ok := response["id"].(string)
	if !ok {
		return "", fmt.Errorf("failed to get transfer ID from response")
	}

	s.logger.Info("Fiat transfer initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"transfer_id", transferID)

	return transferID, nil
}
