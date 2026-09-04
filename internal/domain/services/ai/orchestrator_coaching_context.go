package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	freedompkg "github.com/rail-service/rail_service/internal/domain/services/ai/freedom"
	"github.com/shopspring/decimal"
)

type ConsciousSpendingPlanProvider interface {
	Get(ctx context.Context, userID uuid.UUID) (*entities.ConsciousSpendingPlan, error)
}

func (o *AgentAdapter) SetConsciousSpendingPlanProvider(provider ConsciousSpendingPlanProvider) {
	o.consciousSpendingPlans = provider
}

// buildCoachingContext injects a [COACHING STATE] block on EVERY conversation
// turn (not just onboarding) so Miriam always knows which Financial Freedom
// Step the user is on, their progress, debt situation, savings rate, and what
// to steer toward. This is the "always-on coaching brain" that makes every
// conversation context-aware.
//
// Returns "" when there's not enough data to classify (brand-new users with
// no state, no balances, no profile) — the onboarding context handles those.
func (o *AgentAdapter) buildCoachingContext(ctx context.Context, userID uuid.UUID) string {
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var consciousPlanCh chan *entities.ConsciousSpendingPlan
	if o.consciousSpendingPlans != nil {
		consciousPlanCh = make(chan *entities.ConsciousSpendingPlan, 1)
		go func() {
			plan, err := o.consciousSpendingPlans.Get(fetchCtx, userID)
			if err != nil {
				plan = nil
			}
			consciousPlanCh <- plan
		}()
	}

	// 1. Gather all the data we need in parallel-ish (sequential but fast,
	//    each call is <500ms and we're inside the 1.5s assembly budget).
	var state *entities.MiriamMoneyState
	if o.miriamIntelligence != nil {
		state, _ = o.miriamIntelligence.GetMoneyState(fetchCtx, userID)
	}

	spendBalance, stashBalance := o.coachingBalances(fetchCtx, userID)

	var debts []entities.FinancialObligation
	if o.obligations != nil {
		if list, err := o.obligations.ListActive(fetchCtx, userID); err == nil {
			for _, ob := range list {
				if ob.Type == entities.ObligationTypeDebt && ob.Status == entities.ObligationStatusActive {
					debts = append(debts, ob)
				}
			}
		}
	}

	var profile *entities.FinancialProfile
	if o.financialProfile != nil {
		profile, _ = o.financialProfile.GetByUserID(fetchCtx, userID)
	}

	hasBankStatement := false
	if o.bankStatementCtx != nil && o.bankStatementCtx.provider != nil {
		if _, banks, err := o.bankStatementCtx.provider.GetCompletedUploadSummary(fetchCtx, userID); err == nil && len(banks) > 0 {
			hasBankStatement = true
		}
	}

	// Check for Mono-linked account spending data.
	monoSpendStr := ""
	monoLinked := false
	if o.monoAnalysis != nil {
		if analysis, err := o.monoAnalysis.GetSpendingAnalysis(fetchCtx, userID, 30); err == nil && analysis != nil && analysis.TransactionCount > 0 {
			monoLinked = true
			monoSpendStr = fmt.Sprintf("mono_spend: %.0f debits, %.0f credits, %.0f%% savings_rate, %d txns (last %d days)",
				float64(analysis.TotalDebits)/100, float64(analysis.TotalCredits)/100,
				analysis.SavingsRate*100, analysis.TransactionCount, analysis.Period.Days)
		}
	}

	// 2. Classify the user's current step.
	step, progress := freedompkg.ClassifyFreedomStep(
		state,
		spendBalance,
		stashBalance,
		debts,
		profile,
		freedompkg.IsAuditReady(state, hasBankStatement),
		o.getPortfolioValue(fetchCtx, userID),
	)

	// 3. Build the context block.
	var parts []string
	parts = append(parts, fmt.Sprintf("step: %d (%s)", step, freedompkg.FreedomStepName(step)))
	parts = append(parts, fmt.Sprintf("status: %s", stepStatus(step, progress)))
	parts = append(parts, fmt.Sprintf("progress: %s", progress))

	// Savings rate from money state
	if state != nil && state.AvgMonthlyIncome.IsPositive() {
		savingsRate := state.MonthlySavings.Div(state.AvgMonthlyIncome).Mul(decimal.NewFromInt(100))
		parts = append(parts, fmt.Sprintf("savings_rate: %s%%", savingsRate.StringFixed(1)))
	}

	// Debt summary
	if len(debts) > 0 {
		totalDebt := decimal.Zero
		toxicCount := 0
		for _, d := range debts {
			totalDebt = totalDebt.Add(d.Amount)
			rate := freedompkg.EstimateRateFromObligation(d)
			if rate.GreaterThan(decimal.NewFromFloat(freedompkg.ToxicDebtThreshold)) {
				toxicCount++
			}
		}
		parts = append(parts, fmt.Sprintf("debts: %d (total %.0f, %d toxic)", len(debts), totalDebt.InexactFloat64(), toxicCount))
	}

	// Audit readiness
	auditReady := freedompkg.IsAuditReady(state, hasBankStatement)
	if auditReady {
		parts = append(parts, "audit_ready: true")
	}

	// Mono-linked spending data (if available)
	if monoSpendStr != "" {
		parts = append(parts, monoSpendStr)
	}
	parts = append(parts, fmt.Sprintf("mono_linked: %t", monoLinked))

	var consciousPlan *entities.ConsciousSpendingPlan
	if consciousPlanCh != nil {
		select {
		case consciousPlan = <-consciousPlanCh:
		case <-fetchCtx.Done():
		}
		if consciousPlan != nil {
			plan := consciousPlan
			parts = append(parts, fmt.Sprintf(
				"csp: %s | income %s %s | fixed %s%% | investments %s%% | savings %s%% | guilt_free %s%% | check_in %s",
				plan.Status, plan.BaseCurrency, plan.TakeHomeIncome.StringFixed(2),
				plan.FixedCostsPct.StringFixed(1), plan.InvestmentsPct.StringFixed(1),
				plan.SavingsPct.StringFixed(1), plan.GuiltFreeSpendingPct.StringFixed(1),
				plan.CheckInCadence,
			))
			if plan.Status == entities.ConsciousSpendingPlanStatusCommitted {
				parts = append(parts, "csp_coaching: committed")
			}
		}
	}

	// Coaching nudge — the key instruction that makes every conversation context-aware
	nudge := freedompkg.FreedomStepNudge(step)
	if consciousPlan != nil && consciousPlan.Status == entities.ConsciousSpendingPlanStatusCommitted {
		nudge += " A committed four-number plan exists. Use it as the monthly allocation layer beneath this Freedom Step. Hold the user to their own numbers, ask what changed before revising them, and never silently lower a target."
	}
	if !monoLinked {
		nudge += " If the conversation touches spending or budgets, suggest connecting their bank through Mono for real spending insights — but only if it fits naturally, never force it."
	}
	header := "[COACHING STATE — " + strings.Join(parts, " | ")
	return header + ".\nSTEER: " + nudge + "]"
}

// stepStatus returns a status string for the current step.
func stepStatus(step int, progress string) string {
	// If progress indicates completion language, mark completed
	if strings.Contains(strings.ToLower(progress), "rich life") {
		return "ongoing"
	}
	return "in_progress"
}

// coachingBalances is a helper that fetches spend and stash balances for the
// coaching context. Returns zero values if unavailable.
func (o *AgentAdapter) coachingBalances(ctx context.Context, userID uuid.UUID) (spend, stash decimal.Decimal) {
	if o.aggregateStats == nil {
		return
	}
	spend, _ = o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	stash, _ = o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	return
}

// getPortfolioValue fetches the user's total investment portfolio value.
// Returns zero if unavailable.
func (o *AgentAdapter) getPortfolioValue(ctx context.Context, userID uuid.UUID) decimal.Decimal {
	if o.portfolioProvider == nil {
		return decimal.Zero
	}
	if stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID); err == nil && stats != nil {
		return stats.TotalValue
	}
	return decimal.Zero
}
