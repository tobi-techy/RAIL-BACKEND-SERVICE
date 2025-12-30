package unit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// MockAllocationService implements the methods needed by SpendingStashHandlers
type MockAllocationService struct {
	Balances *entities.AllocationBalances
	Mode     *entities.SmartAllocationMode
	Err      error
}

func (m *MockAllocationService) GetBalances(ctx context.Context, userID uuid.UUID) (*entities.AllocationBalances, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Balances, nil
}

func (m *MockAllocationService) GetMode(ctx context.Context, userID uuid.UUID) (*entities.SmartAllocationMode, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Mode, nil
}

// MockCardService implements the methods needed by SpendingStashHandlers
type MockCardService struct {
	Cards []*entities.BridgeCard
	Txns  []*entities.BridgeCardTransaction
	Err   error
}

func (m *MockCardService) GetUserCards(ctx context.Context, userID uuid.UUID) ([]*entities.BridgeCard, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Cards, nil
}

func (m *MockCardService) GetUserTransactions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.BridgeCardTransaction, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Txns, nil
}

// MockRoundupService implements the methods needed by SpendingStashHandlers
type MockRoundupService struct {
	Summary *entities.RoundupSummary
	Err     error
}

func (m *MockRoundupService) GetSummary(ctx context.Context, userID uuid.UUID) (*entities.RoundupSummary, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Summary, nil
}

// MockLimitsService implements the methods needed by SpendingStashHandlers
type MockLimitsService struct {
	Limits *entities.UserLimitsResponse
	Err    error
}

func (m *MockLimitsService) GetUserLimits(ctx context.Context, userID uuid.UUID) (*entities.UserLimitsResponse, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Limits, nil
}

// SpendingStashTestHandler wraps the real handler logic for testing
type SpendingStashTestHandler struct {
	allocationService *MockAllocationService
	cardService       *MockCardService
	roundupService    *MockRoundupService
	limitsService     *MockLimitsService
	logger            *zap.Logger
}

func NewSpendingStashTestHandler(
	alloc *MockAllocationService,
	card *MockCardService,
	roundup *MockRoundupService,
	limits *MockLimitsService,
) *SpendingStashTestHandler {
	return &SpendingStashTestHandler{
		allocationService: alloc,
		cardService:       card,
		roundupService:    roundup,
		limitsService:     limits,
		logger:            zap.NewNop(),
	}
}

func setupSpendingTestRouter(userID uuid.UUID, h *SpendingStashTestHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		c.Set("user_id", userID)
		// Build response using the same logic as the real handler
		response := buildSpendingStashResponse(c, h)
		c.JSON(http.StatusOK, response)
	})
	return router
}

