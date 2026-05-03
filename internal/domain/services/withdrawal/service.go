package withdrawal

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	bridgepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Crypto withdrawal constants
const (
	CryptoWithdrawalMinAmount   = 10.00 // Minimum crypto withdrawal
	FiatWithdrawalMinAmountUSD  = 10.00 // Minimum USD fiat withdrawal
	FiatWithdrawalMinAmountEUR  = 10.00 // Minimum EUR fiat withdrawal
	CryptoWithdrawalFeePercent  = 0.0  // No percentage fee — flat only
	CryptoWithdrawalFeeSolana   = 0.10 // $0.10 flat service fee for Solana withdrawals
	CryptoWithdrawalFeeEVM      = 0.50 // $0.50 flat service fee for EVM chain withdrawals
	FlatWithdrawalFee           = 0.50 // Default flat fee (legacy, use chain-specific)
	FiatWithdrawalFeeUSD        = 1.00 // $1.00 flat fee for USD withdrawals
	FiatWithdrawalFeeEUR        = 1.00 // €1.00 flat fee for EUR withdrawals
	FiatWithdrawalFeeGBP        = 1.00 // £1.00 flat fee for GBP withdrawals
	FiatWithdrawalFeeNGN        = 0.02 // ~₦30 flat fee for NGN withdrawals
	FiatWithdrawalFeePercentUSD = 0.0  // No percentage fee — flat only
	FiatWithdrawalFeePercentEUR = 0.0
	MinWithdrawalAmount         = 1.00 // Minimum $1 withdrawal
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
	GetByProviderTransferID(ctx context.Context, transferID string) (*entities.Withdrawal, error)
	GetByProviderTransferIDPrefix(ctx context.Context, prefix string) (*entities.Withdrawal, error)
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
	NotifyWithdrawalSubmitted(ctx context.Context, userID uuid.UUID, amount string) error
	NotifyLargeBalanceChange(ctx context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error
	NotifyEmergencyWithdrawal(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal) error
}

// BridgeAdapter interface for Bridge offramp operations
type BridgeAdapter interface {
	CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error)
	InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error)
	GetTransferStatus(ctx context.Context, transferID string) (map[string]interface{}, error)
	CancelTransfer(ctx context.Context, transferID string) error
}

// BridgeCryptoTransferAdapter interface for Bridge crypto wallet transfers
type BridgeCryptoTransferAdapter interface {
	TransferFunds(ctx context.Context, req *bridgepkg.CreateTransferRequest) (*bridgepkg.Transfer, error)
}

// CircleCryptoTransferAdapter interface for Circle crypto wallet transfers.
// Uses Circle wallet ID and chain to route transfers.
type CircleCryptoTransferAdapter interface {
	TransferUSDC(ctx context.Context, walletID, tokenID, destinationAddress, amount string) (*circlepkg.Transaction, error)
	GetWalletBalance(ctx context.Context, circleWalletID string) (string, error)
	FindWalletWithUSDC(ctx context.Context, userRefID string) (walletID string, tokenID string, blockchain string, address string, err error)
}

// StashLockChecker enforces the 90-day lock / 7-day window rule for stash withdrawals.
type StashLockChecker interface {
	CanWithdraw(ctx context.Context, userID uuid.UUID) (bool, time.Time, error)
	MarkWithdrawn(ctx context.Context, userID uuid.UUID) error
	EmergencyWithdrawalFeePercent(ctx context.Context, userID uuid.UUID) (decimal.Decimal, int, error)
	MarkEmergencyWithdrawn(ctx context.Context, userID uuid.UUID) error
}

// EmergencyLedger is the subset of ledger operations needed for emergency withdrawals.
type EmergencyLedger interface {
	EmergencyTransferStashToSpending(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal, idempotencyKey string) error
}

// ChainRailsTransferAdapter creates cross-chain intents via ChainRails.
type ChainRailsTransferAdapter interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
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
	bridgeAdapter       BridgeAdapter
	bridgeCryptoAdapter BridgeCryptoTransferAdapter
	circleTransfer      CircleCryptoTransferAdapter
	chainRailsAdapter  ChainRailsTransferAdapter
	stashLock          StashLockChecker
	emergencyLedger    EmergencyLedger
	complianceScreener ComplianceScreener
	addressWhitelist   AddressWhitelistChecker
	tieredLimits       TieredWithdrawalLimitChecker
	stashLockMu         sync.RWMutex
	db                  *sqlx.DB
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
	bridgeAdapter BridgeAdapter,
	bridgeCryptoAdapter BridgeCryptoTransferAdapter,
	db *sqlx.DB,
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
		bridgeAdapter:       bridgeAdapter,
		bridgeCryptoAdapter: bridgeCryptoAdapter,
		db:                  db,
		logger:              logger,
	}
}

// SetStashLockChecker wires the stash lock enforcement.
// Must be called during initialization before the service handles any requests.
func (s *WithdrawalService) SetStashLockChecker(c StashLockChecker) {
	s.stashLockMu.Lock()
	defer s.stashLockMu.Unlock()
	s.stashLock = c
}

// SetChainRailsAdapter wires the ChainRails cross-chain transfer adapter.
func (s *WithdrawalService) SetChainRailsAdapter(a ChainRailsTransferAdapter) {
	s.chainRailsAdapter = a
}

// SetCircleTransferAdapter wires the Circle crypto transfer adapter.
func (s *WithdrawalService) SetCircleTransferAdapter(a CircleCryptoTransferAdapter) {
	s.circleTransfer = a
}

// ComplianceScreener screens transactions for AML/sanctions compliance.
type ComplianceScreener interface {
	ScreenTransaction(ctx context.Context, userID uuid.UUID, referenceID, direction string, amount decimal.Decimal, currency, userFullName string) (string, error)
}

// SetComplianceScreener sets the compliance screening service (optional).
func (s *WithdrawalService) SetComplianceScreener(cs ComplianceScreener) {
	s.complianceScreener = cs
}

// AddressWhitelistChecker validates withdrawal addresses against user whitelist.
type AddressWhitelistChecker interface {
	ValidateWithdrawalAddress(ctx context.Context, userID uuid.UUID, chain, address string) error
}

// TieredWithdrawalLimitChecker enforces tiered daily/weekly withdrawal limits.
type TieredWithdrawalLimitChecker interface {
	CheckWithdrawalLimit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, accountAge time.Duration, kycLevel string) error
	RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

// TransactionRiskScorer scores transactions for fraud risk.
type TransactionRiskScorer interface {
	ScoreTransaction(ctx context.Context, input interface{}) (interface{}, error)
}

// SetAddressWhitelistChecker wires address whitelist validation (optional).
func (s *WithdrawalService) SetAddressWhitelistChecker(c AddressWhitelistChecker) {
	s.addressWhitelist = c
}

// SetTieredWithdrawalLimits wires tiered withdrawal limit enforcement (optional).
func (s *WithdrawalService) SetTieredWithdrawalLimits(c TieredWithdrawalLimitChecker) {
	s.tieredLimits = c
}

// SetEmergencyLedger wires the emergency ledger for stash-to-spending transfers with fee.
func (s *WithdrawalService) SetEmergencyLedger(l EmergencyLedger) {
	s.emergencyLedger = l
}

// EmergencyWithdrawalPreview returns the fee breakdown for an emergency stash withdrawal.
func (s *WithdrawalService) EmergencyWithdrawalPreview(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.EmergencyWithdrawalPreviewResponse, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	if sl == nil {
		return nil, fmt.Errorf("stash lock service not configured")
	}
	feePct, days, err := sl.EmergencyWithdrawalFeePercent(ctx, userID)
	if err != nil {
		return nil, err
	}
	fee := amount.Mul(feePct).RoundBank(2)
	tier := "1%"
	if days <= 30 {
		tier = "3%"
	} else if days <= 60 {
		tier = "2%"
	}
	return &entities.EmergencyWithdrawalPreviewResponse{
		Amount:      amount,
		FeePercent:  feePct,
		FeeAmount:   fee,
		NetAmount:   amount.Sub(fee),
		LockAgeDays: days,
		FeeTier:     tier,
	}, nil
}

