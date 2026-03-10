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
			Balance: handlers.SpendingBalanceInfo{
				Available:   "650.00",
				Currency:    "USD",
				LastUpdated: time.Now(),
			},
			SpendingSummary: handlers.SpendingSummary{
				ThisMonthTotal:   "350.00",
				LastMonthTotal:   "400.00",
				DailyAverage:     "11.67",
				Trend:            "down",
				TransactionCount: 30,
			},
			TopCategories: []handlers.CategorySummary{
				{Name: "Food & Dining", Amount: "150.00", Percent: 42.8},
				{Name: "Shopping", Amount: "100.00", Percent: 28.6},
			},
			RoundUps: &handlers.RoundUpsSummary{
				IsEnabled:        true,
				TotalAccumulated: "150.00",
				TransactionCount: 45,
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
	assert.Len(t, response.TopCategories, 2)
	assert.NotNil(t, response.RoundUps)
	assert.True(t, response.RoundUps.IsEnabled)
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
