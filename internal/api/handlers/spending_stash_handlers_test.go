package handlers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBuildResponse_SpendingAnalytics(t *testing.T) {
	h := &SpendingStashHandlers{logger: zap.NewNop()}

	merchant := "Coffee Shop"
	category := "Food & Drink"
	now := time.Now().UTC()

	cardTxns := []*entities.BridgeCardTransaction{
		// pending — should be excluded from analytics
		{
			ID: uuid.New(), Type: "authorization", Status: "pending",
			Amount: decimal.NewFromFloat(3.25), Currency: "USD",
			MerchantName: &merchant, MerchantCategory: &category,
			CreatedAt: now,
		},
		// completed purchase this month
		{
			ID: uuid.New(), Type: "capture", Status: "completed",
			Amount: decimal.NewFromFloat(12.34), Currency: "USD",
			MerchantName: &merchant, MerchantCategory: &category,
			CreatedAt: now,
		},
		// refund — should be excluded
		{
			ID: uuid.New(), Type: "refund", Status: "reversed",
			Amount: decimal.NewFromFloat(2.50), Currency: "USD",
			MerchantName: &merchant, MerchantCategory: &category,
			CreatedAt: now,
		},
	}

	balances := &entities.AllocationBalances{
		SpendingRemaining: decimal.NewFromFloat(500.00),
		UpdatedAt:         now,
	}

	resp := h.buildResponse(balances, cardTxns, nil, nil, nil)

	require.NotNil(t, resp)
	assert.Equal(t, "500.00", resp.Balance.Available)
	assert.Equal(t, "12.34", resp.SpendingSummary.ThisMonthTotal)
	assert.Equal(t, 1, resp.SpendingSummary.TransactionCount)
	require.Len(t, resp.TopCategories, 1)
	assert.Equal(t, "Food & Drink", resp.TopCategories[0].Name)
	assert.Equal(t, "12.34", resp.TopCategories[0].Amount)
	assert.Nil(t, resp.RoundUps)
}

func TestBuildResponse_RoundUpsIncluded(t *testing.T) {
	h := &SpendingStashHandlers{logger: zap.NewNop()}

	multiplier := decimal.NewFromInt(1)
	roundupSummary := &entities.RoundupSummary{
		Settings:         &entities.RoundupSettings{Enabled: true, Multiplier: multiplier},
		TotalCollected:   decimal.NewFromFloat(4.75),
		TransactionCount: 3,
	}

	resp := h.buildResponse(nil, nil, nil, nil, roundupSummary)

	require.NotNil(t, resp.RoundUps)
	assert.True(t, resp.RoundUps.IsEnabled)
	assert.Equal(t, "4.75", resp.RoundUps.TotalAccumulated)
	assert.Equal(t, 3, resp.RoundUps.TransactionCount)
}
