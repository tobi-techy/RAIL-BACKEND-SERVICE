package withdrawal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/cctp"
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
	withdrawalLockShards        = 256
)

// LedgerService interface for ledger operations
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
	CreateTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, metadata map[string]interface{}) error
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
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
	MarkCancelled(ctx context.Context, id uuid.UUID) error
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
	GetCCTPTransaction(ctx context.Context, transactionID string) (*entities.CCTPTransactionStatus, error)
	FindRecentOutboundTransfer(ctx context.Context, walletID, destinationAddress string, amount decimal.Decimal, since time.Time) (*entities.CCTPTransactionStatus, error)
	InitiateCCTPBurn(ctx context.Context, req *entities.CCTPBurnRequest) (*entities.CCTPBurnResponse, error)
}

// BridgeAdapter interface for Bridge offramp operations
type BridgeAdapter interface {
	CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error)
	InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error)
	GetTransferStatus(ctx context.Context, transferID string) (map[string]interface{}, error)
	CancelTransfer(ctx context.Context, transferID string) error
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
	withdrawalLocks     [withdrawalLockShards]sync.Mutex
}

type CryptoTransferResult struct {
	TxHash     string
	TransferID string
	State      string
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
	lock := s.userWithdrawalLock(req.UserID)
	lock.Lock()
	defer lock.Unlock()

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

	// Step 1.5: Validate source and destination chains
	if err := validateChainPair(req.SourceChain, req.DestinationChain); err != nil {
		s.logger.Warn("Invalid chain configuration", "source_chain", req.SourceChain, "dest_chain", req.DestinationChain, "error", err)
		return nil, fmt.Errorf("invalid chain configuration: %w", err)
	}

	clientProvidedIdempotency := strings.TrimSpace(req.IdempotencyKey) != ""
	idempotencyKey := scopedWithdrawalIdempotencyKey(req.UserID, "crypto", req.IdempotencyKey)

	// Step 2: Check idempotency (always — auto-key covers retry deduplication)
	existing, err := s.withdrawalRepo.GetByIdempotencyKey(ctx, idempotencyKey)
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
	_ = clientProvidedIdempotency // retained for potential future logging

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
	withdrawal := &entities.Withdrawal{
		ID:                 uuid.New(),
		UserID:             req.UserID,
		WithdrawalType:     entities.WithdrawalTypeCrypto,
		Currency:           entities.WithdrawalCurrencyUSDC,
		Amount:             req.Amount,
		SourceAccount:      req.SourceAccount,
		CircleWalletID:     &req.CircleWalletID,
		DestinationType:    entities.DestinationTypeCryptoWallet,
		DestinationChain:   req.DestinationChain,
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

	// Re-check pending exposure after creating the record to protect against near-simultaneous requests.
	// Must re-fetch balance since the old balance is stale after creating the withdrawal record.
	currentBalance, err := s.getSourceBalance(ctx, req.UserID, req.SourceAccount)
	if err != nil {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "failed to re-check balance")
		return nil, fmt.Errorf("failed to re-check balance: %w", err)
	}

	if err := s.ensurePendingExposureWithinBalance(ctx, req.UserID, currentBalance); err != nil {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, err
	}

	// Step 6.5: Post ledger debit BEFORE executing the on-chain burn.
	// This ensures the balance is decremented even if the burn succeeds but the
	// subsequent ledger write would otherwise fail after funds are already gone.
	if err := s.postWithdrawalLedgerEntries(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to post pre-burn ledger debit", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "ledger debit failed: "+err.Error())
		return nil, fmt.Errorf("failed to post ledger debit: %w", err)
	}

	// Step 7: Execute Circle transfer
	transferResult, err := s.executeCryptoTransfer(ctx, withdrawal, req.DestinationAddress, req.DestinationChain, req.SourceChain)
	if err != nil {
		s.logger.Error("Failed to execute crypto transfer", "error", err)
		// Reverse the ledger debit when transfer fails
		if revErr := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); revErr != nil {
			s.logger.Error("Failed to reverse ledger debit after transfer failure",
				"error", revErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, fmt.Errorf("failed to execute transfer: %w", err)
	}

	// Persist provider transfer reference when available (reuses bridge_transfer_id column).
	if transferResult.TransferID != "" {
		if err := s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, transferResult.TransferID); err != nil {
			s.logger.Error("Failed to update transfer ID", "error", err)
		}
	}

	// Update tx hash when available.
	if transferResult.TxHash != "" {
		if err := s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, transferResult.TxHash); err != nil {
			s.logger.Error("Failed to update tx hash", "error", err)
		} else {
			withdrawal.Status = entities.WithdrawalStatusOnChainTransfer
			withdrawal.TxHash = &transferResult.TxHash
		}
	}

	// Circle transfer requests can complete asynchronously.
	// Only mark completed when Circle explicitly reports a final successful state.
	state := strings.ToUpper(strings.TrimSpace(transferResult.State))
	isFinalSuccess := state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" || state == "SUCCESS"

	// Record against limits as soon as the burn is accepted (sync or async).
	// The withdrawal is committed at this point regardless of final on-chain state.
	if s.limitsService != nil {
		if err := s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount); err != nil {
			s.logger.Error("Failed to record withdrawal against limits", "error", err)
		}
	}

	if !isFinalSuccess {
		// Don't block the HTTP handler polling Circle — let webhooks settle the final state.
		// Just do a single non-blocking status check.
		if _, err := s.syncCryptoWithdrawalStatusFromProvider(ctx, withdrawal); err != nil {
			s.logger.Warn("Failed to sync Circle withdrawal status during initiation",
				"withdrawal_id", withdrawal.ID.String(),
				"error", err)
		}

		if withdrawal.Status == entities.WithdrawalStatusFailed {
			// Reverse the ledger debit — the transfer failed on-chain.
			if revErr := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); revErr != nil {
				s.logger.Error("Failed to reverse ledger debit after provider failure",
					"error", revErr, "withdrawal_id", withdrawal.ID.String())
			}
			failReason := "withdrawal failed during processing"
			if withdrawal.ErrorMessage != nil && strings.TrimSpace(*withdrawal.ErrorMessage) != "" {
				failReason = *withdrawal.ErrorMessage
			}
			return nil, fmt.Errorf("%s", failReason)
		}

		if withdrawal.Status == entities.WithdrawalStatusCompleted {
			// Send notification
			if s.notificationService != nil {
				_ = s.notificationService.NotifyWithdrawalCompleted(ctx, req.UserID, req.Amount, req.DestinationAddress)
			}

			s.logger.Info("Crypto withdrawal completed",
				"withdrawal_id", withdrawal.ID.String(),
				"amount", req.Amount.String(),
				"tx_hash", transferResult.TxHash)

			return &entities.InitiateWithdrawalResponse{
				WithdrawalID: withdrawal.ID,
				Status:       withdrawal.Status,
				Message:      "Withdrawal completed successfully",
			}, nil
		}

		if withdrawal.Status == entities.WithdrawalStatusInitiated {
			if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
				s.logger.Error("Failed to mark withdrawal processing", "error", err)
				return nil, fmt.Errorf("failed to update withdrawal status: %w", err)
			}
			withdrawal.Status = entities.WithdrawalStatusProcessing
		}

		s.logger.Info("Crypto withdrawal submitted and processing",
			"withdrawal_id", withdrawal.ID.String(),
			"amount", req.Amount.String(),
			"state", transferResult.State,
			"transfer_id", transferResult.TransferID)

		return &entities.InitiateWithdrawalResponse{
			WithdrawalID: withdrawal.ID,
			Status:       withdrawal.Status,
			Message:      "Withdrawal submitted and is processing",
		}, nil
	}

	// Step 8: Final success path.
	if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to settle completed withdrawal", "error", err, "withdrawal_id", withdrawal.ID.String())
		withdrawal.Status = entities.WithdrawalStatusProcessing
		return &entities.InitiateWithdrawalResponse{
			WithdrawalID: withdrawal.ID,
			Status:       withdrawal.Status,
			Message:      "Withdrawal submitted and is processing",
		}, nil
	}

	// Step 9: Send notification
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalCompleted(ctx, req.UserID, req.Amount, req.DestinationAddress)
	}

	s.logger.Info("Crypto withdrawal completed",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", req.Amount.String(),
		"tx_hash", transferResult.TxHash)

	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: withdrawal.ID,
		Status:       withdrawal.Status,
		Message:      "Withdrawal completed successfully",
	}, nil
}

