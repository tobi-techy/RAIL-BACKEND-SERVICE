// PRIVACY NOTICE: This module sends user transaction details (merchant names,
// amounts, dates, categories) to the configured LLM provider for AI-powered
// spending analysis. This data sharing MUST be covered by a Data Processing
// Agreement (DPA) with the LLM provider. Merchant names are truncated to 20
// characters before transmission to limit PII exposure.

package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	aifinance "github.com/rail-service/rail_service/internal/domain/services/ai/finance"
	"github.com/rail-service/rail_service/internal/domain/entities"

	aicontext "github.com/rail-service/rail_service/internal/domain/services/ai/context"
	"github.com/rail-service/rail_service/internal/domain/services/spending"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

// Tool names for spending analysis.
const (
	ToolGetSpendingSummary    = "get_spending_summary"
	ToolGetSpendingChart      = "get_spending_chart"
	ToolGetRecentTransactions = "get_recent_transactions"
	ToolGetMoneyFlow          = "get_money_flow"
)

// SpendingAnalyzer is the subset of spending.Service the orchestrator needs.
// Deprecated: Use aifinance.SpendingAnalyzer instead.
type SpendingAnalyzer = aifinance.SpendingAnalyzer

// SpendingEnricher adds plain-English descriptions to spending transactions.
type SpendingEnricher interface {
	EnrichTransactions(ctx context.Context, txns []entities.SpendingTransaction) []entities.SpendingTransaction
}

// SetSpending sets the spending analysis provider.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetSpending(s SpendingAnalyzer) {
	o.spending = s
}

// SetSpendingEnricher sets the spending enricher for plain-English transaction descriptions.
func (o *AgentAdapter) SetSpendingEnricher(e SpendingEnricher) {
	o.spendingEnricher = e
}

// truncateMerchant limits merchant name length to reduce PII sent to LLM.
func truncateMerchant(name string) string {
	if len(name) > 20 {
		return name[:20]
	}
	return name
}

// humanizeCategory replaces generic category names with personality-driven labels.
func humanizeCategory(cat string) string {
	switch strings.ToLower(strings.TrimSpace(cat)) {
	case "food & dining", "food", "food_and_dining", "dining", "restaurants":
		return "Jollof Fund"
	case "transportation", "transport", "ride", "uber", "bolt":
		return "Movement Money"
	case "entertainment", "fun", "leisure":
		return "Fun Fund"
	case "shopping", "retail", "online shopping":
		return "Treat Yourself"
	case "bills & utilities", "bills", "utilities", "electricity", "internet", "airtime":
		return "Adulting Costs"
	case "transfer", "transfers", "p2p", "p2p transfers":
		return "Money Moves"
	case "withdrawal", "withdrawals", "crypto withdrawal":
		return "Crypto Withdrawal"
	case "ngn withdrawal", "ngn_withdrawal", "naira withdrawal":
		return "Naira Cash Out"
	case "subscription", "subscriptions":
		return "Auto-Deductions"
	case "health", "healthcare", "pharmacy", "medical":
		return "Self-Care"
	case "education", "school", "learning", "books":
		return "Level Up"
	case "groceries", "supermarket":
		return "Groceries"
	case "card spend", "card_spend":
		return "Card Spend"
	case "receipts (cash)", "scanned_receipts":
		return "Cash Spending"
	default:
		return cat
	}
}

// humanizeCategoryForLocale returns personality-driven labels for Nigerian locale,
// neutral English labels otherwise.
func humanizeCategoryForLocale(cat, locale string) string {
	if strings.EqualFold(locale, "ng") || strings.EqualFold(locale, "nigeria") {
		return humanizeCategory(cat)
	}
	// Neutral English labels for non-Nigerian locales
	switch strings.ToLower(strings.TrimSpace(cat)) {
	case "food & dining", "food", "food_and_dining", "dining", "restaurants":
		return "Food & Dining"
	case "transportation", "transport", "ride", "uber", "bolt":
		return "Transport"
	case "entertainment", "fun", "leisure":
		return "Entertainment"
	case "shopping", "retail", "online shopping":
		return "Shopping"
	case "bills & utilities", "bills", "utilities", "electricity", "internet", "airtime":
		return "Bills & Utilities"
	case "transfer", "transfers", "p2p", "p2p transfers":
		return "Transfers"
	case "withdrawal", "withdrawals", "crypto withdrawal":
		return "Withdrawals"
	case "ngn withdrawal", "ngn_withdrawal", "naira withdrawal":
		return "NGN Withdrawal"
	case "subscription", "subscriptions":
		return "Subscriptions"
	case "health", "healthcare", "pharmacy", "medical":
		return "Healthcare"
	case "education", "school", "learning", "books":
		return "Education"
	case "groceries", "supermarket":
		return "Groceries"
	case "card spend", "card_spend":
		return "Card Spend"
	case "receipts (cash)", "scanned_receipts":
		return "Cash Spending"
	default:
		return cat
	}
}

