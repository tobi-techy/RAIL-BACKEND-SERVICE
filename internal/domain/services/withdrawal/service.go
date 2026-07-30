package withdrawal

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	domainerrors "github.com/rail-service/rail_service/internal/domain/errors"
	stashlocksvc "github.com/rail-service/rail_service/internal/domain/services/stashlock"
	bridgepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	chainrailspkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	blendpkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/blend"
	"github.com/rail-service/rail_service/pkg/analytics"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/rail-service/rail_service/pkg/metrics"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Crypto withdrawal constants
const (
	CryptoWithdrawalMinAmount   = 1.00 // Minimum crypto withdrawal
	FiatWithdrawalMinAmountUSD  = 1.00 // Minimum USD fiat withdrawal
	FiatWithdrawalMinAmountEUR  = 1.00 // Minimum EUR fiat withdrawal
	CryptoWithdrawalFeePercent  = 0.0  // No percentage fee — flat only
	CryptoWithdrawalFeeSolana   = 0.10 // $0.10 flat service fee for Solana withdrawals
	CryptoWithdrawalFeeEVM      = 0.10 // $0.10 flat service fee for EVM chain withdrawals
	FlatWithdrawalFee           = 0.10 // Default flat fee
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
	CreatePendingTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, idempotencyKey string, metadata map[string]interface{}) error
	CommitPendingTransaction(ctx context.Context, idempotencyKey string) error
	FailPendingTransaction(ctx context.Context, idempotencyKey string) error
	GetLedgerTransactionStatus(ctx context.Context, idempotencyKey string) (entities.TransactionStatus, error)
	ReverseTransaction(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, originalTxID string, amount decimal.Decimal, metadata map[string]interface{}) error
	TransferSpendingToStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
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
	UpdateCompletedAt(ctx context.Context, id uuid.UUID, completedAt time.Time) error
	ForceComplete(ctx context.Context, id uuid.UUID, completedAt time.Time) error
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
	GetTransaction(ctx context.Context, txID string) (*circlepkg.Transaction, error)
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
	CreatePendingEmergencyTransfer(ctx context.Context, userID uuid.UUID, amount, fee decimal.Decimal, idempotencyKey string) error
}

