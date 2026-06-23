package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
func (o *Orchestrator) SetNairaContext(p NairaOrderProvider) {
	if p != nil {
		o.nairaCtx = &nairaCtx{provider: p}
	}
}

// buildNairaContext queries paj_orders and merges with bank statement context
// to produce a dual-currency context block.
func (o *Orchestrator) buildNairaContext(ctx context.Context, userID uuid.UUID) string {
	if o.nairaCtx == nil || o.nairaCtx.provider == nil {
		return ""
	}

	// Check cache first
	if cached, ok := globalContextCache.GetNairaCtx(userID); ok {
		return cached
	}

	orders, err := o.nairaCtx.provider.GetRecentOrders(ctx, userID, 5)
	if err != nil || len(orders) == 0 {
		return ""
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
	return result
}
