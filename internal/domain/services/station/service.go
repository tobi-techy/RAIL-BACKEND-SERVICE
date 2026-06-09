package station

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledger "github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Balances represents user balance data for the Station
type Balances struct {
	SpendingBalance          decimal.Decimal
	StashBalance             decimal.Decimal
	GoalBalance              decimal.Decimal
	SafeToSpend              decimal.Decimal
	InvestBalance            decimal.Decimal
	FiatExposure             decimal.Decimal
	UnallocatedUSDC          decimal.Decimal
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

// AlpacaAccountRepository interface for fetching broker portfolio value
type AlpacaAccountRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
}

// AlpacaAccountService interface for fetching a synced account (triggers live sync if stale)
type AlpacaAccountService interface {
	GetUserAccount(ctx context.Context, userID uuid.UUID) (*entities.AlpacaAccount, error)
}

// ObligationProvider fetches upcoming obligations for safe-to-spend calculation.
type ObligationProvider interface {
	GetUpcomingTotal(ctx context.Context, userID uuid.UUID, withinDays int) (decimal.Decimal, error)
}

// GoalBalanceProvider fetches total goal sub-account balance.
type GoalBalanceProvider interface {
	GetTotalGoalAllocated(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// Service handles station/home screen data retrieval
type Service struct {
	ledgerService     LedgerService
	allocationRepo    AllocationRepository
	depositRepo       DepositRepository
	settingsRepo      UserSettingsRepository
	snapshotRepo      BalanceSnapshotRepository
	notificationRepo  NotificationRepository
	transactionRepo   TransactionRepository
	alpacaAccountRepo AlpacaAccountRepository
	alpacaAccountSvc  AlpacaAccountService
	obligationProv    ObligationProvider
	goalBalanceProv   GoalBalanceProvider
	logger            *zap.Logger
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

// SetAlpacaAccountRepository sets the Alpaca account repository for portfolio value lookups
func (s *Service) SetAlpacaAccountRepository(repo AlpacaAccountRepository) {
	s.alpacaAccountRepo = repo
}

// SetAlpacaAccountService sets the Alpaca account service (preferred over repo — triggers live sync)
func (s *Service) SetAlpacaAccountService(svc AlpacaAccountService) {
	s.alpacaAccountSvc = svc
}

// SetObligationProvider sets the obligation provider for safe-to-spend calculation.
func (s *Service) SetObligationProvider(p ObligationProvider) { s.obligationProv = p }

// SetGoalBalanceProvider sets the goal balance provider.
func (s *Service) SetGoalBalanceProvider(p GoalBalanceProvider) { s.goalBalanceProv = p }

func (s *Service) getAccountBalanceOrZero(ctx context.Context, userID uuid.UUID, accountType entities.AccountType, label string) decimal.Decimal {
	balance, err := s.ledgerService.GetAccountBalance(ctx, userID, accountType)
	if err == nil {
		return balance
	}

	fields := []zap.Field{
		zap.Error(err),
		zap.String("user_id", userID.String()),
		zap.String("account_type", string(accountType)),
	}
	if errors.Is(err, ledger.ErrAccountNotFound) {
		s.logger.Debug("Ledger account missing, defaulting to zero", fields...)
		return decimal.Zero
	}

	s.logger.Warn(label+", defaulting to zero", fields...)
	return decimal.Zero
}

// GetUserBalances retrieves the user's spend and invest balances
func (s *Service) GetUserBalances(ctx context.Context, userID uuid.UUID) (*Balances, error) {
	mode, err := s.allocationRepo.GetMode(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get allocation mode, falling back to legacy balance view",
			zap.Error(err),
			zap.String("user_id", userID.String()))
	}

	// If smart allocation mode is not active, show legacy USDC balance directly
	// and include broker portfolio value so the Stash card reflects real investment worth.
	if mode == nil || !mode.Active {
		var usdcBalance, fiatExposure decimal.Decimal
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			usdcBalance = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeUSDCBalance, "Failed to get USDC balance")
		}()

		go func() {
			defer wg.Done()
			fiatExposure = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeFiatExposure, "Failed to get fiat exposure balance")
		}()

		wg.Wait()

		portfolioValue := decimal.Zero
		if s.alpacaAccountSvc != nil {
			if acct, err := s.alpacaAccountSvc.GetUserAccount(ctx, userID); err == nil && acct != nil {
				portfolioValue = acct.PortfolioValue
			}
		} else if s.alpacaAccountRepo != nil {
			if acct, err := s.alpacaAccountRepo.GetByUserID(ctx, userID); err == nil && acct != nil {
				portfolioValue = acct.PortfolioValue
			}
		}

		investBalance := fiatExposure.Add(portfolioValue)
		totalBalance := usdcBalance.Add(investBalance)

		return &Balances{
			SpendingBalance: usdcBalance,
			StashBalance:    decimal.Zero,
			InvestBalance:   investBalance,
			FiatExposure:    fiatExposure,
			UnallocatedUSDC: decimal.Zero,
			TotalBalance:    totalBalance,
		}, nil
	}

	var (
		spendingBalance decimal.Decimal
		stashBalance    decimal.Decimal
		fiatExposure    decimal.Decimal
		usdcBalance     decimal.Decimal
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		spendingBalance = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeSpendingBalance, "Failed to get spending balance")
	}()

	go func() {
		defer wg.Done()
		stashBalance = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeStashBalance, "Failed to get stash balance")
	}()

	go func() {
		defer wg.Done()
		fiatExposure = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeFiatExposure, "Failed to get fiat exposure")
	}()

	go func() {
		defer wg.Done()
		usdcBalance = s.getAccountBalanceOrZero(ctx, userID, entities.AccountTypeUSDCBalance, "Failed to get unallocated USDC")
	}()

	wg.Wait()

	portfolioValue := decimal.Zero
	if s.alpacaAccountSvc != nil {
		if acct, err := s.alpacaAccountSvc.GetUserAccount(ctx, userID); err == nil && acct != nil {
			portfolioValue = acct.PortfolioValue
		}
	} else if s.alpacaAccountRepo != nil {
		if acct, err := s.alpacaAccountRepo.GetByUserID(ctx, userID); err == nil && acct != nil {
			portfolioValue = acct.PortfolioValue
		}
	}

	investBalance := portfolioValue.Add(stashBalance).Add(fiatExposure)

	// Compute goal balance and safe-to-spend
	var goalBalance decimal.Decimal
	if s.goalBalanceProv != nil {
		if gb, err := s.goalBalanceProv.GetTotalGoalAllocated(ctx, userID); err == nil {
			goalBalance = gb
		}
	}
	safeToSpend := spendingBalance
	if s.obligationProv != nil {
		if upcoming, err := s.obligationProv.GetUpcomingTotal(ctx, userID, 7); err == nil {
			safeToSpend = spendingBalance.Sub(upcoming)
			if safeToSpend.IsNegative() {
				safeToSpend = decimal.Zero
			}
		}
	}

	totalBalance := spendingBalance.Add(investBalance).Add(usdcBalance).Add(goalBalance)

	return &Balances{
		SpendingBalance: spendingBalance,
		StashBalance:    stashBalance,
		GoalBalance:     goalBalance,
		SafeToSpend:     safeToSpend,
		InvestBalance:   investBalance,
		FiatExposure:    fiatExposure,
		UnallocatedUSDC: usdcBalance,
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

	var (
		daySnapshot   *BalanceSnapshot
		weekSnapshot  *BalanceSnapshot
		monthSnapshot *BalanceSnapshot
	)

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, dayAgo)
		if err == nil {
			daySnapshot = snapshot
		}
	}()

	go func() {
		defer wg.Done()
		snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, weekAgo)
		if err == nil {
			weekSnapshot = snapshot
		}
	}()

	go func() {
		defer wg.Done()
		snapshot, err := s.snapshotRepo.GetSnapshot(ctx, userID, monthAgo)
		if err == nil {
			monthSnapshot = snapshot
		}
	}()

	wg.Wait()

	if daySnapshot != nil {
		trends.Spend.DayChange = calcPercentChange(daySnapshot.SpendBalance, currentSpend)
		trends.Invest.DayChange = calcPercentChange(daySnapshot.InvestBalance, currentInvest)
	}

	if weekSnapshot != nil {
		trends.Spend.WeekChange = calcPercentChange(weekSnapshot.SpendBalance, currentSpend)
		trends.Invest.WeekChange = calcPercentChange(weekSnapshot.InvestBalance, currentInvest)
	}

	if monthSnapshot != nil {
		trends.Spend.MonthChange = calcPercentChange(monthSnapshot.SpendBalance, currentSpend)
		trends.Invest.MonthChange = calcPercentChange(monthSnapshot.InvestBalance, currentInvest)
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
