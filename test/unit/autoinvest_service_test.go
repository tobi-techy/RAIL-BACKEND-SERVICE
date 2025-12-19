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
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/strategy"
	"github.com/rail-service/rail_service/pkg/logger"
)

// mockLedgerService implements autoinvest.LedgerService for testing
type mockLedgerService struct {
	balance  decimal.Decimal
	accounts map[entities.AccountType]*entities.LedgerAccount
}

func newMockLedgerService(balance decimal.Decimal) *mockLedgerService {
	return &mockLedgerService{
		balance:  balance,
		accounts: make(map[entities.AccountType]*entities.LedgerAccount),
	}
}

func (m *mockLedgerService) GetAccountBalance(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (decimal.Decimal, error) {
	return m.balance, nil
}

func (m *mockLedgerService) CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error) {
	return &entities.LedgerTransaction{ID: uuid.New()}, nil
}

func (m *mockLedgerService) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	if acc, ok := m.accounts[accountType]; ok {
		return acc, nil
	}
	acc := &entities.LedgerAccount{ID: uuid.New(), AccountType: accountType}
	m.accounts[accountType] = acc
	return acc, nil
}

// mockOrderPlacer implements autoinvest.OrderPlacer for testing
type mockOrderPlacer struct {
	called bool
	orders []orderPlacerCall
}

type orderPlacerCall struct {
	userID uuid.UUID
	symbol string
	amount decimal.Decimal
}

func (m *mockOrderPlacer) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error) {
	m.called = true
	m.orders = append(m.orders, orderPlacerCall{
		userID: userID,
		symbol: symbol,
		amount: amount,
	})
	return &entities.AlpacaOrderResponse{
		ID:     uuid.New().String(),
		Symbol: symbol,
		Status: entities.AlpacaOrderStatusNew,
	}, nil
}

// mockStrategyEngine implements autoinvest.StrategyEngine for testing
type mockStrategyEngine struct {
	result *strategy.StrategyResult
}

func (m *mockStrategyEngine) GetStrategy(ctx context.Context, userID uuid.UUID) (*strategy.StrategyResult, error) {
	if m.result != nil {
		return m.result, nil
	}
	return &strategy.StrategyResult{
		StrategyName: "Test Strategy",
		Allocations: []strategy.Allocation{
			{Symbol: "SPY", Weight: decimal.NewFromInt(60)},
			{Symbol: "QQQ", Weight: decimal.NewFromInt(40)},
		},
	}, nil
}

func testLogger() *logger.Logger {
	zapLog, _ := zap.NewDevelopment()
	return logger.NewLogger(zapLog)
}

func TestAutoInvestService_SetOrderPlacer(t *testing.T) {
	log := testLogger()
	ledger := newMockLedgerService(decimal.NewFromFloat(100))

	// Create service without order placer
	svc := autoinvest.NewService(ledger, nil, autoinvest.Config{}, log)

	// Set order placer after initialization
	orderPlacer := &mockOrderPlacer{}
	svc.SetOrderPlacer(orderPlacer)

	// Trigger auto-investment
	err := svc.TriggerAutoInvestment(context.Background(), autoinvest.TriggerRequest{
		UserID:        uuid.New(),
		StashID:       uuid.New(),
		CorrelationID: "test-correlation-id",
	})

	require.NoError(t, err)
	assert.True(t, orderPlacer.called, "OrderPlacer should have been called")
	assert.Equal(t, "SPY", orderPlacer.orders[0].symbol)
}

func TestAutoInvestService_TriggerAutoInvestment_BelowThreshold(t *testing.T) {
	log := testLogger()
	ledger := newMockLedgerService(decimal.NewFromFloat(5)) // Below default threshold

	orderPlacer := &mockOrderPlacer{}
	config := autoinvest.Config{
		MinThreshold: decimal.NewFromFloat(10),
	}

	svc := autoinvest.NewService(ledger, orderPlacer, config, log)

	err := svc.TriggerAutoInvestment(context.Background(), autoinvest.TriggerRequest{
		UserID:        uuid.New(),
		StashID:       uuid.New(),
		CorrelationID: "test-correlation-id",
	})

	require.NoError(t, err)
	assert.False(t, orderPlacer.called, "OrderPlacer should not be called when below threshold")
}

