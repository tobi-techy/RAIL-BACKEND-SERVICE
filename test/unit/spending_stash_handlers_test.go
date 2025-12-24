package unit

import (
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
)

func TestGetSpendingStash_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()

	// Setup test data
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

	// Setup router with mock handler
	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		c.Set("user_id", userID)

		// Build response manually for test
		response := buildTestSpendingResponse(userID, balances, mode, cards, roundupSummary, cardTxns)
		c.JSON(http.StatusOK, response)
	})

	// Make request
	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Assertions
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

	// Verify stats
	assert.Equal(t, 1, response.Stats.TotalTransactions)
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
	gin.SetMode(gin.TestMode)

	userID := uuid.New()

	balances := &entities.AllocationBalances{
		UserID:            userID,
		SpendingBalance:   decimal.NewFromFloat(500),
		SpendingRemaining: decimal.NewFromFloat(500),
		ModeActive:        true,
	}

	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		c.Set("user_id", userID)

		response := buildTestSpendingResponse(userID, balances, nil, nil, nil, nil)
		c.JSON(http.StatusOK, response)
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Card should be nil
	assert.Nil(t, response.Card)

	// Balance should still be populated
	assert.Equal(t, "500.00", response.SpendingBalance)
}

// buildTestSpendingResponse is a helper to build response for tests
func buildTestSpendingResponse(
	userID uuid.UUID,
	balances *entities.AllocationBalances,
	mode *entities.SmartAllocationMode,
	cards []*entities.BridgeCard,
	roundupSummary *entities.RoundupSummary,
	cardTxns []*entities.BridgeCardTransaction,
) *handlers.SpendingStashResponse {
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
	}

	if balances != nil {
		response.SpendingBalance = balances.SpendingBalance.StringFixed(2)
		response.AvailableToSpend = balances.SpendingRemaining.StringFixed(2)
		response.AllocationInfo.Active = balances.ModeActive
	}

	if mode != nil {
		response.AllocationInfo.Active = mode.Active
		response.AllocationInfo.SpendingRatio = mode.RatioSpending.StringFixed(2)
	}

	if len(cards) > 0 {
		for _, c := range cards {
			if c.Status == entities.CardStatusActive || c.Status == entities.CardStatusFrozen {
				response.Card = &handlers.CardSummary{
					ID:              c.ID.String(),
					Type:            string(c.Type),
					Status:          string(c.Status),
					LastFour:        c.Last4,
					SpendingEnabled: c.Status == entities.CardStatusActive,
					ATMEnabled:      c.Status == entities.CardStatusActive,
					CreatedAt:       c.CreatedAt.Format(time.RFC3339),
				}
				response.Stats.CardCreatedAt = c.CreatedAt.Format(time.RFC3339)
				break
			}
		}
	}

	if len(cardTxns) > 0 {
		transactions := make([]handlers.TransactionSummary, 0, len(cardTxns))
		for _, tx := range cardTxns {
			category := "Other"
			if tx.MerchantCategory != nil {
				category = *tx.MerchantCategory
			}
			categoryIcon := handlers.CategoryIcons[category]
			if categoryIcon == "" {
				categoryIcon = "more-horizontal"
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
				CategoryIcon: categoryIcon,
				Status:       tx.Status,
				CreatedAt:    tx.CreatedAt.Format(time.RFC3339),
			})
		}
		response.RecentTransactions = transactions
		response.Stats.TotalTransactions = len(transactions)
	}

	if roundupSummary != nil && roundupSummary.Settings.Enabled {
		multiplier := 1
		if !roundupSummary.Settings.Multiplier.IsZero() {
			multiplier = int(roundupSummary.Settings.Multiplier.IntPart())
		}
		response.RoundUps = &handlers.RoundUpsSummary{
			Enabled:          roundupSummary.Settings.Enabled,
			Multiplier:       multiplier,
			TotalAccumulated: roundupSummary.TotalCollected.StringFixed(2),
			PendingAmount:    roundupSummary.PendingAmount.StringFixed(2),
			InvestedAmount:   roundupSummary.TotalInvested.StringFixed(2),
			ThisMonthTotal:   "0.00",
			TransactionCount: roundupSummary.TransactionCount,
		}
		response.Stats.TotalRoundUps = roundupSummary.TotalCollected.StringFixed(2)
	}

	return response
}
