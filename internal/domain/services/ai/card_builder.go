package ai

import (
	"fmt"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

var categoryColors = []string{"#4CAF50", "#2196F3", "#FF9800", "#E91E63", "#9C27B0", "#00BCD4", "#FF5722", "#607D8B"}

func buildCardsFromToolResults(results []ToolResult) []entities.InsightCard {
	var cards []entities.InsightCard
	for _, tr := range results {
		if _, hasErr := tr.Result["error"]; hasErr {
			continue
		}
		switch tr.Name {
		case ToolGetPortfolioStats:
			cards = append(cards, buildPortfolioCard(tr.Result))
		case ToolGetSpendingSummary:
			cards = append(cards, buildSpendingCards(tr.Result)...)
		case ToolGetSpendingChart:
			cards = append(cards, buildSpendingChartCard(tr.Result))
		case ToolGetBalanceHistory:
			cards = append(cards, buildBalanceChartCard(tr.Result))
		case ToolGetAllocations:
			cards = append(cards, buildAllocationCard(tr.Result))
		case ToolGetSpendingPatterns:
			cards = append(cards, buildPatternCards(tr.Result)...)
		case ToolSimulateSavings:
			cards = append(cards, buildSimulationCard(tr.Result))
		case ToolGetComparativeContext:
			cards = append(cards, buildComparativeCard(tr.Result))
		case ToolGetMoneyFlow:
			cards = append(cards, buildMoneyFlowCard(tr.Result))
		case ToolGetAccountSummary:
			cards = append(cards, buildAccountSummaryCard(tr.Result))
		case ToolGetFinancialHealth:
			cards = append(cards, buildFinancialHealthCard(tr.Result))
		case ToolGetFinancialAudit:
			cards = append(cards, buildFinancialAuditCard(tr.Result))
		case ToolGetCashFlowForecast:
			cards = append(cards, buildCashFlowForecastCard(tr.Result))
		case ToolGetFinancialPlan:
			cards = append(cards, buildFinancialPlanCard(tr.Result))
		case ToolGetActionReceipts:
			cards = append(cards, buildActionReceiptsCard(tr.Result))
		case ToolGetFinancialAdvice:
			cards = append(cards, buildFinancialAdviceCard(tr.Result))
		case ToolGetFinancialTimeline:
			cards = append(cards, buildFinancialTimelineCard(tr.Result))
		case ToolGetSubscriptions:
			cards = append(cards, BuildSubscriptionAuditCard(tr.Result))
		case ToolGetRunway:
			cards = append(cards, BuildRunwayCard(tr.Result))
		case ToolGetDepositPattern:
			cards = append(cards, BuildDepositPatternCard(tr.Result))
		case ToolGetYieldSummary:
			cards = append(cards, BuildYieldSummaryCard(tr.Result))
		case ToolGetSpendingComparison:
			cards = append(cards, BuildComparisonCard(tr.Result))
		}
	}
	return cards
}

func str(data map[string]interface{}, key string) string {
	v, _ := data[key].(string)
	return v
}

func num(data map[string]interface{}, key string) int {
	switch v := data[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func toMapSlice(v interface{}) []map[string]interface{} {
	switch s := v.(type) {
	case []map[string]interface{}:
		return s
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(s))
		for _, item := range s {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func buildPortfolioCard(data map[string]interface{}) entities.InsightCard {
	totalVal := str(data, "total_value")
	weeklyRet := str(data, "weekly_return")
	weeklyPct := str(data, "weekly_return_pct")

	sentiment := "positive"
	if len(weeklyRet) > 0 && weeklyRet[0] == '-' {
		sentiment = "negative"
	}

	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Portfolio Overview",
		Sentiment: sentiment,
		Data: []entities.StatItem{
			{Label: "Total Value", Value: "$" + totalVal, Sentiment: "neutral"},
			{Label: "This Week", Value: weeklyPct + "%", Change: weeklyRet, Sentiment: sentiment},
		},
	}
}

func buildSpendingCards(data map[string]interface{}) []entities.InsightCard {
	var cards []entities.InsightCard

	total := str(data, "total")
	dailyAvg := str(data, "daily_average")
	txCount := num(data, "transaction_count")

	cards = append(cards, entities.InsightCard{
		Type:  "stat_grid",
		Title: "Spending Summary",
		Data: []entities.StatItem{
			{Label: "Total Spent", Value: "$" + total},
			{Label: "Daily Average", Value: "$" + dailyAvg},
			{Label: "Transactions", Value: fmt.Sprintf("%d", txCount)},
		},
	})

	cats := toMapSlice(data["categories"])
	if len(cats) > 0 {
		items := make([]entities.BreakdownItem, 0, len(cats))
		totalDec, _ := decimal.NewFromString(total)
		for i, c := range cats {
			amt, _ := decimal.NewFromString(str(c, "total"))
			pct := decimal.Zero
			if !totalDec.IsZero() {
				pct = amt.Div(totalDec).Mul(decimal.NewFromInt(100))
			}
			items = append(items, entities.BreakdownItem{
				Label:   humanizeCategory(str(c, "category")),
				Amount:  amt,
				Percent: pct,
				Color:   categoryColors[i%len(categoryColors)],
			})
		}
		cards = append(cards, entities.InsightCard{
			Type:  "breakdown",
			Title: "By Category",
			Data:  items,
		})
	}

	return cards
}

func buildSpendingChartCard(data map[string]interface{}) entities.InsightCard {
	points := toMapSlice(data["data_points"])
	chartPoints := make([]entities.ChartPoint, 0, len(points))
	for _, p := range points {
		val, _ := decimal.NewFromString(str(p, "amount"))
		chartPoints = append(chartPoints, entities.ChartPoint{
			Label: str(p, "date"),
			Value: val,
		})
	}
	return entities.InsightCard{
		Type:  "chart",
		Title: "Spending Trend",
		Data: entities.ChartData{
			ChartType: "bar",
			Points:    chartPoints,
			YLabel:    "$",
		},
	}
}

func buildBalanceChartCard(data map[string]interface{}) entities.InsightCard {
	points := toMapSlice(data["data_points"])
	growthPct := str(data, "growth_pct")

	chartPoints := make([]entities.ChartPoint, 0, len(points))
	for _, p := range points {
		val, _ := decimal.NewFromString(str(p, "balance"))
		chartPoints = append(chartPoints, entities.ChartPoint{
			Label: str(p, "date"),
			Value: val,
		})
	}

	sentiment := "positive"
	if len(growthPct) > 0 && growthPct[0] == '-' {
		sentiment = "negative"
	}

	return entities.InsightCard{
		Type:      "chart",
		Title:     "Savings Growth",
		Subtitle:  growthPct + "% growth",
		Sentiment: sentiment,
		Data: entities.ChartData{
			ChartType: "line",
			Points:    chartPoints,
			YLabel:    "$",
		},
	}
}

func buildAllocationCard(data map[string]interface{}) entities.InsightCard {
	allocs := toMapSlice(data["allocations"])
	items := make([]entities.BreakdownItem, 0, len(allocs))
	for i, m := range allocs {
		val, _ := decimal.NewFromString(fmt.Sprintf("%v", m["value"]))
		weight, _ := decimal.NewFromString(fmt.Sprintf("%v", m["weight"]))
		items = append(items, entities.BreakdownItem{
			Label:   fmt.Sprintf("%v", m["basket_name"]),
			Amount:  val,
			Percent: weight.Mul(decimal.NewFromInt(100)),
			Color:   categoryColors[i%len(categoryColors)],
		})
	}
	return entities.InsightCard{
		Type:  "breakdown",
		Title: "Portfolio Allocation",
		Data:  items,
	}
}

func buildPatternCards(data map[string]interface{}) []entities.InsightCard {
	var cards []entities.InsightCard

	peakDay := str(data, "peak_spending_day")
	weekTrend := str(data, "week_over_week_trend")
	weekChangePct := str(data, "week_change_pct")

	trendSentiment := "neutral"
	trendIcon := "→"
	if weekTrend == "increasing" {
		trendSentiment = "negative"
		trendIcon = "↑"
	} else if weekTrend == "decreasing" {
		trendSentiment = "positive"
		trendIcon = "↓"
	}

	cards = append(cards, entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Spending Patterns",
		Sentiment: trendSentiment,
		Data: []entities.StatItem{
			{Label: "Peak Day", Value: peakDay, Icon: "calendar-03"},
			{Label: "Week Trend", Value: trendIcon + " " + weekChangePct + "%", Sentiment: trendSentiment},
			{Label: "Weekend", Value: "$" + str(data, "weekend_total")},
			{Label: "Weekday", Value: "$" + str(data, "weekday_total")},
		},
	})

	// Day of week chart
	days := toMapSlice(data["day_of_week_breakdown"])
	if len(days) > 0 {
		points := make([]entities.ChartPoint, len(days))
		peakIdx := 0
		peakVal := decimal.Zero
		for i, d := range days {
			val, _ := decimal.NewFromString(str(d, "total"))
			label := str(d, "day")
			if len(label) > 3 {
				label = label[:3]
			}
			points[i] = entities.ChartPoint{Label: label, Value: val}
			if val.GreaterThan(peakVal) {
				peakVal = val
				peakIdx = i
			}
		}
		cards = append(cards, entities.InsightCard{
			Type:  "chart",
			Title: "Spending by Day",
			Data: entities.ChartData{
				ChartType: "bar",
				Points:    points,
				YLabel:    "$",
				Annotations: []entities.ChartAnnotation{
					{Label: "Peak", Value: peakVal, Index: peakIdx, Type: "peak"},
				},
			},
		})
	}

	return cards
}

func buildSimulationCard(data map[string]interface{}) entities.InsightCard {
	sim, _ := data["simulation"].(map[string]interface{})
	points := toMapSlice(data["monthly_projections"])

	chartPoints := make([]entities.ChartPoint, len(points))
	for i, p := range points {
		val, _ := decimal.NewFromString(str(p, "stash_balance"))
		chartPoints[i] = entities.ChartPoint{
			Label: fmt.Sprintf("M%d", num(p, "month")),
			Value: val,
		}
	}

	milestones, _ := data["milestones"].(map[string]int)
	annotations := make([]entities.ChartAnnotation, 0)
	for label, month := range milestones {
		for i, p := range points {
			if num(p, "month") == month {
				val, _ := decimal.NewFromString(str(p, "stash_balance"))
				annotations = append(annotations, entities.ChartAnnotation{
					Label: label, Value: val, Index: i, Type: "milestone",
				})
				break
			}
		}
	}

	return entities.InsightCard{
		Type:      "chart",
		Title:     "Savings Projection",
		Subtitle:  "Stash: $" + str(sim, "final_stash_balance") + " (incl. $" + str(sim, "total_yield_earned") + " yield)",
		Sentiment: "positive",
		Data: entities.ChartData{
			ChartType:   "line",
			Points:      chartPoints,
			YLabel:      "$",
			Annotations: annotations,
		},
	}
}

func buildComparativeCard(data map[string]interface{}) entities.InsightCard {
	stashLevel := str(data, "stash_level")
	sentiment := "neutral"
	if stashLevel == "strong" || stashLevel == "impressive" {
		sentiment = "positive"
	}

	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Your Financial Snapshot",
		Sentiment: sentiment,
		Data: []entities.StatItem{
			{Label: "Spend Balance", Value: "$" + str(data, "spend_balance"), Icon: "credit-card"},
			{Label: "Stash Balance", Value: "$" + str(data, "stash_balance"), Icon: "safe-box"},
			{Label: "Savings Rate", Value: str(data, "savings_rate"), Sentiment: sentiment},
			{Label: "Streak", Value: fmt.Sprintf("%d days", num(data, "streak_days")), Icon: "fire"},
		},
	}
}

func buildMoneyFlowCard(data map[string]interface{}) entities.InsightCard {
	moneyIn, _ := data["money_in"].(map[string]interface{})
	moneyOut, _ := data["money_out"].(map[string]interface{})

	deposits := str(moneyIn, "total_deposits")
	totalOut := str(moneyOut, "total")
	netFlow := str(data, "net_flow")

	sentiment := "positive"
	if len(netFlow) > 0 && netFlow[0] == '-' {
		sentiment = "negative"
	}

	items := []entities.StatItem{
		{Label: "Money In", Value: "$" + deposits, Icon: "arrow-down-01", Sentiment: "positive"},
		{Label: "Money Out", Value: "$" + totalOut, Icon: "arrow-up-01", Sentiment: "negative"},
		{Label: "Net Flow", Value: "$" + netFlow, Sentiment: sentiment},
	}

	// Add breakdown if available
	if withdrawals := str(moneyOut, "withdrawals"); withdrawals != "" && withdrawals != "0.00" {
		items = append(items, entities.StatItem{Label: "Withdrawals", Value: "$" + withdrawals})
	}
	if cardSpend := str(moneyOut, "card_spend"); cardSpend != "" && cardSpend != "0.00" {
		items = append(items, entities.StatItem{Label: "Card Spend", Value: "$" + cardSpend})
	}
	if p2p := str(moneyOut, "p2p_transfers"); p2p != "" && p2p != "0.00" {
		items = append(items, entities.StatItem{Label: "P2P Transfers", Value: "$" + p2p})
	}

	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Money Flow",
		Subtitle:  str(data, "period"),
		Sentiment: sentiment,
		Data:      items,
	}
}

func buildAccountSummaryCard(data map[string]interface{}) entities.InsightCard {
	items := []entities.StatItem{
		{Label: "Spend", Value: "$" + str(data, "spend_balance"), Icon: "credit-card"},
		{Label: "Stash", Value: "$" + str(data, "stash_balance"), Icon: "safe-box"},
		{Label: "Total", Value: "$" + str(data, "total_balance"), Icon: "wallet-01"},
	}

	if thisMonth, ok := data["this_month"].(map[string]interface{}); ok {
		netFlow := str(thisMonth, "net_flow")
		sentiment := "positive"
		if len(netFlow) > 0 && netFlow[0] == '-' {
			sentiment = "negative"
		}
		items = append(items, entities.StatItem{Label: "Net This Month", Value: "$" + netFlow, Sentiment: sentiment})
	}

	if streakDays := num(data, "streak_days"); streakDays > 0 {
		items = append(items, entities.StatItem{Label: "Streak", Value: fmt.Sprintf("%d days", streakDays)})
	}

	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Account Overview",
		Sentiment: "neutral",
		Data:      items,
	}
}

