package ai

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

// scoredStarter couples a conversation starter with an urgency score so the
// prediction layer can rank "what would be most useful right now".
type scoredStarter struct {
	starter core.ConversationStarter
	score   int
}

// PredictiveConversationStarters ranks conversation starters from actual user
// patterns — balances, spending pace, budget, recurring bills and obligations
// — instead of returning static templates. It always returns a non-empty list;
// when no signals are available it degrades to the canonical four templates.
func (o *AgentAdapter) PredictiveConversationStarters(ctx context.Context, userID uuid.UUID) []core.ConversationStarter {
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	spend, stash, _ := o.currentBalances(ctx, userID)

	var monthlySpend decimal.Decimal
	var txCount int
	if o.spending != nil {
		if summary, err := o.spending.GetSummary(ctx, userID, monthStart, now); err == nil {
			monthlySpend = summary.Total
			txCount = summary.TxCount
		}
	}

	// Estimate monthly obligations/recurring outflows.
	var obligationTotal decimal.Decimal
	var obligationsDueSoon []string
	if o.obligations != nil {
		if obls, err := o.obligations.ListActive(ctx, userID); err == nil {
			for _, ob := range obls {
				obligationTotal = obligationTotal.Add(ob.Amount)
				if days, ok := daysUntilDue(ob.DueDay, ob.DueDate, now); ok && days <= 7 {
					obligationsDueSoon = append(obligationsDueSoon, fmt.Sprintf("%s in %d day(s)", ob.Name, days))
				}
			}
		}
	}

	var recurringTotal decimal.Decimal
	var topRecurring string
	if o.recurringDetector != nil {
		if recs, err := o.recurringDetector.DetectRecurring(ctx, userID); err == nil && len(recs) > 0 {
			for _, r := range recs {
				recurringTotal = recurringTotal.Add(r.AvgAmount)
			}
			topRecurring = recs[0].Merchant
		}
	}

	outflow := obligationTotal
	if outflow.IsZero() {
		outflow = recurringTotal
	}

	// Budget usage for over-spend / pace signals.
	var budgetLimit decimal.Decimal
	var pctUsed decimal.Decimal
	hasBudget := false
	if o.budgetProvider != nil {
		if b, err := o.budgetProvider.GetByUserID(ctx, userID); err == nil && b != nil && !b.MonthlyLimit.IsZero() {
			hasBudget = true
			budgetLimit = b.MonthlyLimit
			pctUsed = monthlySpend.Div(budgetLimit).Mul(decimal.NewFromInt(100))
		}
	}

	var scored []scoredStarter
	add := func(score int, text, category string) {
		scored = append(scored, scoredStarter{starter: core.ConversationStarter{Text: text, Category: category}, score: score})
	}

	// 1. Over budget — the most urgent signal.
	if hasBudget && monthlySpend.GreaterThan(budgetLimit) {
		over := monthlySpend.Sub(budgetLimit)
		add(100, fmt.Sprintf("You're %s over your %s budget — want me to help trim spending?", fmtUSD(over), fmtUSD(budgetLimit)), "spending")
	} else if hasBudget && pctUsed.GreaterThan(decimal.NewFromInt(80)) {
		add(85, fmt.Sprintf("You've used %s%% of your budget — want to slow down this month?", pctUsed.StringFixed(0)), "spending")
	}

	// 2. Bills due within the week.
	for _, due := range obligationsDueSoon {
		add(90, fmt.Sprintf("Your %s — want to set the money aside now?", due), "action")
	}

	// 3. Idle cash in Spend that could earn yield in Stash.
	idle := spend.Sub(outflow)
	if spend.IsPositive() && idle.GreaterThan(decimal.NewFromInt(50)) {
		add(88, fmt.Sprintf("You have %s sitting in Spend — move it to Stash and earn yield?", fmtUSD(idle)), "saving")
	}

	// 4. Spend balance running low with stash to fall back on.
	if spend.LessThan(decimal.NewFromInt(100)) && stash.IsPositive() && spend.IsPositive() {
		add(80, "Your Spend balance is running low — top up from Stash?", "action")
	}

	// 5. Recurring bill worth tracking.
	if topRecurring != "" && obligationTotal.IsZero() && recurringTotal.IsPositive() {
		add(70, fmt.Sprintf("You pay about %s/mo to %s — want me to track and protect it?", fmtUSD(recurringTotal), topRecurring), "insight")
	}

	// 6. Saving streak worth celebrating.
	if o.activityProvider != nil {
		if streak, err := o.activityProvider.GetStreak(ctx, userID); err == nil && streak != nil && streak.CurrentStreak > 3 {
			add(60, fmt.Sprintf("You're on a %d-day streak — how does my savings progress look?", streak.CurrentStreak), "saving")
		}
	}

	// 7. Month-end review when there is real spending to analyze.
	if txCount > 0 {
		add(45, "Where did my money go this month?", "spending")
		add(40, "Show me my spending patterns", "insight")
	}

	// 8. Stash growth planning.
	if !stash.IsZero() {
		add(35, "How is my stash doing?", "saving")
	}

	// With no data signals at all, fall back to the canonical templates.
	if len(scored) == 0 {
		return []core.ConversationStarter{
			{Text: "Where did my money go this month?", Category: "spending"},
			{Text: "How is my stash doing?", Category: "saving"},
			{Text: "What are my spending patterns?", Category: "insight"},
			{Text: "Help me save more this month", Category: "action"},
		}
	}

	// 9. General evergreen starters to round out the list.
	add(25, "What's my financial health score?", "insight")
	add(20, "Set up an automation to save every Friday", "action")

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	starters := make([]core.ConversationStarter, 0, len(scored))
	for _, s := range scored {
		starters = append(starters, s.starter)
	}
	if len(starters) > 6 {
		starters = starters[:6]
	}
	return starters
}

// daysUntilDue reports how many days remain until an obligation is due based on
// its due-day-of-month or explicit due date. Returns false when undetermined.
func daysUntilDue(dueDay *int, dueDate *time.Time, now time.Time) (int, bool) {
	var due time.Time
	switch {
	case dueDate != nil:
		due = *dueDate
	case dueDay != nil:
		day := *dueDay
		if day < 1 {
			day = 1
		}
		if day > 31 {
			day = 31
		}
		due = time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.UTC)
		if due.Before(now) {
			due = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			last := due.AddDate(0, 0, -1).Day()
			if day > last {
				day = last
			}
			due = time.Date(now.Year(), now.Month()+1, day, 0, 0, 0, 0, time.UTC)
		}
	default:
		return 0, false
	}
	days := int(due.Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
	if days < 0 {
		return 0, false
	}
	return days, true
}
