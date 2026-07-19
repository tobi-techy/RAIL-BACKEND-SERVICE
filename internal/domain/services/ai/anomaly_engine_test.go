package ai

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- mocks ---

type mockAnomalyCategories struct {
	data []entities.SpendingByCategory
	err  error
}

func (m *mockAnomalyCategories) GetSpendingByCategory(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByCategory, error) {
	return m.data, m.err
}

type mockAnomalyMerchants struct {
	data []entities.SpendingByMerchant
	err  error
}

func (m *mockAnomalyMerchants) GetSpendingByMerchant(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingByMerchant, error) {
	return m.data, m.err
}

type mockAnomalyOutflows struct {
	data []entities.SpendingTransaction
	err  error
}

func (m *mockAnomalyOutflows) GetRecentOutflows(_ context.Context, _ uuid.UUID, _, _ time.Time, _ int) ([]entities.SpendingTransaction, error) {
	return m.data, m.err
}

type mockAnomalyFlow struct {
	flow *entities.MoneyFlowSummary
	err  error
}

func (m *mockAnomalyFlow) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return m.flow, m.err
}

func newTestAnomalyEngine(cat AnomalyCategoryReader, merch AnomalyMerchantReader, out AnomalyOutflowReader, flow AnomalyFlowReader) *AnomalyEngine {
	e := NewAnomalyEngine(cat, merch, out, flow, zap.NewNop())
	e.NoiseFloor = decimal.NewFromInt(10)
	return e
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC)
}

// --- CheckBillSpikes ---

