package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ToolGetBankStatementAnalysis lets Miriam pull a detailed breakdown of the
// user's uploaded bank statements — spending by category with percentages,
// income vs expense, savings rate, recurring payments, and a growth plan
// mapped to Baby Steps. She calls this after the user uploads a statement
// to deliver the "spending pattern reveal" aha moment.
const ToolGetBankStatementAnalysis = "get_bank_statement_analysis"

// BankStatementAnalysisTool returns the tool definition for the tools registry.
func BankStatementAnalysisTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetBankStatementAnalysis,
		Description: `Get a detailed analysis of the user's uploaded bank statements: spending by category with percentages, total income vs total expenses, savings rate, top recurring payments, and a personalized growth plan mapped to their Baby Step. Use this after the user uploads a bank statement and asks "what does it say?", "analyze my spending", "how am I doing", or when they want a spending breakdown from their external bank data. If no statements have been uploaded, tell them to upload one in the app.`,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"months": map[string]interface{}{
					"type":        "integer",
					"description": "Number of months to analyze. Defaults to 6. Max 12.",
				},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

// BankStatementAnalysisAdapter wraps a BankStatementSummaryProvider and
// implements the core.BankStatementAnalysisProvider interface so the
// get_bank_statement_analysis tool can run through the core.Agent system.
// When a MonoAnalysisProvider is also wired in, the adapter will supplement
// or replace bank-statement data with Mono transaction data from linked
// bank accounts.
type BankStatementAnalysisAdapter struct {
	provider BankStatementSummaryProvider
	mono     MonoAnalysisProvider
}

// NewBankStatementAnalysisAdapter creates an adapter from a summary provider.
// Returns nil if the provider is nil.
func NewBankStatementAnalysisAdapter(provider BankStatementSummaryProvider) *BankStatementAnalysisAdapter {
	if provider == nil {
		return nil
	}
	return &BankStatementAnalysisAdapter{provider: provider}
}

// SetMonoAnalysis wires a Mono spending analysis provider so the tool can
// also surface data from Mono-linked bank accounts.
func (a *BankStatementAnalysisAdapter) SetMonoAnalysis(mono MonoAnalysisProvider) {
	if a != nil {
		a.mono = mono
	}
}

// categoryBreakdown holds a single category's spending analysis.
type categoryBreakdown struct {
	Category   string  `json:"category"`
	Total      float64 `json:"total"`
	PctOfSpend float64 `json:"pct_of_spend"`
	MonthlyAvg float64 `json:"monthly_avg"`
}