// StashYieldRedeemer redeems a user's yield position back to USDC before stash funds exit.
type StashYieldRedeemer interface {
	RedeemStashYield(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
}

// RedemptionReserver durably reserves a redemption before the ledger moves, so
// the reconciliation worker always has a record to resume if the async redeem
// attempt dies. Implemented by the Blend deposit router.
type RedemptionReserver interface {
	EnsureRedemptionReserved(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (bool, error)
	AbandonRedemption(ctx context.Context, idempotencyKey, reason string) error
}

// StashYieldRedeemerForTransfer extends StashYieldRedeemer with a variant that skips the
// Base→Solana sweep. Use when funds are about to leave the platform via a crypto transfer
// so the transfer itself can spend the USDC from the Base EOA without racing the sweep.
type StashYieldRedeemerForTransfer interface {
	StashYieldRedeemer
	RedeemStashYieldForTransfer(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error
}

// ChainRailsTransferAdapter creates cross-chain intents via ChainRails.
type ChainRailsTransferAdapter interface {
	CreateIntent(ctx context.Context, req *chainrailspkg.CreateIntentRequest) (*chainrailspkg.CreateIntentResponse, error)
	GetIntentStatus(ctx context.Context, intentAddress string) (*chainrailspkg.IntentStatus, error)
}

// FraudChecker screens withdrawals for fraud risk.
type FraudChecker interface {
	CheckWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination, deviceID, ipAddress, sessionID string) (action string, requiresMFA bool, blockReason string, err error)
}

// SessionAnomalyChecker detects impossible travel and concurrent country anomalies.
type SessionAnomalyChecker interface {
	GetRecentCriticalAnomalies(ctx context.Context, userID uuid.UUID) (hasCritical bool, reason string, err error)
}

// SpendingCommitmentGuard enforces a user's self-imposed daily spending cap.
type SpendingCommitmentGuard interface {
	CheckOutflow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error
	RecordOutflow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error
}

// AdminErrorAlerter sends detailed error emails to the ops team.
type AdminErrorAlerter interface {
	SendErrorAlert(ctx context.Context, payload AdminErrorPayload)
}

// AdminErrorPayload holds structured error context for admin alerts.
type AdminErrorPayload struct {
	UserID      string
	Operation   string
	Error       error
	PanicStack  []byte
	ExtraFields map[string]string
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
	chainRailsAdapter   ChainRailsTransferAdapter
	stashLock           StashLockChecker
	emergencyLedger     EmergencyLedger
	stashYieldRedeemer  StashYieldRedeemer
	complianceScreener  ComplianceScreener
	addressWhitelist    AddressWhitelistChecker
	tieredLimits        TieredWithdrawalLimitChecker
	fraudChecker        FraudChecker
	sessionAnomaly      SessionAnomalyChecker
	commitment          SpendingCommitmentGuard
	adminAlerter        AdminErrorAlerter
	stashLockMu         sync.RWMutex
	db                  *sqlx.DB
	logger              *logger.Logger
	withdrawalLocks     [withdrawalLockShards]sync.Mutex
}

type CryptoTransferResult struct {
	TxHash        string
	TransferID    string
	State         string
	IntentAddress string // ChainRails intent address for polling
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

// SetStashYieldRedeemer wires the yield-provider redeem path for stash withdrawals.
func (s *WithdrawalService) SetStashYieldRedeemer(r StashYieldRedeemer) {
	s.stashYieldRedeemer = r
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

// SetStashTransferRepo wires the stash transfer repository for audit logging.
func (s *WithdrawalService) SetStashTransferRepo(r StashTransferRepository) {
	s.stashTransferRepo = r
}

// SetFraudChecker wires the fraud detection service (optional, graceful degradation).
func (s *WithdrawalService) SetFraudChecker(f FraudChecker) {
	s.fraudChecker = f
}

// SetSessionAnomalyChecker wires the session anomaly detector for impossible travel blocking.
func (s *WithdrawalService) SetSessionAnomalyChecker(a SessionAnomalyChecker) {
	s.sessionAnomaly = a
}

// SetSpendingCommitment wires the self-imposed daily spending cap enforcer.
func (s *WithdrawalService) SetSpendingCommitment(g SpendingCommitmentGuard) {
	s.commitment = g
}

// SetAdminAlerter wires the admin error alert email sender.
func (s *WithdrawalService) SetAdminAlerter(a AdminErrorAlerter) {
	s.adminAlerter = a
}

// alertAdmin is a nil-safe helper that sends an admin error alert if configured.
func (s *WithdrawalService) alertAdmin(payload AdminErrorPayload) {
	if s.adminAlerter != nil {
		s.adminAlerter.SendErrorAlert(context.Background(), payload)
	}
}

// IsStashLocked returns true if the user has no open withdrawal window.
func (s *WithdrawalService) IsStashLocked(ctx context.Context, userID uuid.UUID) (bool, error) {
	s.stashLockMu.RLock()
	sl := s.stashLock
	s.stashLockMu.RUnlock()
	if sl == nil {
		return false, nil // No lock service configured — not locked
	}
	canWithdraw, _, err := sl.CanWithdraw(ctx, userID)
	if err != nil {
		return false, err
	}
	return !canWithdraw, nil
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
		if errors.Is(err, stashlocksvc.ErrNoLockedCycles) {
			feePct = decimal.NewFromFloat(0.03)
			days = 0
		} else {
			return nil, err
		}
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
		// No locked cycles — apply maximum fee tier (3%) as default
		if errors.Is(err, stashlocksvc.ErrNoLockedCycles) {
			feePct = decimal.NewFromFloat(0.03)
		} else {
			return nil, err
		}
	}
	fee := amount.Mul(feePct).RoundBank(2)
	total := amount.Add(fee)
	if balance.LessThan(total) {
		return nil, fmt.Errorf("insufficient stash balance: have %s, need %s (amount %s + fee %s)", balance.String(), total.String(), amount.String(), fee.String())
	}

	// Compliance screening — submit to Didit for AML/sanctions monitoring.
	// Emergency access fails open if compliance is unavailable, but hard-blocks
	// on a DECLINED verdict.
	if s.complianceScreener != nil {
		screenStatus, screenErr := s.complianceScreener.ScreenTransaction(ctx, userID, "emergency-stash-"+idempotencyKey, "emergency_stash_to_spending", amount, "USD", "")
		if screenErr != nil {
			s.logger.Error("Compliance screening unavailable — proceeding with emergency transfer",
				"user_id", userID.String(), "error", screenErr)
		} else if screenStatus == "DECLINED" {
			return nil, domainerrors.NewDomainError(
				domainerrors.ErrConflict,
				"COMPLIANCE_DECLINED",
				"Emergency transfer was declined by compliance screening.",
			).WithDetails(map[string]interface{}{"status": screenStatus})
		} else if screenStatus != "APPROVED" {
			s.logger.Warn("Emergency transfer not immediately approved by compliance — proceeding",
				"user_id", userID.String(), "status", screenStatus)
		}
	}

	// Reserve the Blend redemption BEFORE the ledger moves so a crash after the
	// transfer always leaves a durable record for the reconciliation worker —
	// otherwise the ledger can run ahead of Blend custody with nothing to retry.
	redeemKey := "emergency-redeem-" + idempotencyKey
	blendReserved := false
	if reserver, ok := s.stashYieldRedeemer.(RedemptionReserver); ok && reserver != nil {
		reserved, rerr := reserver.EnsureRedemptionReserved(ctx, userID, total, redeemKey)
		if rerr != nil {
			return nil, fmt.Errorf("reserve yield redemption: %w", rerr)
		}
		blendReserved = reserved
	}

	// Create a PENDING ledger transfer — balances are NOT updated yet. The debit
	// is committed only after the Blend redemption succeeds (see async goroutine below).
	pendingKey := "emergency-stash-pending-" + idempotencyKey
	if err := s.emergencyLedger.CreatePendingEmergencyTransfer(ctx, userID, amount, fee, pendingKey); err != nil {
		if blendReserved {
			if reserver, ok := s.stashYieldRedeemer.(RedemptionReserver); ok {
				abandonCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				if aerr := reserver.AbandonRedemption(abandonCtx, redeemKey, "emergency ledger pending failed: "+err.Error()); aerr != nil {
					s.logger.Error("failed to abandon Blend redemption after emergency pending ledger failure",
						zap.String("user_id", userID.String()), zap.Error(aerr))
				}
				cancel()
			}
		}
		return nil, fmt.Errorf("emergency transfer failed: %w", err)
	}
	if err := sl.MarkEmergencyWithdrawn(ctx, userID); err != nil {
		s.logger.Error("failed to mark cycles as emergency withdrawn", zap.String("user_id", userID.String()), zap.Error(err))
	}

	transferID := uuid.New()
	if s.stashTransferRepo != nil {
		now := time.Now()
		if err := s.stashTransferRepo.Create(ctx, &entities.StashTransfer{
			ID:          transferID,
			UserID:      userID,
			Amount:      amount,
			Direction:   entities.StashTransferDirectionStashToSpending,
			Status:      entities.StashTransferStatusCompleted,
			CreatedAt:   now,
			CompletedAt: &now,
		}); err != nil {
			s.logger.Error("failed to record emergency stash transfer",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	// Async: Blend redemption + commit pending ledger. Balances only move after
	// Blend custody is reconciled, preventing a stash debit without backing funds.
	if s.stashYieldRedeemer != nil && blendReserved {
		total := total
		go func() {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := s.stashYieldRedeemer.RedeemStashYield(rctx, userID, total, redeemKey); err != nil {
				s.logger.Error("emergency stash-to-spending: Blend redemption failed — failing pending ledger",
					zap.String("user_id", userID.String()), zap.String("amount", total.String()), zap.Error(err))
				if failErr := s.ledgerService.FailPendingTransaction(rctx, pendingKey); failErr != nil {
					s.logger.Error("emergency stash-to-spending: failed to mark pending ledger failed",
						zap.String("user_id", userID.String()), zap.Error(failErr))
				}
				return
			}
			if commitErr := s.ledgerService.CommitPendingTransaction(rctx, pendingKey); commitErr != nil {
				s.logger.Error("emergency stash-to-spending: failed to commit pending ledger after Blend redemption",
					zap.String("user_id", userID.String()), zap.Error(commitErr))
			}
		}()
	} else {
		// No Blend reservation — commit the pending ledger immediately.
		if err := s.ledgerService.CommitPendingTransaction(ctx, pendingKey); err != nil {
			s.logger.Error("emergency stash-to-spending: failed to commit pending ledger immediately",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}
	if s.notificationService != nil {
		_ = s.notificationService.NotifyEmergencyWithdrawal(ctx, userID, amount, fee)
	}

	return &entities.EmergencyWithdrawalResult{
		Amount:     amount,
		Fee:        fee,
		FeePercent: feePct,
		NetAmount:  amount.Sub(fee),
		TransferID: transferID,
	}, nil
}

// FundStash transfers funds from spending balance to investment stash.
func (s *WithdrawalService) FundStash(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) (*entities.FundStashResult, error) {
	if amount.LessThan(decimal.NewFromInt(1)) {
		return nil, fmt.Errorf("minimum amount is $1.00")
	}

	balance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return nil, fmt.Errorf("failed to get spending balance: %w", err)
	}
	if balance.LessThan(amount) {
		return nil, fmt.Errorf("insufficient spending balance: have %s, need %s", balance.String(), amount.String())
	}

	if err := s.ledgerService.TransferSpendingToStash(ctx, userID, amount, idempotencyKey); err != nil {
		return nil, fmt.Errorf("transfer failed: %w", err)
	}

	transferID := uuid.New()
	now := time.Now()
	transfer := &entities.StashTransfer{
		ID:        transferID,
		UserID:    userID,
		Amount:    amount,
		Direction: entities.StashTransferDirectionSpendingToStash,
		Status:    entities.StashTransferStatusCompleted,
		CreatedAt: now,
	}
	if s.stashTransferRepo != nil {
		if err := s.stashTransferRepo.Create(ctx, transfer); err != nil {
			s.logger.Error("failed to record stash transfer", zap.String("user_id", userID.String()), zap.Error(err))
			return nil, fmt.Errorf("transfer completed but failed to record: %w", err)
		}
	}

	return &entities.FundStashResult{
		TransferID: transferID,
		Amount:     amount,
		Status:     "completed",
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

	// Advisory locks are session-scoped: lock and unlock MUST run on the same
	// connection. Issued on the pool they land on different connections — the
	// unlock is a no-op and the lock leaks on whichever connection acquired it,
	// blocking the user's subsequent withdrawals until that connection recycles.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection for withdrawal lock: %w", err)
	}

	// Try to acquire with timeout
	deadline := time.Now().Add(10 * time.Second)
	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
			conn.Close() //nolint:errcheck // best-effort cleanup
			return nil, fmt.Errorf("advisory lock query failed: %w", err)
		}
		if acquired {
			unlock := func() {
				defer conn.Close() //nolint:errcheck // best-effort cleanup
				if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key); err != nil {
					s.logger.Error("failed to release advisory lock", zap.Int64("key", key), zap.Error(err))
				}
			}
			return unlock, nil
		}
		if time.Now().After(deadline) {
			conn.Close() //nolint:errcheck // best-effort cleanup
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

	// Step 3.1: Validate destination address is whitelisted (TEMPORARILY DISABLED)
	// TODO: Re-enable whitelist once testing is complete
	if false && s.addressWhitelist != nil {
		user, userErr := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		skipWhitelist := userErr == nil && user != nil && entities.EffectiveKYCTier(user.KYCTier, user.KYCStatus) == entities.KYCTierNonKYC
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

	// Step 3.3: Session anomaly check — block withdrawals if impossible travel detected
	if s.sessionAnomaly != nil {
		hasCritical, reason, err := s.sessionAnomaly.GetRecentCriticalAnomalies(ctx, req.UserID)
		if err != nil {
			s.logger.Warn("Session anomaly check failed, allowing withdrawal", zap.Error(err))
		} else if hasCritical {
			s.logger.Warn("Withdrawal blocked due to session anomaly",
				"user_id", req.UserID.String(), "reason", reason)
			return nil, fmt.Errorf("withdrawal temporarily blocked: unusual login activity detected. Please contact support if this was you")
		}
	}

	// Step 3.4: Enforce user's self-imposed daily spending commitment
	if s.commitment != nil {
		if err := s.commitment.CheckOutflow(ctx, req.UserID, req.Amount, string(req.Currency)); err != nil {
			s.logger.Warn("Withdrawal blocked by daily spending commitment", "user_id", req.UserID.String())
			return nil, entities.ErrCommitmentExceeded
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
	providerWalletType := "bridge"
	if req.CircleWalletID != "" {
		walletProviderID = req.CircleWalletID
		providerWalletType = "circle"
	}
	var sourceWalletAddressPtr *string
	if strings.TrimSpace(req.SourceWalletAddress) != "" {
		sourceWalletAddressPtr = &req.SourceWalletAddress
	}

	withdrawal := &entities.Withdrawal{
		ID:                  uuid.New(),
		UserID:              req.UserID,
		WithdrawalType:      entities.WithdrawalTypeCrypto,
		Currency:            req.Currency,
		Amount:              req.Amount,
		SourceAccount:       req.SourceAccount,
		SourceChain:         req.SourceChain,
		SourceWalletAddress: sourceWalletAddressPtr,
		ProviderWalletType:  providerWalletType,
		Emergency:           req.Emergency,
		BridgeWalletID:      &walletProviderID,
		DestinationType:     entities.DestinationTypeCryptoWallet,
		DestinationChain:    strings.ToUpper(req.DestinationChain),
		DestinationAddress:  &req.DestinationAddress,
		FeeAmount:           fee,
		FeeCurrency:         req.Currency,
		Category:            categoryPtr,
		Narration:           narrationPtr,
		Status:              entities.WithdrawalStatusInitiated,
		IdempotencyKey:      &idempotencyKey,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	if err := s.withdrawalRepo.Create(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create withdrawal", "error", err)
		return nil, fmt.Errorf("failed to create withdrawal: %w", err)
	}

	analytics.TrackEvent(ctx, req.UserID.String(), analytics.EventWithdrawalInitiated, map[string]any{
		"withdrawal_id":     withdrawal.ID.String(),
		"type":              "crypto",
		"amount":            req.Amount.InexactFloat64(),
		"currency":          string(req.Currency),
		"source_account":    string(req.SourceAccount),
		"destination_chain": req.DestinationChain,
		"fee_amount":        withdrawal.FeeAmount.InexactFloat64(),
	})

	// Re-check pending exposure after creating the record to protect against near-simultaneous requests.
	// Must re-fetch balance since the old balance is stale after creating the withdrawal record.
	if err := s.ensureWithdrawalExposure(ctx, withdrawal); err != nil {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, err
	}

	// Step 6.5: Compliance screening — submit to Didit for AML/sanctions monitoring.
	// If Didit returns IN_REVIEW, leave the withdrawal in compliance_review so the
	// transaction webhook can resume it after manual approval.
	if s.complianceScreener != nil {
		user, userErr := s.userRepo.GetUserEntityByID(ctx, req.UserID)
		skipCompliance := userErr == nil && user != nil && entities.EffectiveKYCTier(user.KYCTier, user.KYCStatus) == entities.KYCTierNonKYC
		if !skipCompliance {
			if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusComplianceReview); err != nil {
				_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "failed to hold for compliance review")
				return nil, fmt.Errorf("failed to update withdrawal status: %w", err)
			}
			withdrawal.Status = entities.WithdrawalStatusComplianceReview

			screenStatus, screenErr := s.complianceScreener.ScreenTransaction(ctx, req.UserID, idempotencyKey, "outbound", req.Amount, string(req.Currency), "")
			if screenErr != nil {
				s.logger.Error("Compliance screening unavailable, blocking withdrawal",
					"user_id", req.UserID.String(), "withdrawal_id", withdrawal.ID.String(), "error", screenErr)
				_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "compliance screening unavailable")
				return nil, domainerrors.NewDomainError(
					domainerrors.ErrServiceUnavailable,
					"COMPLIANCE_UNAVAILABLE",
					"Compliance screening is temporarily unavailable. Please try again.",
				).WithRetryable(true)
			}
			if screenStatus != "APPROVED" {
				s.logger.Warn("Withdrawal not approved by compliance screening",
					"user_id", req.UserID.String(), "withdrawal_id", withdrawal.ID.String(), "amount", req.Amount.String(), "status", screenStatus)
				if screenStatus == "DECLINED" {
					_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "compliance declined")
					return nil, domainerrors.NewDomainError(
						domainerrors.ErrConflict,
						"COMPLIANCE_DECLINED",
						"Withdrawal was declined by compliance screening.",
					).WithDetails(map[string]interface{}{"status": screenStatus})
				}
				return &entities.InitiateWithdrawalResponse{
					WithdrawalID: withdrawal.ID,
					Status:       withdrawal.Status,
					Message:      "Withdrawal is pending compliance review",
				}, nil
			}
		}
	}

	return s.submitApprovedCryptoWithdrawal(ctx, withdrawal, req)
}

func (s *WithdrawalService) ensureWithdrawalExposure(ctx context.Context, withdrawal *entities.Withdrawal) error {
	currentBalance, err := s.getSourceBalance(ctx, withdrawal.UserID, withdrawal.SourceAccount)
	if err != nil {
		return fmt.Errorf("failed to re-check balance: %w", err)
	}
	return s.ensurePendingExposureWithinBalance(ctx, withdrawal.UserID, currentBalance)
}

func (s *WithdrawalService) submitApprovedCryptoWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest) (*entities.InitiateWithdrawalResponse, error) {
	if withdrawal.Status == entities.WithdrawalStatusComplianceReview {
		if err := s.ensureWithdrawalExposure(ctx, withdrawal); err != nil {
			_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
			return nil, err
		}
	}

	// Claim the withdrawal before debiting so a webhook retry cannot double-post
	// the ledger entry. The external transfer still starts only after the debit.
	// For stash withdrawals, use 'pending' instead of 'processing' to indicate
	// that the Blend yield redemption hasn't completed yet — the funds are not
	// yet available to transfer.
	initialStatus := entities.WithdrawalStatusProcessing
	if req.SourceAccount == entities.WithdrawalSourceStashBalance {
		initialStatus = entities.WithdrawalStatusPending
	}
	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, initialStatus); err != nil {
		s.logger.Error("Failed to mark withdrawal processing", "error", err)
		return nil, fmt.Errorf("failed to update withdrawal status: %w", err)
	}
	withdrawal.Status = initialStatus

	// Reserve Blend redemption BEFORE the ledger moves so a crash after the
	// pending transaction always leaves a durable record for the reconciliation
	// worker — otherwise the ledger can run ahead of Blend custody with nothing
	// to retry. This mirrors the reserve-before-debit pattern in EmergencyStashToSpending.
	if req.SourceAccount == entities.WithdrawalSourceStashBalance {
		redeemAmount := withdrawal.Amount.Add(withdrawal.FeeAmount)
		if redeemAmount.GreaterThan(decimal.Zero) {
			redeemKey := "withdrawal-" + withdrawal.ID.String()
			if reserver, ok := s.stashYieldRedeemer.(RedemptionReserver); ok && reserver != nil {
				if _, rerr := reserver.EnsureRedemptionReserved(ctx, req.UserID, redeemAmount, redeemKey); rerr != nil {
					s.logger.Error("Failed to reserve Blend redemption before pending ledger",
						"error", rerr, "withdrawal_id", withdrawal.ID.String())
					_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "reserve redemption: "+rerr.Error())
					return nil, fmt.Errorf("failed to reserve yield redemption: %w", rerr)
				}
			}
		}
	}

	// Create a PENDING ledger transaction — entries are inserted for the audit
	// trail but account balances are NOT updated yet. The debit is committed
	// only after the on-chain transfer succeeds (see executeCryptoWithdrawalAsync).
	if err := s.createPendingWithdrawalLedgerEntry(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create pending ledger transaction", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "ledger pending tx failed: "+err.Error())
		return nil, fmt.Errorf("failed to create pending ledger transaction: %w", err)
	}

	// Execute transfer asynchronously.
	// Pending ledger transaction is created, redemption is reserved, withdrawal record exists — return
	// immediately and process in background.

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

// ResumeComplianceApprovedWithdrawal resumes a crypto withdrawal that was held
// by Didit transaction monitoring and later approved by webhook.
func (s *WithdrawalService) ResumeComplianceApprovedWithdrawal(ctx context.Context, referenceID string) error {
	referenceID = strings.TrimSpace(referenceID)
	if referenceID == "" {
		return fmt.Errorf("compliance webhook missing transaction reference")
	}

	withdrawal, err := s.withdrawalRepo.GetByIdempotencyKey(ctx, referenceID)
	if err != nil {
		return fmt.Errorf("load compliance-held withdrawal: %w", err)
	}
	if withdrawal == nil {
		s.logger.Warn("Compliance approval has no matching withdrawal", "reference_id", referenceID)
		return nil
	}

	unlock, err := s.acquireAdvisoryLock(ctx, withdrawal.UserID)
	if err != nil {
		return fmt.Errorf("failed to acquire distributed lock: %w", err)
	}
	defer unlock()

	lock := s.userWithdrawalLock(withdrawal.UserID)
	lock.Lock()
	defer lock.Unlock()

	withdrawal, err = s.withdrawalRepo.GetByIdempotencyKey(ctx, referenceID)
	if err != nil {
		return fmt.Errorf("reload compliance-held withdrawal: %w", err)
	}
	if withdrawal == nil {
		return nil
	}
	if withdrawal.Status != entities.WithdrawalStatusComplianceReview {
		s.logger.Info("Compliance approval ignored for non-review withdrawal",
			"withdrawal_id", withdrawal.ID.String(),
			"status", withdrawal.Status,
			"reference_id", referenceID)
		return nil
	}
	if withdrawal.WithdrawalType != entities.WithdrawalTypeCrypto {
		return fmt.Errorf("compliance approval resume only supports crypto withdrawals: %s", withdrawal.WithdrawalType)
	}
	if withdrawal.DestinationAddress == nil || strings.TrimSpace(*withdrawal.DestinationAddress) == "" {
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "missing destination address for compliance resume")
		return fmt.Errorf("withdrawal %s missing destination address", withdrawal.ID)
	}

	sourceChain := strings.TrimSpace(withdrawal.SourceChain)
	if sourceChain == "" {
		sourceChain = withdrawal.DestinationChain
	}
	sourceWalletAddress := ""
	if withdrawal.SourceWalletAddress != nil {
		sourceWalletAddress = *withdrawal.SourceWalletAddress
	}
	providerWalletID := ""
	if withdrawal.BridgeWalletID != nil {
		providerWalletID = *withdrawal.BridgeWalletID
	}
	req := &entities.InitiateCryptoWithdrawalRequest{
		UserID:              withdrawal.UserID,
		Amount:              withdrawal.Amount,
		Currency:            withdrawal.Currency,
		DestinationAddress:  *withdrawal.DestinationAddress,
		DestinationChain:    withdrawal.DestinationChain,
		SourceChain:         sourceChain,
		SourceAccount:       withdrawal.SourceAccount,
		SourceWalletAddress: sourceWalletAddress,
		Emergency:           withdrawal.Emergency,
		IdempotencyKey:      referenceID,
	}
	switch strings.ToLower(strings.TrimSpace(withdrawal.ProviderWalletType)) {
	case "circle":
		req.CircleWalletID = providerWalletID
	default:
		req.BridgeWalletID = providerWalletID
	}
	if withdrawal.Category != nil {
		req.Category = *withdrawal.Category
	}
	if withdrawal.Narration != nil {
		req.Narration = *withdrawal.Narration
	}

	if _, err := s.submitApprovedCryptoWithdrawal(ctx, withdrawal, req); err != nil {
		return fmt.Errorf("resume approved withdrawal: %w", err)
	}
	return nil
}

// executeCryptoWithdrawalAsync runs the Bridge transfer and post-processing in the background.
func (s *WithdrawalService) executeCryptoWithdrawalAsync(withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			s.logger.Error("async: crypto withdrawal goroutine panicked",
				"panic", r,
				"withdrawal_id", withdrawal.ID.String(),
				"user_id", withdrawal.UserID.String(),
				"stack", string(stack))
			// Use failWithdrawal (not MarkFailed) so the ledger is properly
			// handled: if Circle already confirmed, settle; if pending, fail it;
			// if committed, reverse it. MarkFailed alone would orphan the ledger.
			if failErr := s.failWithdrawal(context.Background(), withdrawal, fmt.Sprintf("internal panic: %v", r)); failErr != nil {
				s.logger.Error("async: failed to mark withdrawal as failed after panic",
					"error", failErr, "withdrawal_id", withdrawal.ID.String())
			}
			s.alertAdmin(AdminErrorPayload{
				UserID:    withdrawal.UserID.String(),
				Operation: "crypto_withdrawal",
				Error:     fmt.Errorf("panic: %v", r),
				PanicStack: stack,
				ExtraFields: map[string]string{
					"withdrawal_id": withdrawal.ID.String(),
					"amount":        withdrawal.Amount.String(),
					"currency":      string(withdrawal.Currency),
					"dest_chain":    req.DestinationChain,
				},
			})
		}
	}()

	// 8 minutes: yield redemption + transfer + the completion pollers below
	// (ChainRails polls up to 5 minutes on its own) must all fit — a shorter
	// parent deadline silently truncated polling and left rows in processing.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Retry loop for yield redemption: when Blend sessions fail/cancel the worker
	// resets the redemption for retry, but the async goroutine must survive to
	// resume the withdrawal once the redemption succeeds. Without this loop the
	// goroutine would exit on ErrRedemptionRetrying, leaving the withdrawal stuck
	// in 'pending' with nobody to drive it to completion.
	const maxRedemptionRetries = 15
	for i := 0; i < maxRedemptionRetries; i++ {
		err := s.prepareStashYieldForCryptoWithdrawal(ctx, withdrawal, req)
		if err == nil {
			break // yield redeemed, proceed to crypto transfer
		}
		if errors.Is(err, blendpkg.ErrRedemptionRetrying) {
			s.logger.Warn("async: Blend redemption retrying — waiting before retry",
				"attempt", i+1, "error", err, "withdrawal_id", withdrawal.ID.String())
			// Back off 30s between retries, respecting the parent deadline.
			select {
			case <-ctx.Done():
				s.logger.Error("async: redemption retry loop timed out", "withdrawal_id", withdrawal.ID.String())
				if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
					s.logger.Error("async: failed to mark pending ledger as failed", "error", failErr, "withdrawal_id", withdrawal.ID.String())
				}
				_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "yield redemption timed out after retries")
				return
			case <-time.After(30 * time.Second):
			}
			continue
		}
		// Non-retrying error — the redemption is dead, fail the withdrawal.
		s.logger.Error("async: stash yield redemption failed — marking pending ledger failed",
			"error", err, "withdrawal_id", withdrawal.ID.String())
		if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
			s.logger.Error("async: failed to mark pending ledger as failed",
				"error", failErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Your stash withdrawal could not be redeemed from yield. Your funds are still in your stash balance.")
		}
		s.alertAdmin(AdminErrorPayload{
			UserID:    withdrawal.UserID.String(),
			Operation: "crypto_withdrawal_stash_yield",
			Error:     err,
			ExtraFields: map[string]string{
				"withdrawal_id": withdrawal.ID.String(),
				"amount":        withdrawal.Amount.String(),
				"currency":      string(withdrawal.Currency),
				"source":        "stash_balance",
			},
		})
		return
	}

	// Blend redemption succeeded — transition from 'pending' to 'processing'
	// so the user sees the withdrawal is now actively moving funds.
	if withdrawal.Status == entities.WithdrawalStatusPending {
		if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
			s.logger.Error("async: failed to transition stash withdrawal from pending to processing",
				"error", err, "withdrawal_id", withdrawal.ID.String())
		} else {
			withdrawal.Status = entities.WithdrawalStatusProcessing
		}
	}

	// Re-check source balance before executing the transfer. The balance was
	// checked during InitiateCryptoWithdrawal, but by now the yield redemption
	// retry loop could have taken minutes — another concurrent withdrawal or
	// deduction could have consumed the funds in the meantime.
	{
		sourceAcct := entities.AccountTypeSpendingBalance
		if req.SourceAccount == entities.WithdrawalSourceStashBalance {
			sourceAcct = entities.AccountTypeStashBalance
		}
		curBal, balErr := s.ledgerService.GetAccountBalance(ctx, req.UserID, sourceAcct)
		if balErr != nil {
			s.logger.Error("async: failed to re-check balance before crypto transfer — proceeding anyway",
				"error", balErr, "withdrawal_id", withdrawal.ID.String())
		} else if curBal.LessThan(req.Amount) {
			s.logger.Error("async: insufficient balance at execution time — failing withdrawal",
				"current_balance", curBal.String(),
				"required", req.Amount.String(),
				"withdrawal_id", withdrawal.ID.String())
			if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
				s.logger.Error("async: failed to mark pending ledger as failed",
					"error", failErr, "withdrawal_id", withdrawal.ID.String())
			}
			_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "insufficient balance at execution time")
			if s.notificationService != nil {
				_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Insufficient balance when processing your withdrawal. Your funds are still available.")
			}
			return
		}
	}

	transferResult, err := s.executeCryptoTransfer(ctx, withdrawal, req.DestinationAddress, req.DestinationChain, req.SourceChain, req.SourceWalletAddress, req.CircleWalletID)
	if err != nil {
		s.logger.Error("async: crypto transfer failed — marking pending ledger failed",
			"error", err, "withdrawal_id", withdrawal.ID.String())
		if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
			s.logger.Error("async: failed to mark pending ledger as failed",
				"error", failErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds are still in your balance.")
		}
		s.alertAdmin(AdminErrorPayload{
			UserID:    withdrawal.UserID.String(),
			Operation: "crypto_withdrawal_transfer",
			Error:     err,
			ExtraFields: map[string]string{
				"withdrawal_id":    withdrawal.ID.String(),
				"amount":           withdrawal.Amount.String(),
				"currency":         string(withdrawal.Currency),
				"dest_chain":       req.DestinationChain,
				"dest_address":     req.DestinationAddress,
				"provider_wallet":  withdrawal.ProviderWalletType,
			},
		})
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
				analytics.G().TrackRevenue(ctx, req.UserID.String(), eFee.InexactFloat64(), map[string]any{
					"type": "emergency_withdrawal_fee", "withdrawal_id": withdrawal.ID.String(),
				})
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
		// Commit the pending ledger debit NOW that transfer succeeded.
		if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
			s.logger.Error("async: failed to commit pending ledger — withdrawal completed but ledger inconsistent",
				"error", commitErr, "withdrawal_id", withdrawal.ID.String())
		}
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
		analytics.TrackEvent(ctx, req.UserID.String(), analytics.EventNetInflowRecorded, map[string]any{
			"amount":    req.Amount.InexactFloat64(),
			"direction": "outflow",
			"type":      "crypto_withdrawal",
		})
		analytics.G().TrackRevenue(ctx, req.UserID.String(), withdrawal.FeeAmount.InexactFloat64(), map[string]any{
			"type": "withdrawal_fee",
		})
		s.logger.Info("async: crypto withdrawal completed",
			"withdrawal_id", withdrawal.ID.String(), "tx_hash", transferResult.TxHash)
	} else if isFinalFailure {
		s.logger.Error("async: crypto transfer returned failure state — marking pending ledger failed",
			"withdrawal_id", withdrawal.ID.String(), "state", state)
		if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
			s.logger.Error("async: failed to mark pending ledger as failed",
				"error", failErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "provider returned: "+state)
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds are still in your balance.")
		}
	} else {
		s.logger.Info("async: crypto withdrawal processing, will poll for completion",
			"withdrawal_id", withdrawal.ID.String(), "state", transferResult.State)
		if strings.HasPrefix(transferResult.TransferID, "cr:") {
			s.pollChainRailsCompletion(ctx, withdrawal, req, transferResult.IntentAddress)
		} else {
			s.pollCircleWithdrawalCompletion(ctx, withdrawal, req, transferResult.TransferID)
		}
	}
}