// InitiateFiatWithdrawal initiates a fiat withdrawal (USDC to fiat via Bridge)
func (s *WithdrawalService) InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	lock := s.userWithdrawalLock(req.UserID)
	lock.Lock()
	defer lock.Unlock()

	s.logger.Info("Initiating fiat withdrawal",
		"user_id", req.UserID.String(),
		"amount", req.Amount.String(),
		"currency", req.Currency,
		"source_account", req.SourceAccount)

	// Step 1: Validate request
	if err := req.Validate(); err != nil {
		s.logger.Warn("Invalid fiat withdrawal request", "error", err.Error())
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	clientProvidedIdempotency := strings.TrimSpace(req.IdempotencyKey) != ""
	_ = clientProvidedIdempotency
	idempotencyKey := scopedWithdrawalIdempotencyKey(req.UserID, "fiat", req.IdempotencyKey)

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

	// Step 3: Check idempotency (always — auto-key covers retry deduplication)
	existing, err := s.withdrawalRepo.GetByIdempotencyKey(ctx, idempotencyKey)
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

	// Step 7: Create or get bank account for the supplied fiat destination.
	bankAccount, err := s.getOrCreateBankAccount(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create bank account", "error", err)
		return nil, fmt.Errorf("failed to setup bank account: %w", err)
	}

	// Step 8: Create withdrawal record
	withdrawal := &entities.Withdrawal{
		ID:               uuid.New(),
		UserID:           req.UserID,
		WithdrawalType:   entities.WithdrawalTypeFiat,
		Currency:         req.Currency,
		Amount:           req.Amount,
		SourceAccount:    req.SourceAccount,
		DestinationType:  entities.DestinationTypeBankAccount,
		DestinationChain: "BANK",
		BankAccountID:    &bankAccount.ID,
		FeeAmount:        fee,
		FeeCurrency:      entities.WithdrawalCurrencyUSDC, // Fees deducted in USDC
		Status:           entities.WithdrawalStatusInitiated,
		IdempotencyKey:   &idempotencyKey,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create withdrawal", "error", err)
		return nil, fmt.Errorf("failed to create withdrawal: %w", err)
	}

	// Re-check pending exposure after creating the record to protect against near-simultaneous requests.
	// Must re-fetch balance since the old balance is stale after creating the withdrawal record.
	currentBalance, err := s.getSourceBalance(ctx, req.UserID, req.SourceAccount)
	if err != nil {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "failed to re-check balance")
		return nil, fmt.Errorf("failed to re-check balance: %w", err)
	}

	if err := s.ensurePendingExposureWithinBalance(ctx, req.UserID, currentBalance); err != nil {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, err
	}

	// Step 9: Execute Bridge offramp transfer
	transferID, err := s.executeFiatTransfer(ctx, withdrawal, bankAccount)
	if err != nil {
		s.logger.Error("Failed to execute fiat transfer", "error", err)
		// Reverse the ledger debit when transfer fails
		if revErr := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); revErr != nil {
			s.logger.Error("Failed to reverse ledger debit after transfer failure",
				"error", revErr, "withdrawal_id", withdrawal.ID.String())
		}
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

