package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubState struct {
	spend, stash decimal.Decimal
}

func (s stubState) GetState(context.Context, uuid.UUID) (*core.UserState, error) {
	return &core.UserState{
		Balances: &core.Balances{
			Spend:    s.spend,
			Stash:    s.stash,
			Currency: "USD",
		},
	}, nil
}
func (stubState) RefreshState(context.Context, uuid.UUID) error    { return nil }
func (stubState) InvalidateCache(context.Context, uuid.UUID) error { return nil }

type stubSpending struct{}

func (stubSpending) GetSpendingSummary(context.Context, uuid.UUID, string) (map[string]interface{}, error) {
	return map[string]interface{}{"total": "42.00"}, nil
}
func (stubSpending) GetRecentTransactions(context.Context, uuid.UUID, int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"amount": "10.00", "merchant": "Cafe"}}, nil
}
func (stubSpending) GetMoneyFlow(context.Context, uuid.UUID, int) (map[string]interface{}, error) {
	return map[string]interface{}{"total_deposits": "100.00", "total_spent": "40.00"}, nil
}
func (stubSpending) GetSpendingPatterns(context.Context, uuid.UUID) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

type stubMiriam struct{}

func (stubMiriam) GetMoneyState(context.Context, uuid.UUID) (map[string]interface{}, error) {
	return map[string]interface{}{"safe_to_spend_daily": "15.00", "liquidity_runway_days": 12}, nil
}
func (stubMiriam) GetMandates(context.Context, uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (stubMiriam) GetDecisionReceipts(context.Context, uuid.UUID, int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (stubMiriam) ListMandateSuggestions(context.Context, uuid.UUID) ([]map[string]interface{}, error) {
	return nil, nil
}
func (stubMiriam) AcceptMandateSuggestion(context.Context, uuid.UUID, uuid.UUID) (map[string]interface{}, error) {
	return nil, nil
}
func (stubMiriam) DismissMandateSuggestion(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func TestGetAccountSummary_ReturnsRealBalances(t *testing.T) {
	r := NewRegistry()
	RegisterAccountSummaryTool(r)
	tool := r.Get("get_account_summary")
	require.NotNil(t, tool)

	deps := &core.Dependencies{
		State:    stubState{spend: decimal.NewFromFloat(120.50), stash: decimal.NewFromFloat(30.25)},
		Spending: stubSpending{},
	}
	res, err := tool.Execute(context.Background(), uuid.New(), nil, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.Equal(t, "$120.50", res.Data["spend_balance"])
	assert.Equal(t, "$30.25", res.Data["stash_balance"])
	assert.Equal(t, "$150.75", res.Data["total_balance"])
	assert.NotEqual(t, "overview generated", res.Data["status"])
	_, hasMonth := res.Data["this_month"]
	assert.True(t, hasMonth)
}

func TestGetMiriamBrief_NotPlaceholder(t *testing.T) {
	r := NewRegistry()
	RegisterMiriamBriefTool(r)
	tool := r.Get("get_miriam_brief")
	require.NotNil(t, tool)

	deps := &core.Dependencies{
		State:        stubState{spend: decimal.NewFromInt(50), stash: decimal.NewFromInt(10)},
		Spending:     stubSpending{},
		MiriamIntell: stubMiriam{},
	}
	res, err := tool.Execute(context.Background(), uuid.New(), nil, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.Equal(t, "$50.00", res.Data["spend_balance"])
	assert.NotEqual(t, "Here's your quick financial brief.", res.Data["message"])
}

func TestListMiriamMandates_EmptyState(t *testing.T) {
	r := NewRegistry()
	RegisterMiriamIntelligenceTools(r)
	tool := r.Get("list_miriam_mandates")
	require.NotNil(t, tool)

	deps := &core.Dependencies{MiriamIntell: stubMiriam{}}
	res, err := tool.Execute(context.Background(), uuid.New(), nil, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.Equal(t, true, res.Data["empty"])
	assert.Equal(t, 0, res.Data["count"])
}

func TestGetRecentTransactions_UsesSpendingProvider(t *testing.T) {
	r := NewRegistry()
	RegisterSpendingTools(r)
	tool := r.Get("get_recent_transactions")
	require.NotNil(t, tool)

	// Transactions nil, Spending set — previously this would false-error.
	deps := &core.Dependencies{Spending: stubSpending{}}
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{"limit": float64(5)}, deps)
	require.NoError(t, err)
	require.Empty(t, res.Error)
	txns, ok := res.Data["transactions"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, txns, 1)
}