func (s *WithdrawalService) pollCircleWithdrawalCompletion(ctx context.Context, withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest, transferID string) {
	if s.circleTransfer == nil || transferID == "" {
		return
	}
	// Poll every 3 seconds for up to 2 minutes
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	deadline := time.After(2 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			s.logger.Warn("async: Circle withdrawal poll timeout — will rely on webhook",
				"withdrawal_id", withdrawal.ID.String(), "transfer_id", transferID)
			return
		case <-ticker.C:
			tx, err := s.circleTransfer.GetTransaction(ctx, transferID)
			if err != nil {
				continue
			}
			state := strings.ToUpper(string(tx.State))
			if state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" {
				if tx.TxHash != "" {
					_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, tx.TxHash)
				}
				if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
					s.logger.Error("async: Circle confirmed but failed to commit ledger",
						"error", commitErr, "withdrawal_id", withdrawal.ID.String())
				}
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
				s.logger.Info("async: Circle withdrawal confirmed via polling",
					"withdrawal_id", withdrawal.ID.String(), "tx_hash", tx.TxHash)
				return
			}
			if state == "FAILED" || state == "DENIED" || state == "CANCELLED" || state == "CANCELED" {
				if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
					s.logger.Error("async: failed to mark pending ledger as failed after Circle failure",
						"error", failErr, "withdrawal_id", withdrawal.ID.String())
				}
				_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "circle transfer "+strings.ToLower(state))
				if s.notificationService != nil {
					_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds are still in your balance.")
				}
				return
			}
		}
	}
}

