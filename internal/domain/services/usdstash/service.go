package usdstash

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// BridgeClient handles Bridge custody wallet operations.
type BridgeClient interface {
	// GetOrCreateUSDBWallet returns the Bridge USDB wallet ID for the customer,
	// creating one on solana if it doesn't exist.
	GetOrCreateUSDBWallet(ctx context.Context, customerID string) (walletID string, err error)
	// GetUSDCWalletID returns the existing USDC custody wallet ID for the customer.
	GetUSDCWalletID(ctx context.Context, customerID string) (walletID string, err error)
	// TransferBetweenWallets moves amount from source to destination Bridge custody wallet.
	TransferBetweenWallets(ctx context.Context, customerID, sourceWalletID, destWalletID string, amount decimal.Decimal, idempotencyKey string) error
}

// UserProfileProvider resolves a user's Bridge customer ID.
type UserProfileProvider interface {
	GetBridgeCustomerID(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service sweeps the stash portion into a Bridge USDB custody wallet.
type Service struct {
	bridge  BridgeClient
	users   UserProfileProvider
	logger  *zap.Logger
}

func NewService(bridge BridgeClient, users UserProfileProvider, logger *zap.Logger) *Service {
	return &Service{bridge: bridge, users: users, logger: logger}
}

// SweepToUSDB transfers amount from the user's USDC custody wallet into their USDB custody wallet.
func (s *Service) SweepToUSDB(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string) error {
	if amount.IsZero() || amount.IsNegative() {
		return nil
	}

	customerID, err := s.users.GetBridgeCustomerID(ctx, userID)
	if err != nil {
		return fmt.Errorf("usdstash: get bridge customer: %w", err)
	}

	sourceWalletID, err := s.bridge.GetUSDCWalletID(ctx, customerID)
	if err != nil {
		return fmt.Errorf("usdstash: get usdc wallet: %w", err)
	}

	destWalletID, err := s.bridge.GetOrCreateUSDBWallet(ctx, customerID)
	if err != nil {
		return fmt.Errorf("usdstash: get/create usdb wallet: %w", err)
	}

	if err := s.bridge.TransferBetweenWallets(ctx, customerID, sourceWalletID, destWalletID, amount, idempotencyKey); err != nil {
		return fmt.Errorf("usdstash: transfer to usdb: %w", err)
	}

	s.logger.Info("Swept stash to USDB wallet",
		zap.String("user_id", userID.String()),
		zap.String("amount", amount.String()),
		zap.String("dest_wallet", destWalletID),
	)
	return nil
}