// EmergencyStashToSpending executes an emergency stash-to-spending transfer with fee.
func (s *WithdrawalService) EmergencyStashToSpending(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.EmergencyWithdrawalResult, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	if sl == nil {
		return nil, fmt.Errorf("stash lock service not configured")
	}
	if s.emergencyLedger == nil {
		return nil, fmt.Errorf("emergency ledger not configured")
	}

	// Check balance
	balance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return nil, fmt.Errorf("failed to get stash balance: %w", err)
	}

	feePct, _, err := sl.EmergencyWithdrawalFeePercent(ctx, userID)
	if err != nil {
		return nil, err
	}
	fee := amount.Mul(feePct).RoundBank(2)
	total := amount.Add(fee)
	if balance.LessThan(total) {
		return nil, fmt.Errorf("insufficient stash balance: have %s, need %s (amount %s + fee %s)", balance.String(), total.String(), amount.String(), fee.String())
	}

	if err := s.emergencyLedger.EmergencyTransferStashToSpending(ctx, userID, amount, fee, idempotencyKey); err != nil {
		return nil, fmt.Errorf("emergency transfer failed: %w", err)
	}
	if err := sl.MarkEmergencyWithdrawn(ctx, userID); err != nil {
		s.logger.Error("failed to mark cycles as emergency withdrawn", zap.String("user_id", userID.String()), zap.Error(err))
	}
	if s.notificationService != nil {
		_ = s.notificationService.NotifyEmergencyWithdrawal(ctx, userID, amount, fee)
	}

	return &entities.EmergencyWithdrawalResult{
		Amount:     amount,
		Fee:        fee,
		FeePercent: feePct,
		NetAmount:  amount.Sub(fee),
		TransferID: uuid.New(),
	}, nil
}

// advisoryLockKey derives a stable int64 from a user UUID for pg_advisory_lock.
func advisoryLockKey(userID uuid.UUID) int64 {
	h := fnv.New64a()
	b := [16]byte(userID)
	h.Write(b[:])
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]))
}

// acquireAdvisoryLock acquires a PostgreSQL session-level advisory lock for the user.
// Returns an unlock function that MUST be called (typically via defer).
func (s *WithdrawalService) acquireAdvisoryLock(ctx context.Context, userID uuid.UUID) (func(), error) {
	if s.db == nil {
		return func() {}, nil
	}
	key := advisoryLockKey(userID)

	// Try to acquire with timeout
	deadline := time.Now().Add(10 * time.Second)
	for {
		var acquired bool
		err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired)
		if err != nil {
			return nil, fmt.Errorf("advisory lock query failed: %w", err)
		}
		if acquired {
			unlock := func() {
				if _, err := s.db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key); err != nil {
					s.logger.Error("failed to release advisory lock", zap.Int64("key", key), zap.Error(err))
				}
			}
			return unlock, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout acquiring withdrawal lock for user %s", userID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// InitiateCryptoWithdrawal initiates a crypto withdrawal (USDC to external wallet)
func (s *WithdrawalService) InitiateCryptoWithdrawal(ctx context.Context, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	// Acquire distributed advisory lock before in-memory lock
	unlock, err := s.acquireAdvisoryLock(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire distributed lock: %w", err)
	}
	defer unlock()

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

	// Step 1.6: Validate currency is supported on destination chain
	if err := validateCurrencyChain(string(req.Currency), req.DestinationChain); err != nil {
		s.logger.Warn("Currency not supported on chain", "currency", req.Currency, "dest_chain", req.DestinationChain, "error", err)
		return nil, fmt.Errorf("unsupported route: %w", err)
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

	// Stash lock enforcement: stash withdrawals only allowed during the 7-day window.
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	var emergencyFee decimal.Decimal
	if req.SourceAccount == entities.WithdrawalSourceStashBalance && sl != nil {
		canWithdraw, _, err := sl.CanWithdraw(ctx, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("stash lock check failed: %w", err)
		}
		if !canWithdraw {
			if !req.Emergency {
				return nil, fmt.Errorf("stash funds are locked: no active withdrawal window (funds lock for 90 days, then a 7-day window opens)")
			}
			// Emergency withdrawal: calculate penalty fee
			feePct, _, feeErr := sl.EmergencyWithdrawalFeePercent(ctx, req.UserID)
			if feeErr != nil {
				return nil, fmt.Errorf("emergency fee calculation failed: %w", feeErr)
			}
			emergencyFee = req.Amount.Mul(feePct).RoundBank(2)
		}
		// MarkWithdrawn is deferred until after the withdrawal succeeds (ledger + transfer).
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

	// Step 3.1: Validate destination address is whitelisted (if whitelist enabled)
	// Skip whitelist for non-KYC users — they have strict transfer limits instead.
	if s.addressWhitelist != nil {
		user, userErr := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		skipWhitelist := userErr == nil && user != nil && entities.DeriveKYCTier(user.KYCStatus) == entities.KYCTierNonKYC
		if !skipWhitelist {
			if err := s.addressWhitelist.ValidateWithdrawalAddress(ctx, req.UserID, req.DestinationChain, req.DestinationAddress); err != nil {
				s.logger.Warn("Address whitelist validation failed", "error", err.Error(), "address", req.DestinationAddress)
				return nil, fmt.Errorf("address not whitelisted or in cooling period: %w", err)
			}
		}
	}

	// Step 3.2: Enforce tiered daily/weekly withdrawal limits
	if s.tieredLimits != nil {
		user, userErr := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		if userErr == nil && user != nil {
			accountAge := time.Since(user.CreatedAt)
			kycLevel := "basic"
			if user.KYCStatus == "approved" {
				kycLevel = "full"
			}
			if limitErr := s.tieredLimits.CheckWithdrawalLimit(ctx, req.UserID, req.Amount, accountAge, kycLevel); limitErr != nil {
				s.logger.Warn("Tiered withdrawal limit exceeded", "error", limitErr.Error())
				return nil, limitErr
			}
		}
	}

	// Step 3.5: Compliance screening — submit to Didit for AML/sanctions monitoring
	// Skip for non-KYC users — they have strict transfer limits ($100/tx) that enforce safety.
	if s.complianceScreener != nil {
		user, userErr := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		skipCompliance := userErr == nil && user != nil && entities.DeriveKYCTier(user.KYCStatus) == entities.KYCTierNonKYC
		if !skipCompliance {
			screenStatus, screenErr := s.complianceScreener.ScreenTransaction(ctx, req.UserID, idempotencyKey, "outbound", req.Amount, string(req.Currency), "")
			if screenErr != nil {
				s.logger.Error("Compliance screening unavailable, blocking withdrawal for review",
					"user_id", req.UserID.String(), "error", screenErr)
				return nil, fmt.Errorf("withdrawal held: compliance screening unavailable")
			}
			if screenStatus != "APPROVED" {
				s.logger.Warn("Withdrawal not approved by compliance screening",
					"user_id", req.UserID.String(), "amount", req.Amount.String(), "status", screenStatus)
				return nil, fmt.Errorf("withdrawal held: compliance status %s", screenStatus)
			}
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
	fee, err := s.calculateCryptoWithdrawalFee(ctx, req.Amount, req.SourceChain, req.DestinationChain)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate fee: %w", err)
	}
	totalAmount := req.Amount.Add(fee).Add(emergencyFee)

	if balance.LessThan(totalAmount) {
		return nil, fmt.Errorf("insufficient balance for withdrawal + fee: have %s, need %s", balance.String(), totalAmount.String())
	}

	// Step 6: Create withdrawal record
	category := strings.TrimSpace(req.Category)
	var categoryPtr *string
	if category != "" {
		categoryPtr = &category
	}
	narration := strings.TrimSpace(req.Narration)
	var narrationPtr *string
	if narration != "" {
		narrationPtr = &narration
	}

	// Use CircleWalletID if available, otherwise BridgeWalletID
	walletProviderID := req.BridgeWalletID
	if req.CircleWalletID != "" {
		walletProviderID = req.CircleWalletID
	}

	withdrawal := &entities.Withdrawal{
		ID:                 uuid.New(),
		UserID:             req.UserID,
		WithdrawalType:     entities.WithdrawalTypeCrypto,
		Currency:           req.Currency,
		Amount:             req.Amount,
		SourceAccount:      req.SourceAccount,
		BridgeWalletID:     &walletProviderID,
		DestinationType:    entities.DestinationTypeCryptoWallet,
		DestinationChain:   strings.ToUpper(req.DestinationChain),
		DestinationAddress: &req.DestinationAddress,
		FeeAmount:          fee,
		FeeCurrency:        req.Currency,
		Category:           categoryPtr,
		Narration:          narrationPtr,
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

	// Step 7: Execute Bridge transfer asynchronously.
	// Ledger is debited, withdrawal record exists — return immediately and process in background.
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
		s.logger.Error("Failed to mark withdrawal processing", "error", err)
		return nil, fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	withdrawal.Status = entities.WithdrawalStatusProcessing

	// Notify user immediately that withdrawal is being processed
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalSubmitted(ctx, req.UserID, req.Amount.String())
	}

	go s.executeCryptoWithdrawalAsync(withdrawal, req)

	s.logger.Info("Crypto withdrawal submitted for async processing",
		"withdrawal_id", withdrawal.ID.String(),
		"amount", req.Amount.String(),
		"destination", req.DestinationAddress)

	return &entities.InitiateWithdrawalResponse{
		WithdrawalID: withdrawal.ID,
		Status:       withdrawal.Status,
		Message:      "Withdrawal submitted and is processing",
	}, nil
}

// executeCryptoWithdrawalAsync runs the Bridge transfer and post-processing in the background.
func (s *WithdrawalService) executeCryptoWithdrawalAsync(withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	transferResult, err := s.executeCryptoTransfer(ctx, withdrawal, req.DestinationAddress, req.DestinationChain, req.SourceChain, req.SourceWalletAddress, req.CircleWalletID)
	if err != nil {
		s.logger.Error("async: crypto transfer failed — reversing ledger",
			"error", err, "withdrawal_id", withdrawal.ID.String())
		if revErr := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); revErr != nil {
			s.logger.Error("async: failed to reverse ledger debit",
				"error", revErr, "withdrawal_id", withdrawal.ID.String())
		}
		// Reverse tiered limit usage on failure — no-op since limits recorded on success only
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds have been returned to your balance.")
		}
		return
	}

	if transferResult.TransferID != "" {
		_ = s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, transferResult.TransferID)
	}

	// Mark stash lock window consumed after successful transfer.
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	if req.SourceAccount == entities.WithdrawalSourceStashBalance && sl != nil {
		if req.Emergency {
			// Emergency withdrawal: debit fee to revenue account and mark cycles
			feePct, _, _ := sl.EmergencyWithdrawalFeePercent(ctx, req.UserID)
			eFee := req.Amount.Mul(feePct).RoundBank(2)
			if eFee.IsPositive() && s.emergencyLedger != nil {
				idemKey := fmt.Sprintf("emergency-fee-%s", withdrawal.ID.String())
				if feeErr := s.emergencyLedger.EmergencyTransferStashToSpending(ctx, req.UserID, decimal.Zero, eFee, idemKey); feeErr != nil {
					s.logger.Error("async: failed to debit emergency fee", "error", feeErr, "withdrawal_id", withdrawal.ID.String())
				}
			}
			if err := sl.MarkEmergencyWithdrawn(ctx, req.UserID); err != nil {
				s.logger.Error("async: failed to mark emergency withdrawn", "user_id", req.UserID, "error", err)
			}
		} else {
			if err := sl.MarkWithdrawn(ctx, req.UserID); err != nil {
				s.logger.Error("async: failed to mark stash window consumed",
					"user_id", req.UserID, "error", err)
			}
		}
	}

	if transferResult.TxHash != "" {
		_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, transferResult.TxHash)
	}

	state := strings.ToUpper(strings.TrimSpace(transferResult.State))
	isFinalSuccess := state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" || state == "SUCCESS"
	isFinalFailure := state == "FAILED" || state == "DENIED" || state == "CANCELLED" ||
		state == "CANCELED" || state == "REJECTED" || state == "ERROR" ||
		state == "UNDELIVERABLE" || state == "RETURNED" || state == "REFUNDED"

	if isFinalSuccess {
		if s.limitsService != nil {
			_ = s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount)
		}
		if s.tieredLimits != nil {
			_ = s.tieredLimits.RecordWithdrawal(ctx, req.UserID, req.Amount)
		}
		_ = s.settleCompletedCryptoWithdrawal(ctx, withdrawal)
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalCompleted(ctx, req.UserID, req.Amount, req.DestinationAddress)
		}
		s.logger.Info("async: crypto withdrawal completed",
			"withdrawal_id", withdrawal.ID.String(), "tx_hash", transferResult.TxHash)
	} else if isFinalFailure {
		s.logger.Error("async: crypto transfer returned failure state — reversing ledger",
			"withdrawal_id", withdrawal.ID.String(), "state", state)
		if revErr := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); revErr != nil {
			s.logger.Error("async: failed to reverse ledger after provider failure",
				"error", revErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "provider returned: "+state)
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds have been returned to your balance.")
		}
	} else {
		s.logger.Info("async: crypto withdrawal processing",
			"withdrawal_id", withdrawal.ID.String(), "state", transferResult.State)
	}
}

