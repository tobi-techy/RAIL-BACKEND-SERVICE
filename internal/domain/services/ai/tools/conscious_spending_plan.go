package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	"github.com/shopspring/decimal"
)

const (
	ToolGetConsciousSpendingPlan    = "get_conscious_spending_plan"
	ToolBuildConsciousSpendingPlan  = "build_conscious_spending_plan"
	ToolCommitConsciousSpendingPlan = "commit_conscious_spending_plan"
	ToolPauseConsciousSpendingPlan  = "pause_conscious_spending_plan"
)

func RegisterConsciousSpendingPlanTools(r *Registry) {
	r.Register(NewTool(
		ToolGetConsciousSpendingPlan,
		"Get the user's saved four-number Conscious Spending Plan. Use for plan check-ins, commitment questions, and comparisons between actual spending and their agreed monthly plan.",
		SimpleArgs(nil, nil),
		core.CategoryOverview,
		func(ctx context.Context, userID uuid.UUID, _ map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ConsciousSpendingPlans == nil {
				return &core.ToolResult{Error: "conscious spending plans are unavailable"}, nil
			}
			plan, err := deps.ConsciousSpendingPlans.Get(ctx, userID)
			if err != nil {
				return nil, err
			}
			if plan == nil {
				return &core.ToolResult{Data: map[string]interface{}{
					"has_plan": false,
					"message":  "No four-number plan exists yet. Discover the goal and reason first, then build the numbers.",
				}}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"has_plan": true, "plan": plan}}, nil
		},
	))

	r.Register(NewTool(
		ToolBuildConsciousSpendingPlan,
		"Reveal the user's four important monthly numbers: fixed costs, investments, savings, and guilt-free spending. Prefer verified Rail/Mono/profile data; accept user-provided values when data is missing. Unknown is not zero. This is read-only and does not commit the plan.",
		cspParameters(false),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			snapshot := buildSnapshot(ctx, userID, args, deps)
			return &core.ToolResult{Data: map[string]interface{}{
				"snapshot": snapshot,
				"guidance": "Reference ranges are guides, not rules: fixed costs 50-60%, investments about 10%, savings 5-10%, guilt-free spending 20-35%. Discuss the largest mismatch first.",
			}}, nil
		},
	))

	r.Register(NewTool(
		ToolCommitConsciousSpendingPlan,
		"Commit the exact household plan after the user has stated a meaningful goal, explained why it matters, reviewed the reveal, chosen trade-offs, and explicitly agreed to these amounts. This creates coaching accountability but never moves money.",
		cspParameters(true),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ConsciousSpendingPlans == nil {
				return &core.ToolResult{Error: "conscious spending plans are unavailable"}, nil
			}
			in, err := cspInput(args)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			plan, err := deps.ConsciousSpendingPlans.Commit(ctx, userID, in)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{
				"committed": true,
				"plan":      plan,
				"message":   "Plan committed. No money moved; budgets and automations require separate actions.",
			}}, nil
		},
	))

	r.Register(NewTool(
		ToolPauseConsciousSpendingPlan,
		"Pause committed-plan coaching when the user explicitly asks to stop plan check-ins. This does not delete history or change any budget or automation.",
		SimpleArgs(nil, nil),
		core.CategoryPlanning,
		func(ctx context.Context, userID uuid.UUID, _ map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.ConsciousSpendingPlans == nil {
				return &core.ToolResult{Error: "conscious spending plans are unavailable"}, nil
			}
			plan, err := deps.ConsciousSpendingPlans.Pause(ctx, userID)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"paused": plan != nil, "plan": plan}}, nil
		},
	))
}

func cspParameters(requireAll bool) map[string]interface{} {
	required := []string{}
	if requireAll {
		required = []string{"take_home_income", "currency", "fixed_costs", "investments", "savings", "guilt_free_spending"}
	}
	return SimpleArgs(map[string]map[string]interface{}{
		"take_home_income":    NumberParam("Monthly take-home income after tax."),
		"currency":            StringParam("Currency code for every amount, for example NGN, USD, GBP, or EUR."),
		"fixed_costs":         NumberParam("Monthly essentials and minimum debt payments."),
		"investments":         NumberParam("Monthly long-term investment contributions."),
		"savings":             NumberParam("Monthly savings and extra goal or debt-payoff contributions."),
		"guilt_free_spending": NumberParam("Monthly amount available for wants without guilt."),
		"check_in_cadence": EnumParam("How often Miriam should check the commitment.", []string{
			entities.CheckInCadenceWeekly, entities.CheckInCadenceBiweekly, entities.CheckInCadenceMonthly,
		}),
	}, required)
}