// pollChainRailsCompletion polls ChainRails for intent status until terminal.
func (s *WithdrawalService) pollChainRailsCompletion(ctx context.Context, withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest, intentAddress string) {
	if s.chainRailsAdapter == nil || intentAddress == "" {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	deadline := time.After(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			s.logger.Warn("async: ChainRails poll timeout — relying on webhook",
				"withdrawal_id", withdrawal.ID.String(), "intent_address", intentAddress)
			return
		case <-ticker.C:
			status, err := s.chainRailsAdapter.GetIntentStatus(ctx, intentAddress)
			if err != nil {
				continue
			}
			state := strings.ToUpper(status.Status)
			if state == "COMPLETED" || state == "COMPLETE" || state == "SUCCESS" {
				if status.TxHash != "" {
					_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, status.TxHash)
				}
				if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
					s.logger.Error("async: ChainRails confirmed but failed to commit ledger",
						"error", commitErr, "withdrawal_id", withdrawal.ID.String())
				}
				if s.limitsService != nil {
					_ = s.limitsService.RecordWithdrawal(ctx, req.UserID, req.Amount)
				}
				if s.tieredLimits != nil {
					_ = s.tieredLimits.RecordWithdrawal(ctx, req.UserID, req.Amount)
				}
				if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
					s.logger.Error("async: ChainRails withdrawal confirmed but failed to settle in database",
						"withdrawal_id", withdrawal.ID.String(), "tx_hash", status.TxHash, "error", err)
				}
				if s.notificationService != nil {
					_ = s.notificationService.NotifyWithdrawalCompleted(ctx, req.UserID, req.Amount, req.DestinationAddress)
				}
				s.logger.Info("async: ChainRails withdrawal confirmed via polling",
					"withdrawal_id", withdrawal.ID.String(), "tx_hash", status.TxHash)
				return
			}
			if state == "REFUNDED" || state == "FAILED" || state == "EXPIRED" || state == "CANCELLED" {
				if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
					s.logger.Error("async: failed to mark pending ledger as failed after ChainRails failure",
						"error", failErr, "withdrawal_id", withdrawal.ID.String())
				}
				_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "chainrails intent "+strings.ToLower(state))
				if s.notificationService != nil {
					_ = s.notificationService.NotifyWithdrawalFailed(ctx, req.UserID, req.Amount, "Transfer failed. Your funds are still in your balance.")
				}
				s.logger.Warn("async: ChainRails withdrawal failed via polling",
					"withdrawal_id", withdrawal.ID.String(), "state", state)
				return
			}
		}
	}
}