func TestAnomalyEngine_CheckBillSpikes_DetectsSpike(t *testing.T) {
	now := fixedNow()
	uid := uuid.New()

	eng := newTestAnomalyEngine(
		&callTrackingCategories{
			calls: []interface{}{
				[]entities.SpendingByCategory{ // first call: current month
					{Category: "Utilities", Total: decimal.NewFromInt(300)},
					{Category: "Food", Total: decimal.NewFromInt(100)},
				},
				[]entities.SpendingByCategory{ // second call: trailing 3 months
					{Category: "Utilities", Total: decimal.NewFromInt(600)}, // 200/mo avg
					{Category: "Food", Total: decimal.NewFromInt(240)},      // 80/mo avg
				},
			},
		},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckBillSpikes(context.Background(), uid, now)

	require.Len(t, results, 2)
	assert.Equal(t, AnomalyBillSpike, results[0].Type)
	assert.Contains(t, results[0].Title, "Utilities")
}

func TestAnomalyEngine_CheckBillSpikes_NoSpikeWhenNormal(t *testing.T) {
	now := fixedNow()
	uid := uuid.New()
	// $500 in 7 days → projected $2,142/mo, trailing avg $2,000/mo → 1.07x, under 1.25 threshold
	eng := newTestAnomalyEngine(
		&callTrackingCategories{
			calls: []interface{}{
				[]entities.SpendingByCategory{
					{Category: "Utilities", Total: decimal.NewFromInt(500)},
				},
				[]entities.SpendingByCategory{
					{Category: "Utilities", Total: decimal.NewFromInt(6000)}, // 2,000/mo avg
				},
			},
		},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckBillSpikes(context.Background(), uid, now)

	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckBillSpikes_EmptyData(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckBillSpikes(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckBillSpikes_ErrorFromRepo(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{err: assert.AnError},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckBillSpikes(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

// --- CheckDuplicateCharges ---

func TestAnomalyEngine_CheckDuplicateCharges_DetectsDuplicate(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{
			data: []entities.SpendingTransaction{
				{Date: today, Amount: decimal.NewFromInt(45), Source: "Starbucks", Category: "Food"},
				{Date: today, Amount: decimal.NewFromInt(45), Source: "Starbucks", Category: "Food"},
				{Date: today, Amount: decimal.NewFromInt(12), Source: "Uber", Category: "Transport"},
			},
		},
		&mockAnomalyFlow{},
	)

	results := eng.CheckDuplicateCharges(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
	assert.Equal(t, AnomalyDuplicateCharge, results[0].Type)
	assert.Contains(t, results[0].Description, "Starbucks")
}

func TestAnomalyEngine_CheckDuplicateCharges_NoDuplicateWhenDifferentAmounts(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{
			data: []entities.SpendingTransaction{
				{Date: today, Amount: decimal.NewFromInt(45), Source: "Starbucks"},
				{Date: today, Amount: decimal.NewFromInt(55), Source: "Starbucks"},
			},
		},
		&mockAnomalyFlow{},
	)

	results := eng.CheckDuplicateCharges(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckDuplicateCharges_NoDuplicateWhenSingleCharge(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{data: []entities.SpendingTransaction{
			{Date: fixedNow().Format("2006-01-02"), Amount: decimal.NewFromInt(45), Source: "Starbucks"},
		}},
		&mockAnomalyFlow{},
	)

	results := eng.CheckDuplicateCharges(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

// --- CheckFraudSignals ---

func TestAnomalyEngine_CheckFraudSignals_DetectsLargeTransaction(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{
			data: []entities.SpendingTransaction{
				{Date: today, Amount: decimal.NewFromInt(1000), Source: "Best Buy", Category: "Electronics"},
			},
		},
		&mockAnomalyFlow{},
	)

	results := eng.CheckFraudSignals(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
	assert.Equal(t, AnomalyFraudSignal, results[0].Type)
	assert.Contains(t, results[0].Title, "Large transaction")
}

func TestAnomalyEngine_CheckFraudSignals_DetectsHighRiskCategory(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{
			data: []entities.SpendingTransaction{
				{Date: today, Amount: decimal.NewFromInt(50), Source: "DraftKings", Category: "Gambling"},
			},
		},
		&mockAnomalyFlow{},
	)

	results := eng.CheckFraudSignals(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
	assert.Contains(t, results[0].Title, "Gambling")
}

func TestAnomalyEngine_CheckFraudSignals_SkipsNormalTransactions(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{
			data: []entities.SpendingTransaction{
				{Date: today, Amount: decimal.NewFromInt(12), Source: "Uber", Category: "Transport"},
				{Date: today, Amount: decimal.NewFromInt(45), Source: "Starbucks", Category: "Food"},
			},
		},
		&mockAnomalyFlow{},
	)

	results := eng.CheckFraudSignals(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckFraudSignals_EmptyData(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckFraudSignals(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

// --- CheckSpendingAcceleration ---

func TestAnomalyEngine_CheckSpendingAcceleration_DetectsAcceleration(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&callTrackingFlow{
			calls: []interface{}{
				&entities.MoneyFlowSummary{ // current month: projected high
					TotalCardSpend: decimal.NewFromInt(1500),
				},
				&entities.MoneyFlowSummary{ // trailing 3 months: 2000 total = 667/mo
					TotalCardSpend: decimal.NewFromInt(1800),
				},
			},
		},
	)

	results := eng.CheckSpendingAcceleration(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
	assert.Equal(t, AnomalySpendingAccel, results[0].Type)
}

func TestAnomalyEngine_CheckSpendingAcceleration_NoAccelerationWhenFine(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&callTrackingFlow{
			calls: []interface{}{
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.NewFromInt(100), // projected 100/7*30=428
				},
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.NewFromInt(1800), // trailing avg 600/mo
				},
			},
		},
	)

	results := eng.CheckSpendingAcceleration(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckSpendingAcceleration_HandlesZeroTrailing(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&callTrackingFlow{
			calls: []interface{}{
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.NewFromInt(100),
				},
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.Zero,
				},
			},
		},
	)

	results := eng.CheckSpendingAcceleration(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

// --- CheckMerchantPatterns ---

func TestAnomalyEngine_CheckMerchantPatterns_DetectsHighRiskMerchant(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{
			data: []entities.SpendingByMerchant{
				{Merchant: "DraftKings", Count: 10, Total: decimal.NewFromInt(500)},
			},
		},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckMerchantPatterns(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
	assert.Equal(t, AnomalyMerchantPattern, results[0].Type)
	assert.Contains(t, results[0].Description, "DraftKings")
}

func TestAnomalyEngine_CheckMerchantPatterns_DetectsHighFrequency(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{
			data: []entities.SpendingByMerchant{
				{Merchant: "Starbucks", Count: 18, Total: decimal.NewFromInt(200)},
			},
		},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckMerchantPatterns(context.Background(), uid, fixedNow())

	require.Len(t, results, 1)
}

func TestAnomalyEngine_CheckMerchantPatterns_SkipsNormalFrequency(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{
			data: []entities.SpendingByMerchant{
				{Merchant: "Starbucks", Count: 3, Total: decimal.NewFromInt(45)},
				{Merchant: "Uber", Count: 2, Total: decimal.NewFromInt(30)},
			},
		},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckMerchantPatterns(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

func TestAnomalyEngine_CheckMerchantPatterns_EmptyData(t *testing.T) {
	uid := uuid.New()
	eng := newTestAnomalyEngine(
		&mockAnomalyCategories{},
		&mockAnomalyMerchants{},
		&mockAnomalyOutflows{},
		&mockAnomalyFlow{},
	)

	results := eng.CheckMerchantPatterns(context.Background(), uid, fixedNow())
	assert.Empty(t, results)
}

// --- RunAllChecks ---

func TestAnomalyEngine_RunAllChecks_AggregatesResults(t *testing.T) {
	uid := uuid.New()
	today := fixedNow().Format("2006-01-02")
	eng := AnomalyEngine{
		categories:             &mockAnomalyCategories{data: []entities.SpendingByCategory{
			{Category: "Utilities", Total: decimal.NewFromInt(300)},
		}},
		merchants:              &mockAnomalyMerchants{data: []entities.SpendingByMerchant{
			{Merchant: "DraftKings", Count: 10, Total: decimal.NewFromInt(500)},
		}},
		outflows:               &mockAnomalyOutflows{data: []entities.SpendingTransaction{
			{Date: today, Amount: decimal.NewFromInt(1000), Source: "Best Buy", Category: "Electronics"},
		}},
		flow:                   &callTrackingFlow{
			calls: []interface{}{
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.NewFromInt(1500),
				},
				&entities.MoneyFlowSummary{
					TotalCardSpend: decimal.NewFromInt(2000),
				},
			},
		},
		logger:                 zap.NewNop(),
		BillSpikeThreshold:     DefaultBillSpikeThreshold,
		SpendingAccelThreshold: DefaultSpendingAccelThreshold,
		DuplicateChargeWindow:  DefaultDuplicateChargeWindow,
		LargeTxThreshold:       DefaultLargeTxThreshold,
		MerchantVisitThreshold: DefaultMerchantVisitThreshold,
		NoiseFloor:             decimal.NewFromInt(10),
	}

	results := eng.RunAllChecks(context.Background(), uid, fixedNow())

	assert.GreaterOrEqual(t, len(results), 2)
}

// --- BuildAlertText ---

func TestBuildAlertText_EmptyReturnsEmpty(t *testing.T) {
	title, body := BuildAlertText(nil)
	assert.Empty(t, title)
	assert.Empty(t, body)

	title, body = BuildAlertText([]AnomalyResult{})
	assert.Empty(t, title)
	assert.Empty(t, body)
}

func TestBuildAlertText_SingleResult(t *testing.T) {
	results := []AnomalyResult{
		{Severity: SeverityHigh, Title: "Test", Description: "Something happened"},
	}

	title, body := BuildAlertText(results)
	assert.Contains(t, title, "1 issue")
	assert.Contains(t, body, "Something happened")
}

func TestBuildAlertText_MultipleResults(t *testing.T) {
	results := []AnomalyResult{
		{Severity: SeverityHigh, Title: "Test 1", Description: "Issue one"},
		{Severity: SeverityLow, Title: "Test 2", Description: "Issue two"},
	}

	title, body := BuildAlertText(results)
	assert.Contains(t, title, "1 issue")
	assert.Contains(t, body, "Issue one")
	assert.Contains(t, body, "Issue two")
}

func TestBuildAlertText_OnlyLowSeverity(t *testing.T) {
	results := []AnomalyResult{
		{Severity: SeverityLow, Title: "Test", Description: "Minor thing"},
	}

	title, _ := BuildAlertText(results)
	assert.Contains(t, title, "Morning Check")
}

// --- call tracking mocks for sequential calls ---

type callTrackingCategories struct {
	calls []interface{}
	idx   int
}

func (m *callTrackingCategories) GetSpendingByCategory(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]entities.SpendingByCategory, error) {
	if m.idx >= len(m.calls) {
		return nil, nil
	}
	data := m.calls[m.idx].([]entities.SpendingByCategory)
	m.idx++
	return data, nil
}

type callTrackingFlow struct {
	calls []interface{}
	idx   int
}

func (m *callTrackingFlow) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	if m.idx >= len(m.calls) {
		return nil, nil
	}
	data := m.calls[m.idx].(*entities.MoneyFlowSummary)
	m.idx++
	return data, nil
}
