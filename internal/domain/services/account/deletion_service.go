package account

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
)

// Minimum balance threshold for sweep (to avoid dust transfers)
var MinSweepThreshold = decimal.NewFromFloat(0.01)

// LedgerService interface for balance operations
type LedgerService interface {
	GetUserBalances(ctx context.Context, userID uuid.UUID) (*entities.UserBalances, error)
}

// WalletRepository interface for wallet operations
type WalletRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error)
}

// CircleClient interface for fund transfers
type CircleClient interface {
	TransferFunds(ctx context.Context, req entities.CircleTransferRequest) (map[string]interface{}, error)
	GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error)
}

// UserRepository interface for user deletion
type UserRepository interface {
	HardDelete(ctx context.Context, userID uuid.UUID) error
	AnonymizeUser(ctx context.Context, userID uuid.UUID) error
}

// AuditService interface for audit logging
type AuditService interface {
	Log(ctx context.Context, userID uuid.UUID, action entities.AuditAction, resourceType string, resourceID *uuid.UUID, metadata map[string]interface{}) error
}

// SessionService interface for session invalidation
type SessionService interface {
	InvalidateAllUserSessions(ctx context.Context, userID uuid.UUID) error
}

// AlpacaAccountRepository interface for Alpaca account lookup
type AlpacaAccountRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
}

// AlpacaClient interface for Alpaca operations
type AlpacaClient interface {
	CloseAccount(ctx context.Context, accountID string) error
	CloseAllPositions(ctx context.Context, accountID string) error
	CancelAllOrders(ctx context.Context, accountID string) error
	ListPositions(ctx context.Context, accountID string) ([]entities.AlpacaPositionResponse, error)
}

// VirtualAccountRepository interface for Bridge virtual accounts
type VirtualAccountRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.VirtualAccount, error)
}

// BridgeClient interface for Bridge operations
type BridgeClient interface {
	DeactivateVirtualAccount(ctx context.Context, customerID, virtualAccountID string) error
}

// DeviceTokenRepository interface for push notification cleanup
type DeviceTokenRepository interface {
	DeactivateAllUserTokens(ctx context.Context, userID uuid.UUID) error
}

// DeletionService handles complete account deletion with fund sweep
type DeletionService struct {
	ledgerService         LedgerService
	walletRepo            WalletRepository
	circleClient          CircleClient
	userRepo              UserRepository
	auditService          AuditService
	sessionService        SessionService
	deviceTokenRepo       DeviceTokenRepository
	alpacaAccountRepo     AlpacaAccountRepository
	alpacaClient          AlpacaClient
	virtualAccountRepo    VirtualAccountRepository
	bridgeClient          BridgeClient
	treasuryWalletAddress string
	logger                *logger.Logger
}

// NewDeletionService creates a new account deletion service
func NewDeletionService(
	ledgerService LedgerService,
	walletRepo WalletRepository,
	circleClient CircleClient,
	userRepo UserRepository,
	auditService AuditService,
	treasuryWalletAddress string,
	logger *logger.Logger,
) *DeletionService {
	return &DeletionService{
		ledgerService:         ledgerService,
		walletRepo:            walletRepo,
		circleClient:          circleClient,
		userRepo:              userRepo,
		auditService:          auditService,
		treasuryWalletAddress: treasuryWalletAddress,
		logger:                logger,
	}
}

// SetAlpacaClient sets the Alpaca client for account closure
func (s *DeletionService) SetAlpacaClient(repo AlpacaAccountRepository, client AlpacaClient) {
	s.alpacaAccountRepo = repo
	s.alpacaClient = client
}

// SetBridgeClient sets the Bridge client for virtual account deactivation
func (s *DeletionService) SetBridgeClient(repo VirtualAccountRepository, client BridgeClient) {
	s.virtualAccountRepo = repo
	s.bridgeClient = client
}

// SetSessionService sets the session service for invalidating user sessions
func (s *DeletionService) SetSessionService(sessionService SessionService) {
	s.sessionService = sessionService
}

// SetDeviceTokenRepo sets the device token repository for push notification cleanup
func (s *DeletionService) SetDeviceTokenRepo(repo DeviceTokenRepository) {
	s.deviceTokenRepo = repo
}

// DeleteAccountRequest represents a request to delete an account
type DeleteAccountRequest struct {
	UserID uuid.UUID
	Reason string
}

// DeleteAccountResponse represents the result of account deletion
type DeleteAccountResponse struct {
	Success     bool            `json:"success"`
	FundsSwept  decimal.Decimal `json:"funds_swept"`
	SweepTxHash string          `json:"sweep_tx_hash,omitempty"`
	DeletedAt   time.Time       `json:"deleted_at"`
}

// DeleteAccount performs complete account deletion with fund sweep (simplified interface)
func (s *DeletionService) DeleteAccount(ctx context.Context, userID uuid.UUID, reason string) (fundsSwept string, txHash string, err error) {
	resp, err := s.deleteAccountInternal(ctx, &DeleteAccountRequest{
		UserID: userID,
		Reason: reason,
	})
	if err != nil {
		return "", "", err
	}
	return resp.FundsSwept.String(), resp.SweepTxHash, nil
}