func (s *WithdrawalService) prepareStashYieldForCryptoWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal, req *entities.InitiateCryptoWithdrawalRequest) error {
	if req.SourceAccount != entities.WithdrawalSourceStashBalance {
		return nil
	}
	if s.stashYieldRedeemer == nil {
		return fmt.Errorf("stash withdrawal requires yield redemption but no redeemer is configured")
	}
	redeemAmount := withdrawal.Amount.Add(withdrawal.FeeAmount)
	if !redeemAmount.GreaterThan(decimal.Zero) {
		return nil
	}
	idemKey := "withdrawal-" + withdrawal.ID.String()
	// Reservation was already done in submitApprovedCryptoWithdrawal (before the
	// ledger debit), so only the actual redemption happens here. RedeemStashYield
	// is idempotent — if the reservation already drove it to completion, this is a
	// fast no-op. Use the no-sweep variant: funds are leaving the platform so the
	// crypto transfer will spend the USDC directly from the Base EOA.
	if rt, ok := s.stashYieldRedeemer.(StashYieldRedeemerForTransfer); ok {
		return rt.RedeemStashYieldForTransfer(ctx, withdrawal.UserID, redeemAmount, idemKey)
	}
	return s.stashYieldRedeemer.RedeemStashYield(ctx, withdrawal.UserID, redeemAmount, idemKey)
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

	// NGN withdrawals are handled exclusively by the Paj Cash endpoint.
	// Returning a clear error here prevents silent failures for NGN users.
	if req.Currency == entities.WithdrawalCurrencyNGN {
		return nil, fmt.Errorf("NGN withdrawals must use the /v1/funding/paj/offramp endpoint")
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

	// Step 4.1: Session anomaly check — block withdrawals if impossible travel detected
	if s.sessionAnomaly != nil {
		hasCritical, reason, err := s.sessionAnomaly.GetRecentCriticalAnomalies(ctx, req.UserID)
		if err != nil {
			s.logger.Warn("Session anomaly check failed, allowing fiat withdrawal", "error", err)
		} else if hasCritical {
			s.logger.Warn("Fiat withdrawal blocked due to session anomaly",
				"user_id", req.UserID.String(), "reason", reason)
			return nil, fmt.Errorf("withdrawal temporarily blocked: unusual login activity detected. Please contact support if this was you")
		}
	}

	// Step 4.2: Enforce user's self-imposed daily spending commitment
	if s.commitment != nil {
		if err := s.commitment.CheckOutflow(ctx, req.UserID, req.Amount, string(req.Currency)); err != nil {
			s.logger.Warn("Fiat withdrawal blocked by daily spending commitment", "user_id", req.UserID.String())
			return nil, entities.ErrCommitmentExceeded
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

	analytics.TrackEvent(ctx, req.UserID.String(), analytics.EventWithdrawalInitiated, map[string]any{
		"withdrawal_id":  withdrawal.ID.String(),
		"type":           "fiat",
		"amount":         req.Amount.InexactFloat64(),
		"currency":       string(req.Currency),
		"source_account": string(req.SourceAccount),
		"fee_amount":     fee.InexactFloat64(),
	})

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

	if err := s.withdrawalRepo.UpdateStatus(ctx, withdrawal.ID, entities.WithdrawalStatusProcessing); err != nil {
		s.logger.Error("Failed to claim fiat withdrawal for processing", "error", err, "withdrawal_id", withdrawal.ID.String())
		return nil, fmt.Errorf("failed to update status: %w", err)
	}
	withdrawal.Status = entities.WithdrawalStatusProcessing

	// Create a PENDING ledger transaction — entries are inserted for the audit
	// trail but account balances are NOT updated yet. The debit is committed
	// only after the Bridge offramp transfer succeeds.
	if err := s.createPendingWithdrawalLedgerEntry(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to create pending ledger transaction for fiat withdrawal", "error", err, "withdrawal_id", withdrawal.ID.String())
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, "ledger pending tx failed: "+err.Error())
		return nil, fmt.Errorf("failed to create pending ledger transaction: %w", err)
	}

	// Step 9: Execute Bridge offramp transfer
	transferID, err := s.executeFiatTransfer(ctx, withdrawal, bankAccount)
	if err != nil {
		s.logger.Error("Failed to execute fiat transfer", "error", err)
		// Mark pending ledger as failed — balances were never updated so no reversal needed.
		if failErr := s.failPendingWithdrawalLedgerEntry(ctx, withdrawal); failErr != nil {
			s.logger.Error("Failed to mark pending ledger as failed after fiat transfer failure",
				"error", failErr, "withdrawal_id", withdrawal.ID.String())
		}
		_ = s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, err.Error())
		return nil, fmt.Errorf("failed to execute transfer: %w", err)
	}

	// Commit the pending ledger debit NOW that fiat transfer is initiated.
	if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
		s.logger.Error("Failed to commit pending ledger after fiat transfer",
			"error", commitErr, "withdrawal_id", withdrawal.ID.String())
		// Don't fail the withdrawal — transfer was initiated successfully.
	}

	// Update bridge transfer ID
	var updateTransferErr error
	for attempt := 1; attempt <= 3; attempt++ {
		updateTransferErr = s.withdrawalRepo.UpdateBridgeTransfer(ctx, withdrawal.ID, transferID)
		if updateTransferErr == nil {
			withdrawal.ProviderTransferID = &transferID
			break
		}
		s.logger.Error("Failed to update bridge transfer ID",
			"error", updateTransferErr,
			"withdrawal_id", withdrawal.ID.String(),
			"transfer_id", transferID,
			"attempt", attempt)
		time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
	}
	if updateTransferErr != nil {
		s.logger.Error("CRITICAL: fiat transfer accepted but provider transfer ID was not persisted",
			"error", updateTransferErr,
			"withdrawal_id", withdrawal.ID.String(),
			"transfer_id", transferID)
	}

	if emergencyFee.IsPositive() && s.emergencyLedger != nil {
		idemKey := fmt.Sprintf("emergency-fee-%s", withdrawal.ID.String())
		if feeErr := s.emergencyLedger.EmergencyTransferStashToSpending(ctx, req.UserID, decimal.Zero, emergencyFee, idemKey); feeErr != nil {
			s.logger.Error("CRITICAL: failed to debit emergency fee after fiat transfer accepted",
				"error", feeErr, "withdrawal_id", withdrawal.ID.String(), "fee", emergencyFee.String())
		}
	}
	if req.Emergency && sl != nil {
		_ = sl.MarkEmergencyWithdrawn(ctx, req.UserID)
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
		case entities.WithdrawalCurrencyGBP:
			sanitizedReqSortCode := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""), "-", "")
			if len(sanitizedReqSortCode) != 6 {
				return nil, fmt.Errorf("invalid UK sort code: must be exactly 6 digits, got %d", len(sanitizedReqSortCode))
			}
			routingMatches := acc.RoutingNumber != nil && *acc.RoutingNumber == sanitizedReqSortCode
			accountMatches := acc.AccountNumberLast4 == accountLast4 && accountLast4 != ""
			if routingMatches && accountMatches {
				s.logger.Info("Found existing GBP bank account", "bank_account_id", acc.ID.String())
				return acc, nil
			}
		}
	}

	bankCurrency := entities.BankAccountCurrencyUSD
	if req.Currency == entities.WithdrawalCurrencyEUR {
		bankCurrency = entities.BankAccountCurrencyEUR
	} else if req.Currency == entities.WithdrawalCurrencyGBP {
		bankCurrency = entities.BankAccountCurrencyGBP
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
	if req.Currency == entities.WithdrawalCurrencyGBP {
		sortCode := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""), "-", "")
		if len(sortCode) != 6 {
			return nil, fmt.Errorf("invalid UK sort code: must be exactly 6 digits, got %d", len(sortCode))
		}
		sortCodeLast4 := sortCode[len(sortCode)-4:]
		bankAccount.RoutingNumberLast4 = &sortCodeLast4
		bankAccount.RoutingNumber = &sortCode
		bankAccount.AccountNumberLast4 = accountLast4
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
		case entities.WithdrawalCurrencyGBP:
			sortCode := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(req.RoutingNumber), " ", ""), "-", "")
			recipientReq["sort_code"] = sortCode
			recipientReq["account_number"] = strings.TrimSpace(req.AccountNumber)
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

	if withdrawal.Status == entities.WithdrawalStatusInitiated || withdrawal.Status == entities.WithdrawalStatusComplianceReview {
		if err := s.withdrawalRepo.MarkCancelled(ctx, withdrawalID); err != nil {
			return fmt.Errorf("failed to cancel withdrawal: %w", err)
		}
		if s.notificationService != nil {
			_ = s.notificationService.NotifyWithdrawalFailed(ctx, userID, withdrawal.Amount, "Cancelled by user")
		}
		return nil
	}

	if withdrawal.ProviderTransferID != nil && strings.TrimSpace(*withdrawal.ProviderTransferID) != "" {
		transferID := *withdrawal.ProviderTransferID
		if strings.HasPrefix(transferID, "cr:") {
			s.logger.Warn("ChainRails withdrawal cancellation rejected after provider transfer started",
				"transfer_id", transferID)
			return fmt.Errorf("cannot cancel cross-chain withdrawal after provider transfer has started")
		}
		if s.bridgeAdapter == nil {
			return fmt.Errorf("provider cancellation unavailable")
		}
		if err := s.bridgeAdapter.CancelTransfer(ctx, transferID); err != nil {
			return fmt.Errorf("provider cancellation failed: %w", err)
		}
		s.logger.Info("Bridge transfer cancelled", "transfer_id", transferID)
	} else {
		switch withdrawal.Status {
		case entities.WithdrawalStatusProcessing, entities.WithdrawalStatusPending:
			s.logger.Info("Cancelling withdrawal before provider transfer was recorded",
				"withdrawal_id", withdrawal.ID.String(), "status", withdrawal.Status)
		default:
			return fmt.Errorf("cannot cancel withdrawal in status: %s", withdrawal.Status)
		}
	}

	if err := s.handleLedgerCancellation(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to handle ledger cancellation: %w", err)
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
		} else if refreshed, refreshErr := s.withdrawalRepo.GetByID(ctx, withdrawalID); refreshErr == nil && refreshed != nil {
			withdrawal = refreshed
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
		"withdrawal_id":    withdrawal.ID.String(),
		"withdrawal_type":  string(withdrawal.WithdrawalType),
		"source_account":   string(withdrawal.SourceAccount),
		"principal_amount": withdrawal.Amount.String(),
		"fee_amount":       withdrawal.FeeAmount.String(),
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
		"fee_amount":      withdrawal.FeeAmount.String(),
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

// withdrawalLedgerIdempotencyKey returns the well-known idempotency key for a
// withdrawal's pending ledger transaction. Both CreatePendingTransaction and
// CommitPendingTransaction/FailPendingTransaction use this key so they can
// resolve the same ledger row without passing IDs across goroutines.
func withdrawalLedgerIdempotencyKey(withdrawalID uuid.UUID) string {
	return "withdrawal-ledger-" + withdrawalID.String()
}

// createPendingWithdrawalLedgerEntry creates a ledger transaction in pending
// status without touching any account balances. The balances are committed
// later by commitPendingWithdrawalLedgerEntry once the transfer succeeds.
func (s *WithdrawalService) createPendingWithdrawalLedgerEntry(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return fmt.Errorf("ledger service not configured")
	}

	accountType, err := mapWithdrawalSourceToAccountType(withdrawal.SourceAccount)
	if err != nil {
		return err
	}

	metadata := map[string]interface{}{
		"withdrawal_id":    withdrawal.ID.String(),
		"withdrawal_type":  string(withdrawal.WithdrawalType),
		"source_account":   string(withdrawal.SourceAccount),
		"principal_amount": withdrawal.Amount.String(),
		"fee_amount":       withdrawal.FeeAmount.String(),
	}
	if withdrawal.DestinationAddress != nil {
		metadata["destination_address"] = *withdrawal.DestinationAddress
	}
	if withdrawal.ProviderTransferID != nil {
		metadata["provider_transfer_id"] = *withdrawal.ProviderTransferID
	}

	return s.ledgerService.CreatePendingTransaction(
		ctx,
		withdrawal.UserID,
		accountType,
		entities.TransactionTypeWithdrawal,
		withdrawal.Amount.Add(withdrawal.FeeAmount),
		withdrawalLedgerIdempotencyKey(withdrawal.ID),
		metadata,
	)
}

