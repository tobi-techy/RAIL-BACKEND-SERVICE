package station

import (
	"context"
	"fmt"
	"time"

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

// BalanceTrend represents percentage change over a period
type BalanceTrend struct {
	DayChange   decimal.Decimal
	WeekChange  decimal.Decimal
	MonthChange decimal.Decimal
}

// BalanceTrends contains trends for spend and invest balances
type BalanceTrends struct {
	Spend  BalanceTrend
	Invest BalanceTrend
}

// UserSettings contains user display preferences
type UserSettings struct {
	Nickname       *string
	CurrencyLocale string
}

// ActivityItem represents a recent transaction for preview
type ActivityItem struct {
	ID          uuid.UUID
	Type        string
	Amount      decimal.Decimal
	Description string
	CreatedAt   time.Time
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

// UserSettingsRepository interface for user settings
type UserSettingsRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*UserSettings, error)
}

// BalanceSnapshotRepository interface for balance history
type BalanceSnapshotRepository interface {
	GetSnapshot(ctx context.Context, userID uuid.UUID, date time.Time) (*BalanceSnapshot, error)
}

// BalanceSnapshot represents a point-in-time balance record
type BalanceSnapshot struct {
	SpendBalance  decimal.Decimal
	InvestBalance decimal.Decimal
	TotalBalance  decimal.Decimal
	SnapshotDate  time.Time
}

// NotificationRepository interface for notification counts
type NotificationRepository interface {
	CountUnreadByUserID(ctx context.Context, userID uuid.UUID) (int, error)
}

// TransactionRepository interface for recent activity
type TransactionRepository interface {
	GetRecentByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*ActivityItem, error)
}

// Service handles station/home screen data retrieval
type Service struct {
	ledgerService    LedgerService
	allocationRepo   AllocationRepository
	depositRepo      DepositRepository
	settingsRepo     UserSettingsRepository
	snapshotRepo     BalanceSnapshotRepository
	notificationRepo NotificationRepository
	transactionRepo  TransactionRepository
	logger           *zap.Logger
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

// SetUserSettingsRepository sets the user settings repository
func (s *Service) SetUserSettingsRepository(repo UserSettingsRepository) {
	s.settingsRepo = repo
}

// SetBalanceSnapshotRepository sets the balance snapshot repository
func (s *Service) SetBalanceSnapshotRepository(repo BalanceSnapshotRepository) {
	s.snapshotRepo = repo
}

// SetNotificationRepository sets the notification repository
func (s *Service) SetNotificationRepository(repo NotificationRepository) {
	s.notificationRepo = repo
}

// SetTransactionRepository sets the transaction repository
func (s *Service) SetTransactionRepository(repo TransactionRepository) {
	s.transactionRepo = repo
}

// GetUserBalances retrieves the user's spend and invest balances
func (s *Service) GetUserBalances(ctx context.Context, userID uuid.UUID) (*Balances, error) {
	spendingBalance, err := s.ledgerService.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		s.logger.Warn("Failed to get spending balance, defaulting to zero",
			zap.Error(err),
			zap.String("user_id", userID.String()))
		spendingBalance = decimal.Zero
	}

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

// GetBalanceTrends calculates day/week/month percentage changes
func (s *Service) GetBalanceTrends(ctx context.Context, userID uuid.UUID, currentSpend, currentInvest decimal.Decimal) (*BalanceTrends, error) {
	if s.snapshotRepo == nil {
		return &BalanceTrends{}, nil
	}

	now := time.Now().UTC()
	dayAgo := now.AddDate(0, 0, -1)
	weekAgo := now.AddDate(0, 0, -7)
	monthAgo := now.AddDate(0, -1, 0)

	trends := &BalanceTrends{}

	// Day change
	if snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, dayAgo); err == nil && snapshot != nil {
		trends.Spend.DayChange = calcPercentChange(snapshot.SpendBalance, currentSpend)
		trends.Invest.DayChange = calcPercentChange(snapshot.InvestBalance, currentInvest)
	}

	// Week change
	if snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, weekAgo); err == nil && snapshot != nil {
		trends.Spend.WeekChange = calcPercentChange(snapshot.SpendBalance, currentSpend)
		trends.Invest.WeekChange = calcPercentChange(snapshot.InvestBalance, currentInvest)
	}

	// Month change
	if snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, monthAgo); err == nil && snapshot != nil {
		trends.Spend.MonthChange = calcPercentChange(snapshot.SpendBalance, currentSpend)
		trends.Invest.MonthChange = calcPercentChange(snapshot.InvestBalance, currentInvest)
	}

	return trends, nil
}

// GetUserSettings retrieves user display settings
func (s *Service) GetUserSettings(ctx context.Context, userID uuid.UUID) (*UserSettings, error) {
	if s.settingsRepo == nil {
		return &UserSettings{CurrencyLocale: "en-US"}, nil
	}

	settings, err := s.settingsRepo.GetByUserID(ctx, userID)
	if err != nil {
		return &UserSettings{CurrencyLocale: "en-US"}, nil
	}
	return settings, nil
}

// GetUnreadNotificationCount returns count of unread notifications
func (s *Service) GetUnreadNotificationCount(ctx context.Context, userID uuid.UUID) (int, error) {
	if s.notificationRepo == nil {
		return 0, nil
	}
	return s.notificationRepo.CountUnreadByUserID(ctx, userID)
}

// GetRecentActivity returns last N transactions for preview
func (s *Service) GetRecentActivity(ctx context.Context, userID uuid.UUID, limit int) ([]*ActivityItem, error) {
	if s.transactionRepo == nil {
		return []*ActivityItem{}, nil
	}
	return s.transactionRepo.GetRecentByUserID(ctx, userID, limit)
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

// calcPercentChange calculates percentage change from old to new value
func calcPercentChange(old, new decimal.Decimal) decimal.Decimal {
	if old.IsZero() {
		if new.IsZero() {
			return decimal.Zero
		}
		return decimal.NewFromInt(100)
	}
	return new.Sub(old).Div(old).Mul(decimal.NewFromInt(100))
}