func buildFinancialHealthCard(data map[string]interface{}) entities.InsightCard {
	score := num(data, "score")
	sentiment := "negative"
	if score >= 80 {
		sentiment = "positive"
	} else if score >= 60 {
		sentiment = "neutral"
	}
	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Financial Health",
		Subtitle:  fmt.Sprintf("%d/100", score),
		Sentiment: sentiment,
		Data: []entities.StatItem{
			{Label: "Score", Value: fmt.Sprintf("%d", score), Sentiment: sentiment},
			{Label: "Net Flow", Value: "$" + str(data, "net_flow")},
			{Label: "Savings Rate", Value: str(data, "savings_rate_pct") + "%"},
			{Label: "Budget", Value: str(data, "budget_status")},
		},
	}
}

func buildFinancialAuditCard(data map[string]interface{}) entities.InsightCard {
	score, _ := data["score"].(map[string]interface{})
	snapshot, _ := data["snapshot"].(map[string]interface{})
	damage, _ := data["the_damage"].(map[string]interface{})

	totalScore := num(score, "total")
	status := fmt.Sprintf("%v", score["status"])
	sentiment := "negative"
	if totalScore >= 80 {
		sentiment = "positive"
	} else if totalScore >= 60 {
		sentiment = "neutral"
	}

	metrics := []entities.StatItem{
		{Label: "Audit Score", Value: fmt.Sprintf("%d/100", totalScore), Sentiment: sentiment},
		{Label: "Money In", Value: "$" + str(snapshot, "money_in"), Sentiment: "positive"},
		{Label: "Money Out", Value: "$" + str(snapshot, "total_money_out"), Sentiment: "negative"},
		{Label: "Net Flow", Value: "$" + str(snapshot, "net_flow"), Sentiment: sentimentFromSignedValue(str(snapshot, "net_flow"))},
	}

	breakdown := make([]entities.BreakdownItem, 0)
	if moneyIn := decimalFromString(str(snapshot, "money_in")); moneyIn.IsPositive() {
		breakdown = append(breakdown, entities.BreakdownItem{Label: "Money In", Amount: moneyIn, Percent: decimal.NewFromInt(100), Color: "#1A7A6D"})
	}
	for _, item := range []struct {
		label string
		key   string
		color string
	}{
		{label: "Digital Out", key: "digital_money_out", color: "#FF3E00"},
		{label: "Cash Receipts", key: "receipt_cash_out", color: "#FFB199"},
	} {
		amount := decimalFromString(str(snapshot, item.key))
		totalOut := decimalFromString(str(snapshot, "total_money_out"))
		if amount.IsPositive() {
			breakdown = append(breakdown, entities.BreakdownItem{
				Label:   item.label,
				Amount:  amount,
				Percent: percentDecimal(amount, totalOut),
				Color:   item.color,
			})
		}
	}

	return entities.InsightCard{
		Type:      "financial_audit",
		Title:     "Miriam Audit",
		Subtitle:  strings.ReplaceAll(status, "_", " "),
		Sentiment: sentiment,
		Data: map[string]interface{}{
			"score":          score,
			"period":         data["period"],
			"data_coverage":  data["data_coverage"],
			"monthly_trend":  data["monthly_trend"],
			"metrics":        metrics,
			"breakdown":      breakdown,
			"snapshot":       snapshot,
			"damage":         damage,
			"patterns":       data["the_pattern"],
			"contradictions": data["contradictions"],
			"top_categories": data["top_spending_categories"],
			"risk_flags":     data["risk_flags"],
			"next_actions":   data["do_this_today"],
		},
	}
}