func TestAutoInvestService_TriggerAutoInvestment_RequiresCorrelationID(t *testing.T) {
	log := testLogger()
	ledger := newMockLedgerService(decimal.NewFromFloat(100))
	orderPlacer := &mockOrderPlacer{}

	svc := autoinvest.NewService(ledger, orderPlacer, autoinvest.Config{}, log)

	err := svc.TriggerAutoInvestment(context.Background(), autoinvest.TriggerRequest{
		UserID:        uuid.New(),
		StashID:       uuid.New(),
		CorrelationID: "", // Empty correlation ID
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "correlation_id is required")
}

func TestAutoInvestService_WithStrategyEngine(t *testing.T) {
	log := testLogger()
	ledger := newMockLedgerService(decimal.NewFromFloat(100))
	orderPlacer := &mockOrderPlacer{}
	strategyEngine := &mockStrategyEngine{
		result: &strategy.StrategyResult{
			StrategyName: "Test Multi-Asset",
			Allocations: []strategy.Allocation{
				{Symbol: "SPY", Weight: decimal.NewFromInt(60)},
				{Symbol: "QQQ", Weight: decimal.NewFromInt(40)},
			},
		},
	}

	svc := autoinvest.NewService(ledger, orderPlacer, autoinvest.Config{}, log)
	svc.SetStrategyEngine(strategyEngine)

	err := svc.TriggerAutoInvestment(context.Background(), autoinvest.TriggerRequest{
		UserID:        uuid.New(),
		StashID:       uuid.New(),
		CorrelationID: "test-correlation-id",
	})

	require.NoError(t, err)
	assert.True(t, orderPlacer.called, "OrderPlacer should have been called")
	assert.Len(t, orderPlacer.orders, 2, "Should have placed 2 orders for 2 allocations")

	// Verify allocation amounts
	var spyOrder, qqqOrder *orderPlacerCall
	for i := range orderPlacer.orders {
		if orderPlacer.orders[i].symbol == "SPY" {
			spyOrder = &orderPlacer.orders[i]
		} else if orderPlacer.orders[i].symbol == "QQQ" {
			qqqOrder = &orderPlacer.orders[i]
		}
	}

	require.NotNil(t, spyOrder, "SPY order should exist")
	require.NotNil(t, qqqOrder, "QQQ order should exist")
	assert.True(t, spyOrder.amount.Equal(decimal.NewFromFloat(60)), "SPY should get 60% of 100 = 60")
	assert.True(t, qqqOrder.amount.Equal(decimal.NewFromFloat(40)), "QQQ should get 40% of 100 = 40")
}

// mockUserProfileProvider implements strategy.UserProfileProvider for testing
type mockUserProfileProvider struct {
	profile *entities.UserProfile
}

func (m *mockUserProfileProvider) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	return m.profile, nil
}

func TestStrategyEngine_AggressiveGrowthForYoungUser(t *testing.T) {
	log := testLogger()

	// User is 22 years old
	dob := time.Now().AddDate(-22, 0, 0)
	provider := &mockUserProfileProvider{
		profile: &entities.UserProfile{
			ID:          uuid.New(),
			DateOfBirth: &dob,
		},
	}

	engine := strategy.NewEngine(provider, log)
	result, err := engine.GetStrategy(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Aggressive Growth", result.StrategyName)
	assert.Len(t, result.Allocations, 3)
}

func TestStrategyEngine_BalancedGrowthForMidAgeUser(t *testing.T) {
	log := testLogger()

	// User is 35 years old
	dob := time.Now().AddDate(-35, 0, 0)
	provider := &mockUserProfileProvider{
		profile: &entities.UserProfile{
			ID:          uuid.New(),
			DateOfBirth: &dob,
		},
	}

	engine := strategy.NewEngine(provider, log)
	result, err := engine.GetStrategy(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Balanced Growth", result.StrategyName)
}

func TestStrategyEngine_ConservativeForMatureUser(t *testing.T) {
	log := testLogger()

	// User is 50 years old
	dob := time.Now().AddDate(-50, 0, 0)
	provider := &mockUserProfileProvider{
		profile: &entities.UserProfile{
			ID:          uuid.New(),
			DateOfBirth: &dob,
		},
	}

	engine := strategy.NewEngine(provider, log)
	result, err := engine.GetStrategy(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Conservative", result.StrategyName)
}

func TestStrategyEngine_FallbackWhenNoAge(t *testing.T) {
	log := testLogger()

	// User has no date of birth
	provider := &mockUserProfileProvider{
		profile: &entities.UserProfile{
			ID:          uuid.New(),
			DateOfBirth: nil,
		},
	}

	engine := strategy.NewEngine(provider, log)
	result, err := engine.GetStrategy(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Global Fallback", result.StrategyName)
}