// getOrCreateBankAccount finds an existing destination account fingerprint or creates a new one.
func (s *WithdrawalService) getOrCreateBankAccount(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.BankAccount, error) {
	// Fail fast if bridge adapter is not configured
	if s.bridgeAdapter == nil {
		return nil, fmt.Errorf("bridge adapter not configured for fiat withdrawals")
	}

	existingAccounts, err := s.bankAccountRepo.GetByUserID(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing accounts: %w", err)
	}

	accountLast4 := ""
	if len(req.AccountNumber) >= 4 {
		accountLast4 = req.AccountNumber[len(req.AccountNumber)-4:]
	}
	ibanLast4 := ""
	if len(req.IBAN) >= 4 {
		ibanLast4 = req.IBAN[len(req.IBAN)-4:]
	}

	for _, acc := range existingAccounts {
		if acc.UserID != req.UserID {
			continue
		}
		switch req.Currency {
		case entities.WithdrawalCurrencyUSD:
			routingMatches := acc.RoutingNumber != nil && *acc.RoutingNumber == req.RoutingNumber
			accountMatches := acc.AccountNumberLast4 == accountLast4 && accountLast4 != ""
			if routingMatches && accountMatches {
				s.logger.Info("Found existing USD bank account", "bank_account_id", acc.ID.String())
				return acc, nil
			}
		case entities.WithdrawalCurrencyEUR:
			ibanMatches := acc.IBAN != nil && strings.EqualFold(strings.TrimSpace(*acc.IBAN), strings.TrimSpace(req.IBAN))
			if ibanMatches {
				s.logger.Info("Found existing EUR bank account", "bank_account_id", acc.ID.String())
				return acc, nil
			}
		}
	}

	bankCurrency := entities.BankAccountCurrencyUSD
	if req.Currency == entities.WithdrawalCurrencyEUR {
		bankCurrency = entities.BankAccountCurrencyEUR
	}

	bankAccount := &entities.BankAccount{
		ID:                 uuid.New(),
		UserID:             req.UserID,
		BankName:           "Pending Verification", // Will be resolved by Bridge after recipient creation
		AccountNumberLast4: accountLast4,
		Currency:           bankCurrency,
		IsVerified:         false,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if req.Currency == entities.WithdrawalCurrencyUSD {
		routing := req.RoutingNumber
		if len(routing) >= 4 {
			routingLast4 := routing[len(routing)-4:]
			bankAccount.RoutingNumberLast4 = &routingLast4
		}
		bankAccount.RoutingNumber = &routing
	}
	if req.Currency == entities.WithdrawalCurrencyEUR {
		iban := strings.TrimSpace(req.IBAN)
		if iban != "" {
			bankAccount.IBAN = &iban
		}
		bankAccount.AccountNumberLast4 = ibanLast4
	}

	// Register with Bridge to get recipient ID
	if s.bridgeAdapter != nil {
		recipientReq := map[string]interface{}{
			"currency":            string(req.Currency),
			"account_holder_name": strings.TrimSpace(req.AccountHolderName),
		}
		switch req.Currency {
		case entities.WithdrawalCurrencyUSD:
			recipientReq["routing_number"] = req.RoutingNumber
			recipientReq["account_number"] = req.AccountNumber
		case entities.WithdrawalCurrencyEUR:
			recipientReq["iban"] = strings.TrimSpace(req.IBAN)
			if bic := strings.TrimSpace(req.BIC); bic != "" {
				recipientReq["bic"] = bic
			}
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
		"currency", bankAccount.Currency)

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

	// For fiat withdrawals, attempt to cancel the Bridge transfer
	if withdrawal.IsFiat() && withdrawal.ProviderTransferID != nil {
		if err := s.bridgeAdapter.CancelTransfer(ctx, *withdrawal.ProviderTransferID); err != nil {
			s.logger.Warn("Failed to cancel Bridge transfer; proceeding with local cancellation",
				"transfer_id", *withdrawal.ProviderTransferID,
				"error", err)
		} else {
			s.logger.Info("Bridge transfer cancelled",
				"transfer_id", *withdrawal.ProviderTransferID)
		}
	}

	if err := s.withdrawalRepo.MarkCancelled(ctx, withdrawalID); err != nil {
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

	if _, err := s.syncCryptoWithdrawalStatusFromProvider(ctx, withdrawal); err != nil {
		s.logger.Warn("Failed to sync withdrawal status on read",
			"withdrawal_id", withdrawal.ID.String(),
			"error", err)
	}

	return withdrawal, nil
}

// GetUserWithdrawals gets all withdrawals for a user.
// Status sync against Circle is intentionally omitted here — it is handled
// asynchronously by webhooks and the stuck-withdrawal worker.
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
	accountType, err := mapWithdrawalSourceToAccountType(sourceAccount)
	if err != nil {
		return decimal.Zero, err
	}

	return s.ledgerService.GetAccountBalance(ctx, userID, accountType)
}

func (s *WithdrawalService) ensurePendingExposureWithinBalance(ctx context.Context, userID uuid.UUID, currentBalance decimal.Decimal) error {
	pendingTotal, err := s.withdrawalRepo.GetPendingWithdrawalsTotal(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to re-check pending withdrawals: %w", err)
	}

	if pendingTotal.GreaterThan(currentBalance) {
		return fmt.Errorf("withdrawal would exceed available balance when accounting for pending withdrawals")
	}

	return nil
}

func (s *WithdrawalService) userWithdrawalLock(userID uuid.UUID) *sync.Mutex {
	// XOR several bytes from across the UUID to get better shard distribution.
	// Avoids clustering from sequential UUIDs and the fixed version/variant nibbles.
	h := int(userID[0]) ^ int(userID[4]) ^ int(userID[9]) ^ int(userID[14])
	return &s.withdrawalLocks[h%withdrawalLockShards]
}

func mapWithdrawalSourceToAccountType(sourceAccount entities.WithdrawalSourceAccount) (entities.AccountType, error) {
	switch sourceAccount {
	case entities.WithdrawalSourceSpendingBalance:
		return entities.AccountTypeSpendingBalance, nil
	case entities.WithdrawalSourceStashBalance:
		return entities.AccountTypeStashBalance, nil
	default:
		return "", fmt.Errorf("invalid source account: %s", sourceAccount)
	}
}

func (s *WithdrawalService) postWithdrawalLedgerEntries(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return fmt.Errorf("ledger service not configured")
	}

	accountType, err := mapWithdrawalSourceToAccountType(withdrawal.SourceAccount)
	if err != nil {
		return err
	}

	metadata := map[string]interface{}{
		"withdrawal_id":   withdrawal.ID.String(),
		"withdrawal_type": string(withdrawal.WithdrawalType),
		"source_account":  string(withdrawal.SourceAccount),
	}
	if withdrawal.DestinationAddress != nil {
		metadata["destination_address"] = *withdrawal.DestinationAddress
	}
	if withdrawal.ProviderTransferID != nil {
		metadata["provider_transfer_id"] = *withdrawal.ProviderTransferID
	}

	return s.ledgerService.CreateTransaction(
		ctx,
		withdrawal.UserID,
		accountType,
		entities.TransactionTypeWithdrawal,
		withdrawal.Amount,
		metadata,
	)
}

func (s *WithdrawalService) reverseWithdrawalLedgerEntry(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return fmt.Errorf("ledger service not configured")
	}

	accountType, err := mapWithdrawalSourceToAccountType(withdrawal.SourceAccount)
	if err != nil {
		return err
	}

	metadata := map[string]interface{}{
		"withdrawal_id":   withdrawal.ID.String(),
		"reversal_reason": "transfer_failed",
		"original_amount": withdrawal.Amount.String(),
		"source_account":  string(withdrawal.SourceAccount),
	}

	return s.ledgerService.ReverseTransaction(
		ctx,
		withdrawal.UserID,
		accountType,
		withdrawal.ID.String(),
		withdrawal.Amount,
		metadata,
	)
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

// resolveWithdrawalRoute determines whether to use CCTP or direct Circle transfer.
// EVM <-> Solana requires CCTP; everything else uses direct transfer.
func resolveWithdrawalRoute(sourceChain, destChain string) string {
	src := strings.ToUpper(sourceChain)
	dst := strings.ToUpper(destChain)
	isSolana := func(c string) bool {
		return c == "SOL" || c == "SOL-DEVNET" || c == "SOLANA"
	}
	isEVM := func(c string) bool {
		return c == "ETH" || c == "ETH-SEPOLIA" ||
			c == "MATIC" || c == "MATIC-AMOY" ||
			c == "AVAX" || c == "AVAX-FUJI" ||
			c == "BASE" || c == "BASE-SEPOLIA" ||
			c == "ARB" || c == "OP"
	}
	if (isSolana(src) && isEVM(dst)) || (isEVM(src) && isSolana(dst)) {
		return "cctp"
	}
	return "direct"
}

// SupportedChains returns all supported blockchain identifiers
var SupportedChains = map[string]bool{
	"SOL": true, "SOL-DEVNET": true, "SOLANA": true,
	"ETH": true, "ETH-SEPOLIA": true, "ETHEREUM": true,
	"MATIC": true, "MATIC-AMOY": true, "POLYGON": true,
	"AVAX": true, "AVAX-FUJI": true, "AVALANCHE": true,
	"BASE": true, "BASE-SEPOLIA": true,
	"ARB": true, "OP": true,
}

// validateChainPair validates that both source and destination chains are supported
func validateChainPair(sourceChain, destChain string) error {
	if sourceChain == "" {
		return fmt.Errorf("source chain is required")
	}
	if destChain == "" {
		return fmt.Errorf("destination chain is required")
	}

	src := strings.ToUpper(sourceChain)
	dst := strings.ToUpper(destChain)

	if !SupportedChains[src] {
		return fmt.Errorf("unsupported source chain: %s", sourceChain)
	}
	if !SupportedChains[dst] {
		return fmt.Errorf("unsupported destination chain: %s", destChain)
	}

	return nil
}

// executeCryptoTransfer executes a crypto transfer via Circle, routing via CCTP when crossing EVM<->Solana.
func (s *WithdrawalService) executeCryptoTransfer(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, destinationChain, sourceChain string) (*CryptoTransferResult, error) {
	if s.circleClient == nil {
		return nil, fmt.Errorf("circle client not configured")
	}

	walletID := withdrawal.CircleWalletID
	if walletID == nil || *walletID == "" {
		return nil, fmt.Errorf("circle wallet ID not provided")
	}

	// Format amount for Circle API (USDC uses 6 decimals)
	amountStr := withdrawal.Amount.StringFixed(6)

	// Route via CCTP for EVM <-> Solana cross-chain transfers
	if resolveWithdrawalRoute(sourceChain, destinationChain) == "cctp" {
		destDomain, ok := cctp.DomainForChain(destinationChain)
		if !ok {
			return nil, fmt.Errorf("unsupported CCTP destination chain: %s", destinationChain)
		}
		burnResp, err := s.circleClient.InitiateCCTPBurn(ctx, &entities.CCTPBurnRequest{
			WalletID:       *walletID,
			Amount:         withdrawal.Amount,
			DestDomain:     destDomain,
			MintRecipient:  destinationAddress,
			IdempotencyKey: *withdrawal.IdempotencyKey,
		})
		if err != nil {
			return nil, fmt.Errorf("cctp burn failed: %w", err)
		}
		s.logger.Info("CCTP burn initiated",
			"withdrawal_id", withdrawal.ID.String(),
			"dest_chain", destinationChain,
			"transfer_id", burnResp.TransactionID)
		return &CryptoTransferResult{
			TransferID: burnResp.TransactionID,
			TxHash:     burnResp.TxHash,
			State:      burnResp.Status,
		}, nil
	}

	req := entities.CircleTransferRequest{
		WalletID:              *walletID,
		TokenID:               "USDC",
		Amounts:               []string{amountStr},
		DestinationAddress:    destinationAddress,
		DestinationBlockchain: circleBlockchainForChain(destinationChain),
		IDempotencyKey:        *withdrawal.IdempotencyKey,
	}

	response, err := s.circleClient.TransferFunds(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("circle transfer failed: %w", err)
	}

	result := &CryptoTransferResult{}

	// Response may be wrapped in data.transaction.
	payload := response
	if data, ok := response["data"].(map[string]interface{}); ok {
		if tx, ok := data["transaction"].(map[string]interface{}); ok {
			payload = tx
		} else {
			payload = data
		}
	}

	if id, ok := payload["id"].(string); ok {
		result.TransferID = id
	}
	if state, ok := payload["state"].(string); ok {
		result.State = state
	}
	if txHashVal, ok := payload["transactionHash"].(string); ok {
		result.TxHash = txHashVal
	} else if txHashVal, ok := payload["txHash"].(string); ok {
		result.TxHash = txHashVal
	}

	state := strings.ToUpper(strings.TrimSpace(result.State))
	if state == "FAILED" || state == "REJECTED" || state == "CANCELLED" {
		if reason, ok := payload["errorReason"].(string); ok && strings.TrimSpace(reason) != "" {
			return nil, fmt.Errorf("circle transfer failed: %s", reason)
		}
		if code, ok := payload["errorCode"].(string); ok && strings.TrimSpace(code) != "" {
			return nil, fmt.Errorf("circle transfer failed: %s", code)
		}
		return nil, fmt.Errorf("circle transfer failed in state: %s", result.State)
	}

	s.logger.Info("Crypto transfer executed",
		"withdrawal_id", withdrawal.ID.String(),
		"state", result.State,
		"transfer_id", result.TransferID,
		"tx_hash", result.TxHash)

	return result, nil
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

func (s *WithdrawalService) settleCompletedCryptoWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if withdrawal == nil {
		return nil
	}

	current, err := s.withdrawalRepo.GetByID(ctx, withdrawal.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal for settlement: %w", err)
	}
	if current != nil && current.Status == entities.WithdrawalStatusCompleted {
		withdrawal.Status = entities.WithdrawalStatusCompleted
		withdrawal.CompletedAt = current.CompletedAt
		withdrawal.TxHash = current.TxHash
		return nil
	}

	// Ledger debit was already posted before the burn in InitiateCryptoWithdrawal.
	// Do not post again here.

	now := time.Now()
	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return fmt.Errorf("failed to complete withdrawal: %w", err)
	}
	withdrawal.Status = entities.WithdrawalStatusCompleted
	withdrawal.CompletedAt = &now
	withdrawal.UpdatedAt = now

	return nil
}