func buildCashFlowForecastCard(data map[string]interface{}) entities.InsightCard {
	projectedNet := str(data, "projected_net_flow")
	sentiment := "positive"
	if len(projectedNet) > 0 && projectedNet[0] == '-' {
		sentiment = "negative"
	}
	return entities.InsightCard{
		Type:      "stat_grid",
		Title:     "Cash Flow Forecast",
		Subtitle:  str(data, "period"),
		Sentiment: sentiment,
		Data: []entities.StatItem{
			{Label: "Safe / Day", Value: "$" + str(data, "safe_daily_spend")},
			{Label: "Daily Burn", Value: "$" + str(data, "daily_burn_rate")},
			{Label: "Projected Net", Value: "$" + projectedNet, Sentiment: sentiment},
			{Label: "End Balance", Value: "$" + str(data, "projected_end_balance")},
		},
	}
}

func buildFinancialPlanCard(data map[string]interface{}) entities.InsightCard {
	steps := toMapSlice(data["next_steps"])
	items := make([]entities.StatItem, 0, len(steps))
	for _, step := range steps {
		items = append(items, entities.StatItem{
			Label: fmt.Sprintf("%v", step["title"]),
			Value: fmt.Sprintf("%v", step["action"]),
		})
	}
	return entities.InsightCard{
		Type:  "stat_grid",
		Title: "Your Money Plan",
		Data:  items,
	}
}

