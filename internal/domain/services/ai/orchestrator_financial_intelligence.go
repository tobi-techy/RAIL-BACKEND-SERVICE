package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiintelligence "github.com/rail-service/rail_service/internal/domain/services/ai/intelligence"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolGetFinancialHealth  = "get_financial_health"
	ToolGetFinancialPlan    = "get_financial_plan"
	ToolGetCashFlowForecast = "get_cash_flow_forecast"
	ToolGetActionReceipts   = "get_action_receipts"
)

// ActionHistoryReader reads executed/cancelled AI action receipts.
// Deprecated: Use aiintelligence.ActionHistoryReader instead.
type ActionHistoryReader = aiintelligence.ActionHistoryReader

func FinancialIntelligenceTools(hasActionHistory bool) []infraai.Tool {
	tools := []infraai.Tool{
		FinancialAuditTool(),
		{
			Name:        ToolGetFinancialHealth,
			Description: "Calculate the user's financial health score from balances, savings rate, budget progress, cash flow, and profile targets. Use for 'how am I doing', financial score, financial health, or progress check questions. Supports multi-month analysis for deeper insights.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        financialAuditPeriods,
						"description": "Time period to analyze. Default to last_90_days for a comprehensive view. Use last_6_months or last_12_months for long-term health trends.",
					},
				},
				"required":             []string{},
				"additionalProperties": false,
			},
		},
		{
			Name:        ToolGetFinancialPlan,
			Description: "Build a practical personalized financial plan using profile, balances, spending, budget, recurring expenses, and savings targets. Use when the user asks what they should do next or wants a plan.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetCashFlowForecast,
			Description: "Forecast end-of-month balance and daily safe-to-spend amount from month-to-date income/spending, recurring expenses, current balances, and budget/profile targets.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
	}
	if hasActionHistory {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetActionReceipts,
			Description: "Get recent confirmed, failed, or cancelled Miriam actions with timestamps and parameters. Use when the user asks what Miriam changed, action history, receipts, or confirmations.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "default": 5, "description": "Number of receipts to return, max 10"},
				},
			},
		})
	}
	return tools
}