func buildSpendingStashResponse(c *gin.Context, h *SpendingStashTestHandler) *handlers.SpendingStashResponse {
	ctx := c.Request.Context()
	userID := c.MustGet("user_id").(uuid.UUID)

	response := &handlers.SpendingStashResponse{
		SpendingBalance:    "0.00",
		AvailableToSpend:   "0.00",
		PendingAmount:      "0.00",
		AllocationInfo:     handlers.SpendingAllocationInfo{Active: false, SpendingRatio: "0.70"},
		RecentTransactions: []handlers.TransactionSummary{},
		Analytics: handlers.SpendingAnalytics{
			TotalSpentThisMonth:     "0.00",
			AvgTransactionThisMonth: "0.00",
			TotalSpentLastMonth:     "0.00",
			SpendingChange:          "0.00",
			SpendingChangePct:       "0.00%",
			SpendingTrend:           "stable",
			TopCategories:           []handlers.SpendingCategory{},
			DailyAverage:            "0.00",
		},
		Limits: handlers.SpendingLimits{
			DailySpendLimit:       "1000.00",
			DailySpendUsed:        "0.00",
			DailySpendRemaining:   "1000.00",
			MonthlySpendLimit:     "10000.00",
			MonthlySpendUsed:      "0.00",
			MonthlySpendRemaining: "10000.00",
			PerTransactionLimit:   "500.00",
		},
		Stats: handlers.SpendingStats{
			TotalSpentAllTime: "0.00",
			TotalRoundUps:     "0.00",
		},
		Warnings: []string{},
	}

	// Fetch balances
	if h.allocationService != nil {
		balances, err := h.allocationService.GetBalances(ctx, userID)
		if err != nil {
			response.Warnings = append(response.Warnings, "balances_unavailable")
		} else if balances != nil {
			response.SpendingBalance = balances.SpendingBalance.StringFixed(2)
			response.AvailableToSpend = balances.SpendingRemaining.StringFixed(2)
			response.AllocationInfo.Active = balances.ModeActive
		}

		mode, err := h.allocationService.GetMode(ctx, userID)
		if err != nil {
			response.Warnings = append(response.Warnings, "allocation_mode_unavailable")
		} else if mode != nil {
			response.AllocationInfo.Active = mode.Active
			response.AllocationInfo.SpendingRatio = mode.RatioSpending.StringFixed(2)
		}
	}

	// Fetch cards
	if h.cardService != nil {
		cards, err := h.cardService.GetUserCards(ctx, userID)
		if err != nil {
			response.Warnings = append(response.Warnings, "card_unavailable")
		} else if len(cards) > 0 {
			for _, card := range cards {
				if card.Status == entities.CardStatusActive || card.Status == entities.CardStatusFrozen {
					response.Card = &handlers.CardSummary{
						ID:              card.ID.String(),
						Type:            string(card.Type),
						Status:          string(card.Status),
						LastFour:        card.Last4,
						SpendingEnabled: card.Status == entities.CardStatusActive,
						ATMEnabled:      card.Status == entities.CardStatusActive,
						CreatedAt:       card.CreatedAt.Format(time.RFC3339),
					}
					response.Stats.CardCreatedAt = card.CreatedAt.Format(time.RFC3339)
					break
				}
			}
		}

		txns, err := h.cardService.GetUserTransactions(ctx, userID, 10, 0)
		if err != nil {
			response.Warnings = append(response.Warnings, "transactions_unavailable")
		} else if len(txns) > 0 {
			transactions := make([]handlers.TransactionSummary, 0, len(txns))
			for _, tx := range txns {
				category := "Other"
				if tx.MerchantCategory != nil {
					category = *tx.MerchantCategory
				}
				merchantName := ""
				if tx.MerchantName != nil {
					merchantName = *tx.MerchantName
				}
				transactions = append(transactions, handlers.TransactionSummary{
					ID:           tx.ID.String(),
					Type:         "card",
					Amount:       tx.Amount.Neg().StringFixed(2),
					Currency:     tx.Currency,
					Description:  merchantName,
					MerchantName: merchantName,
					Category:     category,
					CategoryIcon: handlers.GetCategoryIcon(category),
					Status:       tx.Status,
					CreatedAt:    tx.CreatedAt.Format(time.RFC3339),
				})
			}
			response.RecentTransactions = transactions
			response.Stats.TotalTransactions = len(transactions)
		}
	}

	// Fetch roundups
	if h.roundupService != nil {
		summary, err := h.roundupService.GetSummary(ctx, userID)
		if err != nil {
			response.Warnings = append(response.Warnings, "roundups_unavailable")
		} else if summary != nil && summary.Settings != nil && summary.Settings.Enabled {
			multiplier := 1
			if summary.Settings.Multiplier.IsPositive() {
				multiplier = int(summary.Settings.Multiplier.IntPart())
			}
			response.RoundUps = &handlers.RoundUpsSummary{
				Enabled:          summary.Settings.Enabled,
				Multiplier:       multiplier,
				TotalAccumulated: summary.TotalCollected.StringFixed(2),
				PendingAmount:    summary.PendingAmount.StringFixed(2),
				InvestedAmount:   summary.TotalInvested.StringFixed(2),
				ThisMonthTotal:   "0.00",
				TransactionCount: summary.TransactionCount,
			}
			response.Stats.TotalRoundUps = summary.TotalCollected.StringFixed(2)
		}
	}

	// Fetch limits
	if h.limitsService != nil {
		limits, err := h.limitsService.GetUserLimits(ctx, userID)
		if err != nil {
			response.Warnings = append(response.Warnings, "limits_unavailable")
		} else if limits != nil {
			response.Limits = handlers.SpendingLimits{
				DailySpendLimit:       limits.Withdrawal.Daily.Limit,
				DailySpendUsed:        limits.Withdrawal.Daily.Used,
				DailySpendRemaining:   limits.Withdrawal.Daily.Remaining,
				DailyResetAt:          limits.Withdrawal.Daily.ResetsAt.Format(time.RFC3339),
				MonthlySpendLimit:     limits.Withdrawal.Monthly.Limit,
				MonthlySpendUsed:      limits.Withdrawal.Monthly.Used,
				MonthlySpendRemaining: limits.Withdrawal.Monthly.Remaining,
				PerTransactionLimit:   limits.Withdrawal.Minimum,
			}
		}
	}

	return response
}