// commitPendingWithdrawalLedgerEntry promotes the pending ledger transaction
// to completed, locking accounts and updating balances atomically.
func (s *WithdrawalService) commitPendingWithdrawalLedgerEntry(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return fmt.Errorf("ledger service not configured")
	}
	return s.ledgerService.CommitPendingTransaction(ctx, withdrawalLedgerIdempotencyKey(withdrawal.ID))
}

// failPendingWithdrawalLedgerEntry marks the pending ledger transaction as
// failed. Since balances were never updated, no reversal entries are needed.
func (s *WithdrawalService) failPendingWithdrawalLedgerEntry(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return nil // no-op if ledger not configured
	}
	return s.ledgerService.FailPendingTransaction(ctx, withdrawalLedgerIdempotencyKey(withdrawal.ID))
}

// handleLedgerCancellation checks the ledger transaction status and either
// fails the pending tx (no balance impact) or reverses a committed tx
// (compensating entries). This is used by cancel/refund paths where the
// ledger could be in either state.
func (s *WithdrawalService) handleLedgerCancellation(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if s.ledgerService == nil {
		return nil
	}
	status, err := s.ledgerService.GetLedgerTransactionStatus(ctx, withdrawalLedgerIdempotencyKey(withdrawal.ID))
	if err != nil {
		return fmt.Errorf("get ledger status: %w", err)
	}
	switch status {
	case entities.TransactionStatusPending:
		return s.failPendingWithdrawalLedgerEntry(ctx, withdrawal)
	case entities.TransactionStatusCompleted:
		return s.reverseWithdrawalLedgerEntry(ctx, withdrawal)
	default:
		return nil // already failed/reversed — no-op
	}
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

// isSameChainFamily returns true if source and destination are the same network
// (e.g. both Solana, or both the same EVM chain).
func isSameChainFamily(source, dest string) bool {
	normalize := func(c string) string {
		switch strings.ToLower(c) {
		case "solana", "sol", "sol-devnet":
			return "solana"
		case "ethereum", "eth":
			return "ethereum"
		case "base", "base-sepolia":
			return "base"
		case "polygon", "matic", "matic-amoy":
			return "polygon"
		case "arbitrum", "arb":
			return "arbitrum"
		case "optimism", "op":
			return "optimism"
		case "avalanche", "avax":
			return "avalanche"
		default:
			return strings.ToLower(c)
		}
	}
	return normalize(source) == normalize(dest)
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
	// Unified Solana Settlement: all Circle withdrawals source from Solana.
	if s.circleTransfer != nil && circleWalletID != "" {
		sourceChain = "SOL"
		if isSameChainFamily(sourceChain, destinationChain) {
			return s.executeCircleTransfer(ctx, withdrawal, destinationAddress, destinationChain)
		}
		if s.chainRailsAdapter == nil {
			return nil, fmt.Errorf("circle wallet configured without chainrails — cannot route cross-chain withdrawal")
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
		// The intent address rides along as a fourth segment so recovery can
		// poll ChainRails for rows whose webhook never arrived. Webhook
		// reconciliation matches on the "cr:{intentID}:" prefix, unaffected.
		TransferID:    fmt.Sprintf("cr:%d:%s:%s", intent.ID, tx.ID, intent.IntentAddress),
		State:         intent.IntentStatus,
		IntentAddress: intent.IntentAddress,
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
	if withdrawal.FeeAmount.IsZero() {
		return nil, fmt.Errorf("withdrawal fee amount is required for ChainRails transfer")
	}
	transfer, err := s.bridgeCryptoAdapter.TransferFunds(ctx, &bridgepkg.CreateTransferRequest{
		OnBehalfOf:   withdrawal.UserID.String(),
		Amount:       withdrawal.Amount.Add(withdrawal.FeeAmount).StringFixed(2),
		DeveloperFee: withdrawal.FeeAmount.StringFixed(2),
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

	// Store the ChainRails intent ID as the provider reference for webhook
	// reconciliation, with the intent address as a fourth segment for polling.
	return &CryptoTransferResult{
		TransferID:    fmt.Sprintf("cr:%d:%s:%s", intent.ID, transfer.ID, intent.IntentAddress),
		State:         intent.IntentStatus,
		IntentAddress: intent.IntentAddress,
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

	// Commit the pending ledger debit if the withdrawal has one (stash/crypto).
	if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
		s.logger.Error("ChainRails intent settled but failed to commit ledger",
			"error", commitErr, "withdrawal_id", withdrawal.ID.String())
	}

	if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to settle withdrawal completion: %w", err)
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

	// Handle ledger: fail pending tx or reverse committed tx depending on status.
	if err := s.handleLedgerCancellation(ctx, withdrawal); err != nil {
		s.logger.Error("Failed to handle ledger for ChainRails refund",
			"withdrawal_id", withdrawal.ID, "error", err)
		return fmt.Errorf("failed to handle ledger cancellation: %w", err)
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
		"recipient_id":  *bankAccount.BridgeRecipientID,
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

	// Ensure the pending ledger entry is committed before marking the withdrawal
	// completed. The async goroutine normally commits it, but if polling timed
	// out the webhook/settlement path reaches here with the ledger still pending.
	// CommitPendingTransaction is idempotent — safe to call even if already committed.
	if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
		s.logger.Error("settleCompletedCryptoWithdrawal: failed to commit pending ledger entry",
			"error", commitErr, "withdrawal_id", withdrawal.ID.String())
	}

	now := time.Now()
	if err := s.withdrawalRepo.MarkCompleted(ctx, withdrawal.ID); err != nil {
		return fmt.Errorf("failed to complete withdrawal: %w", err)
	}
	if s.commitment != nil {
		_ = s.commitment.RecordOutflow(ctx, withdrawal.UserID, withdrawal.Amount, string(withdrawal.Currency))
	}
	// #region agent log
	writeWithdrawalDebugLog("withdrawal/service.go:settleCompletedCryptoWithdrawal", "withdrawal completed; fee awaits revenue sweep", "H3", map[string]interface{}{
		"withdrawal_id": withdrawal.ID.String(), "user_id": withdrawal.UserID.String(),
		"fee_amount": withdrawal.FeeAmount.String(), "provider": withdrawal.ProviderWalletType,
		"fee_swept_immediate": false,
	})
	// #endregion
	withdrawal.Status = entities.WithdrawalStatusCompleted
	withdrawal.CompletedAt = &now
	withdrawal.UpdatedAt = now

	if metrics.Business != nil {
		metrics.Business.WithdrawalsCompleted.WithLabelValues("crypto").Inc()
		metrics.Business.WithdrawalAmount.WithLabelValues("crypto").Observe(withdrawal.Amount.InexactFloat64())
	}

	analytics.TrackEvent(context.Background(), withdrawal.UserID.String(), analytics.EventWithdrawalCompleted, map[string]any{
		"withdrawal_id": withdrawal.ID.String(),
		"type":          "crypto",
		"amount":        withdrawal.Amount.InexactFloat64(),
		"currency":      string(withdrawal.Currency),
		"fee_amount":    withdrawal.FeeAmount.InexactFloat64(),
	})
	analytics.G().Increment(context.Background(), withdrawal.UserID.String(), map[string]int{
		analytics.PropWithdrawalCount: 1,
	})

	return nil
}

// forceCompleteCryptoWithdrawal transitions a terminal non-completed withdrawal
// (e.g. cancelled/failed) to completed when the on-chain provider confirms the
// transfer actually succeeded. This bypasses the regular MarkCompleted SQL guard
// which blocks transitions from cancelled/failed states.
func (s *WithdrawalService) forceCompleteCryptoWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal) error {
	if withdrawal == nil {
		return nil
	}

	now := time.Now()
	if err := s.withdrawalRepo.ForceComplete(ctx, withdrawal.ID, now); err != nil {
		return fmt.Errorf("failed to force complete withdrawal: %w", err)
	}
	if withdrawal.TxHash != nil {
		_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, *withdrawal.TxHash)
	}
	withdrawal.Status = entities.WithdrawalStatusCompleted
	withdrawal.CompletedAt = &now
	withdrawal.UpdatedAt = now

	if s.commitment != nil {
		_ = s.commitment.RecordOutflow(ctx, withdrawal.UserID, withdrawal.Amount, string(withdrawal.Currency))
	}
	if s.limitsService != nil {
		_ = s.limitsService.RecordWithdrawal(ctx, withdrawal.UserID, withdrawal.Amount)
	}
	if s.tieredLimits != nil {
		_ = s.tieredLimits.RecordWithdrawal(ctx, withdrawal.UserID, withdrawal.Amount)
	}
	if metrics.Business != nil {
		metrics.Business.WithdrawalsCompleted.WithLabelValues("crypto").Inc()
		metrics.Business.WithdrawalAmount.WithLabelValues("crypto").Observe(withdrawal.Amount.InexactFloat64())
	}
	analytics.TrackEvent(context.Background(), withdrawal.UserID.String(), analytics.EventWithdrawalCompleted, map[string]any{
		"withdrawal_id": withdrawal.ID.String(),
		"type":          "crypto",
		"amount":        withdrawal.Amount.InexactFloat64(),
		"currency":      string(withdrawal.Currency),
		"fee_amount":    withdrawal.FeeAmount.InexactFloat64(),
		"forced":        true,
	})
	analytics.G().Increment(context.Background(), withdrawal.UserID.String(), map[string]int{
		analytics.PropWithdrawalCount: 1,
	})
	s.logger.Warn("forceCompleteCryptoWithdrawal: withdrawal forcibly completed from terminal state",
		"withdrawal_id", withdrawal.ID.String())
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

	if s.limitsService != nil {
		if err := s.limitsService.RecordWithdrawal(ctx, withdrawal.UserID, withdrawal.Amount); err != nil {
			s.logger.Error("Failed to record fiat withdrawal against limits", "error", err, "withdrawal_id", withdrawal.ID.String())
		}
	}
	if s.commitment != nil {
		_ = s.commitment.RecordOutflow(ctx, withdrawal.UserID, withdrawal.Amount, string(withdrawal.Currency))
	}

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

	analytics.TrackEvent(ctx, withdrawal.UserID.String(), analytics.EventWithdrawalCompleted, map[string]any{
		"withdrawal_id": withdrawal.ID.String(),
		"type":          "fiat",
		"amount":        withdrawal.Amount.InexactFloat64(),
		"currency":      string(withdrawal.Currency),
		"fee_amount":    withdrawal.FeeAmount.InexactFloat64(),
	})
	analytics.G().Increment(ctx, withdrawal.UserID.String(), map[string]int{
		analytics.PropWithdrawalCount: 1,
	})

	return nil
}

func (s *WithdrawalService) failWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal, reason string) error {
	if withdrawal == nil {
		return nil
	}
	if withdrawal.Status.IsTerminal() {
		return nil
	}

	// Guard: verify the on-chain transfer hasn't actually succeeded before reversing.
	// This prevents a race where a webhook marks the transfer failed while the tx
	// already landed on-chain, which would double-credit the sender.
	if s.circleTransfer != nil && withdrawal.ProviderTransferID != nil && strings.TrimSpace(*withdrawal.ProviderTransferID) != "" {
		tx, err := s.circleTransfer.GetTransaction(ctx, strings.TrimSpace(*withdrawal.ProviderTransferID))
		if err == nil && tx != nil {
			state := strings.ToUpper(string(tx.State))
			if state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" {
				s.logger.Warn("failWithdrawal: on-chain transfer confirmed — settling instead of reversing",
					"withdrawal_id", withdrawal.ID.String(),
					"provider_transfer_id", *withdrawal.ProviderTransferID,
					"tx_hash", tx.TxHash,
					"reason_ignored", reason)
				if tx.TxHash != "" {
					_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, tx.TxHash)
				}
				// Commit the pending ledger debit since funds actually left.
				if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
					s.logger.Error("failWithdrawal: on-chain confirmed but failed to commit ledger",
						"error", commitErr, "withdrawal_id", withdrawal.ID.String())
				}
				return s.settleCompletedCryptoWithdrawal(ctx, withdrawal)
			}
		}
	}

	if err := s.handleLedgerCancellation(ctx, withdrawal); err != nil {
		return fmt.Errorf("failed to handle ledger cancellation: %w", err)
	}
	if err := s.withdrawalRepo.MarkFailed(ctx, withdrawal.ID, reason); err != nil {
		return fmt.Errorf("failed to mark withdrawal failed: %w", err)
	}

	withdrawal.Status = entities.WithdrawalStatusFailed
	withdrawal.ErrorMessage = &reason
	if s.notificationService != nil {
		_ = s.notificationService.NotifyWithdrawalFailed(ctx, withdrawal.UserID, withdrawal.Amount, reason)
	}

	analytics.TrackEvent(ctx, withdrawal.UserID.String(), analytics.EventWithdrawalFailed, map[string]any{
		"withdrawal_id":  withdrawal.ID.String(),
		"type":           string(withdrawal.WithdrawalType),
		"amount":         withdrawal.Amount.InexactFloat64(),
		"currency":       string(withdrawal.Currency),
		"failure_reason": reason,
	})

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

	// Guard: if the withdrawal is already in a terminal non-completed state
	// (failed/cancelled/reversed) the pending ledger may already be gone.
	// Before giving up, double-check the on-chain reality via the provider.
	// This prevents a race where a CANCELLED/FAILED webhook arrived before
	// the COMPLETE webhook, marking the withdrawal terminal while the funds
	// actually left the wallet.
	if withdrawal.Status.IsTerminal() && withdrawal.Status != entities.WithdrawalStatusCompleted {
		if s.circleTransfer != nil && withdrawal.ProviderTransferID != nil && strings.TrimSpace(*withdrawal.ProviderTransferID) != "" {
			tx, txErr := s.circleTransfer.GetTransaction(ctx, strings.TrimSpace(*withdrawal.ProviderTransferID))
			if txErr == nil && tx != nil {
				state := strings.ToUpper(string(tx.State))
				if state == "COMPLETE" || state == "COMPLETED" || state == "CONFIRMED" {
					s.logger.Warn("CompleteWithdrawalByTransferID: on-chain confirmed despite terminal status — overriding",
						"withdrawal_id", withdrawal.ID.String(),
						"current_status", withdrawal.Status,
						"provider_transfer_id", *withdrawal.ProviderTransferID,
						"tx_hash", tx.TxHash)
					if tx.TxHash != "" {
						_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, tx.TxHash)
					}
					if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
						s.logger.Error("CompleteWithdrawalByTransferID: on-chain confirmed but failed to commit ledger",
							"error", commitErr, "withdrawal_id", withdrawal.ID.String())
					}
					return s.forceCompleteCryptoWithdrawal(ctx, withdrawal)
				}
			}
		}
		s.logger.Warn("CompleteWithdrawalByTransferID: withdrawal in terminal non-completed state — cannot settle",
			"withdrawal_id", withdrawal.ID.String(),
			"status", withdrawal.Status,
			"transfer_id", transferID)
		return nil
	}

	// Commit the pending ledger debit if the withdrawal has one.
	if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
		s.logger.Error("webhook settled crypto withdrawal but failed to commit ledger",
			"error", commitErr, "withdrawal_id", withdrawal.ID.String())
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

	// ChainRails withdrawals: newer transfer IDs embed the intent address as a
	// fourth segment (cr:{intentID}:{fundingTxID}:{intentAddress}) so stuck rows
	// can be polled when the webhook is missed. Legacy 3-segment IDs stay
	// webhook-only.
	if strings.HasPrefix(transferID, "cr:") {
		parts := strings.SplitN(transferID, ":", 4)
		if len(parts) < 4 || parts[3] == "" || s.chainRailsAdapter == nil {
			return withdrawal.Status, nil
		}
		status, err := s.chainRailsAdapter.GetIntentStatus(ctx, parts[3])
		if err != nil || status == nil {
			if err != nil {
				s.logger.Warn("ChainRails intent status check failed",
					zap.String("withdrawal_id", withdrawal.ID.String()), zap.Error(err))
			}
			return withdrawal.Status, nil // non-fatal: retried on next sweep
		}
		state := strings.ToUpper(strings.TrimSpace(status.Status))
		switch state {
		case "COMPLETED", "COMPLETE", "SUCCESS":
			if status.TxHash != "" {
				// tx_hash is non-critical metadata — a failed write must not block
				// settling a withdrawal the chain already completed, else it strands
				// in pending. Log and proceed.
				if hashErr := s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, status.TxHash); hashErr != nil {
					s.logger.Warn("failed to persist ChainRails tx hash (non-fatal)",
						zap.String("withdrawal_id", withdrawal.ID.String()), zap.Error(hashErr))
				}
			}
			if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
				s.logger.Error("sync: ChainRails completed but failed to commit ledger",
					"error", commitErr, "withdrawal_id", withdrawal.ID.String())
			}
			if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
				return withdrawal.Status, err
			}
			return entities.WithdrawalStatusCompleted, nil
		case "REFUNDED", "FAILED", "EXPIRED", "CANCELLED":
			if err := s.failWithdrawal(ctx, withdrawal, "chainrails intent "+strings.ToLower(state)); err != nil {
				return withdrawal.Status, err
			}
			return entities.WithdrawalStatusFailed, nil
		}
		return withdrawal.Status, nil
	}

	// Circle withdrawals: poll Circle for transaction status
	if s.circleTransfer != nil && strings.EqualFold(withdrawal.ProviderWalletType, "circle") {
		tx, err := s.getCircleTransactionStatus(ctx, transferID)
		if err != nil {
			s.logger.Warn("Circle transaction status check failed", zap.String("transfer_id", transferID), zap.Error(err))
			return withdrawal.Status, nil // non-fatal: will retry on next read
		}
		if tx == nil {
			return withdrawal.Status, nil
		}
		state := strings.ToUpper(strings.TrimSpace(string(tx.State)))
		switch state {
		case "COMPLETE", "COMPLETED", "CONFIRMED":
			if tx.TxHash != "" {
				_ = s.withdrawalRepo.UpdateTxHash(ctx, withdrawal.ID, tx.TxHash)
			}
			if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
				s.logger.Error("sync: Circle completed but failed to commit ledger",
					"error", commitErr, "withdrawal_id", withdrawal.ID.String())
			}
			if err := s.settleCompletedCryptoWithdrawal(ctx, withdrawal); err != nil {
				return withdrawal.Status, err
			}
			return entities.WithdrawalStatusCompleted, nil
		case "FAILED", "DENIED", "CANCELLED", "CANCELED":
			reason := "circle transfer " + strings.ToLower(state)
			if err := s.failWithdrawal(ctx, withdrawal, reason); err != nil {
				return withdrawal.Status, err
			}
			return entities.WithdrawalStatusFailed, nil
		}
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
			if commitErr := s.commitPendingWithdrawalLedgerEntry(ctx, withdrawal); commitErr != nil {
				s.logger.Error("sync: Bridge completed but failed to commit ledger",
					"error", commitErr, "withdrawal_id", withdrawal.ID.String())
			}
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