func (o *AgentAdapter) executeFinancialHealth(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	period := normalizeAuditPeriod(auditStringArg(args, "period", "last_90_days"))
	start, end := parsePeriod(period)
	now := time.Now().UTC()
	if period == "this_month" {
		end = now
	}
	observedMonths := end.Sub(start).Hours() / 24 / 30.4375
	if observedMonths < 1 {
		observedMonths = 1
	}

	spend, stash, totalBalance := o.currentBalances(ctx, userID)
	flow := o.monthFlow(ctx, userID, start, end)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	netFlow := flow.TotalDeposits.Sub(totalOut)

	// Monthly averages for multi-month periods
	monthDivisor := decimal.NewFromFloat(observedMonths)
	monthlyIncome := flow.TotalDeposits.Div(monthDivisor)
	monthlyOutflow := totalOut.Div(monthDivisor)
	monthlyNet := netFlow.Div(monthDivisor)

	budgetScore := 20
	budgetStatus := "not_set"
	var budgetLimit, budgetRemaining decimal.Decimal
	if o.budgetProvider != nil {
		budget, err := o.budgetProvider.GetByUserID(ctx, userID)
		if err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			budgetLimit = budget.MonthlyLimit
			budgetRemaining = budget.MonthlyLimit.Sub(monthlyOutflow)
			pctUsed := monthlyOutflow.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
			budgetStatus = "on_track"
			switch {
			case pctUsed.LessThanOrEqual(decimal.NewFromInt(70)):
				budgetScore = 20
			case pctUsed.LessThanOrEqual(decimal.NewFromInt(90)):
				budgetScore = 14
				budgetStatus = "tight"
			case pctUsed.LessThanOrEqual(decimal.NewFromInt(100)):
				budgetScore = 8
				budgetStatus = "near_limit"
			default:
				budgetScore = 2
				budgetStatus = "over_budget"
			}
		}
	}

	savingsRate := decimal.Zero
	if flow.TotalDeposits.IsPositive() {
		savingsRate = netFlow.Div(flow.TotalDeposits).Mul(decimal.NewFromInt(100))
	}
	savingsScore := 8
	if savingsRate.GreaterThanOrEqual(decimal.NewFromInt(25)) {
		savingsScore = 25
	} else if savingsRate.GreaterThanOrEqual(decimal.NewFromInt(15)) {
		savingsScore = 20
	} else if savingsRate.GreaterThanOrEqual(decimal.NewFromInt(5)) {
		savingsScore = 14
	} else if savingsRate.GreaterThanOrEqual(decimal.Zero) {
		savingsScore = 8
	} else {
		savingsScore = 2
	}

	runwayScore := 10
	if monthlyOutflow.IsPositive() {
		avgDailyOut := monthlyOutflow.Div(decimal.NewFromFloat(30.4375))
		runwayDays := decimal.Zero
		if avgDailyOut.IsPositive() {
			runwayDays = totalBalance.Div(avgDailyOut)
		}
		switch {
		case runwayDays.GreaterThanOrEqual(decimal.NewFromInt(90)):
			runwayScore = 25
		case runwayDays.GreaterThanOrEqual(decimal.NewFromInt(30)):
			runwayScore = 18
		case runwayDays.GreaterThanOrEqual(decimal.NewFromInt(14)):
			runwayScore = 10
		default:
			runwayScore = 4
		}
	}

	stashScore := 5
	if totalBalance.IsPositive() {
		stashPct := stash.Div(totalBalance).Mul(decimal.NewFromInt(100))
		switch {
		case stashPct.GreaterThanOrEqual(decimal.NewFromInt(30)):
			stashScore = 20
		case stashPct.GreaterThanOrEqual(decimal.NewFromInt(20)):
			stashScore = 15
		case stashPct.GreaterThanOrEqual(decimal.NewFromInt(10)):
			stashScore = 10
		}
	}

	score := clampScore(savingsScore+budgetScore+runwayScore+stashScore+10, 0, 100)
	status := "needs_attention"
	if score >= 80 {
		status = "strong"
	} else if score >= 60 {
		status = "steady"
	} else if score >= 40 {
		status = "fragile"
	}

	// Monthly trend for multi-month periods
	var monthlyTrend []map[string]interface{}
	if observedMonths >= 2 {
		monthlyTrend = o.auditMonthlyTrend(ctx, userID, start, end)
	}

	actions := []string{}
	if budgetStatus == "not_set" {
		actions = append(actions, "Set a monthly spending budget so Miriam can track safe daily spend.")
	} else if budgetRemaining.IsNegative() {
		actions = append(actions, fmt.Sprintf("Pause non-essential spend; you are $%s over budget on average.", budgetRemaining.Abs().StringFixed(2)))
	}
	if savingsRate.LessThan(decimal.NewFromInt(10)) && flow.TotalDeposits.IsPositive() {
		actions = append(actions, "Aim to save at least 10% of incoming money.")
	}
	if stash.LessThan(spend.Mul(decimal.NewFromFloat(0.25))) && spend.GreaterThan(decimal.NewFromInt(20)) {
		actions = append(actions, "Move a small amount from Spend to Stash so more of your money earns yield.")
	}
	if len(actions) == 0 {
		actions = append(actions, "Keep your current pace and review your forecast weekly.")
	}

	result := map[string]interface{}{
		"score":               score,
		"status":              status,
		"period":              period,
		"period_label":        periodToLabel(period, start, end),
		"spend_balance":       spend.StringFixed(2),
		"stash_balance":       stash.StringFixed(2),
		"total_balance":       totalBalance.StringFixed(2),
		"total_income":        flow.TotalDeposits.StringFixed(2),
		"total_outflow":       totalOut.StringFixed(2),
		"total_net_flow":      netFlow.StringFixed(2),
		"monthly_income":      monthlyIncome.StringFixed(2),
		"monthly_outflow":     monthlyOutflow.StringFixed(2),
		"monthly_net_flow":    monthlyNet.StringFixed(2),
		"savings_rate_pct":    savingsRate.StringFixed(1),
		"budget_status":       budgetStatus,
		"budget_limit":        budgetLimit.StringFixed(2),
		"budget_remaining":    budgetRemaining.StringFixed(2),
		"recommended_actions": actions,
		"score_components": []map[string]interface{}{
			{"name": "Savings Rate", "score": savingsScore, "max": 25, "status": componentStatus(savingsScore, 25)},
			{"name": "Budget Control", "score": budgetScore, "max": 20, "status": componentStatus(budgetScore, 20)},
			{"name": "Runway", "score": runwayScore, "max": 25, "status": componentStatus(runwayScore, 25)},
			{"name": "Stash Discipline", "score": stashScore, "max": 20, "status": componentStatus(stashScore, 20)},
		},
		"data_used": []string{"current_balances", "period_money_flow", "budget", "stash_ratio"},
	}
	if len(monthlyTrend) > 0 {
		result["monthly_trend"] = monthlyTrend
	}
	return result, nil
}

func (o *AgentAdapter) executeFinancialPlan(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	health, err := o.executeFinancialHealth(ctx, userID, nil)
	if err != nil {
		return nil, err
	}
	forecast, err := o.executeCashFlowForecast(ctx, userID)
	if err != nil {
		return nil, err
	}

	profile := map[string]interface{}{"has_profile": false}
	if o.financialProfile != nil {
		if p, err := o.financialProfile.GetByUserID(ctx, userID); err == nil && p != nil {
			profile = map[string]interface{}{
				"has_profile":            true,
				"primary_currency":       p.PrimaryCurrency,
				"income_frequency":       p.IncomeFrequency,
				"monthly_savings_target": p.MonthlySavingsTarget.StringFixed(2),
				"emergency_fund_target":  p.EmergencyFundTarget.StringFixed(2),
				"risk_tolerance":         p.RiskTolerance,
				"investment_horizon":     p.InvestmentHorizon,
				"financial_goal":         p.FinancialGoal,
			}
		}
	}

	steps := []map[string]interface{}{
		{"priority": 1, "title": "Protect this month", "action": forecast["primary_action"]},
		{"priority": 2, "title": "Build automatic savings", "action": "Use Stash as the default place for money you do not need this week."},
		{"priority": 3, "title": "Review recurring spend", "action": "Check subscriptions and recurring merchants before increasing savings targets."},
	}

	return map[string]interface{}{
		"health":     health,
		"forecast":   forecast,
		"profile":    profile,
		"next_steps": steps,
		"data_used":  []string{"financial_health", "cash_flow_forecast", "financial_profile", "recurring_expenses"},
	}, nil
}