// recurringItem holds a recurring payment entry.
type recurringItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// GetAnalysis runs the full bank statement analysis and returns structured
// data for Miriam to present to the user.
func (a *BankStatementAnalysisAdapter) GetAnalysis(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	if a == nil || a.provider == nil {
		return map[string]interface{}{
			"has_data":   false,
			"error":      "Bank statement analysis is not available right now.",
			"suggestion": "You can upload your bank statement in the RAIL app and I'll break down your spending, find your savings rate, and build you a growth plan.",
		}, nil
	}

	if months <= 0 || months > 12 {
		months = 6
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	now := time.Now().UTC()
	start := now.AddDate(0, -months, 0)

	// 1. Total income vs expense
	totalIncome, totalExpense, periodStart, periodEnd, err := a.provider.GetIncomeExpenseSummary(fetchCtx, userID)
	if err != nil {
		return map[string]interface{}{
			"has_data":   false,
			"error":      "I couldn't pull your statement data right now.",
			"suggestion": "Try again in a moment, or re-upload your statement in the app.",
		}, nil
	}

	// If no transactions at all, try Mono data before giving up.
	if totalIncome == 0 && totalExpense == 0 {
		if a.mono != nil {
			return a.getMonoAnalysis(fetchCtx, userID, months)
		}
		return map[string]interface{}{
			"has_data":   false,
			"error":      "No bank statement data found.",
			"suggestion": "Upload your bank statement in the RAIL app, or link your bank account through Mono, and I'll break down your spending, find your savings rate, and build you a growth plan.",
		}, nil
	}

	// 2. Spending by category
	spendingByCategory, err := a.provider.GetSpendingSummaryByCategory(fetchCtx, userID, start, now)
	if err != nil {
		spendingByCategory = map[string]float64{}
	}

	// 3. Total transactions + banks
	totalTxns, banks, _ := a.provider.GetCompletedUploadSummary(fetchCtx, userID)

	// 4. Top recurring recipients
	recurringNames, recurringCounts, _ := a.provider.GetTopRecurringRecipients(fetchCtx, userID, 5)

	// 5. Category monthly averages
	categoryAverages, err := a.provider.GetCategoryMonthlyAverages(fetchCtx, userID)
	if err != nil {
		categoryAverages = map[string]decimal.Decimal{}
	}

	// 6. Compute savings rate
	savingsRate := 0.0
	if totalIncome > 0 {
		savingsRate = (totalIncome - totalExpense) / totalIncome * 100
	}

	// 7. Build sorted category breakdown with percentages
	var categories []categoryBreakdown
	for cat, amount := range spendingByCategory {
		pct := 0.0
		if totalExpense > 0 {
			pct = amount / totalExpense * 100
		}
		monthlyAvg := 0.0
		if avg, ok := categoryAverages[cat]; ok {
			monthlyAvg, _ = avg.Float64()
		}
		categories = append(categories, categoryBreakdown{
			Category:   cat,
			Total:      amount,
			PctOfSpend: pct,
			MonthlyAvg: monthlyAvg,
		})
	}
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Total > categories[j].Total
	})

	// 8. Build recurring payments list
	var recurring []recurringItem
	for i, name := range recurringNames {
		recurring = append(recurring, recurringItem{Name: name, Count: recurringCounts[i]})
	}

	// 9. Format period string
	periodStr := fmt.Sprintf("last %d months", months)
	if periodStart != nil && periodEnd != nil {
		periodStr = fmt.Sprintf("%s to %s", periodStart.Format("Jan 2006"), periodEnd.Format("Jan 2006"))
	}

	// 10. Generate growth plan based on savings rate and baby step
	growthPlan := generateGrowthPlan(totalIncome, totalExpense, savingsRate, categories, recurring)

	// 11. Top 3 spending categories summary for Miriam's text response
	var topCats []string
	for i, c := range categories {
		if i >= 3 {
			break
		}
		topCats = append(topCats, fmt.Sprintf("%s (%.0f%%)", c.Category, c.PctOfSpend))
	}
	topCatsStr := "no categories found"
	if len(topCats) > 0 {
		topCatsStr = strings.Join(topCats, ", ")
	}

	return map[string]interface{}{
		"has_data":           true,
		"period":             periodStr,
		"total_income":       fmt.Sprintf("%.0f", totalIncome),
		"total_expense":      fmt.Sprintf("%.0f", totalExpense),
		"savings_rate":       fmt.Sprintf("%.1f%%", savingsRate),
		"transaction_count":  totalTxns,
		"banks":              banks,
		"top_categories":     topCatsStr,
		"categories":         categories,
		"recurring_payments": recurring,
		"growth_plan":        growthPlan,
		"summary": fmt.Sprintf(
			"Over the %s, you earned %.0f and spent %.0f. Your savings rate is %.1f%%. Top spending: %s.",
			periodStr, totalIncome, totalExpense, savingsRate, topCatsStr,
		),
	}, nil
}

