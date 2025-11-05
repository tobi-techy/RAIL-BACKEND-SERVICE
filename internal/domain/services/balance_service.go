package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/infrastructure/repositories"
	"github.com/stack-service/stack_service/pkg/logger"
)

// BalanceService handles user balance operations
type BalanceService struct {
	balanceRepo *repositories.BalanceRepository
	logger      *logger.Logger
}

// NewBalanceService creates a new balance service
func NewBalanceService(balanceRepo *repositories.BalanceRepository, logger *logger.Logger) *BalanceService {
	return &BalanceService{
		balanceRepo: balanceRepo,
		logger:      logger,
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