// SpendingTools returns tool definitions for spending analysis.
func SpendingTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetSpendingSummary,
			Description: "Category and merchant breakdown of spending. Use for 'what am I spending on', 'top categories', 'biggest expenses by type'. NOT for totals or individual transactions.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"this_month", "last_month", "last_7_days", "last_30_days"},
						"description": "Time period for spending analysis",
					},
				},
			},
		},
		{
			Name:        ToolGetSpendingChart,
			Description: "Get daily spending data for charts and trend visualization. Use when user asks about spending trends, patterns over time, or wants to see a chart.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"this_month", "last_month", "last_7_days", "last_30_days"},
						"description": "Time period for chart data",
					},
				},
			},
		},
		{
			Name:        ToolGetRecentTransactions,
			Description: "List of individual transactions with amounts, dates, merchants. Use for 'show me my transactions', 'what was that charge', 'last 5 purchases'. NOT for totals or category analysis.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"this_month", "last_month", "last_7_days", "last_30_days"},
						"description": "Time period for transactions",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Number of transactions to return (max 20)",
						"default":     15,
					},
				},
			},
		},
		{
			Name:        ToolGetMoneyFlow,
			Description: "Total money in vs money out for a period. Use for 'where did my money go', 'how much did I spend total', 'am I positive or negative'. NOT for category breakdown or individual transactions.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"this_month", "last_month", "last_7_days", "last_30_days"},
						"description": "Time period for the summary",
					},
				},
			},
		},
	}
}

// resolvePeriodFromArgs extracts the period from tool args, falling back to the
// months parameter when ElevenLabs voice sends months instead of a period enum value.
func resolvePeriodFromArgs(args map[string]interface{}) string {
	if p, _ := args["period"].(string); p != "" {
		return p
	}
	if m, ok := args["months"].(float64); ok && m > 0 {
		switch {
		case m <= 1:
			return "last_30_days"
		case m <= 3:
			return "last_90_days"
		case m <= 6:
			return "last_6_months"
		default:
			return "last_12_months"
		}
	}
	if d, ok := args["days"].(float64); ok && d > 0 {
		switch {
		case d <= 7:
			return "last_7_days"
		case d <= 30:
			return "last_30_days"
		case d <= 90:
			return "last_90_days"
		default:
			return "last_6_months"
		}
	}
	return "this_month"
}

// parsePeriod converts a period string to start/end times.
func parsePeriod(period string) (time.Time, time.Time) {
	now := time.Now().UTC()
	switch period {
	case "last_month":
		start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, end
	case "last_7_days":
		return now.AddDate(0, 0, -7), now
	case "last_30_days":
		return now.AddDate(0, 0, -30), now
	case "last_90_days":
		return now.AddDate(0, 0, -90), now
	case "last_6_months":
		return now.AddDate(0, -6, 0), now
	case "last_12_months":
		return now.AddDate(0, -12, 0), now
	default: // this_month
		return spending.MonthStart(), spending.MonthEnd()
	}
}

// periodToLabel returns a human-readable label for the period so the LLM
// knows exactly what timeframe the data covers.
func periodToLabel(period string, start, end time.Time) string {
	switch period {
	case "last_month":
		return fmt.Sprintf("Last month (%s)", start.Format("January 2006"))
	case "last_7_days":
		return fmt.Sprintf("Last 7 days (%s to %s)", start.Format("Jan 2"), end.Format("Jan 2, 2006"))
	case "last_30_days":
		return fmt.Sprintf("Last 30 days (%s to %s)", start.Format("Jan 2"), end.Format("Jan 2, 2006"))
	case "last_90_days":
		return fmt.Sprintf("Last 90 days (%s to %s)", start.Format("Jan 2"), end.Format("Jan 2, 2006"))
	case "last_6_months":
		return fmt.Sprintf("Last 6 months (%s to %s)", start.Format("Jan 2"), end.Format("Jan 2, 2006"))
	case "last_12_months":
		return fmt.Sprintf("Last 12 months (%s to %s)", start.Format("Jan 2, 2006"), end.Format("Jan 2, 2006"))
	default:
		return fmt.Sprintf("This month (%s 1 to today)", start.Format("January"))
	}
}

