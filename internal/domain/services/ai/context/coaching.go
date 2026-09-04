package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

const toxicDebtThreshold = 0.10

func (b *Builder) buildCoachingContext(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var state *entities.MiriamMoneyState
	if b.deps.GetMoneyStateFn != nil {
		state, _ = b.deps.GetMoneyStateFn(fetchCtx, userID)
	}

	spendBalance, stashBalance := b.coachingBalances(fetchCtx, userID)

	var debts []entities.FinancialObligation
	if b.deps.ListActiveObligationsFn != nil {
		if list, err := b.deps.ListActiveObligationsFn(fetchCtx, userID); err == nil {
			for _, ob := range list {
				if ob.Type == entities.ObligationTypeDebt && ob.Status == entities.ObligationStatusActive {
					debts = append(debts, ob)
				}
			}
		}
	}

	var profile *entities.FinancialProfile
	if b.deps.GetFinancialProfileFn != nil {
		profile, _ = b.deps.GetFinancialProfileFn(fetchCtx, userID)
	}

	hasBankStatement := false
	if b.deps.GetBankUploadSummaryFn != nil {
		if _, banks, err := b.deps.GetBankUploadSummaryFn(fetchCtx, userID); err == nil && len(banks) > 0 {
			hasBankStatement = true
		}
	}

	monoSpendStr := ""
	monoLinked := false
	if b.deps.GetMonoSpendingFn != nil {
		if analysis, err := b.deps.GetMonoSpendingFn(fetchCtx, userID, 30); err == nil && analysis != nil && analysis.TransactionCount > 0 {
			monoLinked = true
			monoSpendStr = fmt.Sprintf("mono_spend: %.0f debits, %.0f credits, %.0f%% savings_rate, %d txns (last %d days)",
				float64(analysis.TotalDebits)/100, float64(analysis.TotalCredits)/100,
				analysis.SavingsRate*100, analysis.TransactionCount, analysis.Period.Days)
		}
	}

	step, progress := ClassifyFreedomStep(
		state,
		spendBalance,
		stashBalance,
		debts,
		profile,
		IsAuditReady(state, hasBankStatement),
		b.getPortfolioValue(fetchCtx, userID),
	)

	var parts []string
	parts = append(parts, fmt.Sprintf("step: %d (%s)", step, FreedomStepName(step)))
	parts = append(parts, fmt.Sprintf("status: %s", stepStatus(step, progress)))
	parts = append(parts, fmt.Sprintf("progress: %s", progress))

	if state != nil && state.AvgMonthlyIncome.IsPositive() {
		savingsRate := state.MonthlySavings.Div(state.AvgMonthlyIncome).Mul(decimal.NewFromInt(100))
		parts = append(parts, fmt.Sprintf("savings_rate: %s%%", savingsRate.StringFixed(1)))
	}

	if len(debts) > 0 {
		totalDebt := decimal.Zero
		toxicCount := 0
		for _, d := range debts {
			totalDebt = totalDebt.Add(d.Amount)
			rate := EstimateRateFromObligation(d)
			if rate.GreaterThan(decimal.NewFromFloat(toxicDebtThreshold)) {
				toxicCount++
			}
		}
		parts = append(parts, fmt.Sprintf("debts: %d (total %.0f, %d toxic)", len(debts), totalDebt.InexactFloat64(), toxicCount))
	}

	auditReady := IsAuditReady(state, hasBankStatement)
	if auditReady {
		parts = append(parts, "audit_ready: true")
	}

	if monoSpendStr != "" {
		parts = append(parts, monoSpendStr)
	}
	parts = append(parts, fmt.Sprintf("mono_linked: %t", monoLinked))

	nudge := FreedomStepNudge(step)
	if !monoLinked {
		nudge += " If the conversation touches spending or budgets, suggest connecting their bank through Mono for real spending insights — but only if it fits naturally, never force it."
	}
	header := "[COACHING STATE — " + strings.Join(parts, " | ")
	return header + ".\nSTEER: " + nudge + "]"
}

func stepStatus(step int, progress string) string {
	if strings.Contains(strings.ToLower(progress), "rich life") {
		return "ongoing"
	}
	return "in_progress"
}

func (b *Builder) coachingBalances(ctx context.Context, userID uuid.UUID) (spend, stash decimal.Decimal) {
	if b.deps.GetBalanceFn == nil {
		return
	}
	spend, _ = b.deps.GetBalanceFn(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ = b.deps.GetBalanceFn(ctx, userID, entities.AccountTypeStashBalance)
	return
}

func (b *Builder) getPortfolioValue(ctx context.Context, userID uuid.UUID) decimal.Decimal {
	if b.deps.GetPortfolioStatsFn == nil {
		return decimal.Zero
	}
	if stats, err := b.deps.GetPortfolioStatsFn(ctx, userID); err == nil && stats != nil {
		return stats.TotalValue
	}
	return decimal.Zero
}
