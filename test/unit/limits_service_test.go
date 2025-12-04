package unit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	"github.com/rail-service/rail_service/pkg/logger"
)

// MockUserRepository implements limits.UserRepository for testing
type MockUserRepository struct {
	users map[uuid.UUID]*entities.UserProfile
}

func NewMockUserRepository() *MockUserRepository {
	return &MockUserRepository{
		users: make(map[uuid.UUID]*entities.UserProfile),
	}
}

func (m *MockUserRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if user, ok := m.users[id]; ok {
		return user, nil
	}
	return nil, nil
}

func (m *MockUserRepository) AddUser(user *entities.UserProfile) {
	m.users[user.ID] = user
}

// MockUsageRepository implements limits.UsageRepository for testing
type MockUsageRepository struct {
	usage map[uuid.UUID]*entities.UserTransactionUsage
}

func NewMockUsageRepository() *MockUsageRepository {
	return &MockUsageRepository{
		usage: make(map[uuid.UUID]*entities.UserTransactionUsage),
	}
}

func (m *MockUsageRepository) GetOrCreate(ctx context.Context, userID uuid.UUID) (*entities.UserTransactionUsage, error) {
	if usage, ok := m.usage[userID]; ok {
		return usage, nil
	}
	
	now := time.Now().UTC()
	usage := &entities.UserTransactionUsage{
		ID:                       uuid.New(),
		UserID:                   userID,
		DailyDepositUsed:         decimal.Zero,
		DailyDepositResetAt:      limits.NextDailyReset(),
		MonthlyDepositUsed:       decimal.Zero,
		MonthlyDepositResetAt:    limits.NextMonthlyReset(),
		DailyWithdrawalUsed:      decimal.Zero,
		DailyWithdrawalResetAt:   limits.NextDailyReset(),
		MonthlyWithdrawalUsed:    decimal.Zero,
		MonthlyWithdrawalResetAt: limits.NextMonthlyReset(),
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	m.usage[userID] = usage
	return usage, nil
}

func (m *MockUsageRepository) IncrementDepositUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	usage, _ := m.GetOrCreate(ctx, userID)
	usage.DailyDepositUsed = usage.DailyDepositUsed.Add(amount)
	usage.MonthlyDepositUsed = usage.MonthlyDepositUsed.Add(amount)
	return nil
}

func (m *MockUsageRepository) IncrementWithdrawalUsage(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	usage, _ := m.GetOrCreate(ctx, userID)
	usage.DailyWithdrawalUsed = usage.DailyWithdrawalUsed.Add(amount)
	usage.MonthlyWithdrawalUsed = usage.MonthlyWithdrawalUsed.Add(amount)
	return nil
}

func (m *MockUsageRepository) ResetExpiredPeriods(ctx context.Context, userID uuid.UUID) error {
	return nil
}

func TestLimitsService_ValidateDeposit_BelowMinimum(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "approved",
	})
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	// Test deposit below minimum
	result, err := svc.ValidateDeposit(context.Background(), userID, decimal.NewFromFloat(0.50))
	
	assert.Error(t, err)
	assert.ErrorIs(t, err, entities.ErrBelowMinimumDeposit)
	assert.False(t, result.Allowed)
}

func TestLimitsService_ValidateDeposit_WithinLimits(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "approved", // Basic KYC tier
	})
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	// Test deposit within limits
	result, err := svc.ValidateDeposit(context.Background(), userID, decimal.NewFromFloat(1000.00))
	
	require.NoError(t, err)
	assert.True(t, result.Allowed)
}

func TestLimitsService_ValidateDeposit_ExceedsDailyLimit(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "approved", // Basic KYC tier - $5000 daily limit
	})
	
	// Pre-populate usage near the limit
	usage, _ := usageRepo.GetOrCreate(context.Background(), userID)
	usage.DailyDepositUsed = decimal.NewFromFloat(4500.00)
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	// Test deposit that would exceed daily limit
	result, err := svc.ValidateDeposit(context.Background(), userID, decimal.NewFromFloat(1000.00))
	
	assert.Error(t, err)
	assert.ErrorIs(t, err, entities.ErrDailyDepositExceeded)
	assert.False(t, result.Allowed)
	assert.Equal(t, "daily", result.LimitType)
}

func TestLimitsService_ValidateWithdrawal_BelowMinimum(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "approved",
	})
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	// Test withdrawal below minimum ($10)
	result, err := svc.ValidateWithdrawal(context.Background(), userID, decimal.NewFromFloat(5.00))
	
	assert.Error(t, err)
	assert.ErrorIs(t, err, entities.ErrBelowMinimumWithdrawal)
	assert.False(t, result.Allowed)
}

func TestLimitsService_GetUserLimits(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "approved", // Basic KYC tier
	})
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	limits, err := svc.GetUserLimits(context.Background(), userID)
	
	require.NoError(t, err)
	assert.Equal(t, entities.KYCTierBasic, limits.KYCTier)
	assert.Equal(t, "1", limits.Deposit.Minimum)
	assert.Equal(t, "5000", limits.Deposit.Daily.Limit)
	assert.Equal(t, "25000", limits.Deposit.Monthly.Limit)
	assert.Equal(t, "10", limits.Withdrawal.Minimum)
	assert.Equal(t, "2500", limits.Withdrawal.Daily.Limit)
}

func TestLimitsService_UnverifiedUserLimits(t *testing.T) {
	userRepo := NewMockUserRepository()
	usageRepo := NewMockUsageRepository()
	zapLog, _ := zap.NewDevelopment()
	log := logger.NewLogger(zapLog)
	
	userID := uuid.New()
	userRepo.AddUser(&entities.UserProfile{
		ID:        userID,
		KYCStatus: "pending", // Unverified
	})
	
	svc := limits.NewService(userRepo, usageRepo, log)
	
	limits, err := svc.GetUserLimits(context.Background(), userID)
	
	require.NoError(t, err)
	assert.Equal(t, entities.KYCTierUnverified, limits.KYCTier)
	assert.Equal(t, "100", limits.Deposit.Daily.Limit)    // Much lower for unverified
	assert.Equal(t, "500", limits.Deposit.Monthly.Limit)
}

func TestDeriveKYCTier(t *testing.T) {
	tests := []struct {
		status   string
		expected entities.KYCTier
	}{
		{"approved", entities.KYCTierBasic},
		{"verified", entities.KYCTierBasic},
		{"advanced_approved", entities.KYCTierAdvanced},
		{"advanced_verified", entities.KYCTierAdvanced},
		{"pending", entities.KYCTierUnverified},
		{"rejected", entities.KYCTierUnverified},
		{"", entities.KYCTierUnverified},
	}
	
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			tier := entities.DeriveKYCTier(tt.status)
			assert.Equal(t, tt.expected, tier)
		})
	}
}
