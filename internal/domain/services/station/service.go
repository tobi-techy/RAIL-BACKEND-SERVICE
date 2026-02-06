package station

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Balances represents user balance data for the Station
type Balances struct {
	SpendingBalance          decimal.Decimal
	StashBalance             decimal.Decimal
	TotalBalance             decimal.Decimal
	PendingAmount            decimal.Decimal
	PendingTransactionsCount int
}

// LedgerService interface for balance retrieval
type LedgerService interface {
	GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error)
}

// AllocationRepository interface for allocation mode retrieval
type AllocationRepository interface {
	GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error)
}

// DepositRepository interface for checking pending deposits
type DepositRepository interface {
	CountPendingByUserID(ctx context.Context, userID uuid.UUID) (int, error)
}

// Service handles station/home screen data retrieval
type Service struct {
	ledgerService  LedgerService
	allocationRepo AllocationRepository
	depositRepo    DepositRepository
	logger         *zap.Logger
}

// NewService creates a new station service
func NewService(
	ledgerService LedgerService,
	allocationRepo AllocationRepository,
	depositRepo DepositRepository,
	logger *zap.Logger,
) *Service {
	return &Service{
		ledgerService:  ledgerService,
		allocationRepo: allocationRepo,
		depositRepo:    depositRepo,
		logger:         logger,
	}
}

// GetUserBalances retrieves the user's spend and invest balances
func (s *Service) GetUserBalances(ctx context.Context, userID uuid.UUID) (*Balances, error) {
	// Get spending balance (70% allocation)
	spendingBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		s.logger.Warn("Failed to get spending balance, defaulting to zero",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		spendingBalance = decimal.Zero
	}

	// Get stash balance (30% allocation - invest balance)
	stashBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		s.logger.Warn("Failed to get stash balance, defaulting to zero",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		stashBalance = decimal.Zero
	}

	totalBalance := spendingBalance.Add(stashBalance)

	return &Balances{
		SpendingBalance: spendingBalance,
		StashBalance:    stashBalance,
		TotalBalance:    totalBalance,
	}, nil
}

// GetAllocationMode retrieves the user's allocation mode
func (s *Service) GetAllocationMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error) {
	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get allocation mode: %w", err)
	}
	return mode, nil
}

// HasPendingDeposits checks if the user has any pending deposits
func (s *Service) HasPendingDeposits(ctx context.Context, userID uuid.UUID) (bool, error) {
	count, err := s.depositRepo.CountPendingByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to count pending deposits: %w", err)
	}
	return count > 0, nil
}

// GetPendingInfo retrieves pending transaction count
func (s *Service) GetPendingInfo(ctx context.Context, userID uuid.UUID) (int, error) {
	count, err := s.depositRepo.CountPendingByUserID(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to count pending deposits: %w", err)
	}
	return count, nil
}
