package consciousspending

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func observed(value string) ObservedAmount {
	return ObservedAmount{Amount: decimal.RequireFromString(value), Known: true, Source: "test", Confidence: "high"}
}

func TestCalculateSnapshotDerivesGuiltFreeRemainder(t *testing.T) {
	snapshot := CalculateSnapshot(SnapshotInput{
		TakeHomeIncome: observed("1000"),
		FixedCosts:     observed("550"),
		Investments:    observed("100"),
		Savings:        observed("100"),
		Currency:       "ngn",
	})

	require.True(t, snapshot.Complete)
	assert.Equal(t, "NGN", snapshot.Currency)
	require.Len(t, snapshot.Buckets, 4)
	guiltFree := snapshot.Buckets[3]
	assert.Equal(t, BucketGuiltFreeSpending, guiltFree.Name)
	assert.True(t, guiltFree.Known)
	assert.True(t, guiltFree.Amount.Equal(decimal.NewFromInt(250)))
	assert.Equal(t, "derived_remainder", guiltFree.Source)
	assert.Equal(t, "within_reference", guiltFree.Status)
}

func TestCalculateSnapshotPreservesUnknownInsteadOfZero(t *testing.T) {
	snapshot := CalculateSnapshot(SnapshotInput{
		TakeHomeIncome: observed("1000"),
		FixedCosts:     observed("550"),
		Currency:       "USD",
	})

	assert.False(t, snapshot.Complete)
	assert.False(t, snapshot.Buckets[1].Known)
	assert.Equal(t, "unknown", snapshot.Buckets[1].Status)
}

func TestCompareReturnsOnlyMaterialVariances(t *testing.T) {
	plan := &entities.ConsciousSpendingPlan{
		FixedCostsPct: decimal.NewFromInt(55), InvestmentsPct: decimal.NewFromInt(10),
		SavingsPct: decimal.NewFromInt(10), GuiltFreeSpendingPct: decimal.NewFromInt(25),
	}
	actual := CalculateSnapshot(SnapshotInput{
		TakeHomeIncome:    observed("1000"),
		FixedCosts:        observed("650"),
		Investments:       observed("100"),
		Savings:           observed("50"),
		GuiltFreeSpending: observed("200"),
	})

	variances := Compare(plan, actual, decimal.NewFromInt(5))
	require.Len(t, variances, 3)
	assert.Equal(t, BucketFixedCosts, variances[0].Bucket)
	assert.True(t, variances[0].DeltaPct.Equal(decimal.NewFromInt(10)))
	assert.Equal(t, BucketSavings, variances[1].Bucket)
	assert.True(t, variances[1].DeltaPct.Equal(decimal.NewFromInt(-5)))
}