// generateGrowthPlan creates actionable recommendations based on the user's
// spending analysis, mapped to their Baby Step.
func generateGrowthPlan(
	totalIncome, totalExpense, savingsRate float64,
	categories []categoryBreakdown,
	recurring []recurringItem,
) []string {
	var plan []string

	// Savings rate assessment
	if savingsRate < 0 {
		plan = append(plan, "You're spending more than you earn. The #1 priority is closing that gap — look at your top 2 categories for immediate cuts.")
	} else if savingsRate < 10 {
		plan = append(plan, fmt.Sprintf("You're saving %.0f%% of your income — that's a start, but we want to push it to at least 20%%. Your biggest opportunity is your top spending category.", savingsRate))
	} else if savingsRate < 20 {
		plan = append(plan, fmt.Sprintf("You're saving %.0f%% — solid. If we can get this to 25%%, your emergency fund builds 2x faster.", savingsRate))
	} else {
		plan = append(plan, fmt.Sprintf("You're saving %.0f%% — that's strong. The focus shifts from cutting to growing: let's make sure that savings is working for you in your stash.", savingsRate))
	}

	// Category-specific advice
	if len(categories) > 0 {
		top := categories[0]
		if top.PctOfSpend > 30 {
			plan = append(plan, fmt.Sprintf("%s is %.0f%% of your spending — that's a lot. Trimming it by even 15%% would free up real money for your goals.", top.Category, top.PctOfSpend))
		}
	}

	// Recurring payments advice
	if len(recurring) > 0 {
		plan = append(plan, fmt.Sprintf("You have %d recurring payments. Worth checking if any subscriptions you forgot about are still draining your account.", len(recurring)))
	}

	// Baby Step mapping
	if savingsRate < 0 {
		plan = append(plan, "Baby Step: You're in crisis mode. Before anything else, cover your essentials and stop the bleeding. We'll build a starter emergency fund once income exceeds expenses.")
	} else if savingsRate < 10 {
		plan = append(plan, "Baby Step: Start with a starter emergency fund. At your current savings rate, that's achievable. Then we attack any debts with the snowball method.")
	} else if savingsRate < 20 {
		plan = append(plan, "Baby Step: You're ready to build the starter emergency fund and start the debt snowball if you have debts. Every extra naira goes to the smallest debt first.")
	} else {
		plan = append(plan, "Baby Step: With a 20%+ savings rate, you can fast-track through the emergency fund and move to investing 15% of your income for long-term wealth.")
	}

	return plan
}

// getMonoAnalysis builds an analysis response from Mono-linked account data
// when no bank statement uploads are available. Amounts from Mono are in
// kobo/pesewa/cents and are divided by 100 for display.
func (a *BankStatementAnalysisAdapter) getMonoAnalysis(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error) {
	days := months * 30
	if days <= 0 {
		days = 180
	}
	analysis, err := a.mono.GetSpendingAnalysis(ctx, userID, days)
	if err != nil || analysis == nil || analysis.TransactionCount == 0 {
		return map[string]interface{}{
			"has_data":   false,
			"error":      "No bank statement or Mono data found.",
			"suggestion": "Upload your bank statement in the RAIL app, or link your bank account through Mono, and I'll break down your spending, find your savings rate, and build you a growth plan.",
		}, nil
	}

	totalIncome := float64(analysis.TotalCredits) / 100
	totalExpense := float64(analysis.TotalDebits) / 100
	savingsRate := analysis.SavingsRate * 100

	var categories []categoryBreakdown
	for _, c := range analysis.ByCategory {
		pct := 0.0
		if totalExpense > 0 {
			pct = c.Percent * 100
		}
		categories = append(categories, categoryBreakdown{
			Category:   c.Category,
			Total:      float64(c.Amount) / 100,
			PctOfSpend: pct,
			MonthlyAvg: float64(c.Amount) / 100 / float64(months),
		})
	}
	sort.Slice(categories, func(i, j int) bool {
		return categories[i].Total > categories[j].Total
	})

	var topCats []string
	for i, c := range categories {
		if i >= 3 {
			break
		}
		topCats = append(topCats, fmt.Sprintf("%s (%.0f%%)", c.Category, c.PctOfSpend))
	}
	topCatsStr := "no categories found"
	if len(topCats) > 0 {
		topCatsStr = strings.Join(topCats, ", ")
	}

	periodStr := fmt.Sprintf("last %d months (Mono)", months)
	growthPlan := generateGrowthPlan(totalIncome, totalExpense, savingsRate, categories, nil)

	return map[string]interface{}{
		"has_data":           true,
		"source":             "mono",
		"period":             periodStr,
		"total_income":       fmt.Sprintf("%.0f", totalIncome),
		"total_expense":      fmt.Sprintf("%.0f", totalExpense),
		"savings_rate":       fmt.Sprintf("%.1f%%", savingsRate),
		"transaction_count":  analysis.TransactionCount,
		"top_categories":     topCatsStr,
		"categories":         categories,
		"growth_plan":        growthPlan,
		"summary": fmt.Sprintf(
			"Over the %s from your linked bank account, you earned %.0f and spent %.0f. Your savings rate is %.1f%%. Top spending: %s.",
			periodStr, totalIncome, totalExpense, savingsRate, topCatsStr,
		),
	}, nil
}
