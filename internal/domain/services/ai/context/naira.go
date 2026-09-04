package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// buildNairaContext returns the live NGN/USD rate and the user's recent
// paj orders as a dual-currency context block.
func (b *Builder) buildNairaContext(ctx context.Context, userID uuid.UUID) string {
	rateLine := b.liveNairaRateLine(ctx)

	if b.deps.GetNairaOrdersFn == nil {
		return rateLine
	}

	if cached, ok := b.deps.Cache.GetNairaCtx(userID); ok {
		return joinNonEmpty(rateLine, cached)
	}

	orders, err := b.deps.GetNairaOrdersFn(ctx, userID, 5)
	if err != nil || len(orders) == 0 {
		return rateLine
	}

	var sb strings.Builder
	sb.WriteString("[Naira ↔ USD activity (from Paj orders):\n")

	var totalOnrampNGN, totalOfframpNGN float64
	var lastRate float64
	for _, o := range orders {
		dir := "NGN→USD"
		if o.OrderType == "offramp" {
			dir = "USD→NGN"
			totalOfframpNGN += o.FiatAmount
		} else {
			totalOnrampNGN += o.FiatAmount
		}
		if o.Rate > 0 {
			lastRate = o.Rate
		}
		sb.WriteString(fmt.Sprintf("• %s: ₦%.0f ↔ $%.2f (rate: ₦%.0f/$1) on %s\n",
			dir, o.FiatAmount, o.TokenAmount, o.Rate, o.CreatedAt.Format("Jan 2")))
	}

	if totalOnrampNGN > 0 {
		sb.WriteString(fmt.Sprintf("Total funded from naira: ₦%.0f\n", totalOnrampNGN))
	}
	if totalOfframpNGN > 0 {
		sb.WriteString(fmt.Sprintf("Total withdrawn to naira: ₦%.0f\n", totalOfframpNGN))
	}
	if lastRate > 0 {
		sb.WriteString(fmt.Sprintf("Last rate used: ₦%.0f/$1\n", lastRate))
	}
	sb.WriteString("]")

	result := sb.String()
	b.deps.Cache.SetNairaCtx(userID, result)
	return joinNonEmpty(rateLine, result)
}

func (b *Builder) liveNairaRateLine(ctx context.Context) string {
	if b.deps.GetLatestRateFn == nil {
		return ""
	}
	if cached, ok := b.deps.Cache.GetRateLine(); ok {
		return cached
	}
	rctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	rate, err := b.deps.GetLatestRateFn(rctx, "USD", "NGN")
	if err != nil || !rate.IsPositive() {
		b.deps.Cache.SetRateLine("")
		return ""
	}
	if rate.LessThan(decimal.NewFromInt(10)) {
		b.deps.Cache.SetRateLine("")
		return ""
	}
	line := fmt.Sprintf("[Live rate right now: $1 ≈ ₦%s. Use this exact rate for any naira/dollar conversion, never a memorized rate.]", rate.Round(0).String())
	b.deps.Cache.SetRateLine(line)
	return line
}