// executeSpendingSummary handles the get_spending_summary tool call.
func (o *AgentAdapter) executeSpendingSummary(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period := resolvePeriodFromArgs(args)
	start, end := parsePeriod(period)

	summary, err := o.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("spending summary: %w", err)
	}

	cats := make([]map[string]interface{}, len(summary.Categories))
	for i, c := range summary.Categories {
		cats[i] = map[string]interface{}{"category": humanizeCategory(c.Category), "total": c.Total.String(), "count": c.Count}
	}

	merchants := make([]map[string]interface{}, len(summary.Merchants))
	for i, m := range summary.Merchants {
		merchants[i] = map[string]interface{}{"merchant": truncateMerchant(m.Merchant), "total": m.Total.String(), "count": m.Count}
	}

	// Enrich merchant entries with plain descriptions and context
	if enrichmentMap := aicontext.EnrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		for _, m := range merchants {
			aicontext.EnrichMerchantEntry(m, enrichmentMap)
		}
	}

	return map[string]interface{}{
		"period":            periodToLabel(period, start, end),
		"total":             summary.Total.String(),
		"transaction_count": summary.TxCount,
		"daily_average":     summary.DailyAvg.StringFixed(2),
		"period_days":       summary.PeriodDays,
		"categories":        cats,
		"top_merchants":     merchants,
		"note":              "All amounts are completed transactions only (money out: card payments, withdrawals, P2P transfers)",
	}, nil
}

// executeSpendingChart handles the get_spending_chart tool call.
func (o *AgentAdapter) executeSpendingChart(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period := resolvePeriodFromArgs(args)
	start, end := parsePeriod(period)

	summary, err := o.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("spending chart: %w", err)
	}

	points := make([]map[string]interface{}, len(summary.DailyTrend))
	for i, d := range summary.DailyTrend {
		points[i] = map[string]interface{}{"date": d.Period, "amount": d.Total.String(), "count": d.Count}
	}

	return map[string]interface{}{
		"data_points":   points,
		"total":         summary.Total.String(),
		"daily_average": summary.DailyAvg.StringFixed(2),
		"period_days":   summary.PeriodDays,
	}, nil
}

// executeRecentTransactions handles the get_recent_transactions tool call.
func (o *AgentAdapter) executeRecentTransactions(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period := resolvePeriodFromArgs(args)
	start, end := parsePeriod(period)

	limit := 15
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 20 {
		limit = int(l)
	}

	txns, err := o.spending.GetTransactions(ctx, userID, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("recent transactions: %w", err)
	}

	// Enrich transactions with plain-English descriptions from the ML sidecar
	if o.spendingEnricher != nil {
		txns = o.spendingEnricher.EnrichTransactions(ctx, txns)
	}

	items := make([]map[string]interface{}, len(txns))
	for i, t := range txns {
		item := map[string]interface{}{
			"direction": "money_out",
			"date":      t.Date,
			"amount":    t.Amount.String(),
			"category":  humanizeCategory(t.Category),
			"source":    t.Source,
		}
		catLower := strings.ToLower(t.Category)
		if !strings.HasPrefix(catLower, "p2p") && !strings.Contains(catLower, "withdrawal") {
			item["merchant"] = t.Source
		}
		// Inject enriched understanding fields when available
		if t.PlainDescription != "" {
			item["plain_description"] = t.PlainDescription
		}
		if t.MerchantContext != "" {
			item["merchant_context"] = t.MerchantContext
		}
		if t.Counterparty != "" && t.Counterparty != t.Source {
			item["counterparty"] = t.Counterparty
		}
		if t.IsEssential != nil {
			item["is_essential"] = *t.IsEssential
		}
		items[i] = item
	}

	return map[string]interface{}{"period": periodToLabel(period, start, end), "transactions": items, "count": len(items), "note": "All transactions are completed outflows (money leaving your account). Enriched fields (plain_description, merchant_context) provide Miriam with plain-English context for each transaction."}, nil
}

