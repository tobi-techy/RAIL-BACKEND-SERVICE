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

const ToolGetMiriamBrief = "get_miriam_brief"

func MiriamBriefTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetMiriamBrief,
		Description: "Return Miriam's canonical money intelligence brief: what changed, what matters, and the next safe money move. Use for home screen summaries, proactive check-ins, 'what changed', 'what matters', 'what should I do next', and general Miriam overview questions.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"country":  map[string]interface{}{"type": "string", "description": "Optional ISO country code used only to choose the user's local date window."},
				"timezone": map[string]interface{}{"type": "string", "description": "Optional IANA timezone. Prefer server-known user country/profile when available."},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

type miriamInsight struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Severity   string                 `json:"severity"`
	Title      string                 `json:"title"`
	Body       string                 `json:"body"`
	Importance int                    `json:"importance"`
	Evidence   map[string]interface{} `json:"evidence,omitempty"`
}

type miriamAction struct {
	ID                   string                 `json:"id"`
	Type                 string                 `json:"type"`
	Priority             int                    `json:"priority"`
	Title                string                 `json:"title"`
	Body                 string                 `json:"body"`
	CTA                  string                 `json:"cta"`
	ToolName             string                 `json:"tool_name,omitempty"`
	Params               map[string]interface{} `json:"params,omitempty"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
}

func (o *AgentAdapter) executeMiriamBrief(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	loc, timezone, country := o.resolveMiriamLocation(ctx, userID, args)
	localNow := time.Now().In(loc)
	monthStartLocal := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
	lastMonthStartLocal := monthStartLocal.AddDate(0, -1, 0)
	weekStartLocal := localNow.AddDate(0, 0, -7)
	prevWeekStartLocal := localNow.AddDate(0, 0, -14)

	now := localNow.UTC()
	monthStart := monthStartLocal.UTC()
	lastMonthStart := lastMonthStartLocal.UTC()
	weekStart := weekStartLocal.UTC()
	prevWeekStart := prevWeekStartLocal.UTC()

	spend, stash, totalBalance, err := o.currentBalancesForMiriamBrief(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("build Miriam brief balances: %w", err)
	}
	monthFlow, err := o.moneyFlowForMiriamBrief(ctx, userID, monthStart, now, "current month")
	if err != nil {
		return nil, fmt.Errorf("build Miriam brief money flow: %w", err)
	}
	lastMonthFlow, err := o.moneyFlowForMiriamBrief(ctx, userID, lastMonthStart, monthStart, "previous month")
	if err != nil {
		return nil, fmt.Errorf("build Miriam brief money flow: %w", err)
	}
	weekFlow, err := o.moneyFlowForMiriamBrief(ctx, userID, weekStart, now, "last 7 days")
	if err != nil {
		return nil, fmt.Errorf("build Miriam brief money flow: %w", err)
	}
	prevWeekFlow, err := o.moneyFlowForMiriamBrief(ctx, userID, prevWeekStart, weekStart, "previous 7 days")
	if err != nil {
		return nil, fmt.Errorf("build Miriam brief money flow: %w", err)
	}

	monthOut := totalOutflow(monthFlow)
	lastMonthOut := totalOutflow(lastMonthFlow)
	weekOut := totalOutflow(weekFlow)
	prevWeekOut := totalOutflow(prevWeekFlow)
	netFlow := monthFlow.TotalDeposits.Sub(monthOut)

	insights := make([]miriamInsight, 0, 6)
	actions := make([]miriamAction, 0, 4)

	if totalBalance.IsZero() && monthFlow.TotalDeposits.IsZero() && monthOut.IsZero() {
		insights = append(insights, miriamInsight{
			ID:         "no-money-data-yet",
			Type:       "empty_state",
			Severity:   "info",
			Importance: 100,
			Title:      "Miriam needs money movement first",
			Body:       "No completed deposits or spending yet. Once money enters Rail, Miriam can track what changed and suggest the next move.",
		})
		actions = append(actions, miriamAction{
			ID:       "fund-account",
			Type:     "fund_account",
			Priority: 100,
			Title:    "Fund the account",
			Body:     "Add money first. That gives Miriam real movement to watch.",
			CTA:      "Fund account",
		})
		return miriamBriefResponse(localNow, monthStartLocal, localNow, timezone, country, spend, stash, monthFlow, insights, actions), nil
	}

	insights = append(insights, buildNetFlowInsight(monthFlow, monthOut, netFlow))

	if prevWeekOut.IsPositive() && weekOut.GreaterThan(prevWeekOut.Mul(decimal.NewFromFloat(1.35))) {
		pct := weekOut.Sub(prevWeekOut).Div(prevWeekOut).Mul(decimal.NewFromInt(100))
		insights = append(insights, miriamInsight{
			ID:         "weekly-spend-jump",
			Type:       "spending_change",
			Severity:   "warning",
			Importance: 95,
			Title:      "Spending jumped this week",
			Body:       fmt.Sprintf("You spent $%s in the last 7 days vs $%s the week before. That's up %s%%.", weekOut.StringFixed(2), prevWeekOut.StringFixed(2), pct.StringFixed(0)),
			Evidence: map[string]interface{}{
				"last_7_days_outflow":     weekOut.StringFixed(2),
				"previous_7_days_outflow": prevWeekOut.StringFixed(2),
				"percent_change":          pct.StringFixed(1),
				"data_used":               []string{"weekly_money_flow", "previous_week_money_flow"},
			},
		})
	}

	if lastMonthOut.IsPositive() {
		delta := monthOut.Sub(lastMonthOut)
		if delta.Abs().GreaterThan(lastMonthOut.Mul(decimal.NewFromFloat(0.25))) {
			severity := "info"
			title := "Spending cooled down"
			if delta.IsPositive() {
				severity = "warning"
				title = "Spending is running hotter"
			}
			insights = append(insights, miriamInsight{
				ID:         "month-over-month-outflow",
				Type:       "monthly_change",
				Severity:   severity,
				Importance: 80,
				Title:      title,
				Body:       fmt.Sprintf("This month outflow is $%s vs $%s last month.", monthOut.StringFixed(2), lastMonthOut.StringFixed(2)),
				Evidence: map[string]interface{}{
					"this_month_outflow": monthOut.StringFixed(2),
					"last_month_outflow": lastMonthOut.StringFixed(2),
					"delta":              delta.StringFixed(2),
				},
			})
		}
	}

	budgetSet := false
	if o.budgetProvider != nil {
		if budget, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && budget != nil && budget.MonthlyLimit.IsPositive() {
			budgetSet = true
			pctUsed := monthOut.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
			daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
			expectedPct := decimal.NewFromInt(int64(now.Day())).Div(decimal.NewFromInt(int64(daysInMonth))).Mul(decimal.NewFromInt(100))
			remaining := budget.MonthlyLimit.Sub(monthOut)
			if pctUsed.GreaterThan(decimal.NewFromInt(90)) || pctUsed.GreaterThan(expectedPct.Add(decimal.NewFromInt(20))) {
				insights = append(insights, miriamInsight{
					ID:         "budget-pressure",
					Type:       "budget",
					Severity:   "warning",
					Importance: 90,
					Title:      "Budget pressure is real",
					Body:       fmt.Sprintf("You have used %s%% of your $%s monthly budget. $%s left.", pctUsed.StringFixed(0), budget.MonthlyLimit.StringFixed(2), remaining.StringFixed(2)),
					Evidence: map[string]interface{}{
						"monthly_budget": budget.MonthlyLimit.StringFixed(2),
						"spent":          monthOut.StringFixed(2),
						"remaining":      remaining.StringFixed(2),
						"percent_used":   pctUsed.StringFixed(1),
					},
				})
				actions = append(actions, miriamAction{
					ID:       "open-recovery-plan",
					Type:     "recovery_plan",
					Priority: 90,
					Title:    "Run a short recovery plan",
					Body:     "Use the next few days to bring spending pace back under control.",
					CTA:      "View recovery plan",
				})
			}
		}
	}

	obligationTotal, obligationCount, obligationNames := o.upcomingObligationPressure(ctx, userID, localNow, 7, loc)
	if obligationTotal.IsPositive() {
		severity := "info"
		importance := 70
		title := "Bills are coming up"
		if spend.LessThan(obligationTotal) {
			severity = "warning"
			importance = 92
			title = "Upcoming bills can squeeze Spend"
		}
		insights = append(insights, miriamInsight{
			ID:         "upcoming-obligations",
			Type:       "obligations",
			Severity:   severity,
			Importance: importance,
			Title:      title,
			Body:       fmt.Sprintf("$%s is due in the next 7 days across %d obligation(s).", obligationTotal.StringFixed(2), obligationCount),
			Evidence: map[string]interface{}{
				"upcoming_total": obligationTotal.StringFixed(2),
				"count":          obligationCount,
				"names":          obligationNames,
				"window_days":    7,
			},
		})
	}

	if o.recurringDetector != nil {
		if recurring, err := o.recurringDetector.DetectRecurring(ctx, userID); err == nil && len(recurring) > 0 {
			recurringMonthly := decimal.Zero
			for _, item := range recurring {
				monthly := item.AvgAmount
				if strings.EqualFold(item.Frequency, "weekly") {
					monthly = item.AvgAmount.Mul(decimal.NewFromFloat(4.33))
				}
				recurringMonthly = recurringMonthly.Add(monthly)
			}
			if recurringMonthly.IsPositive() {
				insights = append(insights, miriamInsight{
					ID:         "recurring-spend",
					Type:       "recurring_expense",
					Severity:   "info",
					Importance: 68,
					Title:      "Recurring charges are part of the picture",
					Body:       fmt.Sprintf("Detected recurring spend is about $%s/month across %d item(s).", recurringMonthly.StringFixed(2), len(recurring)),
					Evidence: map[string]interface{}{
						"monthly_recurring": recurringMonthly.StringFixed(2),
						"count":             len(recurring),
					},
				})
			}
		}
	}

	dailyBurn := decimal.Zero
	if weekOut.IsPositive() {
		dailyBurn = weekOut.Div(decimal.NewFromInt(7))
	}
	if dailyBurn.IsPositive() {
		runwayDays := spend.Div(dailyBurn)
		if runwayDays.LessThan(decimal.NewFromInt(7)) {
			insights = append(insights, miriamInsight{
				ID:         "spend-runway-low",
				Type:       "runway",
				Severity:   "critical",
				Importance: 100,
				Title:      "Spend balance may not last the week",
				Body:       fmt.Sprintf("Spend is $%s. At $%s/day, that is about %s days of runway.", spend.StringFixed(2), dailyBurn.StringFixed(2), runwayDays.StringFixed(0)),
				Evidence: map[string]interface{}{
					"spend_balance": spend.StringFixed(2),
					"daily_burn":    dailyBurn.StringFixed(2),
					"runway_days":   runwayDays.StringFixed(1),
				},
			})
		}
	}

	if spend.GreaterThan(decimal.NewFromInt(50)) {
		stashRatio := percentOf(stash, totalBalance)
		if stashRatio.LessThan(decimal.NewFromInt(25)) {
			moveAmount := suggestedStashMove(spend)
			if moveAmount.IsPositive() {
				actions = append(actions, miriamAction{
					ID:                   "move-idle-spend-to-stash",
					Type:                 "transfer_to_stash",
					Priority:             75,
					Title:                fmt.Sprintf("Move $%s to Stash", moveAmount.StringFixed(2)),
					Body:                 "Your Spend balance has room. Put a small piece to work without choking liquidity.",
					CTA:                  "Confirm move",
					ToolName:             ToolTransferFunds,
					RequiresConfirmation: true,
					Params: map[string]interface{}{
						"from":   "spend",
						"to":     "stash",
						"amount": moveAmount.StringFixed(2),
					},
				})
			}
		}
	}

	if !budgetSet {
		actions = append(actions, miriamAction{
			ID:                   "set-monthly-budget",
			Type:                 "set_budget",
			Priority:             65,
			Title:                "Set a monthly budget",
			Body:                 "Miriam can give better safe-to-spend guidance when there is a line she is tracking against.",
			CTA:                  "Set budget",
			ToolName:             ToolSetBudget,
			RequiresConfirmation: true,
		})
	}

	if len(actions) == 0 {
		actions = append(actions, miriamAction{
			ID:       "review-weekly",
			Type:     "review",
			Priority: 50,
			Title:    "Review this again after the next money move",
			Body:     "Nothing urgent is showing. The useful move is to check again after your next deposit or large spend.",
			CTA:      "Open timeline",
		})
	}

	// Portfolio insight — only when the user has investments.
	if o.portfolioProvider != nil {
		if stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID); err == nil && stats != nil && stats.TotalValue.GreaterThan(decimal.Zero) {
			body := fmt.Sprintf("Your investments are worth $%s.", stats.TotalValue.StringFixed(2))
			evidence := map[string]interface{}{
				"total_value": stats.TotalValue.StringFixed(2),
			}
			if !stats.TotalGainLoss.IsZero() {
				evidence["total_gain_loss"] = stats.TotalGainLoss.StringFixed(2)
			}
			// Append yield earned if the yield provider is available.
			if o.yieldProvider != nil {
				yieldFrom := now.AddDate(0, 0, -30)
				if snapshots, yErr := o.yieldProvider.GetSnapshotsInWindow(ctx, userID, yieldFrom, now); yErr == nil && len(snapshots) >= 2 {
					earned := snapshots[len(snapshots)-1].Balance.Sub(snapshots[0].Balance)
					if earned.LessThan(decimal.Zero) {
						earned = decimal.Zero
					}
					if earned.IsPositive() {
						body += fmt.Sprintf(" Stash yield earned in the last 30 days: $%s.", earned.StringFixed(2))
						evidence["yield_earned_30d"] = earned.StringFixed(2)
					}
				}
			}
			insights = append(insights, miriamInsight{
				ID:         "portfolio-summary",
				Type:       "portfolio",
				Severity:   "info",
				Importance: 72,
				Title:      "Portfolio is in the picture",
				Body:       body,
				Evidence:   evidence,
			})
		}
	}

	sort.SliceStable(insights, func(i, j int) bool {
		return insights[i].Importance > insights[j].Importance
	})
	sort.SliceStable(actions, func(i, j int) bool {
		return actions[i].Priority > actions[j].Priority
	})

	if len(insights) > 5 {
		insights = insights[:5]
	}
	if len(actions) > 3 {
		actions = actions[:3]
	}

	resp := miriamBriefResponse(localNow, monthStartLocal, localNow, timezone, country, spend, stash, monthFlow, insights, actions)

	// Proactively search SuperMemory for external bank statement transaction data.
	// Runs in parallel-safe isolation with its own timeout so it never blocks or panics the brief.
	if o.supermemory != nil && userID != uuid.Nil {
		smCtx, smCancel := context.WithTimeout(ctx, 5*time.Second)
		memories, smErr := o.supermemory.SearchMemory(smCtx, userID.String(), "financial transactions spending income last 3 months", 20)
		smCancel()
		if smErr == nil && len(memories) > 0 {
			var sb strings.Builder
			const maxMemoryBytes = 6000 // cap total size to avoid blowing LLM context
			for _, m := range memories {
				if m.Similarity < 0.5 {
					continue
				}
				text := strings.TrimSpace(m.Memory)
				if text == "" {
					continue
				}
				if sb.Len()+len(text) > maxMemoryBytes {
					break
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(text)
			}
			if sb.Len() > 0 {
				resp["external_transactions"] = sb.String()
			}
		}
	}

	// Include spending chart data for visualization (3-month window).
	if o.spending != nil {
		threeMonthsAgo := now.AddDate(0, -3, 0)
		if summary, chartErr := o.spending.GetSummary(ctx, userID, threeMonthsAgo, now); chartErr == nil && summary != nil && len(summary.DailyTrend) > 0 {
			points := make([]map[string]interface{}, 0, len(summary.DailyTrend))
			for _, d := range summary.DailyTrend {
				points = append(points, map[string]interface{}{"date": d.Period, "amount": d.Total.String(), "count": d.Count})
			}
			resp["chart_data"] = map[string]interface{}{
				"type":          "spending_trend",
				"data_points":   points,
				"total":         summary.Total.String(),
				"daily_average": summary.DailyAvg.StringFixed(2),
				"period_days":   summary.PeriodDays,
			}
		}
	}

	return resp, nil
}

func buildNetFlowInsight(flow *entities.MoneyFlowSummary, monthOut, netFlow decimal.Decimal) miriamInsight {
	severity := "positive"
	title := "More came in than went out"
	if netFlow.IsNegative() {
		severity = "warning"
		title = "More left than came in"
	} else if netFlow.IsZero() {
		severity = "info"
		title = "Money in and out are flat"
	}
	return miriamInsight{
		ID:         "month-net-flow",
		Type:       "money_flow",
		Severity:   severity,
		Importance: 85,
		Title:      title,
		Body:       fmt.Sprintf("$%s came in, $%s went out. Net flow is $%s.", flow.TotalDeposits.StringFixed(2), monthOut.StringFixed(2), netFlow.StringFixed(2)),
		Evidence: map[string]interface{}{
			"money_in":  flow.TotalDeposits.StringFixed(2),
			"money_out": monthOut.StringFixed(2),
			"net_flow":  netFlow.StringFixed(2),
		},
	}
}

func miriamBriefResponse(now, start, end time.Time, timezone, country string, spend, stash decimal.Decimal, flow *entities.MoneyFlowSummary, insights []miriamInsight, actions []miriamAction) map[string]interface{} {
	monthOut := totalOutflow(flow)
	return map[string]interface{}{
		"generated_at": now.Format(time.RFC3339),
		"timezone":     timezone,
		"country":      country,
		"window": map[string]interface{}{
			"key":   "this_month",
			"label": periodToLabel("this_month", start, end),
			"start": start.Format("2006-01-02"),
			"end":   end.Format("2006-01-02"),
		},
		"snapshot": map[string]interface{}{
			"spend_balance": spend.StringFixed(2),
			"stash_balance": stash.StringFixed(2),
			"total_balance": spend.Add(stash).StringFixed(2),
			"money_in":      flow.TotalDeposits.StringFixed(2),
			"money_out":     monthOut.StringFixed(2),
			"net_flow":      flow.TotalDeposits.Sub(monthOut).StringFixed(2),
			"deposit_count": flow.DepositCount,
			"outflow_count": flow.WithdrawalCount + flow.CardSpendCount + flow.P2PCount + flow.ReceiptCount,
		},
		"insights":     insights,
		"next_actions": actions,
		"data_used":    []string{"current_balances", "month_money_flow", "weekly_money_flow", "budget_if_present", "obligations_if_present", "recurring_expenses_if_present"},
	}
}

func totalOutflow(flow *entities.MoneyFlowSummary) decimal.Decimal {
	if flow == nil {
		return decimal.Zero
	}
	return flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P).Add(flow.TotalReceipts)
}

func suggestedStashMove(spend decimal.Decimal) decimal.Decimal {
	reserve := decimal.NewFromInt(25)
	available := spend.Sub(reserve)
	if !available.IsPositive() {
		return decimal.Zero
	}
	amount := spend.Mul(decimal.NewFromFloat(0.10))
	if amount.GreaterThan(available) {
		amount = available
	}
	if amount.GreaterThan(decimal.NewFromInt(50)) {
		amount = decimal.NewFromInt(50)
	}
	if amount.LessThan(decimal.NewFromInt(5)) {
		return decimal.Zero
	}
	return amount.RoundBank(2)
}

func (o *AgentAdapter) resolveMiriamLocation(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (*time.Location, string, string) {
	country := strings.ToUpper(strings.TrimSpace(stringArg(args, "country")))
	if country == "" && o.userProfile != nil {
		if c, err := o.userProfile.GetCountry(ctx, userID); err == nil {
			country = strings.ToUpper(strings.TrimSpace(c))
		}
	}
	tz := strings.TrimSpace(stringArg(args, "timezone"))
	if tz == "" {
		tz = timezoneForCountry(country)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		tz = "UTC"
		loc = time.UTC
	}
	return loc, tz, country
}

func timezoneForCountry(country string) string {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "NG":
		return "Africa/Lagos"
	case "GH":
		return "Africa/Accra"
	case "KE":
		return "Africa/Nairobi"
	case "ZA":
		return "Africa/Johannesburg"
	case "GB", "UK":
		return "Europe/London"
	case "US":
		return "America/New_York"
	case "CA":
		return "America/Toronto"
	case "DE":
		return "Europe/Berlin"
	case "FR":
		return "Europe/Paris"
	default:
		return "UTC"
	}
}

func (o *AgentAdapter) upcomingObligationPressure(ctx context.Context, userID uuid.UUID, localNow time.Time, days int, loc *time.Location) (decimal.Decimal, int, []string) {
	if o.obligations == nil {
		return decimal.Zero, 0, nil
	}
	obligations, err := o.obligations.ListActive(ctx, userID)
	if err != nil {
		return decimal.Zero, 0, nil
	}
	windowEnd := localNow.AddDate(0, 0, days)
	total := decimal.Zero
	names := make([]string, 0, len(obligations))
	for _, obligation := range obligations {
		if obligation.Currency != "" && !strings.EqualFold(obligation.Currency, "USD") && !strings.EqualFold(obligation.Currency, "USDC") {
			continue
		}
		if obligationDueInWindow(obligation, localNow, windowEnd, loc) {
			total = total.Add(obligation.Amount)
			names = append(names, obligation.Name)
		}
	}
	return total, len(names), names
}

func obligationDueInWindow(obligation entities.FinancialObligation, start, end time.Time, loc *time.Location) bool {
	if obligation.DueDate != nil {
		due := obligation.DueDate.In(loc)
		return !due.Before(start) && !due.After(end)
	}
	if obligation.DueDay == nil {
		return false
	}
	day := *obligation.DueDay
	if day < 1 {
		return false
	}
	lastDay := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	due := time.Date(start.Year(), start.Month(), day, 0, 0, 0, 0, loc)
	if due.Before(start) {
		nextLastDay := time.Date(start.Year(), start.Month()+2, 0, 0, 0, 0, 0, loc).Day()
		nextDay := *obligation.DueDay
		if nextDay > nextLastDay {
			nextDay = nextLastDay
		}
		due = time.Date(start.Year(), start.Month()+1, nextDay, 0, 0, 0, 0, loc)
	}
	return !due.Before(start) && !due.After(end)
}

func (o *AgentAdapter) buildUserTimeContext(ctx context.Context, userID uuid.UUID) string {
	loc, timezone, _ := o.resolveMiriamLocation(ctx, userID, nil)
	if loc == nil {
		loc = time.Local
		timezone = loc.String()
	}
	now := time.Now().In(loc)
	return buildTimeContextAt(now, timezone)
}

func (o *AgentAdapter) currentBalancesForMiriamBrief(ctx context.Context, userID uuid.UUID) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	if o.aggregateStats == nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("aggregate stats service unavailable")
	}
	spend, err := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("get spending balance: %w", err)
	}
	stash, err := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("get stash balance: %w", err)
	}
	return spend, stash, spend.Add(stash), nil
}

func (o *AgentAdapter) moneyFlowForMiriamBrief(ctx context.Context, userID uuid.UUID, start, end time.Time, label string) (*entities.MoneyFlowSummary, error) {
	if o.spending == nil {
		return nil, fmt.Errorf("spending service unavailable")
	}
	flow, err := o.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get %s money flow: %w", label, err)
	}
	if flow == nil {
		return nil, fmt.Errorf("get %s money flow: empty result", label)
	}
	return flow, nil
}
