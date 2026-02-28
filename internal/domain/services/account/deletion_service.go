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
}

// UserRepository interface for user deletion
type UserRepository interface {
	HardDelete(ctx context.Context, userID uuid.UUID) error
}

// AuditService interface for audit logging
type AuditService interface {
	Log(ctx context.Context, userID uuid.UUID, action entities.AuditAction, resourceType string, resourceID *uuid.UUID, metadata map[string]interface{}) error
}

// DeletionService handles complete account deletion with fund sweep
type DeletionService struct {
	ledgerService         LedgerService
	walletRepo            WalletRepository
	circleClient          CircleClient
	userRepo              UserRepository
	auditService          AuditService
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

	// Step 3: Log audit before deletion
	if s.auditService != nil {
		_ = s.auditService.Log(ctx, req.UserID, entities.AuditActionAccountDelete, "user", nil,
			map[string]interface{}{"reason": req.Reason, "funds_swept": totalBalance.String()})
	}

	// Step 4: Hard delete user (cascades to all related tables)
	if err := s.userRepo.HardDelete(ctx, req.UserID); err != nil {
		s.logger.Error("Failed to delete user", "error", err)
		return nil, fmt.Errorf("failed to delete account: %w", err)
	}

	s.logger.Info("Account deleted successfully",
		"user_id", req.UserID.String(),
		"funds_swept", totalBalance.String())

	return &DeleteAccountResponse{
		Success:     true,
		FundsSwept:  totalBalance,
		SweepTxHash: sweepTxHash,
		DeletedAt:   time.Now(),
	}, nil
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

	// Execute transfer to treasury
	req := entities.CircleTransferRequest{
		WalletID:           sourceWallet.CircleWalletID,
		TokenID:            "USDC",
		Amounts:            []string{amount.StringFixed(6)},
		DestinationAddress: s.treasuryWalletAddress,
		IDempotencyKey:     uuid.New().String(),
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