func buildActionReceiptsCard(data map[string]interface{}) entities.InsightCard {
	receipts := toMapSlice(data["receipts"])
	items := make([]entities.StatItem, 0, len(receipts))
	for _, receipt := range receipts {
		items = append(items, entities.StatItem{
			Label: fmt.Sprintf("%v", receipt["action"]),
			Value: fmt.Sprintf("%v", receipt["status"]),
		})
	}
	return entities.InsightCard{
		Type:  "stat_grid",
		Title: "Miriam Action Receipts",
		Data:  items,
	}
}

func buildFinancialAdviceCard(data map[string]interface{}) entities.InsightCard {
	checks := toMapSlice(data["checks"])
	items := make([]entities.StatItem, 0, len(checks))
	for _, check := range checks {
		items = append(items, entities.StatItem{
			Label: fmt.Sprintf("%v", check["title"]),
			Value: fmt.Sprintf("%v", check["recommendation"]),
		})
	}
	return entities.InsightCard{
		Type:      "alert",
		Title:     "Financial Advice",
		Subtitle:  str(data, "overall_status"),
		Sentiment: sentimentFromStatus(str(data, "overall_status")),
		Data:      items,
	}
}

func buildFinancialTimelineCard(data map[string]interface{}) entities.InsightCard {
	events := toMapSlice(data["events"])
	items := make([]entities.StatItem, 0, len(events))
	for _, event := range events {
		items = append(items, entities.StatItem{
			Label: fmt.Sprintf("%v", event["title"]),
			Value: fmt.Sprintf("%v", event["description"]),
		})
	}
	return entities.InsightCard{
		Type:      "breakdown",
		Title:     "Financial Timeline",
		Subtitle:  str(data, "summary"),
		Sentiment: "neutral",
		Data:      items,
	}
}

func sentimentFromStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "critical", "warning", "fragile", "needs_attention":
		return "negative"
	case "good", "strong", "steady", "on_track":
		return "positive"
	default:
		return "neutral"
	}
}

func sentimentFromSignedValue(value string) string {
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return "negative"
	}
	return "positive"
}

func decimalFromString(value string) decimal.Decimal {
	d, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return decimal.Zero
	}
	return d
}

func percentDecimal(part, total decimal.Decimal) decimal.Decimal {
	if total.IsZero() {
		return decimal.Zero
	}
	return part.Div(total).Mul(decimal.NewFromInt(100))
}

// ─── New Card Builders ────────────────────────────────────────────────────────

// BuildTipCard creates a contextual financial tip card.
func BuildTipCard(emoji, title, message, severity string) entities.InsightCard {
	if severity == "" {
		severity = "info"
	}
	return entities.InsightCard{
		Type:  "tip",
		Title: title,
		Data: entities.TipData{
			Emoji:    emoji,
			Message:  message,
			Severity: severity,
		},
	}
}

// BuildSubscriptionAuditCard creates a subscription analysis card from detected recurring charges.
func BuildSubscriptionAuditCard(data map[string]interface{}) entities.InsightCard {
	subs := toMapSlice(data["subscriptions"])
	totalMonthly := decimalFromString(str(data, "total_monthly"))
	totalYearly := decimalFromString(str(data, "total_yearly"))

	items := make([]entities.SubscriptionItem, 0, len(subs))
	for _, s := range subs {
		items = append(items, entities.SubscriptionItem{
			Name:      str(s, "name"),
			Amount:    decimalFromString(str(s, "amount")),
			Frequency: str(s, "frequency"),
			Category:  str(s, "category"),
			Status:    str(s, "status"),
		})
	}

	return entities.InsightCard{
		Type:  "subscription_audit",
		Title: "Subscriptions",
		Data: entities.SubscriptionAuditData{
			TotalMonthly:  totalMonthly,
			TotalYearly:   totalYearly,
			Subscriptions: items,
			SavingsTip:    str(data, "savings_tip"),
		},
	}
}

