package miriam

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// InterventionTrigger identifies the moment that caused a proactive intervention.
type InterventionTrigger string

const (
	// TriggerSalaryArrived fires when a significant income deposit is detected.
	TriggerSalaryArrived InterventionTrigger = "salary_arrived"
	// TriggerOverspend fires when a spending category exceeds its normal pattern.
	TriggerOverspend InterventionTrigger = "overspend_detected"
	// TriggerIdleMoney fires when a meaningful balance has been sitting untouched.
	TriggerIdleMoney InterventionTrigger = "idle_money"
	// TriggerWinStreak fires after a user has saved without touching funds.
	TriggerWinStreak InterventionTrigger = "win_streak"
)

// InterventionContext carries the numbers Miriam needs to build a message.
type InterventionContext struct {
	// Salary / income
	DepositAmount    decimal.Decimal
	Currency         string // "₦", "$", "£"
	SuggestedSaveAmt decimal.Decimal
	EmergencyFundPct int // current % of emergency fund target

	// Overspend
	Category        string
	OverspendAmount decimal.Decimal
	MonthName       string // "this month"
	BudgetPace      string // "At this pace your travel budget fit finish before month end."

	// Idle money
	IdleAmount decimal.Decimal

	// Win streak
	SavedAmount decimal.Decimal
	PeriodLabel string // "this month", "last 30 days"
}

// MiriamIntervention is a ready-to-deliver proactive message.
type MiriamIntervention struct {
	Trigger    InterventionTrigger
	Title      string // push notification title
	Body       string // push notification body / voice opener
	VoiceHook  string // what Miriam says when the user opens voice after this nudge
	Priority   int    // 1–10
	ActionType string // "save_now", "review_spending", "move_to_stash", ""
	ActionAmt  decimal.Decimal
}

// MiriamNudgeBuilder builds emotionally intelligent interventions.
type MiriamNudgeBuilder struct{}

// NewMiriamNudgeBuilder creates a builder.
func NewMiriamNudgeBuilder() *MiriamNudgeBuilder { return &MiriamNudgeBuilder{} }

// SalaryArrived builds the salary-entry intervention.
// "You receive ₦120k. If we save ₦15k now, your emergency fund reaches 40%. Save am?"
func (b *MiriamNudgeBuilder) SalaryArrived(ctx InterventionContext) MiriamIntervention {
	cur := ctx.Currency
	if cur == "" {
		cur = "₦"
	}
	depositStr := formatAmount(ctx.DepositAmount)
	saveStr := formatAmount(ctx.SuggestedSaveAmt)

	var body string
	if !ctx.SuggestedSaveAmt.IsZero() && ctx.EmergencyFundPct > 0 {
		body = fmt.Sprintf(
			"You receive %s%s. If we save %s%s now, your emergency fund reaches %d%%. Save am?",
			cur, depositStr, cur, saveStr, ctx.EmergencyFundPct,
		)
	} else if !ctx.SuggestedSaveAmt.IsZero() {
		body = fmt.Sprintf(
			"You receive %s%s. Move %s%s to Stash now while it's fresh?",
			cur, depositStr, cur, saveStr,
		)
	} else {
		body = fmt.Sprintf("Money just landed — %s%s. Want to put some to work?", cur, depositStr)
	}

	return MiriamIntervention{
		Trigger:    TriggerSalaryArrived,
		Title:      "Money just landed",
		Body:       body,
		VoiceHook:  fmt.Sprintf("Your %s%s just arrived. Should I move some to Stash before it disappears?", cur, depositStr),
		Priority:   9,
		ActionType: "save_now",
		ActionAmt:  ctx.SuggestedSaveAmt,
	}
}

// Overspend builds the overspend intervention.
// "Food spending pass normal this month by ₦18k." → "At this pace your travel budget fit finish before month end."
func (b *MiriamNudgeBuilder) Overspend(ctx InterventionContext) MiriamIntervention {
	cur := ctx.Currency
	if cur == "" {
		cur = "₦"
	}
	category := ctx.Category
	if category == "" {
		category = "spending"
	}
	overStr := formatAmount(ctx.OverspendAmount)
	period := ctx.MonthName
	if period == "" {
		period = "this month"
	}

	body := fmt.Sprintf("%s spending pass normal %s by %s%s.", titleCase(category), period, cur, overStr)
	voiceHook := body
	if ctx.BudgetPace != "" {
		voiceHook = ctx.BudgetPace
	} else {
		voiceHook = fmt.Sprintf("At this pace your %s budget fit finish before month end.", category)
	}

	return MiriamIntervention{
		Trigger:    TriggerOverspend,
		Title:      "Spending check",
		Body:       body,
		VoiceHook:  voiceHook,
		Priority:   7,
		ActionType: "review_spending",
	}
}

