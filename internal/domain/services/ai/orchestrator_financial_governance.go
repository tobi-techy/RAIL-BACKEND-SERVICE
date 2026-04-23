package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const (
	ToolGetFinancialAdvice   = "get_financial_advice"
	ToolGetFinancialTimeline = "get_financial_timeline"
)

// FinancialRuleCheck is a deterministic rule-based assessment with evidence.
type FinancialRuleCheck struct {
	Code           string                 `json:"code"`
	Severity       string                 `json:"severity"`
	Title          string                 `json:"title"`
	Why            string                 `json:"why"`
	Recommendation string                 `json:"recommendation"`
	Blocked        bool                   `json:"blocked"`
	DataUsed       []string               `json:"data_used"`
	Evidence       map[string]interface{} `json:"evidence,omitempty"`
}

// FinancialAdvice is a deterministic recommendation bundle.
type FinancialAdvice struct {
	OverallStatus         string               `json:"overall_status"`
	Summary               string               `json:"summary"`
	RecommendedNextAction string               `json:"recommended_next_action"`
	Checks                []FinancialRuleCheck `json:"checks"`
	DataUsed              []string             `json:"data_used"`
}

// FinancialTimelineEvent is a source-tagged money event.
type FinancialTimelineEvent struct {
	Type        string                 `json:"type"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Amount      string                 `json:"amount,omitempty"`
	Currency    string                 `json:"currency,omitempty"`
	Source      string                 `json:"source"`
	Severity    string                 `json:"severity,omitempty"`
	OccurredAt  string                 `json:"occurred_at"`
	DataUsed    []string               `json:"data_used"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// FinancialTimeline is the merged event feed returned to the agent/UI.
type FinancialTimeline struct {
	Events   []FinancialTimelineEvent `json:"events"`
	Summary  string                   `json:"summary"`
	DataUsed []string                 `json:"data_used"`
}

// FinancialGovernanceTools returns deterministic advice and timeline tools.
func FinancialGovernanceTools(includeAdvice, includeTimeline bool) []infraai.Tool {
	tools := make([]infraai.Tool, 0, 2)
	if includeAdvice {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetFinancialAdvice,
			Description: "Run rule-based financial checks before giving recommendations. Covers low balance risk, budget overrun, upcoming bill pressure, abnormal spend, savings target drift, missing income data, stale profile data, and risky transfer impact. Use before making recommendations so the answer is explainable and grounded in exact data.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"intent": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"overview", "transfer", "budget", "goal", "investment", "tax", "legal"},
						"description": "Optional topic for extra safety and contextual checks",
					},
					"proposed_amount": map[string]interface{}{
						"type":        "number",
						"description": "Optional amount to evaluate for transfer affordability or safety",
					},
				},
			},
		})
	}
	if includeTimeline {
		tools = append(tools, infraai.Tool{
			Name:        ToolGetFinancialTimeline,
			Description: "Return a merged timeline of recent money events: deposits, withdrawals, card spends, important spending transactions, budget changes, savings goals, and AI actions. Use when the user asks what happened, what Miriam changed, or wants a chronological view of their money history.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"days": map[string]interface{}{
						"type":        "integer",
						"default":     30,
						"description": "Lookback window in days, capped to 90",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum number of events to return, capped to 50",
					},
				},
			},
		})
	}
	return tools
}

