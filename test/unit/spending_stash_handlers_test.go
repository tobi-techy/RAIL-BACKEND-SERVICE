package unit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestGetSpendingStash_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()

	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		c.Set("user_id", userID)

		response := handlers.SpendingStashResponse{
			Balance: handlers.BalanceInfo{
				Available:   "650.00",
				Pending:     "50.00",
				Currency:    "USD",
				LastUpdated: time.Now(),
			},
			Allocation: handlers.SpendingAllocationInfo{
				Active:        true,
				SpendingRatio: 0.70,
				StashRatio:    0.30,
				TotalReceived: "1000.00",
			},
			Card: &handlers.CardSummary{
				ID:        uuid.New().String(),
				Type:      "virtual",
				Network:   "visa",
				Status:    "active",
				LastFour:  "1234",
				IsFrozen:  false,
				CreatedAt: time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
			},
			TopCategories: []handlers.CategorySummary{
				{Name: "Food & Dining", Amount: "150.00", Percent: 42.8},
				{Name: "Shopping", Amount: "100.00", Percent: 28.6},
			},
			RoundUps: &handlers.RoundUpsSummary{
				IsEnabled:        true,
				Multiplier:       2,
				TotalAccumulated: "150.00",
				TransactionCount: 45,
			},
			Limits: handlers.SpendingLimits{
				Daily:          handlers.LimitDetail{Limit: "5000.00", Used: "100.00", Remaining: "4900.00"},
				Monthly:        handlers.LimitDetail{Limit: "50000.00", Used: "350.00", Remaining: "49650.00"},
				PerTransaction: "2500.00",
			},
			PendingAuthorizations: []handlers.PendingAuthorization{},
			RecentTransactions: handlers.TransactionListResponse{
				Items: []handlers.TransactionSummary{
					{
						ID:          uuid.New().String(),
						Type:        "card",
						Amount:      "-5.50",
						Currency:    "USD",
						Description: "Coffee Shop",
						Status:      "completed",
						CreatedAt:   time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
					},
				},
				HasMore: false,
			},
			Links: handlers.SpendingLinks{
				Self:         "/api/v1/account/spending-stash",
				Transactions: "/api/v1/spending/transactions",
			},
		}
		c.JSON(http.StatusOK, response)
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.SpendingStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "650.00", response.Balance.Available)
	assert.Equal(t, "50.00", response.Balance.Pending)
	assert.True(t, response.Allocation.Active)
	assert.Equal(t, 0.70, response.Allocation.SpendingRatio)
	assert.NotNil(t, response.Card)
	assert.Equal(t, "1234", response.Card.LastFour)
	assert.Len(t, response.TopCategories, 2)
	assert.NotNil(t, response.RoundUps)
	assert.True(t, response.RoundUps.IsEnabled)
	assert.Len(t, response.RecentTransactions.Items, 1)
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
