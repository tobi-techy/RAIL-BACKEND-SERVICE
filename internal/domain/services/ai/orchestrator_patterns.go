package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetSpendingPatterns = "get_spending_patterns"

// WeekdaySpending represents spending grouped by day of week.
type WeekdaySpending struct {
	DayOfWeek int             `db:"dow" json:"day_of_week"`
	DayName   string          `json:"day_name"`
	Total     decimal.Decimal `db:"total" json:"total"`
	Count     int             `db:"count" json:"count"`
	AvgPerTx  decimal.Decimal `json:"avg_per_transaction"`
}

// LargeTransaction represents a notable transaction.
type LargeTransaction struct {
	Amount       decimal.Decimal `db:"amount" json:"amount"`
	MerchantName *string         `db:"merchant_name" json:"merchant_name"`
	CreatedAt    time.Time       `db:"created_at" json:"date"`
}

// PatternAnalyzer provides behavioral spending pattern data.
type PatternAnalyzer interface {
	GetSpendingByDayOfWeek(ctx context.Context, userID uuid.UUID, start, end time.Time) ([]WeekdaySpending, error)
	GetLargestTransactions(ctx context.Context, userID uuid.UUID, start, end time.Time, limit int) ([]LargeTransaction, error)
	GetSpendingTotal(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, int, error)
}

// SetPatterns sets the pattern analyzer.
// Deprecated: Use NewOrchestratorWithDeps instead.
func (o *AgentAdapter) SetPatterns(p PatternAnalyzer) {
	o.patterns = p
}

func SpendingPatternsTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetSpendingPatterns,
		Description: "Analyze spending behavior patterns: which days the user spends most, weekend vs weekday habits, largest transactions, and week-over-week trends. Use when user asks about spending habits, patterns, or behavioral insights.",
		Parameters: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

func (o *AgentAdapter) executeSpendingPatterns(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.patterns == nil {
		return map[string]interface{}{"error": "pattern analysis not available"}, nil
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Day of week patterns
	dowData, err := o.patterns.GetSpendingByDayOfWeek(ctx, userID, monthStart, now)
	if err != nil {
		return nil, fmt.Errorf("day of week patterns: %w", err)
	}

	days := make([]map[string]interface{}, len(dowData))
	var weekdayTotal, weekendTotal decimal.Decimal
	var weekdayCount, weekendCount int
	var peakDay string
	peakAmount := decimal.Zero
	for i, d := range dowData {
		days[i] = map[string]interface{}{"day": d.DayName, "total": d.Total.String(), "count": d.Count, "avg": d.AvgPerTx.StringFixed(2)}
		if d.DayOfWeek == 0 || d.DayOfWeek == 6 {
			weekendTotal = weekendTotal.Add(d.Total)
			weekendCount += d.Count
		} else {
			weekdayTotal = weekdayTotal.Add(d.Total)
			weekdayCount += d.Count
		}
		if d.Total.GreaterThan(peakAmount) {
			peakAmount = d.Total
			peakDay = d.DayName
		}
	}

	// Largest transactions
	largest, err := o.patterns.GetLargestTransactions(ctx, userID, monthStart, now, 3)
	if err != nil {
		return nil, fmt.Errorf("largest transactions: %w", err)
	}
	bigTx := make([]map[string]interface{}, len(largest))
	for i, t := range largest {
		merchant := "Unknown"
		if t.MerchantName != nil {
			merchant = *t.MerchantName
		}
		bigTx[i] = map[string]interface{}{"amount": t.Amount.String(), "merchant": merchant, "date": t.CreatedAt.Format("Jan 2")}
	}

	// Enrich largest transactions with plain descriptions and context
	if enrichmentMap := enrichMerchantMap(ctx, o.merchantEnricher, userID); enrichmentMap != nil {
		for _, tx := range bigTx {
			enrichMerchantEntry(tx, enrichmentMap)
		}
	}

	// Week-over-week comparison
	thisWeekStart := now.AddDate(0, 0, -7)
	lastWeekStart := now.AddDate(0, 0, -14)
	thisWeek, _, _ := o.patterns.GetSpendingTotal(ctx, userID, thisWeekStart, now)
	lastWeek, _, _ := o.patterns.GetSpendingTotal(ctx, userID, lastWeekStart, thisWeekStart)
	weekTrend := "stable"
	weekChangePct := decimal.Zero
	if !lastWeek.IsZero() {
		weekChangePct = thisWeek.Sub(lastWeek).Div(lastWeek).Mul(decimal.NewFromInt(100))
		if weekChangePct.GreaterThan(decimal.NewFromInt(20)) {
			weekTrend = "increasing"
		} else if weekChangePct.LessThan(decimal.NewFromInt(-20)) {
			weekTrend = "decreasing"
		}
	}

	return map[string]interface{}{
		"day_of_week_breakdown": days,
		"peak_spending_day":     peakDay,
		"weekend_total":         weekendTotal.String(),
		"weekday_total":         weekdayTotal.String(),
		"weekend_vs_weekday":    fmt.Sprintf("Weekend: %d transactions ($%s), Weekday: %d transactions ($%s)", weekendCount, weekendTotal.StringFixed(2), weekdayCount, weekdayTotal.StringFixed(2)),
		"largest_transactions":  bigTx,
		"week_over_week_trend":  weekTrend,
		"week_change_pct":       weekChangePct.StringFixed(1),
		"this_week_total":       thisWeek.String(),
		"last_week_total":       lastWeek.String(),
	}, nil
}