// InitiateFiatWithdrawal initiates a fiat withdrawal (USDC to fiat via Bridge)
func (s *WithdrawalService) InitiateFiatWithdrawal(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	// Acquire distributed advisory lock before in-memory lock
	unlock, err := s.acquireAdvisoryLock(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire distributed lock: %w", err)
	}
	defer unlock()

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
	var bridgeCustomerID string
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

		if user.BridgeCustomerID != nil {
			bridgeCustomerID = strings.TrimSpace(*user.BridgeCustomerID)
		}
	}
	if bridgeCustomerID == "" {
		return nil, fmt.Errorf("bridge customer profile is required for fiat withdrawals")
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

	// Step 3.5: Stash lock enforcement — stash withdrawals only allowed during the 7-day window.
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	var emergencyFee decimal.Decimal
	if req.SourceAccount == entities.WithdrawalSourceStashBalance && sl != nil {
		canWithdraw, _, err := sl.CanWithdraw(ctx, req.UserID)
		if err != nil {
			return nil, fmt.Errorf("stash lock check failed: %w", err)
		}
		if !canWithdraw {
			if !req.Emergency {
				return nil, fmt.Errorf("stash funds are locked: no active withdrawal window (funds lock for 90 days, then a 7-day window opens)")
			}
			feePct, _, feeErr := sl.EmergencyWithdrawalFeePercent(ctx, req.UserID)
			if feeErr != nil {
				return nil, fmt.Errorf("emergency fee calculation failed: %w", feeErr)
			}
			emergencyFee = req.Amount.Mul(feePct).RoundBank(2)
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
	totalAmount := req.Amount.Add(fee).Add(emergencyFee)

	if balance.LessThan(totalAmount) {
		return nil, fmt.Errorf("insufficient balance for withdrawal + fee: have %s, need %s", balance.String(), totalAmount.String())
	}

	// Step 7: Create or get bank account for the supplied fiat destination.
	bankAccount, err := s.getOrCreateBankAccount(ctx, req, bridgeCustomerID)
	if err != nil {
		s.logger.Error("Failed to create bank account", "error", err)
		return nil, fmt.Errorf("failed to setup bank account: %w", err)
	}

	// SECURITY: Require additional verification for new/unverified bank accounts
	// Check if this is a new or unverified bank account
	if !bankAccount.IsVerified {
		// For new bank accounts, require MFA verification
		// This should be enforced at handler level, but we add a safeguard here
		s.logger.Warn("Withdrawal to unverified bank account - MFA should be required",
			"user_id", req.UserID.String(),
			"bank_account_id", bankAccount.ID.String())
		// Note: The actual MFA enforcement should be done at the handler level
		// before calling this service. This is a defense-in-depth check.
	}

	// Step 8: Create withdrawal record
	category := strings.TrimSpace(req.Category)
	var categoryPtr *string
	if category != "" {
		categoryPtr = &category
	}
	narration := strings.TrimSpace(req.Narration)
	var narrationPtr *string
	if narration != "" {
		narrationPtr = &narration
	}

	withdrawal := &entities.Withdrawal{
		ID:               uuid.New(),
		UserID:           req.UserID,
		WithdrawalType:   entities.WithdrawalTypeFiat,
		Currency:         req.Currency,
		Amount:           req.Amount,
		SourceAccount:    req.SourceAccount,
		BridgeWalletID:   &req.BridgeWalletID,
		DestinationType:  entities.DestinationTypeBankAccount,
		DestinationChain: "BANK",
		BankAccountID:    &bankAccount.ID,
		FeeAmount:        fee,
		FeeCurrency:      entities.WithdrawalCurrencyUSDC, // Fees deducted in USDC
		Category:         categoryPtr,
		Narration:        narrationPtr,
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

	if err := s.postWithdrawalLedgerEntries(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to post pre-transfer ledger debit", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "ledger debit failed: "+err.Error())
		return nil, fmt.Errorf("failed to post ledger debit: %w", err)
	}

	// Debit emergency fee to revenue account if this is an emergency withdrawal
	if emergencyFee.IsPositive() && s.emergencyLedger != nil {
		idemKey := fmt.Sprintf("emergency-fee-%s", withdrawal.ID.String())
		if feeErr := s.emergencyLedger.EmergencyTransferStashToSpending(ctx, req.UserID, decimal.Zero, emergencyFee, idemKey); feeErr != nil {
			s.logger.Error("Failed to debit emergency fee", "error", feeErr, "withdrawal_id", withdrawal.ID.String())
		}
		if sl != nil {
			_ = sl.MarkEmergencyWithdrawn(ctx, req.UserID)
		}
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
	} else {
		withdrawal.ProviderTransferID = &transferID
	}

	// Update status to processing
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
		s.logger.Error("Failed to update withdrawal status", "error", err)
		return nil, fmt.Errorf("failed to update status: %w", err)
	}

	withdrawal.Status = entities.WithdrawalStatusProcessing

	if s.limitsService != nil {
		if err := s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount); err != nil {
			s.logger.Error("Failed to record fiat withdrawal against limits", "error", err)
		}
	}

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
func (s *WithdrawalService) getOrCreateBankAccount(ctx context.Context, req *entities.InitiateFiatWithdrawalRequest, bridgeCustomerID string) (*entities.BankAccount, error) {
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
		IsVerified:         false, // Security: Don't auto-verify - require successful withdrawal first
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
			"customer_id":         bridgeCustomerID,
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
		// Security: Do NOT auto-verify - this should only happen after successful withdrawal
		// bankAccount.IsVerified = true
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
func (s *WithdrawalService) GetWithdrawalFee(ctx context.Context, withdrawalType entities.WithdrawalType, amount decimal.Decimal, currency entities.WithdrawalCurrency, sourceChain, destChain string) (*entities.WithdrawalFee, error) {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("amount must be positive")
	}

	if amount.LessThan(decimal.NewFromFloat(MinWithdrawalAmount)) {
		return nil, fmt.Errorf("minimum withdrawal amount is $%.2f", MinWithdrawalAmount)
	}

	feeResponse := &entities.WithdrawalFee{
		Amount:   amount,
		Currency: currency,
	}

	switch withdrawalType {
	case entities.WithdrawalTypeCrypto:
		fee, err := s.calculateCryptoWithdrawalFee(ctx, amount, sourceChain, destChain)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate fee: %w", err)
		}
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

	if withdrawal.ProviderTransferID != nil && strings.TrimSpace(*withdrawal.ProviderTransferID) != "" {
		transferID := *withdrawal.ProviderTransferID
		if strings.HasPrefix(transferID, "cr:") {
			// ChainRails withdrawals cannot be cancelled via provider — they auto-expire if unfunded
			s.logger.Info("ChainRails withdrawal cancel — skipping provider cancel (auto-expires)",
				"transfer_id", transferID)
		} else if s.bridgeAdapter != nil {
			if err := s.bridgeAdapter.CancelTransfer(ctx, transferID); err != nil {
				return fmt.Errorf("provider cancellation failed: %w", err)
			}
			s.logger.Info("Bridge transfer cancelled", "transfer_id", transferID)
		}
	}

	if err := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to reverse withdrawal ledger entry: %w", err)
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

	isTerminal := withdrawal.Status == entities.WithdrawalStatusCompleted || withdrawal.Status == entities.WithdrawalStatusFailed || withdrawal.Status == entities.WithdrawalStatusCancelled || withdrawal.Status == entities.WithdrawalStatusReversed
	if !isTerminal && time.Since(withdrawal.UpdatedAt) > 30*time.Second {
		if _, err := s.syncWithdrawalStatusFromProvider(ctx, withdrawal); err != nil {
			s.logger.Warn("Failed to sync withdrawal status on read",
				"withdrawal_id", withdrawal.ID.String(),
				"error", err)
		}
	}

	return withdrawal, nil
}

// GetUserWithdrawals gets all withdrawals for a user.
// Status sync against Bridge is intentionally omitted here — it is handled
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
		withdrawal.Amount.Add(withdrawal.FeeAmount),
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
		withdrawal.Amount.Add(withdrawal.FeeAmount),
		metadata,
	)
}

