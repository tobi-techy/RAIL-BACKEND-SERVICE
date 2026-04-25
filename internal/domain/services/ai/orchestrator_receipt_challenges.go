package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
)

const ToolGetReceiptChallenges = "get_receipt_challenges"

// ReceiptChallenges is the response from GetChallenges.
type ReceiptChallenges struct {
	ScanningStreak     map[string]interface{}   `json:"scanning_streak"`
	CategoryChallenges []map[string]interface{} `json:"category_challenges"`
	Suggestions        []string                 `json:"suggestions"`
}

// ReceiptChallengeProvider computes receipt-based challenges and streaks.
type ReceiptChallengeProvider interface {
	GetChallenges(ctx context.Context, userID uuid.UUID) (*ReceiptChallenges, error)
}

// receiptChallengeProvider is the default implementation using existing orchestrator providers.
type receiptChallengeProvider struct {
	receipts ReceiptHistoryProvider
	budgets  BudgetProvider
	spending SpendingAnalyzer
}

// NewReceiptChallengeProvider creates a ReceiptChallengeProvider from existing providers.
func NewReceiptChallengeProvider(receipts ReceiptHistoryProvider, budgets BudgetProvider, spending SpendingAnalyzer) ReceiptChallengeProvider {
	return &receiptChallengeProvider{receipts: receipts, budgets: budgets, spending: spending}
}

func (p *receiptChallengeProvider) GetChallenges(ctx context.Context, userID uuid.UUID) (*ReceiptChallenges, error) {
	now := time.Now().UTC()

	if p.receipts == nil {
		return &ReceiptChallenges{
			ScanningStreak:     map[string]interface{}{"days_active": 0, "last_scan": "No receipts scanned yet", "message": "Start scanning receipts to build a streak!"},
			CategoryChallenges: nil,
			Suggestions:        []string{"Try scanning every cash purchase this week!"},
		}, nil
	}

	// --- Scanning streak: count consecutive days with at least one receipt scan ---
	// Look back up to 90 days of receipts to compute streak
	receipts, err := p.receipts.GetByUserIDInRange(ctx, userID, now.AddDate(0, -3, 0), now, 50)
	if err != nil {
		return nil, fmt.Errorf("receipt challenges: %w", err)
	}

	// Build set of dates with scans
	scanDays := make(map[string]bool)
	for _, r := range receipts {
		scanDays[r.CreatedAt.Format("2006-01-02")] = true
	}

	// Count consecutive days ending today (or yesterday)
	streak := 0
	day := now
	// Allow today to not have a scan yet — start from yesterday if today is missing
	if !scanDays[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
	}
	for i := 0; i < 90; i++ {
		if !scanDays[day.Format("2006-01-02")] {
			break
		}
		streak++
		day = day.AddDate(0, 0, -1)
	}

	lastScanMsg := "No receipts scanned yet"
	if len(receipts) > 0 {
		ago := now.Sub(receipts[0].CreatedAt)
		switch {
		case ago < time.Hour:
			lastScanMsg = fmt.Sprintf("%d minutes ago", int(ago.Minutes()))
		case ago < 24*time.Hour:
			lastScanMsg = fmt.Sprintf("%d hours ago", int(ago.Hours()))
		default:
			lastScanMsg = fmt.Sprintf("%d days ago", int(ago.Hours()/24))
		}
	}

	streakMsg := "Start scanning receipts to build a streak!"
	if streak > 0 {
		streakMsg = fmt.Sprintf("You've been tracking cash spending for %d days!", streak)
	}

	scanningStreak := map[string]interface{}{
		"days_active": streak,
		"last_scan":   lastScanMsg,
		"message":     streakMsg,
	}

	// --- Category challenges: budget vs receipt spending this month ---
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	daysLeft := int(nextMonth.Sub(now).Hours() / 24)

	catTotals, _ := p.receipts.GetTotalByCategory(ctx, userID, monthStart, now)

	var categoryChallenges []map[string]interface{}
	var budget *decimal.Decimal
	if p.budgets != nil {
		b, _ := p.budgets.GetByUserID(ctx, userID)
		if b != nil {
			budget = &b.MonthlyLimit
		}
	}

	for _, cat := range catTotals {
		ch := map[string]interface{}{
			"category":  cat.Category,
			"current":   cat.Total.StringFixed(2),
			"days_left": daysLeft,
		}
		if budget != nil && !budget.IsZero() {
			// Proportional target per category based on its share of total spending
			remaining := budget.Sub(cat.Total)
			status := "on_track"
			if remaining.IsNegative() {
				status = "exceeded"
			} else if cat.Total.Div(*budget).GreaterThan(decimal.NewFromFloat(0.8)) {
				status = "at_risk"
			}
			ch["target"] = budget.StringFixed(2)
			ch["remaining"] = remaining.StringFixed(2)
			ch["status"] = status
		} else {
			ch["status"] = "no_budget"
		}
		categoryChallenges = append(categoryChallenges, ch)
	}

	// --- Suggestions ---
	suggestions := []string{}
	if streak == 0 {
		suggestions = append(suggestions, "Try scanning every cash purchase this week!")
	} else if streak < 7 {
		suggestions = append(suggestions, fmt.Sprintf("You're on a %d-day streak — keep scanning to hit 7 days!", streak))
	} else {
		suggestions = append(suggestions, fmt.Sprintf("Amazing %d-day streak! You're building a great tracking habit.", streak))
	}
	for _, cat := range catTotals {
		if budget != nil && !budget.IsZero() && cat.Total.Div(*budget).GreaterThan(decimal.NewFromFloat(0.5)) {
			suggestions = append(suggestions, fmt.Sprintf("Your %s spending is trending high — keep an eye on it!", cat.Category))
			break
		}
	}

	return &ReceiptChallenges{
		ScanningStreak:     scanningStreak,
		CategoryChallenges: categoryChallenges,
		Suggestions:        suggestions,
	}, nil
}

// SetReceiptChallenges sets the receipt challenge provider.
func (o *Orchestrator) SetReceiptChallenges(p ReceiptChallengeProvider) {
	o.receiptChallenges = p
}

// ReceiptChallengeTool returns the tool definition.
func ReceiptChallengeTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetReceiptChallenges,
		Description: "Get active spending challenges and receipt scanning streaks. Shows progress on budget challenges and scanning consistency.",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
	}
}

func (o *Orchestrator) executeReceiptChallenges(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	if o.receiptChallenges == nil {
		return map[string]interface{}{"error": "receipt challenges not available"}, nil
	}
	ch, err := o.receiptChallenges.GetChallenges(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("receipt challenges: %w", err)
	}
	return map[string]interface{}{
		"scanning_streak":     ch.ScanningStreak,
		"category_challenges": ch.CategoryChallenges,
		"suggestions":         ch.Suggestions,
	}, nil
}
