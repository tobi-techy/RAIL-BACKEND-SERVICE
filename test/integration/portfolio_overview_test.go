package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	ErrBalanceNotFound = errors.New("balance not found")
)

// Mock repositories
type mockBalanceRepo struct {
	balances map[uuid.UUID]*entities.Balance
}

func (m *mockBalanceRepo) Get(ctx context.Context, userID uuid.UUID) (*entities.Balance, error) {
	if balance, exists := m.balances[userID]; exists {
		return balance, nil
	}
	return nil, ErrBalanceNotFound
}

func (m *mockBalanceRepo) DeductBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	if balance, exists := m.balances[userID]; exists {
		balance.BuyingPower = balance.BuyingPower.Sub(amount)
		balance.UpdatedAt = time.Now()
		return nil
	}
	return ErrBalanceNotFound
}

func (m *mockBalanceRepo) AddBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	if balance, exists := m.balances[userID]; exists {
		balance.BuyingPower = balance.BuyingPower.Add(amount)
		balance.UpdatedAt = time.Now()
		return nil
	}
	return ErrBalanceNotFound
}

func (m *mockBalanceRepo) UpdateBuyingPower(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	if _, exists := m.balances[userID]; !exists {
		m.balances[userID] = &entities.Balance{
			UserID:      userID,
			BuyingPower: decimal.Zero,
			Currency:    "USD",
			UpdatedAt:   time.Now(),
		}
	}
	return m.AddBuyingPower(ctx, userID, amount)
}

type mockPositionRepo struct {
	positions map[uuid.UUID][]*entities.Position
}

func (m *mockPositionRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.Position, error) {
	if positions, exists := m.positions[userID]; exists {
		return positions, nil
	}
	return []*entities.Position{}, nil
}

func (m *mockPositionRepo) CreateOrUpdate(ctx context.Context, position *entities.Position) error {
	if _, exists := m.positions[position.UserID]; !exists {
		m.positions[position.UserID] = []*entities.Position{}
	}
	m.positions[position.UserID] = append(m.positions[position.UserID], position)
	return nil
}

func (m *mockPositionRepo) GetByUserAndBasket(ctx context.Context, userID, basketID uuid.UUID) (*entities.Position, error) {
	if positions, exists := m.positions[userID]; exists {
		for _, pos := range positions {
			if pos.BasketID == basketID {
				return pos, nil
			}
		}
	}
	return nil, investing.ErrPositionNotFound
}

// TestPortfolioOverviewNilServiceHandling tests that handlers properly handle nil services
func TestPortfolioOverviewNilServiceHandling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := logger.New("debug", "test")

	// Create handler with nil investing service
	handler := handlers.NewStackHandlers(nil, nil, log)

	// Setup router
	router := gin.New()
	router.GET("/api/v1/portfolio/overview", func(c *gin.Context) {
		// Simulate authentication middleware setting user_id
		c.Set("user_id", uuid.New())
		c.Next()
	}, handler.GetPortfolioOverview)

	// Make request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Verify we get a proper error response, not a panic
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	
	var errorResp entities.ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errorResp)
	require.NoError(t, err)
	assert.Equal(t, "SERVICE_UNAVAILABLE", errorResp.Code)
	assert.Contains(t, errorResp.Message, "not available")
}

