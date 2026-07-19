package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// NairaOrderSummary is a lightweight view of recent paj orders.
type NairaOrderSummary struct {
	OrderType   string  // "onramp" or "offramp"
	FiatAmount  float64 // NGN amount
	TokenAmount float64 // USD amount
	Rate        float64
	Currency    string
	CreatedAt   time.Time
}

// NairaOrderProvider abstracts paj_orders queries for the AI context layer.
type NairaOrderProvider interface {
	GetRecentOrders(ctx context.Context, userID uuid.UUID, limit int) ([]NairaOrderSummary, error)
}

// nairaCtx holds the provider for naira context building.
type nairaCtx struct {
	provider NairaOrderProvider
}

// SetNairaContext wires the naira context provider.
func (o *AgentAdapter) SetNairaContext(p NairaOrderProvider) {
	if p != nil {
		o.nairaCtx = &nairaCtx{provider: p}
	}
}

// liveNairaRateLine returns a context line with the current NGN/USD rate so
// Miriam never quotes a stale, memorized rate. Empty when no live rate is known.
func (o *AgentAdapter) liveNairaRateLine(ctx context.Context) string {
	if o.currencyRates == nil {
		return ""
	}
	// The rate is user-independent and slow to fetch (live ramp API); cache it
	// process-wide so it stays off the per-turn hot path. An empty cached value
	// means "unavailable last time" and is honored until it expires.
	if cached, ok := globalContextCache.GetRateLine(); ok {
		return cached
	}
	rctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	rate, err := o.currencyRates.GetLatestRate(rctx, "USD", "NGN")
	if err != nil || !rate.IsPositive() {
		globalContextCache.SetRateLine("")
		return ""
	}
	// Guard against an inverted pair (USD-per-NGN would be a tiny fraction).
	if rate.LessThan(decimal.NewFromInt(10)) {
		globalContextCache.SetRateLine("")
		return ""
	}
	line := fmt.Sprintf("[Live rate right now: $1 ≈ ₦%s. Use this exact rate for any naira/dollar conversion, never a memorized rate.]", rate.Round(0).String())
	globalContextCache.SetRateLine(line)
	return line
}

// buildNairaContext prepends the live NGN/USD rate, then merges the user's recent
// paj_orders into a dual-currency context block.
func (o *AgentAdapter) buildNairaContext(ctx context.Context, userID uuid.UUID) string {
	rateLine := o.liveNairaRateLine(ctx)

	if o.nairaCtx == nil || o.nairaCtx.provider == nil {
		return rateLine
	}

	// Check cache first (order-history portion); prepend the fresh rate.
	if cached, ok := globalContextCache.GetNairaCtx(userID); ok {
		return joinNonEmpty(rateLine, cached)
	}

	orders, err := o.nairaCtx.provider.GetRecentOrders(ctx, userID, 5)
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
	globalContextCache.SetNairaCtx(userID, result)
	return joinNonEmpty(rateLine, result)
}

// joinNonEmpty joins non-empty blocks with a newline.
func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}