func buildSnapshot(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) consciousspending.Snapshot {
	// This interactive view prioritizes explicit user input and planning data.
	// Weekly adherence uses AgentAdapter.GetConsciousSpendingSnapshot instead.
	currency := strings.ToUpper(strings.TrimSpace(GetArgString(args, "currency")))
	income := observedArg(args, "take_home_income")
	fixed := observedArg(args, "fixed_costs")
	investments := observedArg(args, "investments")
	savings := observedArg(args, "savings")
	guiltFree := observedArg(args, "guilt_free_spending")

	if deps.FinancialProfile != nil {
		if profile, err := deps.FinancialProfile.GetFinancialProfile(ctx, userID); err == nil && profile != nil {
			if currency == "" {
				currency = profile.PrimaryCurrency
			}
			if !income.Known && profile.MonthlyIncome.IsPositive() {
				income = consciousspending.NewObservedAmount(profile.MonthlyIncome, "financial_profile", "medium")
			}
			if !fixed.Known && profile.MonthlyFixedCosts.IsPositive() {
				fixed = consciousspending.NewObservedAmount(profile.MonthlyFixedCosts, "financial_profile", "medium")
			}
		}
	}

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if deps.Activity != nil && !investments.Known {
		if summary, err := deps.Activity.GetContributions(ctx, userID, "", start, now); err == nil && summary != nil && summary.Total.IsPositive() {
			investments = consciousspending.NewObservedAmount(summary.Total, "investment_activity", "high")
		}
	}
	if deps.SpendingAnalyzer != nil {
		if flow, err := deps.SpendingAnalyzer.GetMoneyFlow(ctx, userID, start, now); err == nil && flow != nil && flow.TotalDeposits.IsPositive() {
			if !income.Known {
				income = consciousspending.NewObservedAmount(flow.TotalDeposits, "month_money_flow", "medium")
			}
		}
	}

	return consciousspending.CalculateSnapshot(consciousspending.SnapshotInput{
		TakeHomeIncome: income, FixedCosts: fixed, Investments: investments,
		Savings: savings, GuiltFreeSpending: guiltFree, Currency: currency,
	})
}

func observedArg(args map[string]interface{}, key string) consciousspending.ObservedAmount {
	value, ok := args[key]
	if !ok {
		return consciousspending.ObservedAmount{}
	}
	amount, err := decimal.NewFromString(fmt.Sprintf("%v", value))
	if err != nil || amount.IsNegative() {
		return consciousspending.ObservedAmount{}
	}
	return consciousspending.NewObservedAmount(amount, "user_provided", "high")
}

func cspInput(args map[string]interface{}) (core.ConsciousSpendingPlanInput, error) {
	fields := []string{"take_home_income", "fixed_costs", "investments", "savings", "guilt_free_spending"}
	values := make(map[string]decimal.Decimal, len(fields))
	for _, field := range fields {
		value, ok := args[field]
		if !ok {
			return core.ConsciousSpendingPlanInput{}, fmt.Errorf("%s is required", field)
		}
		amount, err := decimal.NewFromString(fmt.Sprintf("%v", value))
		if err != nil || amount.IsNegative() {
			return core.ConsciousSpendingPlanInput{}, fmt.Errorf("%s must be a non-negative number", field)
		}
		values[field] = amount
	}
	if !values["take_home_income"].IsPositive() {
		return core.ConsciousSpendingPlanInput{}, consciousspending.ErrInvalidIncome
	}
	currency := strings.ToUpper(strings.TrimSpace(GetArgString(args, "currency")))
	if currency == "" {
		return core.ConsciousSpendingPlanInput{}, fmt.Errorf("currency is required")
	}
	return core.ConsciousSpendingPlanInput{
		TakeHomeIncome: values["take_home_income"].String(),
		Currency:       currency, FixedCosts: values["fixed_costs"].String(),
		Investments: values["investments"].String(), Savings: values["savings"].String(),
		GuiltFreeSpending: values["guilt_free_spending"].String(),
		CheckInCadence:    GetArgString(args, "check_in_cadence"),
	}, nil
}
