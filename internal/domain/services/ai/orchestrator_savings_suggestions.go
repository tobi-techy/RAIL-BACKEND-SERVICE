package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetSavingsSuggestions = "get_savings_suggestions"

// SavingsSuggestions is the response from GetSuggestions.
type SavingsSuggestions struct {
	Suggestions              []map[string]interface{} `json:"suggestions"`
	TotalPotentialMonthlySav string                   `json:"total_potential_monthly_savings"`
	AnnualStashGrowth        string                   `json:"annual_stash_growth_if_saved"`
	Message                  string                   `json:"message"`
}

// SavingsSuggestionProvider computes savings suggestions from spending data.
type SavingsSuggestionProvider interface {
	GetSuggestions(ctx context.Context, userID uuid.UUID) (*SavingsSuggestions, error)
}

// savingsSuggestionProvider is the default implementation.
type savingsSuggestionProvider struct {
	receipts ReceiptHistoryProvider
	spending SpendingAnalyzer
}

// NewSavingsSuggestionProvider creates a SavingsSuggestionProvider from existing providers.
func NewSavingsSuggestionProvider(receipts ReceiptHistoryProvider, spending SpendingAnalyzer) SavingsSuggestionProvider {
	return &savingsSuggestionProvider{receipts: receipts, spending: spending}
}

// Category-specific tips and reduction ratios.
var categoryTips = map[string]struct {
	ratio float64
	tip   string
}{
	"Food & Dining":    {0.33, "Cook 2 more meals per week"},
	"Transport":        {0.25, "Reduce ride-hailing trips where possible"},
	"Entertainment":    {0.30, "Look for free or cheaper alternatives"},
	"Shopping":         {0.25, "Try a no-spend week each month"},
	"Groceries":        {0.15, "Buy in bulk and plan meals ahead"},
	"Subscriptions":    {0.50, "Review and cancel unused subscriptions"},
	"Health & Fitness": {0.20, "Consider home workouts some days"},
}

func (p *savingsSuggestionProvider) GetSuggestions(ctx context.Context, userID uuid.UUID) (*SavingsSuggestions, error) {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	if p.receipts == nil {
		return &SavingsSuggestions{
			Suggestions:              nil,
			TotalPotentialMonthlySav: "0.00",
			AnnualStashGrowth:        "0.00",
			Message:                  "Not enough spending data yet to make suggestions.",
		}, nil
	}

	// Get receipt category totals for this month
	catTotals, err := p.receipts.GetTotalByCategory(ctx, userID, monthStart, now)
	if err != nil {
		return nil, fmt.Errorf("savings suggestions: %w", err)
	}

	// Also get card spending summary if available
	if p.spending != nil {
		summary, err := p.spending.GetSummary(ctx, userID, monthStart, now)
		if err == nil && summary != nil {
			// Merge card spending categories into receipt categories
			for _, sc := range summary.Categories {
				found := false
				for i, rc := range catTotals {
					if rc.Category == sc.Category {
						catTotals[i].Total = catTotals[i].Total.Add(sc.Total)
						catTotals[i].Count += sc.Count
						found = true
						break
					}
				}
				if !found {
					catTotals = append(catTotals, sc)
				}
			}
		}
	}

	// Extrapolate to full month if we're mid-month
	daysPassed := now.Sub(monthStart).Hours() / 24
	if daysPassed < 1 {
		daysPassed = 1
	}
	daysInMonth := float64(time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	monthMultiplier := decimal.NewFromFloat(daysInMonth / daysPassed)

	totalPotentialSavings := decimal.Zero
	var suggestions []map[string]interface{}

	for _, cat := range catTotals {
		projected := cat.Total.Mul(monthMultiplier)
		tip, ok := categoryTips[cat.Category]
		if !ok {
			tip = struct {
				ratio float64
				tip   string
			}{0.20, "Look for ways to reduce spending here"}
		}

		potentialSavings := projected.Mul(decimal.NewFromFloat(tip.ratio))
		if potentialSavings.LessThan(decimal.NewFromFloat(5)) {
			continue // Skip trivial savings
		}

		suggestedTarget := projected.Sub(potentialSavings)
		// Annual stash impact: savings * 0.30 (stash split) * 12 months * 1.035 (yield)
		annualStash := potentialSavings.Mul(decimal.NewFromFloat(0.30)).Mul(decimal.NewFromInt(12)).Mul(decimal.NewFromFloat(1.035))

		suggestions = append(suggestions, map[string]interface{}{
			"category":           cat.Category,
			"current_monthly":    projected.StringFixed(2),
			"suggested_target":   suggestedTarget.StringFixed(2),
			"potential_savings":  potentialSavings.StringFixed(2),
			"annual_stash_impact": annualStash.StringFixed(2),
			"tip":                tip.tip,
		})
		totalPotentialSavings = totalPotentialSavings.Add(potentialSavings)
	}

	annualGrowth := totalPotentialSavings.Mul(decimal.NewFromFloat(0.30)).Mul(decimal.NewFromInt(12)).Mul(decimal.NewFromFloat(1.035))

	msg := "Not enough spending data yet to make suggestions."
	if totalPotentialSavings.IsPositive() {
		msg = fmt.Sprintf("You could save $%s/month. That's $%s extra in your stash earning yield over a year.",
			totalPotentialSavings.StringFixed(2), annualGrowth.StringFixed(2))
	}

	return &SavingsSuggestions{
		Suggestions:              suggestions,
		TotalPotentialMonthlySav: totalPotentialSavings.StringFixed(2),
		AnnualStashGrowth:        annualGrowth.StringFixed(2),
		Message:                  msg,
	}, nil
}

// SetSavingsSuggestions sets the savings suggestion provider.
func (o *Orchestrator) SetSavingsSuggestions(p SavingsSuggestionProvider) {
	o.savingsSuggestions = p
}

// SavingsSuggestionTool returns the tool definition.
func SavingsSuggestionTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetSavingsSuggestions,
		Description: "Analyze spending patterns from receipts and transactions to suggest concrete ways to save money. Shows how much could be saved and the impact on stash growth.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *Orchestrator) executeSavingsSuggestions(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.savingsSuggestions == nil {
		return map[string]interface{}{"error": "savings suggestions not available"}, nil
	}
	s, err := o.savingsSuggestions.GetSuggestions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("savings suggestions: %w", err)
	}
	return map[string]interface{}{
		"suggestions":                   s.Suggestions,
		"total_potential_monthly_savings": s.TotalPotentialMonthlySav,
		"annual_stash_growth_if_saved":   s.AnnualStashGrowth,
		"message":                        s.Message,
	}, nil
}
