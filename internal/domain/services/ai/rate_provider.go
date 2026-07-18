package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// BestQuoteFunc is a function that returns the best rate for a currency corridor.
// Matches ramp.Service.GetBestQuote signature.
type BestQuoteFunc func(ctx context.Context, side string, fiatAmount, tokenAmount float64, currency string) (rate float64, err error)

// RampHubRateProvider wraps RampHub's GetBestQuote to satisfy CurrencyRateProvider.
// Unlike the DB-backed repository, this fetches the live interbank rate on every
// call — no stale data, no background worker needed.
type RampHubRateProvider struct {
	getQuote BestQuoteFunc
}

func NewRampHubRateProvider(getQuote BestQuoteFunc) *RampHubRateProvider {
	return &RampHubRateProvider{getQuote: getQuote}
}

func (r *RampHubRateProvider) GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error) {
	if r.getQuote == nil {
		return decimal.Zero, fmt.Errorf("ramp hub rate provider not configured")
	}

	from = strings.ToUpper(from)
	to = strings.ToUpper(to)

	if from == "USD" && to == "NGN" {
		rate, err := r.getQuote(ctx, "onramp", 10000, 0, to)
		if err != nil {
			return decimal.Zero, fmt.Errorf("ramp hub rate: %w", err)
		}
		if rate <= 0 {
			return decimal.Zero, fmt.Errorf("ramp hub returned zero rate")
		}
		return decimal.NewFromFloat(rate), nil
	}

	return decimal.Zero, fmt.Errorf("unsupported currency pair: %s/%s", from, to)
}
