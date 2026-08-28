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
// "you just received ₦120k. move ₦15k to Stash and your emergency fund reaches 40%. want to do it?"
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
			"you just received %s%s. move %s%s to Stash and your emergency fund reaches %d%%. want to do it?",
			cur, depositStr, cur, saveStr, ctx.EmergencyFundPct,
		)
	} else if !ctx.SuggestedSaveAmt.IsZero() {
		body = fmt.Sprintf(
			"you just received %s%s. want to move %s%s to Stash while it's fresh?",
			cur, depositStr, cur, saveStr,
		)
	} else {
		body = fmt.Sprintf("money just landed: %s%s. want to put some of it to work?", cur, depositStr)
	}

	return MiriamIntervention{
		Trigger:    TriggerSalaryArrived,
		Title:      "money just landed",
		Body:       body,
		VoiceHook:  fmt.Sprintf("your %s%s just arrived. want to move some to Stash before it gets spent?", cur, depositStr),
		Priority:   9,
		ActionType: "save_now",
		ActionAmt:  ctx.SuggestedSaveAmt,
	}
}

// Overspend builds the overspend intervention.
// "food spending is ₦18k over your usual pace this month. want to take a look?"
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

	body := fmt.Sprintf(
		"your %s spending is %s%s over your usual pace %s. want to take a look?",
		strings.ToLower(category), cur, overStr, strings.ToLower(period),
	)
	voiceHook := body
	if ctx.BudgetPace != "" {
		voiceHook = strings.ToLower(strings.ReplaceAll(ctx.BudgetPace, "—", ","))
	} else {
		voiceHook = fmt.Sprintf(
			"at this pace, your %s budget may run out before month end. want to see what's driving it?",
			strings.ToLower(category),
		)
	}

	return MiriamIntervention{
		Trigger:    TriggerOverspend,
		Title:      "spending check",
		Body:       body,
		VoiceHook:  voiceHook,
		Priority:   7,
		ActionType: "review_spending",
	}
}

// IdleMoney builds the idle-money intervention.
// Body keeps a Pidgin variant; VoiceHook offers standard English.
func (b *MiriamNudgeBuilder) IdleMoney(ctx InterventionContext) MiriamIntervention {
	cur := ctx.Currency
	if cur == "" {
		cur = "₦"
	}
	idleStr := formatAmount(ctx.IdleAmount)

	body := fmt.Sprintf("%s%s dey sit down for account. make we move small to Stash?", cur, idleStr)

	return MiriamIntervention{
		Trigger:    TriggerIdleMoney,
		Title:      "idle money alert",
		Body:       body,
		VoiceHook:  fmt.Sprintf("you've got %s%s sitting idle. want to move some to Stash and give it a job?", cur, idleStr),
		Priority:   6,
		ActionType: "move_to_stash",
		ActionAmt:  ctx.IdleAmount.Mul(decimal.NewFromFloat(0.3)), // suggest moving 30%
	}
}

// WinStreak builds the identity-reinforcing win message.
// "you saved ₦50k and left it alone this month. that's a real win."
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

	body := fmt.Sprintf(
		"you saved %s%s and left it alone %s. that's a real win.",
		cur, savedStr, strings.ToLower(period),
	)

	return MiriamIntervention{
		Trigger: TriggerWinStreak,
		Title:   "you're building something",
		Body:    body,
		VoiceHook: fmt.Sprintf(
			"you saved %s%s %s and left it untouched. this is how good money habits compound.",
			cur, savedStr, strings.ToLower(period),
		),
		Priority: 5,
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