// calculateCryptoWithdrawalFee returns chain-specific flat service fees.
// Gas/network fees are handled by Circle internally — these are Rail's service charges.
func (s *WithdrawalService) calculateCryptoWithdrawalFee(ctx context.Context, amount decimal.Decimal, sourceChain, destChain string) (decimal.Decimal, error) {
	chain := strings.ToUpper(strings.TrimSpace(destChain))
	if chain == "SOL" || chain == "" {
		return decimal.NewFromFloat(CryptoWithdrawalFeeSolana), nil
	}
	return decimal.NewFromFloat(CryptoWithdrawalFeeEVM), nil
}

// calculateFiatWithdrawalFee returns currency-specific fees.
func (s *WithdrawalService) calculateFiatWithdrawalFee(amount decimal.Decimal, currency entities.WithdrawalCurrency) decimal.Decimal {
	switch strings.ToUpper(string(currency)) {
	case "NGN":
		return decimal.NewFromFloat(FiatWithdrawalFeeNGN)
	case "EUR":
		return decimal.NewFromFloat(FiatWithdrawalFeeEUR)
	case "GBP":
		return decimal.NewFromFloat(FiatWithdrawalFeeGBP)
	default:
		return decimal.NewFromFloat(FiatWithdrawalFeeUSD)
	}
}