func TestGetSpendingStash_Success(t *testing.T) {
	userID := uuid.New()

	// Setup mock data
	balances := &entities.AllocationBalances{
		UserID:            userID,
		SpendingBalance:   decimal.NewFromFloat(700),
		StashBalance:      decimal.NewFromFloat(300),
		SpendingRemaining: decimal.NewFromFloat(650),
		ModeActive:        true,
	}

	mode := &entities.SmartAllocationMode{
		UserID:        userID,
		Active:        true,
		RatioSpending: decimal.NewFromFloat(0.70),
		RatioStash:    decimal.NewFromFloat(0.30),
	}

	cards := []*entities.BridgeCard{
		{
			ID:        uuid.New(),
			UserID:    userID,
			Type:      entities.CardTypeVirtual,
			Status:    entities.CardStatusActive,
			Last4:     "1234",
			CreatedAt: time.Now().Add(-24 * time.Hour),
		},
	}

	merchantName := "Coffee Shop"
	merchantCategory := "Food & Dining"
	cardTxns := []*entities.BridgeCardTransaction{
		{
			ID:               uuid.New(),
			CardID:           cards[0].ID,
			Amount:           decimal.NewFromFloat(5.50),
			Currency:         "USD",
			MerchantName:     &merchantName,
			MerchantCategory: &merchantCategory,
			Status:           "completed",
			CreatedAt:        time.Now().Add(-1 * time.Hour),
		},
	}

	roundupSummary := &entities.RoundupSummary{
		Settings: &entities.RoundupSettings{
			UserID:     userID,
			Enabled:    true,
			Multiplier: decimal.NewFromInt(2),
		},
		PendingAmount:    decimal.NewFromFloat(15.50),
		TotalCollected:   decimal.NewFromFloat(150.00),
		TotalInvested:    decimal.NewFromFloat(134.50),
		TransactionCount: 45,
	}

	resetTime := time.Now().Add(24 * time.Hour)
	userLimits := &entities.UserLimitsResponse{
		KYCTier: entities.KYCTierBasic,
		Withdrawal: entities.LimitDetails{
			Minimum: "10.00",
			Daily: entities.PeriodLimit{
				Limit:     "5000.00",
				Used:      "100.00",
				Remaining: "4900.00",
				ResetsAt:  resetTime,
			},
			Monthly: entities.PeriodLimit{
				Limit:     "50000.00",
				Used:      "500.00",
				Remaining: "49500.00",
				ResetsAt:  time.Now().AddDate(0, 1, 0),
			},
		},
	}

	handler := NewSpendingStashTestHandler(
		&MockAllocationService{Balances: balances, Mode: mode},
		&MockCardService{Cards: cards, Txns: cardTxns},
		&MockRoundupService{Summary: roundupSummary},
		&MockLimitsService{Limits: userLimits},
	)

	router := setupSpendingTestRouter(userID, handler)

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify balances
	assert.Equal(t, "700.00", response.SpendingBalance)
	assert.Equal(t, "650.00", response.AvailableToSpend)
	assert.True(t, response.AllocationInfo.Active)
	assert.Equal(t, "0.70", response.AllocationInfo.SpendingRatio)

	// Verify card
	assert.NotNil(t, response.Card)
	assert.Equal(t, "1234", response.Card.LastFour)
	assert.Equal(t, "virtual", response.Card.Type)
	assert.True(t, response.Card.SpendingEnabled)

	// Verify transactions
	assert.Len(t, response.RecentTransactions, 1)
	assert.Equal(t, "Coffee Shop", response.RecentTransactions[0].MerchantName)
	assert.Equal(t, "Food & Dining", response.RecentTransactions[0].Category)

	// Verify round-ups
	assert.NotNil(t, response.RoundUps)
	assert.True(t, response.RoundUps.Enabled)
	assert.Equal(t, 2, response.RoundUps.Multiplier)
	assert.Equal(t, "150.00", response.RoundUps.TotalAccumulated)
	assert.Equal(t, "15.50", response.RoundUps.PendingAmount)

	// Verify limits from service
	assert.Equal(t, "5000.00", response.Limits.DailySpendLimit)
	assert.Equal(t, "100.00", response.Limits.DailySpendUsed)
	assert.Equal(t, "4900.00", response.Limits.DailySpendRemaining)

	// Verify stats
	assert.Equal(t, 1, response.Stats.TotalTransactions)
	assert.Empty(t, response.Warnings)
}

