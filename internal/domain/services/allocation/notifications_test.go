package allocation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockNotificationService is a mock implementation of NotificationService
type MockNotificationService struct {
	mock.Mock
}

func (m *MockNotificationService) Send(ctx context.Context, notification *entities.Notification, prefs *entities.UserPreference) error {
	args := m.Called(ctx, notification, prefs)
	return args.Error(0)
}

func TestDefaultNotificationThresholds(t *testing.T) {
	thresholds := DefaultNotificationThresholds()
	
	assert.Equal(t, decimal.NewFromFloat(0.80), thresholds.Warning)
	assert.Equal(t, decimal.NewFromFloat(0.95), thresholds.Critical)
	assert.Equal(t, decimal.NewFromInt(1), thresholds.Depleted)
}

func TestCheckAndNotifyThresholds_NoSpending(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	// No spending allocation
	err := nm.CheckAndNotifyThresholds(ctx, userID, decimal.NewFromInt(100), decimal.Zero, decimal.Zero)
	
	assert.NoError(t, err)
	mockService.AssertNotCalled(t, "Send")
}

func TestCheckAndNotifyThresholds_80PercentWarning(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	// 80% spending threshold
	totalSpending := decimal.NewFromInt(100)
	spendingUsed := decimal.NewFromInt(80)
	spendingBalance := decimal.NewFromInt(20)
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Spending Limit Warning" &&
			n.Priority == entities.PriorityMedium &&
			n.Data["threshold_type"] == "80_percent"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.CheckAndNotifyThresholds(ctx, userID, spendingBalance, spendingUsed, totalSpending)
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestCheckAndNotifyThresholds_95PercentCritical(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	// 95% spending threshold
	totalSpending := decimal.NewFromInt(100)
	spendingUsed := decimal.NewFromInt(95)
	spendingBalance := decimal.NewFromInt(5)
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Spending Limit Critical" &&
			n.Priority == entities.PriorityHigh &&
			n.Data["threshold_type"] == "95_percent"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.CheckAndNotifyThresholds(ctx, userID, spendingBalance, spendingUsed, totalSpending)
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestCheckAndNotifyThresholds_100PercentDepleted(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	// 100% spending threshold
	totalSpending := decimal.NewFromInt(100)
	spendingUsed := decimal.NewFromInt(100)
	spendingBalance := decimal.Zero
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Spending Limit Reached" &&
			n.Priority == entities.PriorityCritical &&
			n.Data["threshold_type"] == "100_percent"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.CheckAndNotifyThresholds(ctx, userID, spendingBalance, spendingUsed, totalSpending)
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestCheckAndNotifyThresholds_BelowWarningThreshold(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	// 50% spending - below warning threshold
	totalSpending := decimal.NewFromInt(100)
	spendingUsed := decimal.NewFromInt(50)
	spendingBalance := decimal.NewFromInt(50)
	
	err := nm.CheckAndNotifyThresholds(ctx, userID, spendingBalance, spendingUsed, totalSpending)
	
	assert.NoError(t, err)
	mockService.AssertNotCalled(t, "Send")
}

func TestNotifyTransactionDeclined(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	amount := decimal.NewFromInt(50)
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Transaction Declined" &&
			n.Priority == entities.PriorityCritical &&
			n.Data["transaction_type"] == "withdrawal" &&
			n.Data["reason"] == "spending_limit_reached"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.NotifyTransactionDeclined(ctx, userID, amount, "withdrawal")
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestNotifyModeEnabled(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	spendingRatio := decimal.NewFromFloat(0.70)
	stashRatio := decimal.NewFromFloat(0.30)
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Smart Allocation Enabled" &&
			n.Priority == entities.PriorityMedium &&
			n.Data["mode_status"] == "enabled"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.NotifyModeEnabled(ctx, userID, spendingRatio, stashRatio)
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestNotifyModePaused(t *testing.T) {
	mockService := new(MockNotificationService)
	log := logger.NewLogger("test", "debug")
	nm := NewNotificationManager(mockService, log)
	
	ctx := context.Background()
	userID := uuid.New()
	
	mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
		return n.Title == "Smart Allocation Paused" &&
			n.Priority == entities.PriorityLow &&
			n.Data["mode_status"] == "paused"
	}), (*entities.UserPreference)(nil)).Return(nil)
	
	err := nm.NotifyModePaused(ctx, userID)
	
	assert.NoError(t, err)
	mockService.AssertExpectations(t)
}

func TestNotificationManager_TableDrivenThresholds(t *testing.T) {
	testCases := []struct {
		name              string
		spendingUsed      decimal.Decimal
		totalSpending     decimal.Decimal
		expectedTitle     string
		expectedPriority  entities.NotificationPriority
		shouldNotify      bool
	}{
		{
			name:              "Below warning - 50%",
			spendingUsed:      decimal.NewFromInt(50),
			totalSpending:     decimal.NewFromInt(100),
			shouldNotify:      false,
		},
		{
			name:              "At warning - 80%",
			spendingUsed:      decimal.NewFromInt(80),
			totalSpending:     decimal.NewFromInt(100),
			expectedTitle:     "Spending Limit Warning",
			expectedPriority:  entities.PriorityMedium,
			shouldNotify:      true,
		},
		{
			name:              "Between warning and critical - 90%",
			spendingUsed:      decimal.NewFromInt(90),
			totalSpending:     decimal.NewFromInt(100),
			expectedTitle:     "Spending Limit Warning",
			expectedPriority:  entities.PriorityMedium,
			shouldNotify:      true,
		},
		{
			name:              "At critical - 95%",
			spendingUsed:      decimal.NewFromInt(95),
			totalSpending:     decimal.NewFromInt(100),
			expectedTitle:     "Spending Limit Critical",
			expectedPriority:  entities.PriorityHigh,
			shouldNotify:      true,
		},
		{
			name:              "Between critical and depleted - 98%",
			spendingUsed:      decimal.NewFromInt(98),
			totalSpending:     decimal.NewFromInt(100),
			expectedTitle:     "Spending Limit Critical",
			expectedPriority:  entities.PriorityHigh,
			shouldNotify:      true,
		},
		{
			name:              "At depleted - 100%",
			spendingUsed:      decimal.NewFromInt(100),
			totalSpending:     decimal.NewFromInt(100),
			expectedTitle:     "Spending Limit Reached",
			expectedPriority:  entities.PriorityCritical,
			shouldNotify:      true,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockService := new(MockNotificationService)
			log := logger.NewLogger("test", "debug")
			nm := NewNotificationManager(mockService, log)
			
			ctx := context.Background()
			userID := uuid.New()
			spendingBalance := tc.totalSpending.Sub(tc.spendingUsed)
			
			if tc.shouldNotify {
				mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
					return n.Title == tc.expectedTitle && n.Priority == tc.expectedPriority
				}), (*entities.UserPreference)(nil)).Return(nil)
			}
			
			err := nm.CheckAndNotifyThresholds(ctx, userID, spendingBalance, tc.spendingUsed, tc.totalSpending)
			
			assert.NoError(t, err)
			
			if tc.shouldNotify {
				mockService.AssertExpectations(t)
			} else {
				mockService.AssertNotCalled(t, "Send")
			}
		})
	}
}

func TestNotificationManager_DifferentTransactionTypes(t *testing.T) {
	testCases := []struct {
		name            string
		transactionType string
	}{
		{"Withdrawal declined", "withdrawal"},
		{"Investment declined", "investment"},
		{"Transfer declined", "transfer"},
		{"Payment declined", "payment"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockService := new(MockNotificationService)
			log := logger.NewLogger("test", "debug")
			nm := NewNotificationManager(mockService, log)
			
			ctx := context.Background()
			userID := uuid.New()
			amount := decimal.NewFromInt(100)
			
			mockService.On("Send", ctx, mock.MatchedBy(func(n *entities.Notification) bool {
				return n.Data["transaction_type"] == tc.transactionType
			}), (*entities.UserPreference)(nil)).Return(nil)
			
			err := nm.NotifyTransactionDeclined(ctx, userID, amount, tc.transactionType)
			
			assert.NoError(t, err)
			mockService.AssertExpectations(t)
		})
	}
}
