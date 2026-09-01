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
		TakeHomeIncome:    observed("1000"),
		FixedCosts:        observed("550"),
		PreTaxInvestments: observed("0"),
		Investments:       observed("100"),
		Savings:           observed("100"),
		Currency:          "ngn",
		Coverage:          EvidenceCoverage{Known: true, Confidence: "high"},
	})

	require.True(t, snapshot.Complete)
	assert.Equal(t, "NGN", snapshot.Currency)
	assert.Len(t, snapshot.Buckets, 5)
	guiltFree := snapshot.Buckets[4]
	assert.Equal(t, BucketGuiltFreeSpending, guiltFree.Name)
	assert.True(t, guiltFree.Known)
	assert.True(t, guiltFree.Amount.Equal(decimal.NewFromInt(250)))
	assert.Equal(t, "derived_remainder", guiltFree.Source)
	assert.Equal(t, "reference_above", guiltFree.Status)
	assert.True(t, snapshot.Coverage.Known)
}

func TestCalculateSnapshotPreservesUnknownInsteadOfZero(t *testing.T) {
	snapshot := CalculateSnapshot(SnapshotInput{
		TakeHomeIncome: observed("1000"),
		FixedCosts:     observed("550"),
		Currency:       "USD",
	})

	assert.False(t, snapshot.Complete)
	assert.False(t, snapshot.Buckets[2].Known)
	assert.Equal(t, "unknown", snapshot.Buckets[2].Status)
}

func TestCompareReturnsOnlyMaterialVariances(t *testing.T) {
	plan := &entities.ConsciousSpendingPlan{
		FixedCosts: decimal.NewFromInt(550), PostTaxInvestments: decimal.NewFromInt(100),
		Savings: decimal.NewFromInt(100), GuiltFreeSpending: decimal.NewFromInt(250),
		TakeHomeIncome: decimal.NewFromInt(1000),
	}
	actual := CalculateSnapshot(SnapshotInput{
		TakeHomeIncome:    observed("1000"),
		FixedCosts:        observed("650"),
		Investments:       observed("100"),
		Savings:           observed("50"),
		GuiltFreeSpending: observed("200"),
		Coverage:          EvidenceCoverage{Known: true, Confidence: "high"},
	})

	variances, coverageKnown := Compare(plan, actual, decimal.NewFromInt(10))
	require.True(t, coverageKnown)
	require.Len(t, variances, 3)
	assert.Equal(t, BucketFixedCosts, variances[0].Bucket)
	assert.True(t, variances[0].DeltaAmount.Equal(decimal.NewFromInt(100)))
	assert.Equal(t, BucketSavings, variances[2].Bucket)
	assert.True(t, variances[2].DeltaAmount.Equal(decimal.NewFromInt(-50)))
}