// deleteAccountInternal performs complete account deletion with fund sweep
func (s *DeletionService) deleteAccountInternal(ctx context.Context, req *DeleteAccountRequest) (*DeleteAccountResponse, error) {
	s.logger.Info("Starting account deletion",
		"user_id", req.UserID.String(),
		"reason", req.Reason)

	// Step 1: Get all user balances
	balances, err := s.ledgerService.GetUserBalances(ctx, req.UserID)
	if err != nil {
		s.logger.Error("Failed to get user balances", "error", err)
		return nil, fmt.Errorf("failed to get balances: %w", err)
	}

	totalBalance := s.calculateTotalBalance(balances)
	var sweepTxHash string

	// Step 2: Sweep funds to treasury if balance exists
	if totalBalance.GreaterThan(MinSweepThreshold) {
		sweepTxHash, err = s.sweepFundsToTreasury(ctx, req.UserID, totalBalance)
		if err != nil {
			s.logger.Error("Failed to sweep funds", "error", err, "amount", totalBalance.String())
			return nil, fmt.Errorf("failed to sweep funds to treasury: %w", err)
		}
		s.logger.Info("Funds swept to treasury",
			"user_id", req.UserID.String(),
			"amount", totalBalance.String(),
			"tx_hash", sweepTxHash)
	}

	// Step 3: Clean up external provider accounts
	if err := s.cleanupExternalProviders(ctx, req.UserID); err != nil {
		s.logger.Error("External provider cleanup failed", "error", err)
		return nil, fmt.Errorf("failed to cleanup external accounts: %w", err)
	}

	// Step 4: Log audit before anonymization
	if s.auditService != nil {
		_ = s.auditService.Log(ctx, req.UserID, entities.AuditActionAccountAnonymize, "user", nil,
			map[string]interface{}{"reason": req.Reason, "funds_swept": totalBalance.String()})
	}

	// Step 5: Anonymize user PII (GDPR compliance - preserve UUID for financial audit trail)
	if err := s.userRepo.AnonymizeUser(ctx, req.UserID); err != nil {
		s.logger.Error("Failed to anonymize user", "error", err)
		return nil, fmt.Errorf("failed to anonymize account: %w", err)
	}

	// Step 6: Invalidate all active sessions (JWT tokens)
	if s.sessionService != nil {
		if err := s.sessionService.InvalidateAllUserSessions(ctx, req.UserID); err != nil {
			s.logger.Warn("Failed to invalidate user sessions", "error", err)
			// Non-critical - sessions will expire naturally
		} else {
			s.logger.Info("Invalidated all user sessions", "user_id", req.UserID.String())
		}
	}

	// Step 7: Deactivate push notification tokens
	if s.deviceTokenRepo != nil {
		if err := s.deviceTokenRepo.DeactivateAllUserTokens(ctx, req.UserID); err != nil {
			s.logger.Warn("Failed to deactivate device tokens", "error", err)
		}
	}

	s.logger.Info("Account anonymized successfully",
		"user_id", req.UserID.String(),
		"funds_swept", totalBalance.String())

	return &DeleteAccountResponse{
		Success:     true,
		FundsSwept:  totalBalance,
		SweepTxHash: sweepTxHash,
		DeletedAt:   time.Now(),
	}, nil
}

// cleanupExternalProviders closes/deactivates accounts on external providers
// Returns error if critical cleanup fails (Alpaca positions/orders)
func (s *DeletionService) cleanupExternalProviders(ctx context.Context, userID uuid.UUID) error {
	var criticalErrors []string

	// Close Alpaca account - must liquidate positions and cancel orders first
	if s.alpacaAccountRepo != nil && s.alpacaClient != nil {
		if alpacaAccount, err := s.alpacaAccountRepo.GetByUserID(ctx, userID); err == nil && alpacaAccount != nil {
			alpacaID := alpacaAccount.AlpacaAccountID

			// Step 1: Cancel all open orders
			if err := s.alpacaClient.CancelAllOrders(ctx, alpacaID); err != nil {
				s.logger.Warn("Failed to cancel Alpaca orders", "user_id", userID.String(), "alpaca_id", alpacaID, "error", err)
				// Continue - orders may already be filled or none exist
			}

			// Step 2: Close all positions (liquidate)
			positions, _ := s.alpacaClient.ListPositions(ctx, alpacaID)
			if len(positions) > 0 {
				if err := s.alpacaClient.CloseAllPositions(ctx, alpacaID); err != nil {
					s.logger.Error("Failed to liquidate Alpaca positions", "user_id", userID.String(), "alpaca_id", alpacaID, "error", err)
					criticalErrors = append(criticalErrors, fmt.Sprintf("alpaca positions: %v", err))
				} else {
					s.logger.Info("Liquidated Alpaca positions", "user_id", userID.String(), "alpaca_id", alpacaID, "count", len(positions))
				}
			}

			// Step 3: Close the account (only if positions were liquidated)
			if len(criticalErrors) == 0 {
				if err := s.alpacaClient.CloseAccount(ctx, alpacaID); err != nil {
					s.logger.Warn("Failed to close Alpaca account", "user_id", userID.String(), "alpaca_id", alpacaID, "error", err)
					// Not critical - account may have pending settlements
				} else {
					s.logger.Info("Closed Alpaca account", "user_id", userID.String(), "alpaca_id", alpacaID)
				}
			}
		}
	}

	// Deactivate Bridge virtual accounts
	if s.virtualAccountRepo != nil && s.bridgeClient != nil {
		if virtualAccounts, err := s.virtualAccountRepo.GetByUserID(ctx, userID); err == nil {
			for _, va := range virtualAccounts {
				if va.BridgeCustomerID != "" && va.BridgeAccountID != nil && *va.BridgeAccountID != "" {
					if err := s.bridgeClient.DeactivateVirtualAccount(ctx, va.BridgeCustomerID, *va.BridgeAccountID); err != nil {
						s.logger.Warn("Failed to deactivate Bridge virtual account", "user_id", userID.String(), "bridge_account_id", *va.BridgeAccountID, "error", err)
					} else {
						s.logger.Info("Deactivated Bridge virtual account", "user_id", userID.String(), "bridge_account_id", *va.BridgeAccountID)
					}
				}
			}
		}
	}

	// Note: Circle wallets cannot be deleted (blockchain addresses are permanent)
	// Funds have already been swept to treasury

	if len(criticalErrors) > 0 {
		return fmt.Errorf("external provider cleanup failed: %v", criticalErrors)
	}
	return nil
}

