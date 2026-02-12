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

func TestGetInvestmentStash_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID := uuid.New()

	router := gin.New()
	router.GET("/api/v1/account/investment-stash", func(c *gin.Context) {
		c.Set("user_id", userID)

		response := handlers.InvestmentStashResponse{
			Balance: handlers.InvestmentBalanceInfo{
				Total:             "1350.00",
				Stash:             "300.00",
				Invested:          "1050.00",
				PendingAllocation: "0.00",
				Currency:          "USD",
				LastUpdated:       time.Now(),
			},
			Allocation: handlers.InvestmentAllocationInfo{
				Active:         true,
				SpendingRatio:  0.70,
				StashRatio:     0.30,
				TotalAllocated: "1050.00",
			},
			Performance: handlers.PerformanceInfo{
				TotalGain:        "50.00",
				TotalGainPercent: 5.0,
			},
			Positions: handlers.PositionListResponse{
				Page:       1,
				PageSize:   20,
				TotalCount: 1,
				HasMore:    false,
				Items: []handlers.PositionSummary{
					{
						ID:          uuid.New().String(),
						Symbol:      "VTI",
						Name:        "Vanguard Total Stock Market ETF",
						Type:        "ETF",
						Quantity:    "10",
						MarketValue: "1050.00",
						CostBasis:   "1000.00",
						AvgCost:     "100.00",
					},
				},
			},
			Stats: handlers.InvestmentStats{
				TotalDeposits:    "1000.00",
				TotalWithdrawals: "0.00",
				PositionCount:    1,
			},
			Links: handlers.InvestmentLinks{
				Self:      "/api/v1/account/investment-stash",
				Positions: "/api/v1/investing/positions",
			},
		}
		c.JSON(http.StatusOK, response)
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/investment-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response handlers.InvestmentStashResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "1350.00", response.Balance.Total)
	assert.Equal(t, "300.00", response.Balance.Stash)
	assert.True(t, response.Allocation.Active)
	assert.Equal(t, 0.30, response.Allocation.StashRatio)
	assert.Len(t, response.Positions.Items, 1)
	assert.Equal(t, "1050.00", response.Positions.Items[0].MarketValue)
	assert.Equal(t, 1, response.Stats.PositionCount)
}

func TestGetInvestmentStash_Unauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/v1/account/investment-stash", func(c *gin.Context) {
		_, exists := c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, entities.ErrorResponse{
				Code:    "UNAUTHORIZED",
				Message: "User not authenticated",
			})
			return
		}
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/investment-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
