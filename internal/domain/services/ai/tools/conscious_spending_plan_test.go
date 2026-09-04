package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cspStoreFake struct {
	committed core.ConsciousSpendingPlanInput
	plan      *entities.ConsciousSpendingPlan
}

func (f *cspStoreFake) Get(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return f.plan, nil
}

func (f *cspStoreFake) Commit(_ context.Context, _ uuid.UUID, in core.ConsciousSpendingPlanInput) (*entities.ConsciousSpendingPlan, error) {
	f.committed = in
	f.plan = &entities.ConsciousSpendingPlan{Status: entities.ConsciousSpendingPlanStatusCommitted}
	return f.plan, nil
}

func (f *cspStoreFake) Pause(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return f.plan, nil
}

func TestConsciousSpendingPlanToolsRegistered(t *testing.T) {
	registry := NewRegistry()
	RegisterConsciousSpendingPlanTools(registry)

	for _, name := range []string{
		ToolGetConsciousSpendingPlan, ToolBuildConsciousSpendingPlan,
		ToolCommitConsciousSpendingPlan, ToolPauseConsciousSpendingPlan,
	} {
		assert.NotNil(t, registry.Get(name), name)
	}
}

func TestBuildConsciousSpendingPlanPreservesUnknowns(t *testing.T) {
	registry := NewRegistry()
	RegisterConsciousSpendingPlanTools(registry)

	result, err := registry.Execute(context.Background(), uuid.New(), ToolBuildConsciousSpendingPlan,
		map[string]interface{}{"take_home_income": 1000, "currency": "usd", "fixed_costs": 550},
		&core.Dependencies{})

	require.NoError(t, err)
	typed, ok := result.Data["snapshot"].(consciousspending.Snapshot)
	require.True(t, ok)
	assert.False(t, typed.Complete)
	assert.False(t, typed.Buckets[1].Known)
}

func TestCommitConsciousSpendingPlanPassesExactAmounts(t *testing.T) {
	registry := NewRegistry()
	RegisterConsciousSpendingPlanTools(registry)
	store := &cspStoreFake{}

	result, err := registry.Execute(context.Background(), uuid.New(), ToolCommitConsciousSpendingPlan,
		map[string]interface{}{
			"take_home_income": 1000, "currency": "ngn", "fixed_costs": 550,
			"investments": 100, "savings": 100, "guilt_free_spending": 250,
			"check_in_cadence": "weekly",
		}, &core.Dependencies{ConsciousSpendingPlans: store})

	require.NoError(t, err)
	assert.Empty(t, result.Error)
	assert.Equal(t, "1000", store.committed.TakeHomeIncome)
	assert.Equal(t, "NGN", store.committed.Currency)
	assert.Equal(t, "250", store.committed.GuiltFreeSpending)
	assert.Equal(t, true, result.Data["committed"])
	assert.Contains(t, result.Data["message"], "No money moved")
}

func TestCommitConsciousSpendingPlanRejectsMissingBucket(t *testing.T) {
	registry := NewRegistry()
	RegisterConsciousSpendingPlanTools(registry)
	store := &cspStoreFake{}

	result, err := registry.Execute(context.Background(), uuid.New(), ToolCommitConsciousSpendingPlan,
		map[string]interface{}{"take_home_income": 1000, "currency": "USD"}, &core.Dependencies{ConsciousSpendingPlans: store})

	require.NoError(t, err)
	assert.Contains(t, result.Error, "fixed_costs is required")
	assert.Empty(t, store.committed.TakeHomeIncome)
}
