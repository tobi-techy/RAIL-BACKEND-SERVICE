package limits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
)

// UserRepository interface for fetching user KYC status
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
}

// UsageRepository interface for tracking transaction usage
type UsageRepository interface {
	GetOrCreate(ctx context.Context, userID uuid.UUID) (*entities.UserTransactionUsage, error)
	IncrementDepositUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	IncrementWithdrawalUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
	AtomicIncrementDeposit(ctx context.Context, userID uuid.UUID, amount, dailyLimit, monthlyLimit decimal.Decimal) error
	AtomicIncrementWithdrawal(ctx context.Context, userID uuid.UUID, amount, dailyLimit, monthlyLimit decimal.Decimal) error
	ResetExpiredPeriods(ctx context.Context, userID uuid.UUID) error
}

// ExchangeRateProvider returns the current exchange rate for a currency pair.
// Used to normalize non-USD amounts to USD for unified usage tracking.
type ExchangeRateProvider interface {
	GetNGNRate(ctx context.Context) (decimal.Decimal, error)
}

// Service handles transaction limit validation
type Service struct {
	userRepo     UserRepository
	usageRepo    UsageRepository
	rateProvider ExchangeRateProvider
	logger       *logger.Logger
}

// NewService creates a new limits service
func NewService(userRepo UserRepository, usageRepo UsageRepository, logger *logger.Logger) *Service {
	return &Service{
		userRepo:  userRepo,
		usageRepo: usageRepo,
		logger:    logger,
	}
}

// SetRateProvider sets an optional exchange rate provider for currency normalization.
func (s *Service) SetRateProvider(rp ExchangeRateProvider) {
	s.rateProvider = rp
}

// ValidateDeposit checks if a deposit amount is within user's limits
func (s *Service) ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error) {
	return s.ValidateDepositWithCurrency(ctx, userID, amount, "USD")
}

// ValidateDepositWithCurrency checks deposit limits for a specific currency
func (s *Service) ValidateDepositWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) (*entities.LimitCheckResult, error) {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	// Block unverified users entirely
	if tier == entities.KYCTierUnverified {
		return &entities.LimitCheckResult{Allowed: false, Reason: "identity verification required"}, entities.ErrUnverifiedUser
	}

	config := entities.GetLimitConfigForTierAndCurrency(tier, currency)

	// Check minimum (in native currency)
	if amount.LessThan(config.MinDeposit) {
		return &entities.LimitCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("amount %s is below minimum deposit of %s", amount.String(), config.MinDeposit.String()),
		}, entities.ErrBelowMinimumDeposit
	}

	// Normalize amount to USD for unified usage tracking
	usageAmount := amount
	usdConfig := entities.GetLimitConfigForTierAndCurrency(tier, "USD")
	dailyLimit := usdConfig.DailyDepositLimit
	monthlyLimit := usdConfig.MonthlyDepositLimit
	if currency == "NGN" {
		rate := s.getNGNRate(ctx)
		if rate.IsPositive() {
			usageAmount = amount.Div(rate).Round(2)
		} else {
			dailyLimit = config.DailyDepositLimit
			monthlyLimit = config.MonthlyDepositLimit
		}
	} else {
		dailyLimit = config.DailyDepositLimit
		monthlyLimit = config.MonthlyDepositLimit
	}

	// Reset expired periods and get current usage
	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}

	usage, err := s.usageRepo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Check daily limit (usage stored in USD-equivalent)
	newDailyUsage := usage.DailyDepositUsed.Add(usageAmount)
	if newDailyUsage.GreaterThan(dailyLimit) {
		remaining := dailyLimit.Sub(usage.DailyDepositUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "daily deposit limit exceeded",
			CurrentUsage:      usage.DailyDepositUsed,
			Limit:             dailyLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.DailyDepositResetAt,
			LimitType:         "daily",
		}, entities.ErrDailyDepositExceeded
	}

	// Check monthly limit
	newMonthlyUsage := usage.MonthlyDepositUsed.Add(usageAmount)
	if newMonthlyUsage.GreaterThan(monthlyLimit) {
		remaining := monthlyLimit.Sub(usage.MonthlyDepositUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "monthly deposit limit exceeded",
			CurrentUsage:      usage.MonthlyDepositUsed,
			Limit:             monthlyLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.MonthlyDepositResetAt,
			LimitType:         "monthly",
		}, entities.ErrMonthlyDepositExceeded
	}

	// Calculate remaining capacity
	dailyRemaining := dailyLimit.Sub(usage.DailyDepositUsed)
	monthlyRemaining := monthlyLimit.Sub(usage.MonthlyDepositUsed)
	remaining := dailyRemaining
	resetsAt := usage.DailyDepositResetAt
	limitType := "daily"
	if monthlyRemaining.LessThan(dailyRemaining) {
		remaining = monthlyRemaining
		resetsAt = usage.MonthlyDepositResetAt
		limitType = "monthly"
	}

	return &entities.LimitCheckResult{
		Allowed:           true,
		CurrentUsage:      usage.DailyDepositUsed,
		Limit:             config.DailyDepositLimit,
		RemainingCapacity: remaining,
		ResetsAt:          resetsAt,
		LimitType:         limitType,
	}, nil
}

