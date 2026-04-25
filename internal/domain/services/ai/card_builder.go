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
			{Label: "Peak Day", Value: peakDay, Icon: "📅"},
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
			{Label: "Spend Balance", Value: "$" + str(data, "spend_balance"), Icon: "💳"},
			{Label: "Stash Balance", Value: "$" + str(data, "stash_balance"), Icon: "🏦"},
			{Label: "Savings Rate", Value: str(data, "savings_rate"), Sentiment: sentiment},
			{Label: "Streak", Value: fmt.Sprintf("%d days", num(data, "streak_days")), Icon: "🔥"},
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
		{Label: "Money In", Value: "$" + deposits, Icon: "📥", Sentiment: "positive"},
		{Label: "Money Out", Value: "$" + totalOut, Icon: "📤", Sentiment: "negative"},
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
		{Label: "Spend", Value: "$" + str(data, "spend_balance"), Icon: "💳"},
		{Label: "Stash", Value: "$" + str(data, "stash_balance"), Icon: "🏦"},
		{Label: "Total", Value: "$" + str(data, "total_balance"), Icon: "💰"},
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
		items = append(items, entities.StatItem{Label: "Streak", Value: fmt.Sprintf("%d days 🔥", streakDays)})
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
