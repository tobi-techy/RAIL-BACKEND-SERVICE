package balance

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AlpacaBalanceAdapter interface for Alpaca balance operations
type AlpacaBalanceAdapter interface {
	GetAccountBalance(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error)
}

// BalanceService handles user balance operations
type BalanceService struct {
	balanceRepo   *repositories.BalanceRepository
	alpacaAdapter AlpacaBalanceAdapter
	logger        *logger.Logger
}

// NewBalanceService creates a new balance service
func NewBalanceService(balanceRepo *repositories.BalanceRepository, alpacaAdapter AlpacaBalanceAdapter, logger *logger.Logger) *BalanceService {
	return &BalanceService{
		balanceRepo:   balanceRepo,
		alpacaAdapter: alpacaAdapter,
		logger:        logger,
	}
}

// UpdateBuyingPower updates user's buying power after funding
func (s *BalanceService) UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	s.logger.Info("Updating buying power",
		"user_id", userID.String(),
		"amount", amount.String())

	if err := s.balanceRepo.UpdateBuyingPower(ctx, userID, amount); err != nil {
		s.logger.Error("Failed to update buying power", "error", err, "user_id", userID.String())
		return fmt.Errorf("update buying power: %w", err)
	}

	s.logger.Info("Successfully updated buying power", "user_id", userID.String(), "amount", amount.String())
	return nil
}

// GetBalance retrieves user's current balance
func (s *BalanceService) GetBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	balance, err := s.balanceRepo.GetOrCreate(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get balance", "error", err, "user_id", userID.String())
		return decimal.Zero, fmt.Errorf("get balance: %w", err)
	}

	return balance.BuyingPower, nil
}

// SyncWithAlpaca syncs local balance with Alpaca buying power
func (s *BalanceService) SyncWithAlpaca(ctx context.Context, userID uuid.UUID, alpacaAccountID string) error {
	s.logger.Info("Syncing balance with Alpaca",
		"user_id", userID.String(),
		"alpaca_account_id", alpacaAccountID)

	// Get current Alpaca balance
	alpacaAccount, err := s.alpacaAdapter.GetAccountBalance(ctx, alpacaAccountID)
	if err != nil {
		s.logger.Error("Failed to get Alpaca balance", "error", err)
		return fmt.Errorf("get Alpaca balance: %w", err)
	}

	// Update local balance to match Alpaca buying power
	if err := s.balanceRepo.UpdateBuyingPower(ctx, userID, alpacaAccount.BuyingPower); err != nil {
		s.logger.Error("Failed to sync balance", "error", err)
		return fmt.Errorf("sync balance: %w", err)
	}

	s.logger.Info("Balance synced with Alpaca",
		"user_id", userID.String(),
		"buying_power", alpacaAccount.BuyingPower.String())

	return nil
}