// ValidateWithdrawal checks if a withdrawal amount is within user's limits
func (s *Service) ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error) {
	return s.ValidateWithdrawalWithCurrency(ctx, userID, amount, "USD")
}

// ValidateWithdrawalWithCurrency checks withdrawal limits for a specific currency
func (s *Service) ValidateWithdrawalWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) (*entities.LimitCheckResult, error) {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	// Block unverified users entirely
	if tier == entities.KYCTierUnverified {
		return &entities.LimitCheckResult{Allowed: false, Reason: "identity verification required"}, entities.ErrUnverifiedUser
	}

	config := entities.GetLimitConfigForTierAndCurrency(tier, currency)

	// Check minimum (in native currency)
	if amount.LessThan(config.MinWithdrawal) {
		return &entities.LimitCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("amount %s is below minimum withdrawal of %s", amount.String(), config.MinWithdrawal.String()),
		}, entities.ErrBelowMinimumWithdrawal
	}

	// Normalize amount to USD for unified usage tracking
	usageAmount := amount
	usdConfig := entities.GetLimitConfigForTierAndCurrency(tier, "USD")
	dailyLimit := usdConfig.DailyWithdrawalLimit
	monthlyLimit := usdConfig.MonthlyWithdrawalLimit
	if currency == "NGN" {
		rate := s.getNGNRate(ctx)
		if rate.IsPositive() {
			usageAmount = amount.Div(rate).Round(2)
		} else {
			// Rate unavailable — use NGN-specific limits directly (no normalization)
			dailyLimit = config.DailyWithdrawalLimit
			monthlyLimit = config.MonthlyWithdrawalLimit
		}
	} else {
		dailyLimit = config.DailyWithdrawalLimit
		monthlyLimit = config.MonthlyWithdrawalLimit
	}

	// Reset expired periods and get current usage
	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}

	usage, err := s.usageRepo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Check daily limit (usage is stored in USD-equivalent)
	newDailyUsage := usage.DailyWithdrawalUsed.Add(usageAmount)
	if newDailyUsage.GreaterThan(dailyLimit) {
		remaining := dailyLimit.Sub(usage.DailyWithdrawalUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "daily withdrawal limit exceeded",
			CurrentUsage:      usage.DailyWithdrawalUsed,
			Limit:             dailyLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.DailyWithdrawalResetAt,
			LimitType:         "daily",
		}, entities.ErrDailyWithdrawalExceeded
	}

	// Check monthly limit
	newMonthlyUsage := usage.MonthlyWithdrawalUsed.Add(usageAmount)
	if newMonthlyUsage.GreaterThan(monthlyLimit) {
		remaining := monthlyLimit.Sub(usage.MonthlyWithdrawalUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "monthly withdrawal limit exceeded",
			CurrentUsage:      usage.MonthlyWithdrawalUsed,
			Limit:             monthlyLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.MonthlyWithdrawalResetAt,
			LimitType:         "monthly",
		}, entities.ErrMonthlyWithdrawalExceeded
	}

	dailyRemaining := dailyLimit.Sub(usage.DailyWithdrawalUsed)
	monthlyRemaining := monthlyLimit.Sub(usage.MonthlyWithdrawalUsed)
	remaining := dailyRemaining
	resetsAt := usage.DailyWithdrawalResetAt
	limitType := "daily"
	if monthlyRemaining.LessThan(dailyRemaining) {
		remaining = monthlyRemaining
		resetsAt = usage.MonthlyWithdrawalResetAt
		limitType = "monthly"
	}

	return &entities.LimitCheckResult{
		Allowed:           true,
		CurrentUsage:      usage.DailyWithdrawalUsed,
		Limit:             config.DailyWithdrawalLimit,
		RemainingCapacity: remaining,
		ResetsAt:          resetsAt,
		LimitType:         limitType,
	}, nil
}