// resolveWithdrawalRoute determines the transfer route.
// Solana↔EVM cross-chain transfers are routed via CCTP.
func resolveWithdrawalRoute(sourceChain, destChain string) string {
	src := strings.ToUpper(sourceChain)
	dst := strings.ToUpper(destChain)
	isSolana := func(c string) bool {
		return c == "SOL" || c == "SOL-DEVNET" || c == "SOLANA"
	}
	isEVM := func(c string) bool {
		return c == "ETH" || c == "ETH-SEPOLIA" ||
			c == "MATIC" || c == "MATIC-AMOY" ||
			c == "AVAX" || c == "AVAX-FUJI"
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
	// ChainRails-routed chains (not natively supported by Bridge)
	"STARKNET": true, "BSC": true, "BNB": true,
	"MONAD": true, "HYPEREVM": true, "LISK": true,
}

// chainRailsChains are destination chains routed through ChainRails
// because Bridge does not support them as payment rails.
var chainRailsChains = map[string]string{
	"STARKNET": "STARKNET_MAINNET",
	"BSC":      "BSC_MAINNET",
	"BNB":      "BSC_MAINNET",
	"MONAD":    "MONAD_MAINNET",
	"HYPEREVM": "HYPEREVM_MAINNET",
	"LISK":     "LISK_MAINNET",
}

// isChainRailsChain returns the ChainRails chain ID if the destination
// must be routed through ChainRails, or empty string for Bridge-native chains.
func isChainRailsChain(destChain string) string {
	return chainRailsChains[strings.ToUpper(destChain)]
}

// withdrawalChainsForCurrency maps each stablecoin to the chains that support it.
// Source: Bridge route table + ChainRails token availability docs.
var withdrawalChainsForCurrency = map[string]map[string]bool{
	"USDC":  {"SOL": true, "ETH": true, "BASE": true, "ARB": true, "OP": true, "MATIC": true, "AVAX": true, "HYPEREVM": true, "BSC": true, "STARKNET": true, "MONAD": true, "LISK": true},
	"USDT":  {"SOL": true, "ETH": true, "BSC": true, "STARKNET": true},
	"EURC":  {"SOL": true, "ETH": true, "BASE": true},
	"PYUSD": {"SOL": true, "ETH": true},
	"USDG":  {"SOL": true},
}

// validateCurrencyChain checks that the given currency is supported on the destination chain.
func validateCurrencyChain(currency, destChain string) error {
	cur := strings.ToUpper(currency)
	dst := strings.ToUpper(destChain)
	chains, ok := withdrawalChainsForCurrency[cur]
	if !ok {
		return nil // fiat currencies (USD/EUR/NGN) don't have chain restrictions
	}
	if !chains[dst] {
		return fmt.Errorf("%s is not supported on %s", cur, dst)
	}
	return nil
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

// executeCryptoTransfer executes a crypto transfer via Bridge custodial wallets
// or ChainRails for chains not natively supported by Bridge.
func (s *WithdrawalService) executeCryptoTransfer(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, destinationChain, sourceChain, sourceWalletAddress, circleWalletID string) (*CryptoTransferResult, error) {
	// Circle users: route ALL crypto withdrawals through ChainRails for cross-chain support.
	// Circle transfer funds the ChainRails intent, ChainRails delivers to any destination chain.
	if s.circleTransfer != nil && circleWalletID != "" {
		if s.chainRailsAdapter == nil {
			return nil, fmt.Errorf("circle wallet configured without chainrails — cannot route withdrawal")
		}
		return s.executeCircleViaChainRails(ctx, withdrawal, destinationAddress, destinationChain)
	}

	// Bridge users: route exotic chains through ChainRails
	if crChain := isChainRailsChain(destinationChain); crChain != "" {
		return s.executeChainRailsTransfer(ctx, withdrawal, destinationAddress, crChain, sourceWalletAddress)
	}

	// Bridge users: direct transfer for supported chains
	if s.bridgeCryptoAdapter == nil {
		return nil, fmt.Errorf("bridge crypto adapter not configured")
	}

	walletID := withdrawal.BridgeWalletID
	if walletID == nil || *walletID == "" {
		return nil, fmt.Errorf("bridge wallet ID not provided")
	}

	transfer, err := s.bridgeCryptoAdapter.TransferFunds(ctx, &bridgepkg.CreateTransferRequest{
		OnBehalfOf:   withdrawal.UserID.String(),
		Amount:       withdrawal.Amount.Add(withdrawal.FeeAmount).StringFixed(2),
		DeveloperFee: withdrawal.FeeAmount.StringFixed(2),
		Source: bridgepkg.TransferSource{
			PaymentRail:    bridgepkg.PaymentRail("bridge_wallet"),
			Currency:       bridgepkg.StablecoinToBridgeCurrency(string(withdrawal.Currency)),
			BridgeWalletID: *walletID,
		},
		Destination: bridgepkg.TransferDestination{
			PaymentRail: mapDestChainToPaymentRail(destinationChain),
			Currency:    bridgepkg.StablecoinToBridgeCurrency(string(withdrawal.Currency)),
			ToAddress:   destinationAddress,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("bridge transfer failed: %w", err)
	}

	s.logger.Info("Bridge crypto transfer initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"transfer_id", transfer.ID,
		"state", transfer.State)

	return &CryptoTransferResult{
		TransferID: transfer.ID,
		State:      string(transfer.State),
	}, nil
}

// circleChainToChainRails maps Circle blockchain identifiers to ChainRails source chain IDs.
var circleChainToChainRails = map[string]struct{ chain, token string }{
	"ETH-SEPOLIA":  {"ETHEREUM_TESTNET", "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238"},
	"ETH":          {"ETHEREUM_MAINNET", "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"},
	"BASE-SEPOLIA": {"BASE_TESTNET", "0x036CbD53842c5426634e7929541eC2318f3dCF7e"},
	"BASE":         {"BASE_MAINNET", usdcBaseMainnet},
	"SOL-DEVNET":   {"SOLANA_TESTNET", "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"},
	"SOL":          {"SOLANA_MAINNET", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"},
	"ARB-SEPOLIA":  {"ARBITRUM_TESTNET", "0x75faf114eafb1BDbe2F0316DF893fd58CE46AA4d"},
	"ARB":          {"ARBITRUM_MAINNET", "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"},
	"OP-SEPOLIA":   {"OPTIMISM_TESTNET", "0x5fd84259d66Cd46123540766Be93DFE6D43130D7"},
	"OP":           {"OPTIMISM_MAINNET", "0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85"},
	"MATIC-AMOY":   {"POLYGON_MAINNET", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"},
	"MATIC":        {"POLYGON_MAINNET", "0x3c499c542cEF5E3811e1192ce70d8cC03d5c3359"},
	"AVAX-FUJI":    {"AVALANCHE_TESTNET", "0x5425890298aed601595a70AB815c96711a31Bc65"},
	"AVAX":         {"AVALANCHE_MAINNET", "0xB97EF9Ef8734C71904D8002F8b6Bc66Dd9c48a6E"},
}

var destChainToChainRails = map[string]string{
	"SOL": "SOLANA_MAINNET", "SOL-DEVNET": "SOLANA_TESTNET", "SOLANA": "SOLANA_MAINNET",
	"ETH": "ETHEREUM_MAINNET", "ETH-SEPOLIA": "ETHEREUM_TESTNET", "ETHEREUM": "ETHEREUM_MAINNET",
	"BASE": "BASE_MAINNET", "BASE-SEPOLIA": "BASE_TESTNET",
	"ARB": "ARBITRUM_MAINNET", "ARB-SEPOLIA": "ARBITRUM_TESTNET", "ARBITRUM": "ARBITRUM_MAINNET",
	"OP": "OPTIMISM_MAINNET", "OP-SEPOLIA": "OPTIMISM_TESTNET", "OPTIMISM": "OPTIMISM_MAINNET",
	"MATIC": "POLYGON_MAINNET", "POLYGON": "POLYGON_MAINNET",
	"AVAX": "AVALANCHE_MAINNET", "AVAX-FUJI": "AVALANCHE_TESTNET", "AVALANCHE": "AVALANCHE_MAINNET",
	"STARKNET": "STARKNET_MAINNET", "BSC": "BSC_MAINNET", "BNB": "BSC_MAINNET",
	"MONAD": "MONAD_MAINNET", "HYPEREVM": "HYPEREVM_MAINNET", "LISK": "LISK_MAINNET",
}

// executeCircleViaChainRails routes Circle wallet withdrawals through ChainRails.
// 1. Find user's Circle wallet with USDC
// 2. Create ChainRails intent (source = wallet's chain, dest = user's chosen chain)
// 3. Circle transfers USDC to ChainRails intent address (same-chain)
// 4. ChainRails delivers to destination on any chain
func (s *WithdrawalService) executeCircleViaChainRails(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, destinationChain string) (*CryptoTransferResult, error) {
	// Step 1: Find wallet with USDC, its blockchain, and on-chain address
	walletID, tokenID, blockchain, onChainAddress, err := s.circleTransfer.FindWalletWithUSDC(ctx, withdrawal.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("no USDC wallet found: %w", err)
	}

	// Step 2: Map source blockchain to ChainRails
	sourceChainRails, ok := circleChainToChainRails[blockchain]
	if !ok {
		return nil, fmt.Errorf("unsupported source chain for ChainRails: %s", blockchain)
	}

	// Step 3: Map destination chain — match testnet/mainnet to source
	crDestChain := destChainToChainRails[strings.ToUpper(destinationChain)]
	if crDestChain == "" {
		return nil, fmt.Errorf("unsupported destination chain for ChainRails: %s", destinationChain)
	}
	// If source is testnet, force destination to testnet too
	sourceIsTestnet := strings.Contains(sourceChainRails.chain, "TESTNET")
	if sourceIsTestnet && strings.Contains(crDestChain, "MAINNET") {
		crDestChain = strings.Replace(crDestChain, "MAINNET", "TESTNET", 1)
	}

	amountMicro := withdrawal.Amount.Shift(6).IntPart()

	// Step 4: Create ChainRails intent — use on-chain address for sender/refund, NOT Circle UUID
	intent, err := s.chainRailsAdapter.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          sourceChainRails.token,
		SourceChain:      sourceChainRails.chain,
		DestinationChain: crDestChain,
		Recipient:        destinationAddress,
		Sender:           onChainAddress,
		RefundAddress:    onChainAddress,
		Metadata: map[string]interface{}{
			"withdrawal_id": withdrawal.ID.String(),
			"user_id":       withdrawal.UserID.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("chainrails intent creation failed: %w", err)
	}

	s.logger.Info("ChainRails intent created for Circle withdrawal",
		"withdrawal_id", withdrawal.ID.String(),
		"intent_id", intent.ID,
		"intent_address", intent.IntentAddress,
		"source_chain", sourceChainRails.chain,
		"dest_chain", crDestChain,
		"total_amount", intent.TotalAmountInAssetToken)

	// Step 5: Fund the intent — use total_amount_in_asset_token (includes fees) converted to human-readable
	if intent.TotalAmountInAssetToken == "" || intent.AssetTokenDecimals == 0 {
		return nil, fmt.Errorf("chainrails did not return total amount — cannot determine funding amount")
	}
	totalMicro, parseOk := new(big.Int).SetString(intent.TotalAmountInAssetToken, 10)
	if !parseOk {
		return nil, fmt.Errorf("failed to parse chainrails total amount: %s", intent.TotalAmountInAssetToken)
	}
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(intent.AssetTokenDecimals)), nil)
	humanAmount := new(big.Float).Quo(new(big.Float).SetInt(totalMicro), new(big.Float).SetInt(divisor))
	circleAmount := humanAmount.Text('f', intent.AssetTokenDecimals)

	// Calculate ChainRails bridging fee (total - user amount) for post-transfer debit
	crFee := decimal.NewFromBigInt(totalMicro, 0).Div(decimal.New(1, int32(intent.AssetTokenDecimals))).Sub(withdrawal.Amount)

	tx, err := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, intent.IntentAddress, circleAmount)
	if err != nil {
		s.logger.Error("Circle transfer to ChainRails intent failed",
			"withdrawal_id", withdrawal.ID.String(),
			"intent_id", intent.ID,
			"error", err)
		return nil, fmt.Errorf("circle transfer to chainrails intent failed: %w", err)
	}

	// H6: Check Circle transfer state
	if tx.State == "DENIED" || tx.State == "FAILED" || tx.State == "CANCELLED" {
		return nil, fmt.Errorf("circle transfer %s", string(tx.State))
	}

	// Debit ChainRails bridging fee from ledger AFTER successful Circle transfer
	if crFee.IsPositive() && s.ledgerService != nil {
		accountType, _ := mapWithdrawalSourceToAccountType(withdrawal.SourceAccount)
		if feeErr := s.ledgerService.CreateTransaction(ctx, withdrawal.UserID, accountType, entities.TransactionTypeWithdrawal,
			crFee, map[string]interface{}{
				"withdrawal_id": withdrawal.ID.String(),
				"fee_type":      "chainrails_bridging",
			}); feeErr != nil {
			// Fee debit failed but Circle transfer already sent — log critical, don't fail the withdrawal
			s.logger.Error("CRITICAL: ChainRails fee debit failed after Circle transfer succeeded",
				"error", feeErr, "withdrawal_id", withdrawal.ID.String(), "fee", crFee.String())
		} else {
			s.logger.Info("ChainRails bridging fee debited", "withdrawal_id", withdrawal.ID.String(), "fee", crFee.String())
		}
	}

	s.logger.Info("Circle transfer to ChainRails intent initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"circle_tx_id", tx.ID,
		"intent_address", intent.IntentAddress)

	return &CryptoTransferResult{
		TransferID: fmt.Sprintf("cr:%d:%s", intent.ID, tx.ID),
		State:      intent.IntentStatus,
	}, nil
}

// executeCircleTransfer sends USDC via Circle Programmable Wallets (same-chain only).
func (s *WithdrawalService) executeCircleTransfer(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, destinationChain string) (*CryptoTransferResult, error) {
	walletID, tokenID, _, _, err := s.circleTransfer.FindWalletWithUSDC(ctx, withdrawal.UserID.String())
	if err != nil {
		return nil, fmt.Errorf("no USDC wallet found: %w", err)
	}

	tx, err := s.circleTransfer.TransferUSDC(ctx, walletID, tokenID, destinationAddress, withdrawal.Amount.StringFixed(2))
	if err != nil {
		return nil, fmt.Errorf("circle transfer failed: %w", err)
	}

	s.logger.Info("Circle crypto transfer initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"source_wallet", walletID,
		"destination", destinationAddress,
		"dest_chain", destinationChain,
		"circle_tx_id", tx.ID,
		"state", string(tx.State))

	return &CryptoTransferResult{
		TransferID: tx.ID,
		TxHash:     tx.TxHash,
		State:      string(tx.State),
	}, nil
}

// USDC contract address on Base mainnet.
const usdcBaseMainnet = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

// executeChainRailsTransfer creates a ChainRails intent and funds it via Bridge.
// Flow: Create intent → get intent_address → Bridge transfer USDC to intent_address → ChainRails bridges cross-chain.
func (s *WithdrawalService) executeChainRailsTransfer(ctx context.Context, withdrawal *entities.Withdrawal, destinationAddress, crDestChain, sourceWalletAddress string) (*CryptoTransferResult, error) {
	if s.chainRailsAdapter == nil {
		return nil, fmt.Errorf("chainrails adapter not configured")
	}
	if s.bridgeCryptoAdapter == nil {
		return nil, fmt.Errorf("bridge crypto adapter not configured")
	}

	// ChainRails only supports USDC bridging — reject other currencies explicitly.
	if withdrawal.Currency != entities.WithdrawalCurrencyUSDC {
		return nil, fmt.Errorf("ChainRails cross-chain transfers only support USDC, got %s", withdrawal.Currency)
	}

	walletID := withdrawal.BridgeWalletID
	if walletID == nil || *walletID == "" {
		return nil, fmt.Errorf("bridge wallet ID not provided")
	}

	// Amount in smallest unit (USDC has 6 decimals)
	amountMicro := withdrawal.Amount.Shift(6).IntPart()

	// Step 1: Create ChainRails intent
	intent, err := s.chainRailsAdapter.CreateIntent(ctx, &chainrailspkg.CreateIntentRequest{
		Amount:           fmt.Sprintf("%d", amountMicro),
		AmountSymbol:     "USDC",
		TokenIn:          usdcBaseMainnet,
		SourceChain:      "BASE_MAINNET",
		DestinationChain: crDestChain,
		Recipient:        destinationAddress,
		Sender:           sourceWalletAddress,
		RefundAddress:    sourceWalletAddress, // Refund to user's Bridge custody wallet on Base
		Metadata: map[string]interface{}{
			"withdrawal_id": withdrawal.ID.String(),
			"user_id":       withdrawal.UserID.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("chainrails intent creation failed: %w", err)
	}

	s.logger.Info("ChainRails intent created",
		"withdrawal_id", withdrawal.ID.String(),
		"intent_id", intent.ID,
		"intent_address", intent.IntentAddress,
		"dest_chain", crDestChain,
		"fees_usd", intent.FeesInUSD)

	// Step 2: Fund the intent by transferring USDC from user's Bridge wallet to the intent address on Base
	transfer, err := s.bridgeCryptoAdapter.TransferFunds(ctx, &bridgepkg.CreateTransferRequest{
		OnBehalfOf: withdrawal.UserID.String(),
		Amount:     withdrawal.Amount.StringFixed(2),
		Source: bridgepkg.TransferSource{
			PaymentRail:    bridgepkg.PaymentRail("bridge_wallet"),
			Currency:       bridgepkg.CurrencyUSDC,
			BridgeWalletID: *walletID,
		},
		Destination: bridgepkg.TransferDestination{
			PaymentRail: bridgepkg.PaymentRailBase,
			Currency:    bridgepkg.CurrencyUSDC,
			ToAddress:   intent.IntentAddress,
		},
	})
	if err != nil {
		// Intent was created but funding failed. ChainRails will auto-expire the unfunded intent.
		s.logger.Error("Bridge transfer to ChainRails intent failed — intent will expire unfunded",
			"withdrawal_id", withdrawal.ID.String(),
			"intent_id", intent.ID,
			"intent_address", intent.IntentAddress,
			"error", err)
		return nil, fmt.Errorf("bridge transfer to chainrails intent failed: %w", err)
	}

	s.logger.Info("Bridge transfer to ChainRails intent initiated",
		"withdrawal_id", withdrawal.ID.String(),
		"bridge_transfer_id", transfer.ID,
		"intent_address", intent.IntentAddress)

	// Store the ChainRails intent ID as the provider reference for webhook reconciliation
	return &CryptoTransferResult{
		TransferID: fmt.Sprintf("cr:%d:%s", intent.ID, transfer.ID),
		State:      intent.IntentStatus,
	}, nil
}

// CompleteChainRailsWithdrawal marks a ChainRails-routed withdrawal as completed.
// Called by the webhook handler when intent.completed fires for a withdrawal intent.
func (s *WithdrawalService) CompleteChainRailsWithdrawal(ctx context.Context, intentID int, txHash string) error {
	// Find withdrawal by provider transfer ID prefix "cr:{intentID}:"
	prefix := fmt.Sprintf("cr:%d:", intentID)
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferIDPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("withdrawal not found for intent %d: %w", intentID, err)
	}

	lock := s.userWithdrawalLock(withdrawal.UserID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read after acquiring lock
	withdrawal, err = s.withdrawalRepo.GetByProviderTransferIDPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("withdrawal not found for intent %d: %w", intentID, err)
	}
	if withdrawal.Status == entities.WithdrawalStatusCompleted {
		return fmt.Errorf("already processed")
	}

	if txHash != "" {
		if err := s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, txHash); err != nil {
			s.logger.Error("Failed to update tx hash", "withdrawal_id", withdrawal.ID, "error", err)
		}
	}

	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return fmt.Errorf("failed to mark withdrawal completed: %w", err)
	}

	// Record limit usage on successful completion
	if s.limitsService != nil {
		_ = s.limitsService.RecordWithdrawal(ctx, withdrawal.UserID, withdrawal.Amount)
	}
	if s.tieredLimits != nil {
		_ = s.tieredLimits.RecordWithdrawal(ctx, withdrawal.UserID, withdrawal.Amount)
	}

	// Notify user
	if s.notificationService != nil {
		destAddr := ""
		if withdrawal.DestinationAddress != nil {
			destAddr = *withdrawal.DestinationAddress
		}
		_ = s.notificationService.NotifyWithdrawalCompleted(ctx, withdrawal.UserID, withdrawal.Amount, destAddr)
	}

	s.logger.Info("ChainRails withdrawal completed",
		"withdrawal_id", withdrawal.ID.String(),
		"intent_id", intentID,
		"tx_hash", txHash)
	return nil
}

// RefundChainRailsWithdrawal reverses a failed ChainRails withdrawal.
// Called by the webhook handler when intent.refunded fires.
func (s *WithdrawalService) RefundChainRailsWithdrawal(ctx context.Context, intentID int) error {
	prefix := fmt.Sprintf("cr:%d:", intentID)
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferIDPrefix(ctx, prefix)
	if err != nil {
		return fmt.Errorf("withdrawal not found for intent %d: %w", intentID, err)
	}

	// Acquire user lock to prevent double-reversal from webhook retries
	lock := s.userWithdrawalLock(withdrawal.UserID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read status under lock to prevent TOCTOU race
	withdrawal, err = s.withdrawalRepo.GetByID(ctx, withdrawal.ID)
	if err != nil {
		return fmt.Errorf("failed to re-read withdrawal: %w", err)
	}

	if withdrawal.Status == entities.WithdrawalStatusCompleted {
		s.logger.Warn("Ignoring refund for already completed withdrawal", "withdrawal_id", withdrawal.ID)
		return nil
	}
	if withdrawal.Status == entities.WithdrawalStatusFailed || withdrawal.Status == entities.WithdrawalStatusCancelled {
		s.logger.Warn("Ignoring refund for already failed/cancelled withdrawal", "withdrawal_id", withdrawal.ID)
		return nil
	}

	// Reverse the ledger debit to restore the user's balance
	if err := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to reverse ledger for ChainRails refund",
			"withdrawal_id", withdrawal.ID, "error", err)
		return fmt.Errorf("failed to reverse ledger: %w", err)
	}

	if err := s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, fmt.Sprintf("chainrails intent %d refunded", intentID)); err != nil {
		return fmt.Errorf("failed to mark withdrawal failed: %w", err)
	}

	s.logger.Info("ChainRails withdrawal refunded and reversed",
		"withdrawal_id", withdrawal.ID.String(),
		"intent_id", intentID)

	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalFailed(ctx, withdrawal.UserID, withdrawal.Amount,
			"Cross-chain transfer was refunded. Your funds have been returned.")
	}
	return nil
}