func (o *Orchestrator) executeFinancialAdvice(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	intent := strings.ToLower(strings.TrimSpace(stringArg(args, "intent")))
	proposedAmount := decimal.Zero
	if v, ok := floatArg(args, "proposed_amount"); ok && v > 0 {
		proposedAmount = decimal.NewFromFloat(v)
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := monthStart.AddDate(0, -1, 0)

	spend, stash, totalBalance := o.currentBalances(ctx, userID)
	flow := o.monthFlow(ctx, userID, monthStart, now)
	lastFlow := o.monthFlow(ctx, userID, lastMonthStart, monthStart)
	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
	lastMonthOut := lastFlow.TotalWithdrawals.Add(lastFlow.TotalCardSpend).Add(lastFlow.TotalP2P).Add(lastFlow.TotalReceipts)
	netFlow := flow.TotalDeposits.Sub(totalOut)
	if spend.IsZero() &&
		stash.IsZero() &&
		totalBalance.IsZero() &&
		flow.TotalDeposits.IsZero() &&
		flow.TotalWithdrawals.IsZero() &&
		flow.TotalCardSpend.IsZero() &&
		flow.TotalP2P.IsZero() &&
		flow.TotalReceipts.IsZero() &&
		lastFlow.TotalDeposits.IsZero() &&
		lastFlow.TotalWithdrawals.IsZero() &&
		lastFlow.TotalCardSpend.IsZero() &&
		lastFlow.TotalP2P.IsZero() &&
		lastFlow.TotalReceipts.IsZero() {
		return map[string]interface{}{
			"error": "financial data is temporarily unavailable; please try again shortly",
		}, nil
	}

	checks := make([]FinancialRuleCheck, 0, 8)

	if spend.LessThan(decimal.NewFromInt(25)) || (o.budgetProvider != nil && spend.LessThan(decimal.NewFromInt(75))) {
		checks = append(checks, FinancialRuleCheck{
			Code:           "low_balance",
			Severity:       "warning",
			Title:          "Spend balance is tight",
			Why:            fmt.Sprintf("Spend balance is $%s and total balance is $%s.", spend.StringFixed(2), totalBalance.StringFixed(2)),
			Recommendation: "Pause discretionary spend and leave room for upcoming bills.",
			DataUsed:       []string{"current_balances"},
			Evidence: map[string]interface{}{
				"spend_balance": spend.StringFixed(2),
				"stash_balance": stash.StringFixed(2),
				"total_balance": totalBalance.StringFixed(2),
			},
		})
	}

	if o.budgetProvider != nil {
		if budget, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && budget != nil {
			hasPositiveBudgetLimit := !budget.MonthlyLimit.IsZero() && budget.MonthlyLimit.IsPositive()
			if hasPositiveBudgetLimit {
				remaining := budget.MonthlyLimit.Sub(totalOut)
				pctUsed := totalOut.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
				if remaining.IsNegative() || pctUsed.GreaterThan(decimal.NewFromInt(90)) {
					checks = append(checks, FinancialRuleCheck{
						Code:           "budget_overrun",
						Severity:       "critical",
						Title:          "Budget is at risk",
						Why:            fmt.Sprintf("You've used %s%% of a $%s monthly budget with $%s left.", pctUsed.StringFixed(1), budget.MonthlyLimit.StringFixed(2), remaining.StringFixed(2)),
						Recommendation: "Cut non-essential spend until the next budget reset.",
						DataUsed:       []string{"budget", "month_money_flow"},
						Evidence: map[string]interface{}{
							"monthly_limit": budget.MonthlyLimit.StringFixed(2),
							"spent_so_far":  totalOut.StringFixed(2),
							"remaining":     remaining.StringFixed(2),
							"percent_used":  pctUsed.StringFixed(1),
						},
					})
				}
			}
		}
	}

	if o.financialProfile != nil {
		if profile, err := o.financialProfile.GetByUserID(ctx, userID); err == nil && profile != nil {
			if profile.MonthlyFixedCosts.IsPositive() {
				upcomingBillPressure := profile.MonthlyFixedCosts.Add(profile.MonthlySavingsTarget)
				if spend.LessThan(upcomingBillPressure.Div(decimal.NewFromInt(2))) {
					checks = append(checks, FinancialRuleCheck{
						Code:           "upcoming_bills",
						Severity:       "warning",
						Title:          "Upcoming bills may squeeze Spend",
						Why:            fmt.Sprintf("Monthly fixed costs are $%s and your Spend balance is $%s.", profile.MonthlyFixedCosts.StringFixed(2), spend.StringFixed(2)),
						Recommendation: "Keep enough in Spend to cover fixed costs before moving money out.",
						DataUsed:       []string{"financial_profile", "current_balances"},
						Evidence: map[string]interface{}{
							"monthly_fixed_costs":    profile.MonthlyFixedCosts.StringFixed(2),
							"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
							"spend_balance":          spend.StringFixed(2),
						},
					})
				}
			}

			if profile.MonthlyIncome.IsZero() || strings.EqualFold(strings.TrimSpace(profile.IncomeFrequency), "unknown") {
				checks = append(checks, FinancialRuleCheck{
					Code:           "missing_income",
					Severity:       "info",
					Title:          "Income data is missing",
					Why:            "The profile does not contain reliable income data yet.",
					Recommendation: "Tell Miriam your typical income and pay schedule so forecasts become more accurate.",
					DataUsed:       []string{"financial_profile"},
					Evidence: map[string]interface{}{
						"monthly_income":   profile.MonthlyIncome.StringFixed(2),
						"income_frequency": profile.IncomeFrequency,
					},
				})
			}

			if now.Sub(profile.UpdatedAt) > 90*24*time.Hour {
				checks = append(checks, FinancialRuleCheck{
					Code:           "stale_profile",
					Severity:       "info",
					Title:          "Profile data is stale",
					Why:            fmt.Sprintf("Your financial profile was last updated on %s.", profile.UpdatedAt.Format("2006-01-02")),
					Recommendation: "Update your profile before relying on deeper recommendations.",
					DataUsed:       []string{"financial_profile"},
					Evidence: map[string]interface{}{
						"updated_at": profile.UpdatedAt.Format(time.RFC3339),
					},
				})
			}

			if profile.MonthlySavingsTarget.IsPositive() {
				if netFlow.LessThan(profile.MonthlySavingsTarget) {
					checks = append(checks, FinancialRuleCheck{
						Code:           "savings_drift",
						Severity:       "warning",
						Title:          "Savings pace is behind target",
						Why:            fmt.Sprintf("Net flow this month is $%s and your savings target is $%s.", netFlow.StringFixed(2), profile.MonthlySavingsTarget.StringFixed(2)),
						Recommendation: "Reduce non-essential spending or move a smaller amount into Stash this week.",
						DataUsed:       []string{"financial_profile", "month_money_flow"},
						Evidence: map[string]interface{}{
							"monthly_savings_target": profile.MonthlySavingsTarget.StringFixed(2),
							"net_flow":               netFlow.StringFixed(2),
							"stash_balance":          stash.StringFixed(2),
						},
					})
				}
			}
		}
	}

	if o.savingsGoalStore != nil {
		if goal, err := o.savingsGoalStore.Get(ctx, userID); err == nil && goal != nil {
			if target, err := decimal.NewFromString(goal.Target); err == nil {
				if target.IsZero() {
					// Skip malformed goal targets so we do not divide by zero.
				} else if target.IsPositive() {
					progress := decimal.Zero
					if stash.IsPositive() {
						progress = stash.Div(target).Mul(decimal.NewFromInt(100))
					}
					if stash.LessThan(target) {
						missing := target.Sub(stash)
						weeklyCatchUp := missing.Div(decimal.NewFromInt(4))
						checks = append(checks, FinancialRuleCheck{
							Code:           "goal_drift",
							Severity:       "warning",
							Title:          "Savings goal is behind pace",
							Why:            fmt.Sprintf("Goal '%s' is %.1f%% complete and still needs $%s.", goal.Name, progress.InexactFloat64(), missing.StringFixed(2)),
							Recommendation: fmt.Sprintf("Add about $%s per week to close the gap in roughly a month.", weeklyCatchUp.StringFixed(2)),
							DataUsed:       []string{"savings_goal", "current_balances"},
							Evidence: map[string]interface{}{
								"goal_name":      goal.Name,
								"goal_target":    target.StringFixed(2),
								"stash_balance":  stash.StringFixed(2),
								"completion_pct": progress.StringFixed(1),
							},
						})
					}
				}
			}
		}
	}

	if lastMonthOut.IsPositive() && totalOut.GreaterThan(lastMonthOut.Mul(decimal.NewFromFloat(1.5))) {
		checks = append(checks, FinancialRuleCheck{
			Code:           "abnormal_spend",
			Severity:       "warning",
			Title:          "Spending is running hot",
			Why:            fmt.Sprintf("This month outflow is $%s vs $%s last month.", totalOut.StringFixed(2), lastMonthOut.StringFixed(2)),
			Recommendation: "Open the timeline and review what changed in the last 7 to 30 days.",
			DataUsed:       []string{"month_money_flow", "previous_month_money_flow"},
			Evidence: map[string]interface{}{
				"this_month_outflow": totalOut.StringFixed(2),
				"last_month_outflow": lastMonthOut.StringFixed(2),
			},
		})
	}

	if proposedAmount.IsPositive() {
		reserve := decimal.NewFromInt(25)
		if o.financialProfile != nil {
			if profile, err := o.financialProfile.GetByUserID(ctx, userID); err == nil && profile != nil && profile.MonthlyFixedCosts.IsPositive() {
				reserve = profile.MonthlyFixedCosts.Div(decimal.NewFromInt(2))
				if reserve.LessThan(decimal.NewFromInt(25)) {
					reserve = decimal.NewFromInt(25)
				}
			}
		}
		postTransfer := spend.Sub(proposedAmount)
		if postTransfer.LessThan(reserve) {
			checks = append(checks, FinancialRuleCheck{
				Code:           "risky_transfer",
				Severity:       "critical",
				Title:          "Transfer could leave Spend too low",
				Why:            fmt.Sprintf("A $%s transfer would leave Spend at $%s, below the reserve of $%s.", proposedAmount.StringFixed(2), postTransfer.StringFixed(2), reserve.StringFixed(2)),
				Recommendation: "Reduce the transfer or wait until after the next income deposit.",
				Blocked:        true,
				DataUsed:       []string{"current_balances", "financial_profile"},
				Evidence: map[string]interface{}{
					"proposed_amount":             proposedAmount.StringFixed(2),
					"post_transfer_spend_balance": postTransfer.StringFixed(2),
					"reserve":                     reserve.StringFixed(2),
				},
			})
		}
	}

	if o.recurringDetector != nil {
		if recurring, err := o.recurringDetector.DetectRecurring(ctx, userID); err == nil && len(recurring) > 0 {
			totalRecurring := decimal.Zero
			for _, item := range recurring {
				switch strings.ToLower(item.Frequency) {
				case "weekly":
					totalRecurring = totalRecurring.Add(item.AvgAmount.Mul(decimal.NewFromFloat(4.33)))
				default:
					totalRecurring = totalRecurring.Add(item.AvgAmount)
				}
			}
			if spend.LessThan(totalRecurring) {
				checks = append(checks, FinancialRuleCheck{
					Code:           "upcoming_bills_pressure",
					Severity:       "warning",
					Title:          "Recurring spend may outrun Spend balance",
					Why:            fmt.Sprintf("Recurring spend is about $%s/month and Spend is $%s.", totalRecurring.StringFixed(2), spend.StringFixed(2)),
					Recommendation: "Keep enough in Spend for the recurring costs that are coming up.",
					DataUsed:       []string{"recurring_expenses", "current_balances"},
					Evidence: map[string]interface{}{
						"recurring_monthly": totalRecurring.StringFixed(2),
						"spend_balance":     spend.StringFixed(2),
					},
				})
			}
		}
	}

	overallStatus := "good"
	nextAction := "Keep going and review this weekly."
	summary := "Your money looks steady."
	for _, check := range checks {
		switch check.Severity {
		case "critical":
			overallStatus = "critical"
			nextAction = check.Recommendation
			summary = check.Why
		case "warning":
			if overallStatus != "critical" {
				overallStatus = "warning"
				nextAction = check.Recommendation
				summary = check.Why
			}
		case "info":
			if overallStatus == "good" {
				overallStatus = "needs_info"
				nextAction = check.Recommendation
				summary = check.Why
			}
		}
	}

	if intent == "investment" {
		overallStatus = "caution"
		nextAction = "I can help compare options, but I should not tell you what to buy. Check risk, time horizon, and fees first."
		summary = "Investment guidance needs a safety-first frame."
	} else if intent == "tax" {
		overallStatus = "caution"
		nextAction = "I can summarize the data, but tax treatment should be confirmed with a qualified tax professional."
		summary = "Tax advice should stay informational and conservative."
	} else if intent == "legal" {
		overallStatus = "caution"
		nextAction = "I can help organize facts, but legal conclusions should come from a qualified professional."
		summary = "Legal advice must stay conservative."
	}

	if len(checks) == 0 {
		checks = append(checks, FinancialRuleCheck{
			Code:           "healthy",
			Severity:       "good",
			Title:          "No immediate issues found",
			Why:            "Current balances, flow, and profile data do not show a rule-based warning.",
			Recommendation: "Keep your current pace and review again after new income or large spend.",
			DataUsed:       []string{"current_balances", "month_money_flow", "financial_profile"},
			Evidence: map[string]interface{}{
				"spend_balance": spend.StringFixed(2),
				"stash_balance": stash.StringFixed(2),
				"net_flow":      netFlow.StringFixed(2),
			},
		})
	}

	dataUsed := []string{"current_balances", "month_money_flow", "financial_profile"}
	return map[string]interface{}{
		"overall_status":          overallStatus,
		"summary":                 summary,
		"recommended_next_action": nextAction,
		"checks":                  checks,
		"data_used":               dataUsed,
	}, nil
}

func (o *Orchestrator) executeFinancialTimeline(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	days := 30
	if v, ok := intArg(args, "days"); ok && v > 0 {
		days = minInt(v, 90)
	}
	limit := 20
	if v, ok := intArg(args, "limit"); ok && v > 0 {
		limit = minInt(v, 50)
	}

	now := time.Now().UTC()
	start := now.AddDate(0, 0, -days)

	type timelineItem struct {
		eventType   string
		title       string
		description string
		amount      string
		currency    string
		source      string
		severity    string
		occurredAt  time.Time
		dataUsed    []string
		metadata    map[string]interface{}
	}

	items := make([]timelineItem, 0, limit*6)
	appendItem := func(item timelineItem) {
		items = append(items, item)
	}

	if o.depositHistory != nil {
		deposits, err := o.depositHistory.GetByUserID(ctx, userID, limit, 0)
		if err == nil {
			for _, d := range deposits {
				if d == nil {
					continue
				}
				if d.CreatedAt.Before(start) || !strings.EqualFold(d.Status, "confirmed") {
					continue
				}
				appendItem(timelineItem{
					eventType:   "income_received",
					title:       "Income received",
					description: fmt.Sprintf("$%s deposit confirmed on %s", d.Amount.StringFixed(2), d.CreatedAt.Format("2006-01-02")),
					amount:      d.Amount.StringFixed(2),
					currency:    "USD",
					source:      "deposit_history",
					severity:    "positive",
					occurredAt:  d.CreatedAt,
					dataUsed:    []string{"deposit_history"},
					metadata: map[string]interface{}{
						"chain":   d.Chain,
						"status":  d.Status,
						"tx_hash": d.TxHash,
					},
				})
			}
		}
	}

	if o.withdrawalHistory != nil {
		withdrawals, err := o.withdrawalHistory.GetByUserID(ctx, userID, limit, 0)
		if err == nil {
			for _, w := range withdrawals {
				if w == nil || w.CreatedAt.Before(start) {
					continue
				}
				appendItem(timelineItem{
					eventType:   "withdrawal",
					title:       "Withdrawal activity",
					description: fmt.Sprintf("$%s withdrawal to %s", w.Amount.StringFixed(2), withdrawalDestinationLabel(w)),
					amount:      w.Amount.StringFixed(2),
					currency:    string(w.Currency),
					source:      "withdrawal_history",
					severity:    "neutral",
					occurredAt:  w.CreatedAt,
					dataUsed:    []string{"withdrawal_history"},
					metadata: map[string]interface{}{
						"status": w.Status,
						"type":   w.WithdrawalType,
					},
				})
			}
		}
	}

	if o.cardTransactions != nil {
		txs, err := o.cardTransactions.GetTransactionsByUserID(ctx, userID, limit, 0)
		if err == nil {
			for _, tx := range txs {
				if tx == nil || tx.CreatedAt.Before(start) {
					continue
				}
				if !strings.EqualFold(tx.Status, "completed") {
					continue
				}
				merchant := "Unknown merchant"
				if tx.MerchantName != nil && *tx.MerchantName != "" {
					merchant = *tx.MerchantName
				}
				appendItem(timelineItem{
					eventType:   "major_spend",
					title:       "Card spend",
					description: fmt.Sprintf("$%s at %s", tx.Amount.StringFixed(2), merchant),
					amount:      tx.Amount.StringFixed(2),
					currency:    tx.Currency,
					source:      "card_transactions",
					severity:    "neutral",
					occurredAt:  tx.CreatedAt,
					dataUsed:    []string{"card_transactions"},
					metadata: map[string]interface{}{
						"merchant_category": tx.MerchantCategory,
						"type":              tx.Type,
					},
				})
			}
		}
	}

	if o.spending != nil {
		summary, err := o.spending.GetSummary(ctx, userID, start, now)
		if err == nil && summary != nil {
			// Major transactions are more useful than every single line item here.
			txs, err := o.spending.GetTransactions(ctx, userID, start, now, limit)
			if err == nil {
				for _, tx := range txs {
					appendItem(timelineItem{
						eventType:   "spending_transaction",
						title:       "Spending transaction",
						description: fmt.Sprintf("$%s on %s", tx.Amount.StringFixed(2), tx.Date),
						amount:      tx.Amount.StringFixed(2),
						currency:    "USD",
						source:      strings.TrimSpace(tx.Source),
						severity:    "neutral",
						occurredAt:  parseTimelineDate(tx.Date),
						dataUsed:    []string{"spending_transactions"},
						metadata: map[string]interface{}{
							"category": tx.Category,
						},
					})
				}
			}
		}
	}

	if o.actionHistory != nil {
		actions, err := o.actionHistory.ListRecentActions(ctx, userID, limit)
		if err == nil {
			for _, a := range actions {
				if a == nil || a.CreatedAt.Before(start) {
					continue
				}
				label, severity := actionTimelineLabel(a.Action, a.Status)
				appendItem(timelineItem{
					eventType:   "agent_action",
					title:       label,
					description: actionTimelineDescription(a),
					source:      "action_receipts",
					severity:    severity,
					occurredAt:  a.CreatedAt,
					dataUsed:    []string{"action_receipts"},
					metadata: map[string]interface{}{
						"action": a.Action,
						"status": a.Status,
					},
				})
			}
		}
	}

	if o.financialProfile != nil {
		if profile, err := o.financialProfile.GetByUserID(ctx, userID); err == nil && profile != nil && profile.UpdatedAt.After(start) {
			appendItem(timelineItem{
				eventType:   "profile_update",
				title:       "Profile updated",
				description: "Your durable financial profile was updated.",
				source:      "financial_profile",
				severity:    "neutral",
				occurredAt:  profile.UpdatedAt,
				dataUsed:    []string{"financial_profile"},
				metadata: map[string]interface{}{
					"primary_currency": profile.PrimaryCurrency,
					"income_frequency": profile.IncomeFrequency,
				},
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].occurredAt.After(items[j].occurredAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}

	events := make([]FinancialTimelineEvent, 0, len(items))
	for _, item := range items {
		events = append(events, FinancialTimelineEvent{
			Type:        item.eventType,
			Title:       item.title,
			Description: item.description,
			Amount:      item.amount,
			Currency:    item.currency,
			Source:      item.source,
			Severity:    item.severity,
			OccurredAt:  item.occurredAt.Format(time.RFC3339),
			DataUsed:    item.dataUsed,
			Metadata:    item.metadata,
		})
	}

	return map[string]interface{}{
		"events":    events,
		"summary":   fmt.Sprintf("Showing %d money events from the last %d days.", len(events), days),
		"data_used": []string{"deposit_history", "withdrawal_history", "card_transactions", "spending_transactions", "action_receipts", "financial_profile"},
	}, nil
}

func intArg(args map[string]interface{}, key string) (int, bool) {
	if args == nil {
		return 0, false
	}
	if v, ok := args[key].(float64); ok {
		return int(v), true
	}
	if v, ok := args[key].(int); ok {
		return v, true
	}
	return 0, false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func floatArg(args map[string]interface{}, key string) (float64, bool) {
	if args == nil {
		return 0, false
	}
	if v, ok := args[key].(float64); ok {
		return v, true
	}
	if v, ok := args[key].(int); ok {
		return float64(v), true
	}
	return 0, false
}

func stringArg(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func parseTimelineDate(value string) time.Time {
	if value == "" {
		return time.Now().UTC()
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}

func actionTimelineLabel(action, status string) (string, string) {
	switch strings.ToLower(action) {
	case ToolTransferFunds:
		return "Transfer action", severityFromStatus(status)
	case ToolSetBudget:
		return "Budget updated", severityFromStatus(status)
	case ToolSetSavingsGoal:
		return "Savings goal updated", severityFromStatus(status)
	case ToolUpdateFinancialProfile:
		return "Financial profile updated", severityFromStatus(status)
	case ToolSendReport:
		return "Report action", severityFromStatus(status)
	case ToolSplitReceipt:
		return "Receipt split", severityFromStatus(status)
	default:
		return "Agent action", severityFromStatus(status)
	}
}

func actionTimelineDescription(a *entities.ActionAuditEntry) string {
	if a == nil {
		return ""
	}
	switch strings.ToLower(a.Action) {
	case ToolTransferFunds:
		return "Miriam created a transfer request."
	case ToolSetBudget:
		return "Miriam created or updated a budget request."
	case ToolSetSavingsGoal:
		return "Miriam created a savings goal request."
	case ToolUpdateFinancialProfile:
		return "Miriam updated the durable profile."
	default:
		return fmt.Sprintf("Miriam recorded %s.", a.Action)
	}
}

func severityFromStatus(status string) string {
	switch strings.ToLower(status) {
	case "failed", "expired":
		return "warning"
	case "cancelled":
		return "neutral"
	default:
		return "positive"
	}
}

func withdrawalDestinationLabel(w *entities.Withdrawal) string {
	if w == nil {
		return "unknown destination"
	}
	if w.DestinationAddress != nil && *w.DestinationAddress != "" {
		return *w.DestinationAddress
	}
	if w.Narration != nil && *w.Narration != "" {
		return *w.Narration
	}
	if w.DestinationChain != "" {
		return w.DestinationChain
	}
	return "destination"
}