// RecordDeposit atomically validates and records a deposit against user's limits.
func (s *Service) RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return s.RecordDepositWithCurrency(ctx, userID, amount, "USD")
}

// RecordDepositWithCurrency atomically validates and records a deposit for a specific currency.
func (s *Service) RecordDepositWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user tier: %w", err)
	}
	if tier == entities.KYCTierUnverified {
		return entities.ErrUnverifiedUser
	}

	// Normalize to USD-equivalent for unified usage tracking
	usageAmount := amount
	usdConfig := entities.GetLimitConfigForTierAndCurrency(tier, "USD")
	dailyLimit := usdConfig.DailyDepositLimit
	monthlyLimit := usdConfig.MonthlyDepositLimit
	if currency == "NGN" {
		rate := s.getNGNRate(ctx)
		if rate.IsPositive() {
			usageAmount = amount.Div(rate).Round(2)
		} else {
			ngnConfig := entities.GetLimitConfigForTierAndCurrency(tier, "NGN")
			dailyLimit = ngnConfig.DailyDepositLimit
			monthlyLimit = ngnConfig.MonthlyDepositLimit
		}
	}

	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}
	if _, err := s.usageRepo.GetOrCreate(ctx, userID); err != nil {
		return fmt.Errorf("failed to ensure usage record: %w", err)
	}
	return s.usageRepo.AtomicIncrementDeposit(ctx, userID, usageAmount, dailyLimit, monthlyLimit)
}

// RecordWithdrawal atomically validates and records a withdrawal against user's limits.
func (s *Service) RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return s.RecordWithdrawalWithCurrency(ctx, userID, amount, "USD")
}

// RecordWithdrawalWithCurrency atomically validates and records a withdrawal for a specific currency.
func (s *Service) RecordWithdrawalWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user tier: %w", err)
	}
	if tier == entities.KYCTierUnverified {
		return entities.ErrUnverifiedUser
	}

	// Normalize to USD-equivalent for unified usage tracking
	usageAmount := amount
	usdConfig := entities.GetLimitConfigForTierAndCurrency(tier, "USD")
	dailyLimit := usdConfig.DailyWithdrawalLimit
	monthlyLimit := usdConfig.MonthlyWithdrawalLimit
	if currency == "NGN" {
		rate := s.getNGNRate(ctx)
		if rate.IsPositive() {
			usageAmount = amount.Div(rate).Round(2)
		} else {
			// Rate unavailable — use NGN limits directly
			ngnConfig := entities.GetLimitConfigForTierAndCurrency(tier, "NGN")
			dailyLimit = ngnConfig.DailyWithdrawalLimit
			monthlyLimit = ngnConfig.MonthlyWithdrawalLimit
		}
	}

	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}
	if _, err := s.usageRepo.GetOrCreate(ctx, userID); err != nil {
		return fmt.Errorf("failed to ensure usage record: %w", err)
	}
	return s.usageRepo.AtomicIncrementWithdrawal(ctx, userID, usageAmount, dailyLimit, monthlyLimit)
}

