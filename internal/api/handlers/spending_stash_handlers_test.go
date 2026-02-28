package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGetSpendingStash_DependencyUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &SpendingStashHandlers{
		logger: zap.NewNop(),
	}

	router := gin.New()
	router.GET("/api/v1/account/spending-stash", func(c *gin.Context) {
		c.Set("user_id", uuid.New())
		h.GetSpendingStash(c)
	})

	req, _ := http.NewRequest("GET", "/api/v1/account/spending-stash", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp entities.ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "SERVICE_UNAVAILABLE", resp.Code)
}

func TestBuildResponse_MapsLimitsAndRefunds(t *testing.T) {
	h := &SpendingStashHandlers{
		logger: zap.NewNop(),
	}

	merchant := "Coffee Shop"
	category := "Food & Drink"

	txPurchaseID := uuid.New()
	txRefundID := uuid.New()
	txPendingID := uuid.New()

	cardTxns := []*entities.BridgeCardTransaction{
		{
			ID:               txPendingID,
			Type:             "authorization",
			Amount:           decimal.NewFromFloat(3.25),
			Currency:         "USD",
			Status:           "pending",
			MerchantName:     &merchant,
			MerchantCategory: &category,
		},
		{
			ID:               txPurchaseID,
			Type:             "capture",
			Amount:           decimal.NewFromFloat(12.34),
			Currency:         "USD",
			Status:           "completed",
			MerchantName:     &merchant,
			MerchantCategory: &category,
		},
		{
			ID:               txRefundID,
			Type:             "refund",
			Amount:           decimal.NewFromFloat(2.50),
			Currency:         "USD",
			Status:           "reversed",
			MerchantName:     &merchant,
			MerchantCategory: &category,
		},
	}

	userLimits := &entities.UserLimitsResponse{
		Withdrawal: entities.LimitDetails{
			Minimum: "1.00",
			Daily: entities.PeriodLimit{
				Limit:     "1000.00",
				Used:      "250.00",
				Remaining: "750.00",
			},
			Monthly: entities.PeriodLimit{
				Limit:     "10000.00",
				Used:      "1500.00",
				Remaining: "8500.00",
			},
		},
	}

	resp := h.buildResponse(nil, nil, nil, nil, cardTxns, userLimits, 10)

	require.Len(t, resp.PendingAuthorizations, 1)
	require.Len(t, resp.RecentTransactions.Items, 2)

	assert.Equal(t, "-12.34", resp.RecentTransactions.Items[0].Amount)
	assert.Equal(t, "2.50", resp.RecentTransactions.Items[1].Amount)

	assert.Equal(t, "1000.00", resp.Limits.Daily.Limit)
	assert.Equal(t, "750.00", resp.Limits.PerTransaction)
	assert.Equal(t, 75, resp.Limits.DailyTransactionsRemaining)
}
