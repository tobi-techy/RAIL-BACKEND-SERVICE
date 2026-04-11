package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/spending"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// Tool names for spending analysis.
const (
	ToolGetSpendingSummary = "get_spending_summary"
	ToolGetSpendingChart   = "get_spending_chart"
)

// SpendingAnalyzer is the subset of spending.Service the orchestrator needs.
type SpendingAnalyzer interface {
	GetSummary(ctx context.Context, userID uuid.UUID, start, end time.Time) (*spending.Summary, error)
}

// SetSpending sets the spending analysis provider.
func (o *Orchestrator) SetSpending(s SpendingAnalyzer) {
	o.spending = s
}

// SpendingTools returns tool definitions for spending analysis.
func SpendingTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolGetSpendingSummary,
			Description: "Get spending analysis: total spent, breakdown by category and merchant, daily average. Use when user asks about their spending, expenses, where their money goes, or budgeting.",
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
	}
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
	default: // this_month
		return spending.MonthStart(), spending.MonthEnd()
	}
}

// executeSpendingSummary handles the get_spending_summary tool call.
func (o *Orchestrator) executeSpendingSummary(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period, _ := args["period"].(string)
	start, end := parsePeriod(period)

	summary, err := o.spending.GetSummary(ctx, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("spending summary: %w", err)
	}

	cats := make([]map[string]interface{}, len(summary.Categories))
	for i, c := range summary.Categories {
		cats[i] = map[string]interface{}{"category": c.Category, "total": c.Total.String(), "count": c.Count}
	}

	merchants := make([]map[string]interface{}, len(summary.Merchants))
	for i, m := range summary.Merchants {
		merchants[i] = map[string]interface{}{"merchant": m.Merchant, "total": m.Total.String(), "count": m.Count}
	}

	return map[string]interface{}{
		"total":             summary.Total.String(),
		"transaction_count": summary.TxCount,
		"daily_average":     summary.DailyAvg.StringFixed(2),
		"period_days":       summary.PeriodDays,
		"categories":        cats,
		"top_merchants":     merchants,
	}, nil
}

// executeSpendingChart handles the get_spending_chart tool call.
func (o *Orchestrator) executeSpendingChart(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	if o.spending == nil {
		return map[string]interface{}{"error": "spending analysis not available"}, nil
	}

	period, _ := args["period"].(string)
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