// GetUserLimits returns the user's current limits and usage for a currency
func (s *Service) GetUserLimits(ctx context.Context, userID uuid.UUID) (*entities.UserLimitsResponse, error) {
	return s.GetUserLimitsForCurrency(ctx, userID, "USD")
}

// GetUserLimitsForCurrency returns currency-specific limits and usage
func (s *Service) GetUserLimitsForCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.UserLimitsResponse, error) {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	config := entities.GetLimitConfigForTierAndCurrency(tier, currency)

	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}

	usage, err := s.usageRepo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	dailyDepositRemaining := config.DailyDepositLimit.Sub(usage.DailyDepositUsed)
	monthlyDepositRemaining := config.MonthlyDepositLimit.Sub(usage.MonthlyDepositUsed)
	dailyWithdrawalRemaining := config.DailyWithdrawalLimit.Sub(usage.DailyWithdrawalUsed)
	monthlyWithdrawalRemaining := config.MonthlyWithdrawalLimit.Sub(usage.MonthlyWithdrawalUsed)

	return &entities.UserLimitsResponse{
		KYCTier:  tier,
		Currency: currency,
		Deposit: entities.LimitDetails{
			Minimum: config.MinDeposit.String(),
			Daily: entities.PeriodLimit{
				Limit:     config.DailyDepositLimit.String(),
				Used:      usage.DailyDepositUsed.String(),
				Remaining: dailyDepositRemaining.String(),
				ResetsAt:  usage.DailyDepositResetAt,
			},
			Monthly: entities.PeriodLimit{
				Limit:     config.MonthlyDepositLimit.String(),
				Used:      usage.MonthlyDepositUsed.String(),
				Remaining: monthlyDepositRemaining.String(),
				ResetsAt:  usage.MonthlyDepositResetAt,
			},
		},
		Withdrawal: entities.LimitDetails{
			Minimum: config.MinWithdrawal.String(),
			Daily: entities.PeriodLimit{
				Limit:     config.DailyWithdrawalLimit.String(),
				Used:      usage.DailyWithdrawalUsed.String(),
				Remaining: dailyWithdrawalRemaining.String(),
				ResetsAt:  usage.DailyWithdrawalResetAt,
			},
			Monthly: entities.PeriodLimit{
				Limit:     config.MonthlyWithdrawalLimit.String(),
				Used:      usage.MonthlyWithdrawalUsed.String(),
				Remaining: monthlyWithdrawalRemaining.String(),
				ResetsAt:  usage.MonthlyWithdrawalResetAt,
			},
		},
	}, nil
}

func (s *Service) getUserTier(ctx context.Context, userID uuid.UUID) (entities.KYCTier, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return entities.KYCTierUnverified, err
	}
	return entities.DeriveKYCTier(user.KYCStatus), nil
}

// NextDailyReset returns the next daily reset time (midnight UTC)
func NextDailyReset() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// NextMonthlyReset returns the next monthly reset time (first of next month UTC)
func NextMonthlyReset() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

// getNGNRate returns the current NGN/USD rate from the last cached PAJ rate.
// Returns zero if unavailable (caller should skip normalization).
func (s *Service) getNGNRate(ctx context.Context) decimal.Decimal {
	if s.rateProvider != nil {
		rate, err := s.rateProvider.GetNGNRate(ctx)
		if err == nil && rate.IsPositive() {
			return rate
		}
		s.logger.Warn("Failed to get NGN rate from cache", "error", err)
	}
	return decimal.Zero
}