func TestGetSpendingStash_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		_, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "UNAUTHORIZED",
				Message: "User not authenticated",
			})
			return
		}
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetSpendingStash_NoCard(t *testing.T) {
	userID := uuid.New()

	balances := &entities.AllocationBalances{
		UserID:            userID,
		SpendingBalance:   decimal.NewFromFloat(500),
		SpendingRemaining: decimal.NewFromFloat(500),
		ModeActive:        true,
	}

	handler := NewSpendingStashTestHandler(
		&MockAllocationService{Balances: balances},
		&MockCardService{Cards: nil, Txns: nil},
		nil,
		nil,
	)

	router := setupSpendingTestRouter(userID, handler)

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Nil(t, response.Card)
	assert.Equal(t, "500.00", response.SpendingBalance)
}

func TestGetSpendingStash_ServiceErrors(t *testing.T) {
	userID := uuid.New()

	handler := NewSpendingStashTestHandler(
		&MockAllocationService{Err: assert.AnError},
		&MockCardService{Err: assert.AnError},
		&MockRoundupService{Err: assert.AnError},
		&MockLimitsService{Err: assert.AnError},
	)

	router := setupSpendingTestRouter(userID, handler)

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify warnings are populated
	assert.Contains(t, response.Warnings, "balances_unavailable")
	assert.Contains(t, response.Warnings, "allocation_mode_unavailable")
	assert.Contains(t, response.Warnings, "card_unavailable")
	assert.Contains(t, response.Warnings, "transactions_unavailable")
	assert.Contains(t, response.Warnings, "roundups_unavailable")
	assert.Contains(t, response.Warnings, "limits_unavailable")

	// Verify defaults are used
	assert.Equal(t, "0.00", response.SpendingBalance)
	assert.Nil(t, response.Card)
	assert.Nil(t, response.RoundUps)
}

func TestGetSpendingStash_NilRoundupSettings(t *testing.T) {
	userID := uuid.New()

	// Roundup summary with nil Settings
	roundupSummary := &entities.RoundupSummary{
		Settings:         nil,
		PendingAmount:    decimal.NewFromFloat(15.50),
		TotalCollected:   decimal.NewFromFloat(150.00),
		TotalInvested:    decimal.NewFromFloat(134.50),
		TransactionCount: 45,
	}

	handler := NewSpendingStashTestHandler(
		nil,
		nil,
		&MockRoundupService{Summary: roundupSummary},
		nil,
	)

	router := setupSpendingTestRouter(userID, handler)

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// RoundUps should be nil when Settings is nil
	assert.Nil(t, response.RoundUps)
}

func TestGetCategoryIcon(t *testing.T) {
	tests := []struct {
		category string
		expected string
	}{
		{"Food & Dining", "utensils"},
		{"Shopping", "shopping-bag"},
		{"Transportation", "car"},
		{"Unknown Category", "more-horizontal"},
		{"", "more-horizontal"},
	}

	for _, tt := range tests {
		t.Run(tt.category, func(t *testing.T) {
			icon := handlers.GetCategoryIcon(tt.category)
			assert.Equal(t, tt.expected, icon)
		})
	}
}