// TestPortfolioOverviewReflectsBalanceUpdates tests that portfolio overview reflects real-time balance updates
func TestPortfolioOverviewReflectsBalanceUpdates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := logger.New("debug", "test")

	userID := uuid.New()

	// Create mock repositories
	balanceRepo := &mockBalanceRepo{
		balances: map[uuid.UUID]*entities.Balance{
			userID: {
				UserID:      userID,
				BuyingPower: decimal.NewFromInt(1000),
				Currency:    "USD",
				UpdatedAt:   time.Now(),
			},
		},
	}

	positionRepo := &mockPositionRepo{
		positions: map[uuid.UUID][]*entities.Position{
			userID: {
				{
					ID:          uuid.New(),
					UserID:      userID,
					BasketID:    uuid.New(),
					Quantity:    decimal.NewFromInt(10),
					AvgPrice:    decimal.NewFromInt(50),
					MarketValue: decimal.NewFromInt(550),
					UpdatedAt:   time.Now(),
				},
			},
		},
	}

	// Create investing service
	investingService := investing.NewService(
		nil, // basket repo not needed for this test
		nil, // order repo not needed for this test
		positionRepo,
		balanceRepo,
		nil, // brokerage adapter not needed for this test
		log,
	)

	// Create handler
	handler := handlers.NewStackHandlers(nil, investingService, log)

	// Setup router
	router := gin.New()
	router.GET("/api/v1/portfolio/overview", func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Next()
	}, handler.GetPortfolioOverview)

	// Test 1: Initial portfolio state
	t.Run("InitialState", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var overview entities.PortfolioOverview
		err := json.Unmarshal(w.Body.Bytes(), &overview)
		require.NoError(t, err)

		assert.Equal(t, "1000.00", overview.BuyingPower)
		assert.Equal(t, "550.00", overview.PositionsValue)
		assert.Equal(t, "1550.00", overview.TotalPortfolio)
	})

	// Test 2: Simulate deposit - update buying power
	t.Run("AfterDeposit", func(t *testing.T) {
		// Simulate a deposit of 500 USD
		depositAmount := decimal.NewFromInt(500)
		err := balanceRepo.UpdateBuyingPower(context.Background(), userID, depositAmount)
		require.NoError(t, err)

		// Fetch portfolio overview again
		req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var overview entities.PortfolioOverview
		err = json.Unmarshal(w.Body.Bytes(), &overview)
		require.NoError(t, err)

		// Verify buying power and total portfolio are updated
		assert.Equal(t, "1500.00", overview.BuyingPower) // 1000 + 500
		assert.Equal(t, "550.00", overview.PositionsValue)
		assert.Equal(t, "2050.00", overview.TotalPortfolio) // 1500 + 550
	})

	// Test 3: Simulate another deposit
	t.Run("AfterSecondDeposit", func(t *testing.T) {
		// Simulate another deposit of 1000 USD
		depositAmount := decimal.NewFromInt(1000)
		err := balanceRepo.UpdateBuyingPower(context.Background(), userID, depositAmount)
		require.NoError(t, err)

		// Fetch portfolio overview again
		req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var overview entities.PortfolioOverview
		err = json.Unmarshal(w.Body.Bytes(), &overview)
		require.NoError(t, err)

		// Verify buying power and total portfolio are updated again
		assert.Equal(t, "2500.00", overview.BuyingPower) // 1500 + 1000
		assert.Equal(t, "550.00", overview.PositionsValue)
		assert.Equal(t, "3050.00", overview.TotalPortfolio) // 2500 + 550
	})
}

// TestPortfolioOverviewNewUser tests portfolio overview for a user with no balance yet
func TestPortfolioOverviewNewUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := logger.New("debug", "test")

	userID := uuid.New()

	// Create empty mock repositories
	balanceRepo := &mockBalanceRepo{
		balances: map[uuid.UUID]*entities.Balance{},
	}

	positionRepo := &mockPositionRepo{
		positions: map[uuid.UUID][]*entities.Position{},
	}

	// Create investing service
	investingService := investing.NewService(
		nil,
		nil,
		positionRepo,
		balanceRepo,
		nil,
		log,
	)

	// Create handler
	handler := handlers.NewStackHandlers(nil, investingService, log)

	// Setup router
	router := gin.New()
	router.GET("/api/v1/portfolio/overview", func(c *gin.Context) {
		c.Set("user_id", userID)
		c.Next()
	}, handler.GetPortfolioOverview)

	// Make request
	req := httptest.NewRequest(http.MethodGet, "/api/v1/portfolio/overview", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var overview entities.PortfolioOverview
	err := json.Unmarshal(w.Body.Bytes(), &overview)
	require.NoError(t, err)

	// Verify zero balances for new user
	assert.Equal(t, "0.00", overview.BuyingPower)
	assert.Equal(t, "0.00", overview.PositionsValue)
	assert.Equal(t, "0.00", overview.TotalPortfolio)
	assert.Equal(t, 0.0, overview.PerformanceLast30d)
}