// BuildRunwayCard creates a runway card showing how long money lasts.
func BuildRunwayCard(data map[string]interface{}) entities.InsightCard {
	months := num(data, "months")
	days := num(data, "days")
	status := str(data, "status")
	if status == "" {
		switch {
		case months >= 6:
			status = "healthy"
		case months >= 2:
			status = "caution"
		default:
			status = "critical"
		}
	}

	sentiment := "positive"
	if status == "critical" {
		sentiment = "negative"
	} else if status == "caution" {
		sentiment = "neutral"
	}

	return entities.InsightCard{
		Type:      "runway",
		Title:     "Financial Runway",
		Sentiment: sentiment,
		Data: entities.RunwayData{
			Months:       months,
			Days:         days,
			Status:       status,
			DailyBurn:    decimalFromString(str(data, "daily_burn")),
			Balance:      decimalFromString(str(data, "balance")),
			ProjectedEnd: str(data, "projected_end"),
		},
	}
}

// BuildDepositPatternCard creates a deposit frequency/consistency card.
func BuildDepositPatternCard(data map[string]interface{}) entities.InsightCard {
	history := toMapSlice(data["history"])
	points := make([]entities.ChartPoint, 0, len(history))
	for _, h := range history {
		val := decimalFromString(str(h, "amount"))
		points = append(points, entities.ChartPoint{Label: str(h, "date"), Value: val})
	}

	consistency := num(data, "consistency")
	sentiment := "positive"
	if consistency < 50 {
		sentiment = "negative"
	} else if consistency < 75 {
		sentiment = "neutral"
	}

	return entities.InsightCard{
		Type:      "deposit_pattern",
		Title:     "Deposit Pattern",
		Sentiment: sentiment,
		Data: entities.DepositPatternData{
			Frequency:     str(data, "frequency"),
			AverageAmount: decimalFromString(str(data, "average_amount")),
			LastDeposit:   str(data, "last_deposit"),
			NextExpected:  str(data, "next_expected"),
			Streak:        num(data, "streak"),
			Consistency:   consistency,
			History:       points,
		},
	}
}