func mapDestChainToPaymentRail(chain string) bridgepkg.PaymentRail {
	switch strings.ToLower(chain) {
	case "solana", "sol", "sol-devnet":
		return bridgepkg.PaymentRailSolana
	case "polygon", "matic", "matic-amoy":
		return bridgepkg.PaymentRailPolygon
	case "base", "base-sepolia":
		return bridgepkg.PaymentRailBase
	case "avalanche", "avax", "avax-fuji":
		return bridgepkg.PaymentRailAvalanche
	case "ethereum", "eth":
		return bridgepkg.PaymentRailEthereum
	case "arbitrum", "arb":
		return bridgepkg.PaymentRailArbitrum
	case "optimism", "op":
		return bridgepkg.PaymentRailOptimism
	default:
		return bridgepkg.PaymentRailBase
	}
}

// executeFiatTransfer executes a fiat transfer via Bridge offramp
func (s *WithdrawalService) executeFiatTransfer(ctx context.Context, withdrawal *entities.Withdrawal, bankAccount *entities.BankAccount) (string, error) {
	if s.bridgeAdapter == nil {
		return "", fmt.Errorf("bridge adapter not configured")
	}

	if bankAccount.BridgeRecipientID == nil || *bankAccount.BridgeRecipientID == "" {
		return "", fmt.Errorf("bank account not registered with Bridge")
	}

	// Format amount (include fee so Bridge delivers net amount to recipient)
	amountStr := withdrawal.Amount.Add(withdrawal.FeeAmount).StringFixed(2)

	// Create transfer request
	req := map[string]interface{}{
		"source":        "USDC",
		"amount":        amountStr,
		"developer_fee": withdrawal.FeeAmount.StringFixed(2),
		"currency":      string(withdrawal.Currency),
		"recipient_id": *bankAccount.BridgeRecipientID,
		"source_wallet_id": func() string {
			if withdrawal.BridgeWalletID == nil {
				return ""
			}
			return *withdrawal.BridgeWalletID
		}(),
		"on_behalf_of":    withdrawal.UserID.String(),
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

	if metrics.Business != nil {
		metrics.Business.WithdrawalsCompleted.WithLabelValues("crypto").Inc()
		metrics.Business.WithdrawalAmount.WithLabelValues("crypto").Observe(withdrawal.Amount.InexactFloat64())
	}

	return nil
}

func (s *WithdrawalService) settleCompletedFiatWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal) error {
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
		return nil
	}

	now := time.Now()
	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return fmt.Errorf("failed to complete withdrawal: %w", err)
	}
	withdrawal.Status = entities.WithdrawalStatusCompleted
	withdrawal.CompletedAt = &now
	withdrawal.UpdatedAt = now

	if metrics.Business != nil {
		metrics.Business.WithdrawalsCompleted.WithLabelValues("fiat").Inc()
		metrics.Business.WithdrawalAmount.WithLabelValues("fiat").Observe(withdrawal.Amount.InexactFloat64())
	}

	if withdrawal.BankAccountID != nil {
		if err := s.VerifyBankAccount(ctx, withdrawal.UserID, *withdrawal.BankAccountID); err != nil {
			s.logger.Warn("Failed to verify bank account after successful fiat withdrawal",
				"withdrawal_id", withdrawal.ID.String(),
				"bank_account_id", withdrawal.BankAccountID.String(),
				"error", err)
		}
	}

	if s.notificationService != nil {
		destination := "bank account"
		if withdrawal.BankAccountID != nil {
			if bankAccount, err := s.bankAccountRepo.GetByID(ctx, *withdrawal.BankAccountID); err == nil && bankAccount != nil {
				switch {
				case bankAccount.AccountNumberLast4 != "":
					destination = "bank account ending in " + bankAccount.AccountNumberLast4
				case bankAccount.IBAN != nil && len(strings.TrimSpace(*bankAccount.IBAN)) >= 4:
					iban := strings.TrimSpace(*bankAccount.IBAN)
					destination = "IBAN ending in " + iban[len(iban)-4:]
				}
			}
		}
		_ = s.notificationService.NotifyWithdrawalCompleted(ctx, withdrawal.UserID, withdrawal.Amount, destination)
	}

	return nil
}

