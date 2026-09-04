package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type consciousPlanProviderFake struct {
	plan *entities.ConsciousSpendingPlan
}

func (f consciousPlanProviderFake) Get(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return f.plan, nil
}

func TestBuildCoachingContextIncludesCommittedFourNumbers(t *testing.T) {
	adapter := &AgentAdapter{}
	adapter.SetConsciousSpendingPlanProvider(consciousPlanProviderFake{plan: &entities.ConsciousSpendingPlan{
		TakeHomeIncome: decimal.NewFromInt(1000), Currency: "USD",
		FixedCostsPct: decimal.NewFromInt(55), InvestmentsPct: decimal.NewFromInt(10),
		SavingsPct: decimal.NewFromInt(10), GuiltFreeSpendingPct: decimal.NewFromInt(25),
		Status: entities.ConsciousSpendingPlanStatusCommitted, CheckInCadence: entities.CheckInCadenceWeekly,
	}})

	contextBlock := adapter.buildCoachingContext(context.Background(), uuid.New())

	for _, want := range []string{
		"csp: committed", "fixed 55.0%", "investments 10.0%", "savings 10.0%",
		"guilt_free 25.0%", "Hold the user to their own numbers", "never silently lower a target",
	} {
		if !strings.Contains(contextBlock, want) {
			t.Errorf("coaching context missing %q:\n%s", want, contextBlock)
		}
	}
}

func TestClassifyRailCategoriesLeavesAmbiguousTransfersUnknown(t *testing.T) {
	fixed, investments, guiltFree, complete := classifyRailCategories([]entities.SpendingByCategory{
		{Category: "Rent", Total: decimal.NewFromInt(500)},
		{Category: "Broker investment", Total: decimal.NewFromInt(100)},
		{Category: "Dining", Total: decimal.NewFromInt(200)},
		{Category: "P2P transfer", Total: decimal.NewFromInt(200)},
	})

	if complete {
		t.Fatal("ambiguous transfer must prevent a complete four-number snapshot")
	}
	if !fixed.Equal(decimal.NewFromInt(500)) ||
		!investments.Equal(decimal.NewFromInt(100)) ||
		!guiltFree.Equal(decimal.NewFromInt(200)) {
		t.Fatalf("unexpected classified amounts: fixed=%s investments=%s guilt_free=%s", fixed, investments, guiltFree)
	}
}
