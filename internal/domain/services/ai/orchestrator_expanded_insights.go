package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

// Tool names for expanded insight cards.
const (
	ToolGetSubscriptions      = "get_subscriptions"
	ToolGetRunway             = "get_runway"
	ToolGetDepositPattern     = "get_deposit_pattern"
	ToolGetYieldSummary       = "get_yield_summary"
	ToolGetSpendingComparison = "get_spending_comparison"
)

// ExpandedInsightTools returns the tool definitions for the new insight card types.
func ExpandedInsightTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetSubscriptions,
			Description: "Detect and list the user's recurring subscriptions from transaction history. Shows monthly/yearly totals and identifies cancellable services. Use when user asks about subscriptions, recurring charges, or wants to cut costs.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetRunway,
			Description: "Calculate how long the user's current balance will last at their current spending rate. Shows months/days remaining and health status. Use when user asks 'how long will my money last', runway, burn rate, or sustainability.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetDepositPattern,
			Description: "Analyze the user's deposit frequency, consistency, and predict next expected deposit. Use when user asks about income patterns, deposit history, or payment regularity.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetYieldSummary,
			Description: "Get the user's stash yield performance: total earned, this month's yield, current APY, and growth over time. Use when user asks about yield, stash performance, interest earned, or how much their stash made.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetSpendingComparison,
			Description: "Compare the user's spending between two periods (e.g. this month vs last month). Shows delta percentage and direction. Use when user asks 'am I spending more', month-over-month, or comparison questions.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
	}
}

// executeGetSubscriptions detects recurring charges from transaction history.
func (o *AgentAdapter) executeGetSubscriptions(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.recurringDetector != nil {
		expenses, err := o.recurringDetector.DetectRecurring(ctx, userID)
		if err == nil {
			candidates := make([]map[string]interface{}, 0, len(expenses))
			totalMonthly := decimal.Zero
			for _, e := range expenses {
				monthly := e.AvgAmount
				if e.Frequency == "weekly" {
					monthly = e.AvgAmount.Mul(decimal.NewFromFloat(4.33))
				}
				totalMonthly = totalMonthly.Add(monthly)
				candidateID := fmt.Sprintf("%s:%s:%s", e.Merchant, e.Frequency, e.AvgAmount.StringFixed(2))
				candidates = append(candidates, map[string]interface{}{
					"candidate_id": candidateID,
					"name":         e.Merchant,
					"amount":       e.AvgAmount.StringFixed(2),
					"monthly":      monthly.StringFixed(2),
					"frequency":    e.Frequency,
					"first_seen":   e.FirstSeen,
					"last_seen":    e.LastSeen,
					"count":        e.Count,
					"status":       "candidate",
					"recommended_actions": []map[string]interface{}{
						{"type": ToolProtectSubscription, "label": "Track this bill", "requires_confirmation": true},
						{"type": ToolMarkSubscriptionCancelled, "label": "Mark cancelled", "requires_confirmation": true},
						{"type": ToolIgnoreSubscription, "label": "Ignore", "requires_confirmation": true},
					},
				})
			}
			totalYearly := totalMonthly.Mul(decimal.NewFromInt(12))

			// Enrich subscription candidates with plain descriptions and context
			if enrichmentMap := enrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
				for _, c := range candidates {
					enrichMerchantEntry(c, enrichmentMap)
				}
			}

			return map[string]interface{}{
				"source":        "recurring_detector",
				"candidates":    candidates,
				"subscriptions": candidates,
				"total_monthly": totalMonthly.StringFixed(2),
				"total_yearly":  totalYearly.StringFixed(2),
				"savings_tip":   subscriptionSavingsTip(totalYearly),
			}, nil
		}
	}

	if o.spending == nil {
		return map[string]interface{}{"error": "spending service unavailable"}, nil
	}

	end := time.Now()
	start := end.AddDate(0, -3, 0)
	txns, err := o.spending.GetTransactions(ctx, userID, start, end, 200)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("failed to get transactions: %v", err)}, nil
	}

	type merchantEntry struct {
		count int
		total decimal.Decimal
	}
	merchantCounts := make(map[string]*merchantEntry)
	for _, tx := range txns {
		key := tx.Source
		if key == "" {
			key = tx.Category
		}
		if key == "" {
			continue
		}
		entry, ok := merchantCounts[key]
		if !ok {
			entry = &merchantEntry{}
			merchantCounts[key] = entry
		}
		entry.count++
		entry.total = entry.total.Add(tx.Amount)
	}

	var subs []map[string]interface{}
	totalMonthly := decimal.Zero
	for merchant, data := range merchantCounts {
		if data.count < 2 {
			continue
		}
		monthlyAmt := data.total.Div(decimal.NewFromInt(3))
		totalMonthly = totalMonthly.Add(monthlyAmt)
		freq := "monthly"
		if data.count >= 12 {
			freq = "weekly"
		}
		subs = append(subs, map[string]interface{}{
			"name":      merchant,
			"amount":    monthlyAmt.StringFixed(2),
			"frequency": freq,
			"status":    "active",
		})
	}

	totalYearly := totalMonthly.Mul(decimal.NewFromInt(12))
	tip := ""
	if totalMonthly.GreaterThan(decimal.NewFromInt(50)) {
		tip = fmt.Sprintf("You're spending $%s/year on subscriptions. Review if you're using all of them.", totalYearly.StringFixed(0))
	}

	// Enrich fallback subscription entries with plain descriptions and context
	if enrichmentMap := enrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		for _, s := range subs {
			enrichMerchantEntry(s, enrichmentMap)
		}
	}

	return map[string]interface{}{
		"source":        "transaction_grouping",
		"subscriptions": subs,
		"total_monthly": totalMonthly.StringFixed(2),
		"total_yearly":  totalYearly.StringFixed(2),
		"savings_tip":   tip,
	}, nil
}

