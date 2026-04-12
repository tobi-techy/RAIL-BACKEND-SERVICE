package ai

import (
	"fmt"

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
				Label:   str(c, "category"),
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
