package unit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
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
	called    bool
	lastOrder *orderPlacerCall
}

type orderPlacerCall struct {
	userID uuid.UUID
	symbol string
	amount decimal.Decimal
}

func (m *mockOrderPlacer) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error) {
	m.called = true
	m.lastOrder = &orderPlacerCall{
		userID: userID,
		symbol: symbol,
		amount: amount,
	}
	return &entities.AlpacaOrderResponse{
		ID:     uuid.New().String(),
		Symbol: symbol,
		Status: entities.AlpacaOrderStatusNew,
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
	assert.Equal(t, "SPY", orderPlacer.lastOrder.symbol)
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