func (s *WithdrawalService) syncCryptoWithdrawalStatusFromProvider(ctx context.Context, withdrawal *entities.Withdrawal) (entities.WithdrawalStatus, error) {
	if withdrawal == nil || !withdrawal.IsCrypto() || withdrawal.Status.IsTerminal() {
		if withdrawal == nil {
			return entities.WithdrawalStatusInitiated, nil
		}
		return withdrawal.Status, nil
	}
	if s.circleClient == nil || withdrawal.ProviderTransferID == nil || strings.TrimSpace(*withdrawal.ProviderTransferID) == "" {
		return withdrawal.Status, nil
	}

	transferID := strings.TrimSpace(*withdrawal.ProviderTransferID)
	status, err := s.circleClient.GetCCTPTransaction(ctx, transferID)
	if err != nil {
		if !isCircleNotFoundError(err) {
			return withdrawal.Status, err
		}

		walletID := ""
		if withdrawal.CircleWalletID != nil {
			walletID = strings.TrimSpace(*withdrawal.CircleWalletID)
		}
		destinationAddress := ""
		if withdrawal.DestinationAddress != nil {
			destinationAddress = strings.TrimSpace(*withdrawal.DestinationAddress)
		}

		if walletID != "" {
			s.logger.Warn("Circle transfer ID not found; attempting outbound transaction fallback lookup",
				"withdrawal_id", withdrawal.ID.String(),
				"transfer_id", transferID,
				"wallet_id", walletID)

			fallbackStatus, fallbackErr := s.circleClient.FindRecentOutboundTransfer(
				ctx,
				walletID,
				destinationAddress,
				withdrawal.Amount,
				withdrawal.CreatedAt.Add(-2*time.Minute),
			)
			if fallbackErr != nil {
				return withdrawal.Status, fallbackErr
			}
			if fallbackStatus != nil {
				status = fallbackStatus
				if strings.TrimSpace(status.ID) != "" && status.ID != transferID {
					if updateErr := s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, status.ID); updateErr != nil {
						s.logger.Warn("Failed to update withdrawal transfer ID after fallback lookup",
							"withdrawal_id", withdrawal.ID.String(),
							"old_transfer_id", transferID,
							"new_transfer_id", status.ID,
							"error", updateErr)
					} else {
						updatedID := strings.TrimSpace(status.ID)
						withdrawal.ProviderTransferID = &updatedID
					}
				}
			}
		}

		// Keep withdrawal as-is if transaction is no longer retrievable.
		if status == nil {
			return withdrawal.Status, nil
		}
	}
	if status == nil {
		return withdrawal.Status, nil
	}

	txHash := strings.TrimSpace(status.TxHash)
	if txHash != "" && (withdrawal.TxHash == nil || strings.TrimSpace(*withdrawal.TxHash) != txHash) {
		if err := s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, txHash); err != nil {
			s.logger.Warn("Failed to persist tx hash from provider status",
				"withdrawal_id", withdrawal.ID.String(),
				"error", err)
		} else {
			withdrawal.TxHash = &txHash
			withdrawal.Status = entities.WithdrawalStatusOnChainTransfer
		}
	}

	providerState := strings.ToUpper(strings.TrimSpace(status.Status))
	switch providerState {
	case "COMPLETE", "COMPLETED", "CONFIRMED", "SUCCESS":
		s.logger.Info("Provider reported completed crypto withdrawal",
			"withdrawal_id", withdrawal.ID.String(),
			"provider_state", providerState,
			"transfer_id", strings.TrimSpace(*withdrawal.ProviderTransferID))
		if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
			return withdrawal.Status, err
		}
	case "FAILED", "REJECTED", "CANCELLED":
		s.logger.Warn("Provider reported failed crypto withdrawal",
			"withdrawal_id", withdrawal.ID.String(),
			"provider_state", providerState,
			"transfer_id", strings.TrimSpace(*withdrawal.ProviderTransferID),
			"error_reason", status.ErrorReason)
		reason := "circle transfer failed"
		if strings.TrimSpace(status.ErrorReason) != "" {
			reason = status.ErrorReason
		} else if providerState != "" {
			reason = "circle transfer failed: " + strings.ToLower(providerState)
		}
		if err := s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason); err != nil {
			return withdrawal.Status, fmt.Errorf("failed to mark withdrawal failed: %w", err)
		}
		withdrawal.Status = entities.WithdrawalStatusFailed
		withdrawal.ErrorMessage = &reason
	}

	return withdrawal.Status, nil
}

func isCircleNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	var circleErr entities.CircleAPIError
	if errors.As(err, &circleErr) && circleErr.Code == http.StatusNotFound {
		return true
	}

	var legacyErr entities.CircleErrorResponse
	if errors.As(err, &legacyErr) && legacyErr.Code == http.StatusNotFound {
		return true
	}

	// Defensive fallback for wrapped/non-typed errors.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 404") ||
		strings.Contains(msg, "error 404") ||
		strings.Contains(msg, "status 404") ||
		strings.Contains(msg, "\"code\":404")
}

// circleBlockchainForChain maps internal chain identifiers to Circle's blockchain name strings.
func circleBlockchainForChain(chain string) string {
	switch strings.ToUpper(chain) {
	case "ETH", "ETH-SEPOLIA", "ETHEREUM":
		return "ETH"
	case "MATIC", "MATIC-AMOY", "POLYGON":
		return "MATIC"
	case "AVAX", "AVAX-FUJI", "AVALANCHE":
		return "AVAX"
	case "SOL", "SOL-DEVNET", "SOLANA":
		return "SOL"
	case "BASE", "BASE-SEPOLIA":
		return "BASE"
	case "ARB":
		return "ARB"
	case "OP":
		return "OP"
	default:
		return ""
	}
}

func scopedWithdrawalIdempotencyKey(userID uuid.UUID, flow string, clientKey string) string {
	normalized := strings.TrimSpace(clientKey)
	if normalized == "" {
		// Derive a stable key from the request identity so that retries within
		// the same 5-minute window are deduplicated even without a client key.
		window := time.Now().UTC().Truncate(5 * time.Minute).Unix()
		normalized = fmt.Sprintf("auto:%d", window)
	}
	// Use UUID v5 (deterministic) so the result is a valid UUID format
	// as required by Circle's idempotencyKey field.
	name := "withdrawal:" + flow + ":" + userID.String() + ":" + normalized
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}