// BuildYieldSummaryCard creates a stash yield performance card.
func BuildYieldSummaryCard(data map[string]interface{}) entities.InsightCard {
	growthPoints := toMapSlice(data["growth_points"])
	points := make([]entities.ChartPoint, 0, len(growthPoints))
	for _, p := range growthPoints {
		val := decimalFromString(str(p, "balance"))
		points = append(points, entities.ChartPoint{Label: str(p, "date"), Value: val})
	}

	return entities.InsightCard{
		Type:      "yield_summary",
		Title:     "Stash Yield",
		Sentiment: "positive",
		Data: entities.YieldSummaryData{
			TotalEarned:   decimalFromString(str(data, "total_earned")),
			MonthEarned:   decimalFromString(str(data, "month_earned")),
			CurrentAPY:    decimalFromString(str(data, "current_apy")),
			StashBalance:  decimalFromString(str(data, "stash_balance")),
			DailyEstimate: decimalFromString(str(data, "daily_estimate")),
			GrowthPoints:  points,
		},
	}
}

// BuildComparisonCard creates a month-over-month or period comparison card.
func BuildComparisonCard(data map[string]interface{}) entities.InsightCard {
	deltaPct := decimalFromString(str(data, "delta_pct"))
	sentiment := "neutral"
	direction := str(data, "direction") // "spending" or "saving"
	if direction == "spending" {
		if deltaPct.IsNegative() {
			sentiment = "positive" // spending went down = good
		} else if deltaPct.IsPositive() {
			sentiment = "negative"
		}
	} else {
		if deltaPct.IsPositive() {
			sentiment = "positive" // saving went up = good
		} else if deltaPct.IsNegative() {
			sentiment = "negative"
		}
	}

	current, _ := data["current"].(map[string]interface{})
	previous, _ := data["previous"].(map[string]interface{})

	return entities.InsightCard{
		Type:      "comparison",
		Title:     str(data, "title"),
		Sentiment: sentiment,
		Data: entities.ComparisonData{
			MetricLabel: str(data, "metric_label"),
			Current: entities.ComparisonItem{
				Label: str(current, "label"),
				Value: decimalFromString(str(current, "value")),
				Color: str(current, "color"),
			},
			Previous: entities.ComparisonItem{
				Label: str(previous, "label"),
				Value: decimalFromString(str(previous, "value")),
				Color: str(previous, "color"),
			},
			DeltaPct:  deltaPct,
			Sentiment: sentiment,
		},
	}
}
