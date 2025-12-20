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
	"github.com/rail-service/rail_service/internal/domain/services/card"
)

// mockCardRepository implements card.CardRepository for testing
type mockCardRepository struct {
	cards        map[string]*entities.BridgeCard
	transactions map[string]*entities.BridgeCardTransaction
}

func newMockCardRepository() *mockCardRepository {
	return &mockCardRepository{
		cards:        make(map[string]*entities.BridgeCard),
		transactions: make(map[string]*entities.BridgeCardTransaction),
	}
}

func (m *mockCardRepository) Create(ctx context.Context, c *entities.BridgeCard) error {
	m.cards[c.BridgeCardID] = c
	return nil
}

func (m *mockCardRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeCard, error) {
	for _, c := range m.cards {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, nil
}

func (m *mockCardRepository) GetByBridgeCardID(ctx context.Context, bridgeCardID string) (*entities.BridgeCard, error) {
	return m.cards[bridgeCardID], nil
}

func (m *mockCardRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.BridgeCard, error) {
	var result []*entities.BridgeCard
	for _, c := range m.cards {
		if c.UserID == userID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *mockCardRepository) GetActiveVirtualCard(ctx context.Context, userID uuid.UUID) (*entities.BridgeCard, error) {
	for _, c := range m.cards {
		if c.UserID == userID && c.Status == entities.CardStatusActive && c.Type == entities.CardTypeVirtual {
			return c, nil
		}
	}
	return nil, nil
}

func (m *mockCardRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.CardStatus) error {
	for _, c := range m.cards {
		if c.ID == id {
			c.Status = status
			return nil
		}
	}
	return nil
}

func (m *mockCardRepository) CreateTransaction(ctx context.Context, tx *entities.BridgeCardTransaction) error {
	tx.ID = uuid.New()
	m.transactions[tx.BridgeTransID] = tx
	return nil
}

func (m *mockCardRepository) GetTransactionByBridgeID(ctx context.Context, bridgeTransID string) (*entities.BridgeCardTransaction, error) {
	return m.transactions[bridgeTransID], nil
}

func (m *mockCardRepository) GetTransactionsByCardID(ctx context.Context, cardID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	var result []*entities.BridgeCardTransaction
	for _, tx := range m.transactions {
		if tx.CardID == cardID {
			result = append(result, tx)
		}
	}
	return result, nil
}

func (m *mockCardRepository) GetTransactionsByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	var result []*entities.BridgeCardTransaction
	for _, tx := range m.transactions {
		if tx.UserID == userID {
			result = append(result, tx)
		}
	}
	return result, nil
}

func (m *mockCardRepository) UpdateTransactionStatus(ctx context.Context, id uuid.UUID, status string, declineReason *string) error {
	for _, tx := range m.transactions {
		if tx.ID == id {
			tx.Status = status
			tx.DeclineReason = declineReason
			return nil
		}
	}
	return nil
}

func (m *mockCardRepository) CountByUserID(ctx context.Context, userID uuid.UUID) (int, error) {
	count := 0
	for _, c := range m.cards {
		if c.UserID == userID {
			count++
		}
	}
	return count, nil
}

// mockCardBalanceProvider implements card.BalanceProvider for testing
type mockCardBalanceProvider struct {
	balance       decimal.Decimal
	deductCalled  bool
	deductAmount  decimal.Decimal
	deductRef     string
}

func (m *mockCardBalanceProvider) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return m.balance, nil
}

func (m *mockCardBalanceProvider) DeductSpendBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	m.deductCalled = true
	m.deductAmount = amount
	m.deductRef = reference
	m.balance = m.balance.Sub(amount)
	return nil
}

func TestCardService_ProcessCardAuthorization_Approved(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	repo := newMockCardRepository()
	balanceProvider := &mockCardBalanceProvider{balance: decimal.NewFromFloat(100)}

	userID := uuid.New()
	cardID := uuid.New()
	bridgeCardID := "bridge-card-123"

	// Create a card
	repo.cards[bridgeCardID] = &entities.BridgeCard{
		ID:           cardID,
		UserID:       userID,
		BridgeCardID: bridgeCardID,
		Status:       entities.CardStatusActive,
		Type:         entities.CardTypeVirtual,
	}

	svc := card.NewService(repo, nil, nil, nil, balanceProvider, zapLog)

	approved, reason, err := svc.ProcessCardAuthorization(
		context.Background(),
		bridgeCardID,
		decimal.NewFromFloat(50),
		"Test Merchant",
		"retail",
	)

	require.NoError(t, err)
	assert.True(t, approved, "Authorization should be approved")
	assert.Empty(t, reason, "No decline reason expected")
}