func (s *WithdrawalService) failWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal, reason string) error {
	if withdrawal == nil {
		return nil
	}
	if withdrawal.Status.IsTerminal() {
		return nil
	}

	if err := s.reverseWithdrawalLedgerEntry(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to reverse withdrawal ledger entry: %w", err)
	}
	if err := s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason); err != nil {
		return fmt.Errorf("failed to mark withdrawal failed: %w", err)
	}

	withdrawal.Status = entities.WithdrawalStatusFailed
	withdrawal.ErrorMessage = &reason
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalFailed(ctx, withdrawal.UserID, withdrawal.Amount, reason)
	}

	return nil
}

func (s *WithdrawalService) CompleteWithdrawalByTransferID(ctx context.Context, transferID string) error {
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal by transfer id: %w", err)
	}
	if withdrawal == nil {
		return nil
	}

	if withdrawal.IsFiat() {
		return s.settleCompletedFiatWithdrawal(ctx, withdrawal)
	}
	return s.settleCompletedCryptoWithdrawal(ctx, withdrawal)
}

func (s *WithdrawalService) FailWithdrawalByTransferID(ctx context.Context, transferID, reason string) error {
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal by transfer id: %w", err)
	}
	if withdrawal == nil {
		return nil
	}

	return s.failWithdrawal(ctx, withdrawal, reason)
}

