package context

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func (b *Builder) buildBalanceContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetBalanceFn == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	spend, _ := b.deps.GetBalanceFn(fetchCtx, userID, entities.AccountTypeSpendingBalance)
	stash, _ := b.deps.GetBalanceFn(fetchCtx, userID, entities.AccountTypeStashBalance)

	if spend.IsZero() && stash.IsZero() {
		return ""
	}
	return fmt.Sprintf("[ACCOUNTS — Spend: $%s, Stash: $%s]", spend.StringFixed(2), stash.StringFixed(2))
}

func (b *Builder) buildStashLockContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetBalanceFn == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	stash, _ := b.deps.GetBalanceFn(fetchCtx, userID, entities.AccountTypeStashBalance)
	if !stash.IsPositive() {
		return ""
	}
	return fmt.Sprintf("[STASH LOCKED: $%s is in the user's Stash account — protected, not available for spending.]", stash.StringFixed(2))
}

func (b *Builder) buildUserProfileContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetUserCountryFn == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	country, err := b.deps.GetUserCountryFn(fetchCtx, userID)
	if err != nil || country == "" {
		return ""
	}
	return fmt.Sprintf("[USER PROFILE — country: %s]", country)
}

func (b *Builder) buildFinancialProfileContext(ctx context.Context, userID uuid.UUID) string {
	if b.deps.GetFinancialProfileFn == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	profile, err := b.deps.GetFinancialProfileFn(fetchCtx, userID)
	if err != nil || profile == nil {
		return ""
	}
	var parts []string
	if profile.MonthlyIncome.IsPositive() {
		parts = append(parts, fmt.Sprintf("monthly_income: $%s", profile.MonthlyIncome.StringFixed(0)))
	}
	if profile.MonthlyFixedCosts.IsPositive() {
		parts = append(parts, fmt.Sprintf("monthly_expenses: $%s", profile.MonthlyFixedCosts.StringFixed(0)))
	}
	if profile.EmergencyFundTarget.IsPositive() {
		parts = append(parts, fmt.Sprintf("emergency_fund_target: $%s", profile.EmergencyFundTarget.StringFixed(0)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[FINANCIAL PROFILE — " + joinNonEmpty(parts...) + "]"
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	out := ""
	for i, k := range kept {
		if i > 0 {
			out += " | "
		}
		out += k
	}
	return out
}
