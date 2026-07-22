package core

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestClassifyIntent verifies the keyword router maps representative messages to
// the expected category, and that ambiguous messages fall back to CategoryFull.
func TestClassifyIntent(t *testing.T) {
	a := &Agent{}
	cases := []struct {
		msg  string
		want ToolCategory
	}{
		{"how much have I been spending lately?", CategorySpending},
		{"where did my money go this month", CategorySpending},
		{"am I spending more than normal this month?", CategorySpending},
		{"move 200 into my stash", CategoryAction},
		{"send $500 to my bank now please", CategoryAction},
		{"withdraw $100 to my bank", CategoryAction},
		{"set a budget for food", CategoryAction},
		{"show me my recent transactions", CategoryHistory},
		{"how much did I deposit last month", CategoryHistory},
		{"is my income lower compared to usual?", CategoryHistory},
		{"what will my investments be worth next year?", CategoryPlanning},
		{"audit my spending", CategoryPlanning},
		{"when are my bills due", CategoryPlanning},
		{"automate my rent every month", CategoryAutomation},
		{"what do you know about me?", CategoryMemory},
		{"how's my portfolio doing", CategoryInvestment},
		{"how am i doing overall", CategoryOverview},
		{"what's my balance", CategoryOverview},
		{"how's my saving going", CategoryOverview},
		{"hello there friend", CategoryFull},
	}
	for _, c := range cases {
		intents := a.classifyIntent(c.msg, nil)
		if len(intents) != 1 {
			t.Fatalf("%q: expected 1 intent, got %d", c.msg, len(intents))
		}
		if intents[0].Category != c.want {
			t.Errorf("%q: got category %s, want %s", c.msg, intents[0].Category, c.want)
		}
	}
}

// routingRegistry stubs the registry with a fixed pool of tools across
// categories so selectTools can be checked deterministically.
type routingRegistry struct{ pool []*Tool }

func (r *routingRegistry) Get(name string) *Tool {
	for _, t := range r.pool {
		if t.Name == name {
			return t
		}
	}
	return nil
}
func (r *routingRegistry) GetAll() []*Tool { return r.pool }
func (r *routingRegistry) GetByCategory(cat ToolCategory) []*Tool {
	var out []*Tool
	for _, t := range r.pool {
		if t.Category == cat {
			out = append(out, t)
		}
	}
	return out
}
func (r *routingRegistry) GetByCategories(cats []ToolCategory) []*Tool {
	lookup := make(map[ToolCategory]bool, len(cats))
	for _, c := range cats {
		lookup[c] = true
	}
	var out []*Tool
	for _, t := range r.pool {
		if lookup[t.Category] {
			out = append(out, t)
		}
	}
	return out
}
func (r *routingRegistry) Execute(context.Context, uuid.UUID, string, map[string]interface{}, *Dependencies) (*ToolResult, error) {
	return nil, nil
}
func (r *routingRegistry) ToInfrastructureTools() []map[string]interface{} { return nil }
func (r *routingRegistry) Count() int                                      { return len(r.pool) }

func testPool() []*Tool {
	return []*Tool{
		{Name: "get_money_flow", Category: CategorySpending},
		{Name: "get_spending_summary", Category: CategorySpending},
		{Name: "get_account_summary", Category: CategoryOverview},
		{Name: "get_miriam_brief", Category: CategoryOverview},
		{Name: "get_card_transactions", Category: CategoryHistory},
		{Name: "get_income_trend", Category: CategoryHistory},
		{Name: "transfer_funds", Category: CategoryAction},
		{Name: "initiate_withdrawal", Category: CategoryAction},
		{Name: "pay_bill", Category: CategoryAction},
		{Name: "get_budget", Category: CategoryBudget},
		{Name: "list_automations", Category: CategoryAutomation},
		{Name: "web_search", Category: CategoryKnowledge},
		{Name: "list_memory", Category: CategoryMemory},
		{Name: "get_portfolio_stats", Category: CategoryPortfolio},
		{Name: "search_flights", Category: CategoryAction},
	}
}

func toolNames(tools []*Tool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, t := range tools {
		out[t.Name] = true
	}
	return out
}

// TestSelectTools_AlwaysOnUnion ensures every classified intent still offers
// the core money tools, and the intent category's own tools are included.
func TestSelectTools_AlwaysOnUnion(t *testing.T) {
	reg := &routingRegistry{pool: testPool()}
	a := &Agent{deps: &Dependencies{ToolRegistry: reg}}

	tools := a.selectTools([]Intent{{Category: CategorySpending, Confidence: 0.8}})
	names := toolNames(tools)

	// Intent category tools present.
	for _, want := range []string{"get_money_flow", "get_spending_summary"} {
		if !names[want] {
			t.Errorf("spending turn missing %s", want)
		}
	}
	// Always-on core present (transfer, balances, income, budget).
	for _, want := range []string{"transfer_funds", "get_account_summary", "get_income_trend", "get_budget", "initiate_withdrawal"} {
		if !names[want] {
			t.Errorf("spending turn missing always-on %s", want)
		}
	}
	// Out-of-scope categories excluded.
	for _, unwanted := range []string{"web_search", "list_memory", "get_portfolio_stats", "search_flights"} {
		if names[unwanted] {
			t.Errorf("spending turn should not offer %s", unwanted)
		}
	}
}

// TestSelectTools_FullReturnsEverything ensures ambiguous messages fall back
// to the complete registry so no capability is lost.
func TestSelectTools_FullReturnsEverything(t *testing.T) {
	reg := &routingRegistry{pool: testPool()}
	a := &Agent{deps: &Dependencies{ToolRegistry: reg}}

	tools := a.selectTools([]Intent{{Category: CategoryFull, Confidence: 0.5}})
	if len(tools) != len(testPool()) {
		t.Fatalf("CategoryFull should return the whole registry: got %d, want %d", len(tools), len(testPool()))
	}
}

// TestSelectTools_NeverEmpty guards the historical zero-tools bug: whatever
// the intent, the offered set must never be empty when the registry has tools.
func TestSelectTools_NeverEmpty(t *testing.T) {
	reg := &routingRegistry{pool: testPool()}
	a := &Agent{deps: &Dependencies{ToolRegistry: reg}}

	for _, cat := range []ToolCategory{CategorySpending, CategoryHistory, CategoryAction, CategoryOverview, CategoryPlanning, CategoryFull, CategoryVoice, CategoryFiling} {
		if got := a.selectTools([]Intent{{Category: cat}}); len(got) == 0 {
			t.Errorf("category %s produced an empty tool set", cat)
		}
	}
}