func (o *AgentAdapter) executeCashFlowForecast(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	daysElapsed := maxInt(1, now.Day())
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	daysRemaining := maxInt(0, daysInMonth-daysElapsed)

	flow := o.monthFlow(ctx, userID, monthStart, nextMonth)
	spend, _, totalBalance := o.currentBalances(ctx, userID)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	dailyBurn := decimal.Zero
	if daysElapsed > 0 {
		dailyBurn = totalOut.Div(decimal.NewFromInt(int64(daysElapsed)))
	}

	projectedOut := dailyBurn.Mul(decimal.NewFromInt(int64(daysInMonth)))
	projectedNet := flow.TotalDeposits.Sub(projectedOut)
	projectedEndBalance := totalBalance.Sub(dailyBurn.Mul(decimal.NewFromInt(int64(daysRemaining))))
	if projectedEndBalance.IsNegative() {
		projectedEndBalance = decimal.Zero
	}

	safeDailySpend := decimal.Zero
	primaryAction := "Keep spending near your current daily average."
	if o.budgetProvider != nil {
		if budget, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && budget != nil {
			remaining := budget.MonthlyLimit.Sub(totalOut)
			if daysRemaining > 0 && remaining.IsPositive() {
				safeDailySpend = remaining.Div(decimal.NewFromInt(int64(daysRemaining)))
				primaryAction = fmt.Sprintf("Stay under $%s/day for the rest of the month.", safeDailySpend.StringFixed(2))
			} else if remaining.IsNegative() {
				primaryAction = fmt.Sprintf("You are $%s over budget; pause discretionary spend.", remaining.Abs().StringFixed(2))
			}
		}
	}
	if safeDailySpend.IsZero() && daysRemaining > 0 && spend.IsPositive() {
		safeDailySpend = spend.Div(decimal.NewFromInt(int64(daysRemaining)))
	}

	confidence := "medium"
	if flow.DepositCount > 0 && totalOut.IsPositive() {
		confidence = "high"
	} else if totalOut.IsZero() {
		confidence = "low"
	}

	return map[string]interface{}{
		"period":                fmt.Sprintf("%s %d", now.Month().String(), now.Year()),
		"days_elapsed":          daysElapsed,
		"days_remaining":        daysRemaining,
		"income_so_far":         flow.TotalDeposits.StringFixed(2),
		"spent_so_far":          totalOut.StringFixed(2),
		"daily_burn_rate":       dailyBurn.StringFixed(2),
		"safe_daily_spend":      safeDailySpend.StringFixed(2),
		"projected_outflow":     projectedOut.StringFixed(2),
		"projected_net_flow":    projectedNet.StringFixed(2),
		"projected_end_balance": projectedEndBalance.StringFixed(2),
		"confidence":            confidence,
		"primary_action":        primaryAction,
		"data_used":             []string{"month_money_flow", "current_balances", "budget"},
	}, nil
}

func (o *AgentAdapter) executeActionReceipts(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.actionHistory == nil {
		return map[string]interface{}{"error": "action receipts are not available"}, nil
	}
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 10 {
		limit = int(l)
	}
	actions, err := o.actionHistory.ListRecentActions(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list action receipts: %w", err)
	}
	receipts := make([]map[string]interface{}, 0, len(actions))
	for _, a := range actions {
		receipts = append(receipts, map[string]interface{}{
			"id":              a.ID.String(),
			"conversation_id": a.ConversationID.String(),
			"action":          a.Action,
			"params":          a.Params,
			"status":          a.Status,
			"error_message":   a.ErrorMessage,
			"created_at":      a.CreatedAt.Format(time.RFC3339),
		})
	}
	return map[string]interface{}{"receipts": receipts, "count": len(receipts)}, nil
}

func (o *AgentAdapter) currentBalances(ctx context.Context, userID uuid.UUID) (decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	if o.aggregateStats == nil {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}
	spend, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	return spend, stash, spend.Add(stash)
}

func (o *AgentAdapter) monthFlow(ctx context.Context, userID uuid.UUID, start, end time.Time) *entities.MoneyFlowSummary {
	if o.spending == nil {
		return &entities.MoneyFlowSummary{}
	}
	flow, err := o.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil || flow == nil {
		return &entities.MoneyFlowSummary{}
	}
	return flow
}

func clampScore(score, min, max int) int {
	if score < min {
		return min
	}
	if score > max {
		return max
	}
	return score
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func componentStatus(score, max int) string {
	pct := float64(score) / float64(max) * 100
	switch {
	case pct >= 80:
		return "strong"
	case pct >= 50:
		return "okay"
	default:
		return "needs_work"
	}
}