func (s *WithdrawalService) MarkWithdrawalUnderReview(ctx context.Context, transferID string) error {
	s.logger.Info("Marking withdrawal as under review", zap.String("transfer_id", transferID))
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal: %w", err)
	}
	if withdrawal == nil {
		s.logger.Warn("Withdrawal not found for transfer ID", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal not found for transfer_id: %s", transferID)
	}
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	return nil
}

func (s *WithdrawalService) UpdateWithdrawalStatus(ctx context.Context, transferID, status string) error {
	s.logger.Info("Updating withdrawal status",
		zap.String("transfer_id", transferID),
		zap.String("status", status))
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal: %w", err)
	}
	if withdrawal == nil {
		s.logger.Warn("Withdrawal not found for transfer ID", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal not found for transfer_id: %s", transferID)
	}
	var newStatus entities.WithdrawalStatus
	switch status {
	case "refund_in_flight":
		newStatus = entities.WithdrawalStatusProcessing
	default:
		newStatus = entities.WithdrawalStatus(status)
	}
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, newStatus); err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	return nil
}

func (s *WithdrawalService) MarkWithdrawalRefundFailed(ctx context.Context, transferID string) error {
	s.logger.Error("Marking withdrawal refund as failed - manual intervention required",
		zap.String("transfer_id", transferID))
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal: %w", err)
	}
	if withdrawal == nil {
		s.logger.Warn("Withdrawal not found for transfer ID", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal not found for transfer_id: %s", transferID)
	}
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusFailed); err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	return nil
}

func (s *WithdrawalService) MarkWithdrawalRefunded(ctx context.Context, transferID string) error {
	s.logger.Info("Marking withdrawal as refunded", zap.String("transfer_id", transferID))
	withdrawal, err := s.withdrawalRepo.GetByProviderTransferID(ctx, strings.TrimSpace(transferID))
	if err != nil {
		return fmt.Errorf("failed to fetch withdrawal: %w", err)
	}
	if withdrawal == nil {
		s.logger.Warn("Withdrawal not found for transfer ID", zap.String("transfer_id", transferID))
		return fmt.Errorf("withdrawal not found for transfer_id: %s", transferID)
	}
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusReversed); err != nil {
		return fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	return nil
}

func (s *WithdrawalService) syncWithdrawalStatusFromProvider(ctx context.Context, withdrawal *entities.Withdrawal) (entities.WithdrawalStatus, error) {
	if withdrawal == nil || withdrawal.Status.IsTerminal() {
		if withdrawal == nil {
			return entities.WithdrawalStatusInitiated, nil
		}
		return withdrawal.Status, nil
	}
	if withdrawal.ProviderTransferID == nil || strings.TrimSpace(*withdrawal.ProviderTransferID) == "" {
		return withdrawal.Status, nil
	}

	transferID := strings.TrimSpace(*withdrawal.ProviderTransferID)

	// ChainRails withdrawals are tracked via webhooks, not polling
	if strings.HasPrefix(transferID, "cr:") {
		return withdrawal.Status, nil
	}

	transfer, err := s.bridgeAdapter.GetTransferStatus(ctx, transferID)
	if err != nil {
		return withdrawal.Status, err
	}
	if transfer == nil {
		return withdrawal.Status, nil
	}

	state := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", transfer["status"])))
	switch state {
	case "PAYMENT_PROCESSED", "COMPLETED", "SUCCESS":
		if withdrawal.IsFiat() {
			if err := s.settleCompletedFiatWithdrawal(ctx, withdrawal); err != nil {
				return withdrawal.Status, err
			}
		} else {
			if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
				return withdrawal.Status, err
			}
		}
	case "CANCELED", "UNDELIVERABLE", "RETURNED", "FAILED", "REFUNDED", "ERROR":
		reason := "bridge transfer " + strings.ToLower(state)
		if err := s.failWithdrawal(ctx, withdrawal, reason); err != nil {
			return withdrawal.Status, err
		}
	}

	return withdrawal.Status, nil
}

func (s *WithdrawalService) syncCryptoWithdrawalStatusFromProvider(ctx context.Context, withdrawal *entities.Withdrawal) (entities.WithdrawalStatus, error) {
	return s.syncWithdrawalStatusFromProvider(ctx, withdrawal)
}

func scopedWithdrawalIdempotencyKey(userID uuid.UUID, flow string, clientKey string) string {
	normalized := strings.TrimSpace(clientKey)
	if normalized == "" {
		// SECURITY: Auto-generated keys must NOT collide across different withdrawal
		// requests. Using only a time window caused silent deduplication of distinct
		// withdrawals. Require a client-provided idempotency key for all withdrawals.
		// If none provided, use a random UUID so each request is unique (no dedup).
		normalized = "auto:" + uuid.New().String()
	}
	// Use UUID v5 (deterministic) so the result is a valid UUID format
	// as required by provider idempotencyKey fields.
	name := "withdrawal:" + flow + ":" + userID.String() + ":" + normalized
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

// VerifyBankAccount marks a bank account as verified after successful withdrawal.
// userID must match the account owner to prevent IDOR.
func (s *WithdrawalService) VerifyBankAccount(ctx context.Context, userID, bankAccountID uuid.UUID) error {
	bankAccount, err := s.bankAccountRepo.GetByID(ctx, bankAccountID)
	if err != nil {
		return fmt.Errorf("failed to get bank account: %w", err)
	}
	if bankAccount == nil {
		return fmt.Errorf("bank account not found")
	}

	if bankAccount.UserID != userID {
		s.logger.Error("VerifyBankAccount: ownership mismatch",
			"requesting_user_id", userID.String(),
			"account_owner_id", bankAccount.UserID.String(),
			"bank_account_id", bankAccountID.String())
		return fmt.Errorf("unauthorized: bank account does not belong to user")
	}

	if bankAccount.IsVerified {
		return nil // Already verified
	}

	bankAccount.IsVerified = true
	bankAccount.UpdatedAt = time.Now()

	if err := s.bankAccountRepo.Update(ctx, bankAccount); err != nil {
		return fmt.Errorf("failed to verify bank account: %w", err)
	}

	s.logger.Info("Bank account verified after successful withdrawal",
		"user_id", userID.String(),
		"bank_account_id", bankAccountID.String())
	return nil
}