// executeMoneyFlow handles the get_money_flow tool call.
func (o *AgentAdapter) executeMoneyFlow(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period := resolvePeriodFromArgs(args)
	start, end := parsePeriod(period)

	flow, err := o.spending.GetMoneyFlow(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("money flow: %w", err)
	}

	totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
	totalSpending := totalOut.Add(flow.TotalReceipts)
	net := flow.TotalDeposits.Sub(totalOut)

	// Human-readable period label
	periodLabel := periodToLabel(period, start, end)

	// Build chart data: breakdown by category for pie/bar charts
	chartBreakdown := []map[string]interface{}{}
	if flow.TotalWithdrawals.IsPositive() {
		chartBreakdown = append(chartBreakdown, map[string]interface{}{"category": humanizeCategory("Withdrawals"), "amount": flow.TotalWithdrawals.StringFixed(2), "count": flow.WithdrawalCount})
	}
	if flow.TotalCardSpend.IsPositive() {
		chartBreakdown = append(chartBreakdown, map[string]interface{}{"category": humanizeCategory("Card Spend"), "amount": flow.TotalCardSpend.StringFixed(2), "count": flow.CardSpendCount})
	}
	if flow.TotalP2P.IsPositive() {
		chartBreakdown = append(chartBreakdown, map[string]interface{}{"category": humanizeCategory("P2P Transfers"), "amount": flow.TotalP2P.StringFixed(2), "count": flow.P2PCount})
	}
	if flow.TotalReceipts.IsPositive() {
		chartBreakdown = append(chartBreakdown, map[string]interface{}{"category": "Receipts (cash)", "amount": flow.TotalReceipts.StringFixed(2), "count": flow.ReceiptCount})
	}

	// Daily spending trend
	dailyTrend := []map[string]interface{}{}
	if o.spending != nil {
		days, err := o.spending.GetDailyTrend(ctx, userID, start, end)
		if err != nil {
			return nil, fmt.Errorf("daily trend: %w", err)
		}
		for _, d := range days {
			dailyTrend = append(dailyTrend, map[string]interface{}{"date": d.Period, "amount": d.Total.String(), "count": d.Count})
		}
	}

	return map[string]interface{}{
		"period": periodLabel,
		"money_in": map[string]interface{}{
			"total_deposits": flow.TotalDeposits.StringFixed(2),
			"deposit_count":  flow.DepositCount,
		},
		"money_out": map[string]interface{}{
			"total":            totalOut.StringFixed(2),
			"withdrawals":      flow.TotalWithdrawals.StringFixed(2),
			"withdrawal_count": flow.WithdrawalCount,
			"card_spend":       flow.TotalCardSpend.StringFixed(2),
			"card_count":       flow.CardSpendCount,
			"p2p_transfers":    flow.TotalP2P.StringFixed(2),
			"p2p_count":        flow.P2PCount,
		},
		"scanned_receipts": map[string]interface{}{
			"total": flow.TotalReceipts.StringFixed(2),
			"count": flow.ReceiptCount,
		},
		"cash_vs_digital": func() map[string]interface{} {
			cashPct := decimal.Zero
			digitalPct := decimal.Zero
			if totalSpending.IsPositive() {
				cashPct = flow.TotalReceipts.Div(totalSpending).Mul(decimal.NewFromInt(100))
				digitalPct = totalOut.Div(totalSpending).Mul(decimal.NewFromInt(100))
			}
			return map[string]interface{}{
				"digital_total":       totalOut.StringFixed(2),
				"cash_total":          flow.TotalReceipts.StringFixed(2),
				"total_real_spending": totalSpending.StringFixed(2),
				"cash_percent":        cashPct.StringFixed(1),
				"digital_percent":     digitalPct.StringFixed(1),
				"insight": fmt.Sprintf("You spent $%s digitally and $%s in cash this month. Your total real spending is $%s.",
					totalOut.StringFixed(2), flow.TotalReceipts.StringFixed(2), totalSpending.StringFixed(2)),
			}
		}(),
		"total_all_spending": totalSpending.StringFixed(2),
		"net_flow":           net.StringFixed(2),
		"chart": map[string]interface{}{
			"spending_breakdown": chartBreakdown,
			"daily_trend":        dailyTrend,
		},
		"note": "All amounts are in US Dollars (USD). Read as dollars and cents. Only completed transactions included.",
	}, nil
}