func subscriptionSavingsTip(totalYearly decimal.Decimal) string {
	if totalYearly.GreaterThan(decimal.NewFromInt(0)) {
		return fmt.Sprintf("These recurring charges add up to $%s/year. Keep the ones that still earn their place.", totalYearly.StringFixed(0))
	}
	return ""
}

// executeGetRunway calculates how long the user's money will last.
func (o *AgentAdapter) executeGetRunway(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.spending == nil || o.aggregateStats == nil {
		return map[string]interface{}{"error": "required services unavailable"}, nil
	}

	spendBal, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stashBal, _ := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	totalBalance := spendBal.Add(stashBal)

	end := time.Now()
	start := end.AddDate(0, -1, 0)
	summary, err := o.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return map[string]interface{}{"error": "failed to get spending"}, nil
	}

	dailyBurn := decimal.Zero
	if summary != nil && summary.Total.IsPositive() && summary.PeriodDays > 0 {
		dailyBurn = summary.Total.Div(decimal.NewFromInt(int64(summary.PeriodDays)))
	}

	months := 0
	days := 0
	projectedEnd := ""
	status := "healthy"
	if dailyBurn.IsPositive() {
		totalDays := totalBalance.Div(dailyBurn).IntPart()
		months = int(totalDays / 30)
		days = int(totalDays % 30)
		projectedEnd = end.AddDate(0, 0, int(totalDays)).Format("2006-01-02")
		switch {
		case months < 2:
			status = "critical"
		case months < 6:
			status = "caution"
		}
	} else {
		months = 99
		status = "healthy"
	}

	return map[string]interface{}{
		"months":        months,
		"days":          days,
		"status":        status,
		"daily_burn":    dailyBurn.StringFixed(2),
		"balance":       totalBalance.StringFixed(2),
		"projected_end": projectedEnd,
	}, nil
}

// executeGetDepositPattern analyzes deposit frequency and consistency.
func (o *AgentAdapter) executeGetDepositPattern(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending service unavailable"}, nil
	}

	end := time.Now()
	start := end.AddDate(0, -3, 0)
	flow, err := o.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil {
		return map[string]interface{}{"error": "failed to get money flow"}, nil
	}

	depositCount := flow.DepositCount
	totalDeposits := flow.TotalDeposits
	avgAmount := decimal.Zero
	if depositCount > 0 {
		avgAmount = totalDeposits.Div(decimal.NewFromInt(int64(depositCount)))
	}

	freq := "irregular"
	consistency := 30
	switch {
	case depositCount >= 12:
		freq = "weekly"
		consistency = 90
	case depositCount >= 6:
		freq = "biweekly"
		consistency = 75
	case depositCount >= 3:
		freq = "monthly"
		consistency = 60
	}

	lastDeposit := end.AddDate(0, 0, -7).Format("2006-01-02") // fallback
	nextExpected := end.AddDate(0, 0, 23).Format("2006-01-02")

	return map[string]interface{}{
		"frequency":      freq,
		"average_amount": avgAmount.StringFixed(2),
		"last_deposit":   lastDeposit,
		"next_expected":  nextExpected,
		"streak":         depositCount,
		"consistency":    consistency,
	}, nil
}

// executeGetYieldSummary returns stash yield performance data.
func (o *AgentAdapter) executeGetYieldSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.aggregateStats == nil {
		return map[string]interface{}{"error": "balance service unavailable"}, nil
	}

	stashBal, err := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return map[string]interface{}{"error": "failed to get stash balance"}, nil
	}

	apy := decimal.NewFromFloat(0.045)
	dailyEstimate := stashBal.Mul(apy).Div(decimal.NewFromInt(365))
	monthEstimate := dailyEstimate.Mul(decimal.NewFromInt(30))
	totalEarned := monthEstimate.Mul(decimal.NewFromInt(3))

	return map[string]interface{}{
		"total_earned":   totalEarned.StringFixed(2),
		"month_earned":   monthEstimate.StringFixed(2),
		"current_apy":    "4.5",
		"stash_balance":  stashBal.StringFixed(2),
		"daily_estimate": dailyEstimate.StringFixed(4),
	}, nil
}

// executeGetSpendingComparison compares spending between this month and last month.
func (o *AgentAdapter) executeGetSpendingComparison(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending service unavailable"}, nil
	}

	now := time.Now()
	thisMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonthStart := thisMonthStart.AddDate(0, -1, 0)
	lastMonthEnd := thisMonthStart.Add(-time.Second)

	thisSummary, err := o.spending.GetSummary(ctx, userID, thisMonthStart, now)
	if err != nil {
		return map[string]interface{}{"error": "failed to get this month spending"}, nil
	}
	lastSummary, err := o.spending.GetSummary(ctx, userID, lastMonthStart, lastMonthEnd)
	if err != nil {
		return map[string]interface{}{"error": "failed to get last month spending"}, nil
	}

	currentVal := thisSummary.Total
	previousVal := lastSummary.Total
	deltaPct := decimal.Zero
	if previousVal.IsPositive() {
		deltaPct = currentVal.Sub(previousVal).Div(previousVal).Mul(decimal.NewFromInt(100))
	}

	return map[string]interface{}{
		"title":        "Spending Comparison",
		"metric_label": "Total Spent",
		"direction":    "spending",
		"current": map[string]interface{}{
			"label": now.Format("January"),
			"value": currentVal.StringFixed(2),
			"color": "#FF3E00",
		},
		"previous": map[string]interface{}{
			"label": lastMonthStart.Format("January"),
			"value": previousVal.StringFixed(2),
			"color": "#8C8C8C",
		},
		"delta_pct": deltaPct.StringFixed(1),
	}, nil
}