// SyncStuckWithdrawal is the public entry point used by the recovery worker
// to poll the upstream provider for a withdrawal stuck in processing after
// the provider transfer was initiated. It either settles the withdrawal on
// success or reverses it on a provider-confirmed terminal failure.
func (s *WithdrawalService) SyncStuckWithdrawal(ctx context.Context, withdrawalID uuid.UUID) error {
	withdrawal, err := s.withdrawalRepo.GetByID(ctx, withdrawalID)
	if err != nil {
		return fmt.Errorf("get withdrawal: %w", err)
	}
	if withdrawal == nil || withdrawal.Status.IsTerminal() {
		return nil
	}
	_, err = s.syncWithdrawalStatusFromProvider(ctx, withdrawal)
	return err
}

// getCircleTransactionStatus fetches a Circle transaction by ID for status polling.
func (s *WithdrawalService) getCircleTransactionStatus(ctx context.Context, txID string) (*circlepkg.Transaction, error) {
	if s.circleTransfer == nil {
		return nil, fmt.Errorf("circle adapter not configured")
	}
	return s.circleTransfer.GetTransaction(ctx, txID)
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

// #region agent log
func writeWithdrawalDebugLog(location, message, hypothesisID string, data map[string]interface{}) {
	payload := map[string]interface{}{
		"sessionId": "b38437", "location": location, "message": message,
		"hypothesisId": hypothesisID, "data": data, "timestamp": time.Now().UnixMilli(),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	f, err := os.OpenFile("/Users/tobi/Development/RAIL_BACKEND/.cursor/debug-b38437.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// #endregion
