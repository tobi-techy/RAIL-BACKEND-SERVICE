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
	ResetExpiredPeriods(ctx context.Context, userID uuid.UUID) error
}

// Service handles transaction limit validation
type Service struct {
	userRepo  UserRepository
	usageRepo UsageRepository
	logger    *logger.Logger
}

// NewService creates a new limits service
func NewService(userRepo UserRepository, usageRepo UsageRepository, logger *logger.Logger) *Service {
	return &Service{
		userRepo:  userRepo,
		usageRepo: usageRepo,
		logger:    logger,
	}
}

// ValidateDeposit checks if a deposit amount is within user's limits
func (s *Service) ValidateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (*entities.LimitCheckResult, error) {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	config := entities.GetLimitConfigForTier(tier)

	// Check minimum
	if amount.LessThan(config.MinDeposit) {
		return &entities.LimitCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("amount %s is below minimum deposit of %s", amount.String(), config.MinDeposit.String()),
		}, entities.ErrBelowMinimumDeposit
	}

	// Reset expired periods and get current usage
	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}

	usage, err := s.usageRepo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Check daily limit
	newDailyUsage := usage.DailyDepositUsed.Add(amount)
	if newDailyUsage.GreaterThan(config.DailyDepositLimit) {
		remaining := config.DailyDepositLimit.Sub(usage.DailyDepositUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "daily deposit limit exceeded",
			CurrentUsage:      usage.DailyDepositUsed,
			Limit:             config.DailyDepositLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.DailyDepositResetAt,
			LimitType:         "daily",
		}, entities.ErrDailyDepositExceeded
	}

	// Check monthly limit
	newMonthlyUsage := usage.MonthlyDepositUsed.Add(amount)
	if newMonthlyUsage.GreaterThan(config.MonthlyDepositLimit) {
		remaining := config.MonthlyDepositLimit.Sub(usage.MonthlyDepositUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "monthly deposit limit exceeded",
			CurrentUsage:      usage.MonthlyDepositUsed,
			Limit:             config.MonthlyDepositLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.MonthlyDepositResetAt,
			LimitType:         "monthly",
		}, entities.ErrMonthlyDepositExceeded
	}

	// Calculate remaining capacity (minimum of daily and monthly remaining)
	dailyRemaining := config.DailyDepositLimit.Sub(usage.DailyDepositUsed)
	monthlyRemaining := config.MonthlyDepositLimit.Sub(usage.MonthlyDepositUsed)
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
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	config := entities.GetLimitConfigForTier(tier)

	// Check minimum
	if amount.LessThan(config.MinWithdrawal) {
		return &entities.LimitCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("amount %s is below minimum withdrawal of %s", amount.String(), config.MinWithdrawal.String()),
		}, entities.ErrBelowMinimumWithdrawal
	}

	// Reset expired periods and get current usage
	if err := s.usageRepo.ResetExpiredPeriods(ctx, userID); err != nil {
		s.logger.Warn("Failed to reset expired periods", "error", err, "user_id", userID.String())
	}

	usage, err := s.usageRepo.GetOrCreate(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage: %w", err)
	}

	// Check daily limit
	newDailyUsage := usage.DailyWithdrawalUsed.Add(amount)
	if newDailyUsage.GreaterThan(config.DailyWithdrawalLimit) {
		remaining := config.DailyWithdrawalLimit.Sub(usage.DailyWithdrawalUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "daily withdrawal limit exceeded",
			CurrentUsage:      usage.DailyWithdrawalUsed,
			Limit:             config.DailyWithdrawalLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.DailyWithdrawalResetAt,
			LimitType:         "daily",
		}, entities.ErrDailyWithdrawalExceeded
	}

	// Check monthly limit
	newMonthlyUsage := usage.MonthlyWithdrawalUsed.Add(amount)
	if newMonthlyUsage.GreaterThan(config.MonthlyWithdrawalLimit) {
		remaining := config.MonthlyWithdrawalLimit.Sub(usage.MonthlyWithdrawalUsed)
		if remaining.LessThan(decimal.Zero) {
			remaining = decimal.Zero
		}
		return &entities.LimitCheckResult{
			Allowed:           false,
			Reason:            "monthly withdrawal limit exceeded",
			CurrentUsage:      usage.MonthlyWithdrawalUsed,
			Limit:             config.MonthlyWithdrawalLimit,
			RemainingCapacity: remaining,
			ResetsAt:          usage.MonthlyWithdrawalResetAt,
			LimitType:         "monthly",
		}, entities.ErrMonthlyWithdrawalExceeded
	}

	dailyRemaining := config.DailyWithdrawalLimit.Sub(usage.DailyWithdrawalUsed)
	monthlyRemaining := config.MonthlyWithdrawalLimit.Sub(usage.MonthlyWithdrawalUsed)
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

// RecordDeposit records a successful deposit against user's limits
func (s *Service) RecordDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return s.usageRepo.IncrementDepositUsage(ctx, userID, amount)
}

// RecordWithdrawal records a successful withdrawal against user's limits
func (s *Service) RecordWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	return s.usageRepo.IncrementWithdrawalUsage(ctx, userID, amount)
}

// GetUserLimits returns the user's current limits and usage
func (s *Service) GetUserLimits(ctx context.Context, userID uuid.UUID) (*entities.UserLimitsResponse, error) {
	tier, err := s.getUserTier(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user tier: %w", err)
	}

	config := entities.GetLimitConfigForTier(tier)

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
		KYCTier: tier,
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