func TestCardService_ProcessCardAuthorization_InsufficientFunds(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	repo := newMockCardRepository()
	balanceProvider := &mockCardBalanceProvider{balance: decimal.NewFromFloat(10)}

	userID := uuid.New()
	cardID := uuid.New()
	bridgeCardID := "bridge-card-123"

	repo.cards[bridgeCardID] = &entities.BridgeCard{
		ID:           cardID,
		UserID:       userID,
		BridgeCardID: bridgeCardID,
		Status:       entities.CardStatusActive,
		Type:         entities.CardTypeVirtual,
	}

	svc := card.NewService(repo, nil, nil, nil, balanceProvider, zapLog)

	approved, reason, err := svc.ProcessCardAuthorization(
		context.Background(),
		bridgeCardID,
		decimal.NewFromFloat(50),
		"Test Merchant",
		"retail",
	)

	require.Error(t, err)
	assert.False(t, approved, "Authorization should be declined")
	assert.Equal(t, "insufficient_funds", reason)
}

func TestCardService_ProcessCardAuthorization_CardFrozen(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	repo := newMockCardRepository()
	balanceProvider := &mockCardBalanceProvider{balance: decimal.NewFromFloat(100)}

	userID := uuid.New()
	cardID := uuid.New()
	bridgeCardID := "bridge-card-123"

	repo.cards[bridgeCardID] = &entities.BridgeCard{
		ID:           cardID,
		UserID:       userID,
		BridgeCardID: bridgeCardID,
		Status:       entities.CardStatusFrozen,
		Type:         entities.CardTypeVirtual,
	}

	svc := card.NewService(repo, nil, nil, nil, balanceProvider, zapLog)

	approved, reason, err := svc.ProcessCardAuthorization(
		context.Background(),
		bridgeCardID,
		decimal.NewFromFloat(50),
		"Test Merchant",
		"retail",
	)

	require.Error(t, err)
	assert.False(t, approved, "Authorization should be declined")
	assert.Equal(t, "card_frozen", reason)
}

func TestCardService_RecordTransaction_DeductsBalance(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	repo := newMockCardRepository()
	balanceProvider := &mockCardBalanceProvider{balance: decimal.NewFromFloat(100)}

	userID := uuid.New()
	cardID := uuid.New()
	bridgeCardID := "bridge-card-123"
	transID := "trans-456"

	repo.cards[bridgeCardID] = &entities.BridgeCard{
		ID:           cardID,
		UserID:       userID,
		BridgeCardID: bridgeCardID,
		Status:       entities.CardStatusActive,
		Type:         entities.CardTypeVirtual,
		Currency:     "USD",
	}

	svc := card.NewService(repo, nil, nil, nil, balanceProvider, zapLog)

	err := svc.RecordTransaction(
		context.Background(),
		bridgeCardID,
		transID,
		"capture",
		decimal.NewFromFloat(25),
		"Coffee Shop",
		"food",
		"completed",
		nil,
	)

	require.NoError(t, err)
	assert.True(t, balanceProvider.deductCalled, "Balance should be deducted")
	assert.True(t, balanceProvider.deductAmount.Equal(decimal.NewFromFloat(25)), "Deducted amount should be 25")

	// Verify transaction was recorded
	tx, _ := repo.GetTransactionByBridgeID(context.Background(), transID)
	require.NotNil(t, tx)
	assert.Equal(t, "completed", tx.Status)
}

func TestCardService_RecordTransaction_PendingDoesNotDeduct(t *testing.T) {
	zapLog, _ := zap.NewDevelopment()
	repo := newMockCardRepository()
	balanceProvider := &mockCardBalanceProvider{balance: decimal.NewFromFloat(100)}

	userID := uuid.New()
	cardID := uuid.New()
	bridgeCardID := "bridge-card-123"
	transID := "trans-789"

	repo.cards[bridgeCardID] = &entities.BridgeCard{
		ID:           cardID,
		UserID:       userID,
		BridgeCardID: bridgeCardID,
		Status:       entities.CardStatusActive,
		Type:         entities.CardTypeVirtual,
		Currency:     "USD",
	}

	svc := card.NewService(repo, nil, nil, nil, balanceProvider, zapLog)

	err := svc.RecordTransaction(
		context.Background(),
		bridgeCardID,
		transID,
		"authorization",
		decimal.NewFromFloat(25),
		"Coffee Shop",
		"food",
		"pending",
		nil,
	)

	require.NoError(t, err)
	assert.False(t, balanceProvider.deductCalled, "Balance should NOT be deducted for pending transactions")
}