// IdleMoney builds the idle-money intervention.
// "₦50k dey sit down for account. Should I move small?"
func (b *MiriamNudgeBuilder) IdleMoney(ctx InterventionContext) MiriamIntervention {
	cur := ctx.Currency
	if cur == "" {
		cur = "₦"
	}
	idleStr := formatAmount(ctx.IdleAmount)

	body := fmt.Sprintf("%s%s dey sit down for account. Should I move small?", cur, idleStr)

	return MiriamIntervention{
		Trigger:    TriggerIdleMoney,
		Title:      "Idle money alert",
		Body:       body,
		VoiceHook:  fmt.Sprintf("You've got %s%s sitting idle. Want me to move some to Stash?", cur, idleStr),
		Priority:   6,
		ActionType: "move_to_stash",
		ActionAmt:  ctx.IdleAmount.Mul(decimal.NewFromFloat(0.3)), // suggest moving 30%
	}
}

// WinStreak builds the identity-reinforcing win message.
// "You saved ₦50k without touching am this month."
func (b *MiriamNudgeBuilder) WinStreak(ctx InterventionContext) MiriamIntervention {
	cur := ctx.Currency
	if cur == "" {
		cur = "₦"
	}
	savedStr := formatAmount(ctx.SavedAmount)
	period := ctx.PeriodLabel
	if period == "" {
		period = "this month"
	}

	body := fmt.Sprintf("You saved %s%s without touching am %s.", cur, savedStr, period)

	return MiriamIntervention{
		Trigger:   TriggerWinStreak,
		Title:     "You're building something",
		Body:      body,
		VoiceHook: fmt.Sprintf("You saved %s%s %s without touching it. You're becoming someone who handles money well.", cur, savedStr, period),
		Priority:  5,
	}
}

// ToProactiveNudge converts a MiriamIntervention to the persisted entity.
func (i MiriamIntervention) ToProactiveNudge(userID uuid.UUID) *entities.ProactiveNudge {
	triggerMap := map[InterventionTrigger]string{
		TriggerSalaryArrived: entities.NudgeTriggerIncomeEvent,
		TriggerOverspend:     entities.NudgeTriggerPattern,
		TriggerIdleMoney:     entities.NudgeTriggerIdleMoney,
		TriggerWinStreak:     entities.NudgeTriggerMilestone,
	}
	triggerType, ok := triggerMap[i.Trigger]
	if !ok {
		triggerType = entities.NudgeTriggerPattern
	}

	return &entities.ProactiveNudge{
		ID:          uuid.New(),
		UserID:      userID,
		TriggerType: triggerType,
		Priority:    i.Priority,
		Message:     i.Body,
		ExpiresAt:   time.Now().Add(6 * time.Hour),
		CreatedAt:   time.Now(),
	}
}

// EvaluateInterventions checks a user's money state and returns any triggered interventions.
// This is the single entry point for the proactive system.
func EvaluateInterventions(
	ctx context.Context,
	userID uuid.UUID,
	state *entities.MiriamMoneyState,
	recentDepositAmt decimal.Decimal,
	idleSpendBalance decimal.Decimal,
	currency string,
) []MiriamIntervention {
	builder := NewMiriamNudgeBuilder()
	var results []MiriamIntervention

	// 1. Salary / income arrived
	salaryThreshold := decimal.NewFromInt(10000) // ₦10k minimum to trigger
	if recentDepositAmt.GreaterThan(salaryThreshold) {
		suggestSave := recentDepositAmt.Mul(decimal.NewFromFloat(0.15)).Round(0)
		results = append(results, builder.SalaryArrived(InterventionContext{
			DepositAmount:    recentDepositAmt,
			Currency:         currency,
			SuggestedSaveAmt: suggestSave,
		}))
	}

	// 2. Idle money in Spend wallet
	idleThreshold := decimal.NewFromInt(20000)
	if idleSpendBalance.GreaterThan(idleThreshold) {
		results = append(results, builder.IdleMoney(InterventionContext{
			IdleAmount: idleSpendBalance,
			Currency:   currency,
		}))
	}

	return results
}

// formatAmount formats a decimal for display (e.g. 120000 → "120k", 1500 → "1.5k").
func formatAmount(amt decimal.Decimal) string {
	if amt.IsZero() {
		return "0"
	}
	f, _ := amt.Float64()
	switch {
	case f >= 1_000_000:
		return fmt.Sprintf("%.1fm", f/1_000_000)
	case f >= 1_000:
		v := f / 1_000
		if v == float64(int(v)) {
			return fmt.Sprintf("%dk", int(v))
		}
		return fmt.Sprintf("%.1fk", v)
	default:
		return amt.StringFixed(0)
	}
}

// titleCase capitalises the first letter of a string (replaces deprecated strings.Title).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