// calculateTotalBalance sums all user balances
func (s *DeletionService) calculateTotalBalance(balances *entities.UserBalances) decimal.Decimal {
	if balances == nil {
		return decimal.Zero
	}
	return balances.TotalValue()
}

// sweepFundsToTreasury transfers all user funds to company treasury wallet
func (s *DeletionService) sweepFundsToTreasury(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (string, error) {
	if s.treasuryWalletAddress == "" {
		return "", fmt.Errorf("treasury wallet address not configured")
	}

	if s.circleClient == nil {
		return "", fmt.Errorf("circle client not configured")
	}

	// Get user's Circle wallet
	wallets, err := s.walletRepo.GetByUserID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user wallets: %w", err)
	}

	if len(wallets) == 0 {
		return "", fmt.Errorf("no wallets found for user")
	}

	// Find the primary wallet (SOL chain preferred)
	var sourceWallet *entities.ManagedWallet
	for _, w := range wallets {
		if w.Chain == "SOL" && w.CircleWalletID != "" {
			sourceWallet = w
			break
		}
	}
	if sourceWallet == nil {
		for _, w := range wallets {
			if w.CircleWalletID != "" {
				sourceWallet = w
				break
			}
		}
	}

	if sourceWallet == nil {
		return "", fmt.Errorf("no Circle wallet found for user")
	}

	// Get actual wallet balance from Circle (not ledger balance)
	actualBalance, err := s.getCircleWalletBalance(ctx, sourceWallet.CircleWalletID)
	if err != nil {
		s.logger.Warn("Failed to get Circle wallet balance, using ledger balance", "error", err)
		actualBalance = amount
	}

	// Use the smaller of ledger balance or actual balance
	sweepAmount := amount
	if actualBalance.LessThan(amount) {
		sweepAmount = actualBalance
	}

	// Skip if nothing to sweep
	if sweepAmount.LessThanOrEqual(MinSweepThreshold) {
		s.logger.Info("No funds to sweep", "user_id", userID.String(), "balance", sweepAmount.String())
		return "", nil
	}

	// Execute transfer to treasury
	req := entities.CircleTransferRequest{
		WalletID:           sourceWallet.CircleWalletID,
		TokenID:            "USDC",
		Amounts:            []string{sweepAmount.StringFixed(6)},
		DestinationAddress: s.treasuryWalletAddress,
		IDempotencyKey:     fmt.Sprintf("account-closure-%s", userID.String()),
	}

	response, err := s.circleClient.TransferFunds(ctx, req)
	if err != nil {
		return "", fmt.Errorf("circle transfer failed: %w", err)
	}

	// Extract tx hash
	var txHash string
	if h, ok := response["transactionHash"].(string); ok {
		txHash = h
	} else if h, ok := response["txHash"].(string); ok {
		txHash = h
	}

	return txHash, nil
}

// getCircleWalletBalance fetches actual USDC balance from Circle wallet
func (s *DeletionService) getCircleWalletBalance(ctx context.Context, walletID string) (decimal.Decimal, error) {
	balances, err := s.circleClient.GetWalletBalances(ctx, walletID)
	if err != nil {
		return decimal.Zero, err
	}

	// Find USDC balance
	for _, tb := range balances.TokenBalances {
		if tb.Token.Symbol == "USDC" {
			amount, err := decimal.NewFromString(tb.Amount)
			if err != nil {
				return decimal.Zero, fmt.Errorf("failed to parse USDC balance: %w", err)
			}
			return amount, nil
		}
	}

	return decimal.Zero, nil
}
