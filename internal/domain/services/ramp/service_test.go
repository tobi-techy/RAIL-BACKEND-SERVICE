package ramp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMapEventStatus(t *testing.T) {
	cases := map[[2]string]string{
		{"transaction.completed", ""}: "completed",
		{"transaction.failed", ""}:    "failed",
		{"", "Marked completed"}:      "completed",
		{"", "Marked failed"}:         "failed",
		{"", "Cancelled"}:             "failed",
		{"", "Awaiting settlement"}:   "processing",
		{"", "settling"}:              "processing",
		{"", "awaiting_deposit"}:      "processing",
		{"", "Forwarded to provider"}: "processing",
		{"", "Order placed"}:          "pending",
		{"", "something unknown"}:     "pending",
	}
	for in, want := range cases {
		if got := ramphub.MapEventStatus(in[0], in[1]); got != want {
			t.Errorf("MapEventStatus(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestTransactionMappedStatus(t *testing.T) {
	require.Equal(t, "completed", (&ramphub.Transaction{Completed: true, Status: "settling"}).MappedStatus())
	require.Equal(t, "failed", (&ramphub.Transaction{Terminal: true, Status: "whatever"}).MappedStatus())
	require.Equal(t, "processing", (&ramphub.Transaction{Status: "awaiting_settlement"}).MappedStatus())
	require.Equal(t, "pending", (&ramphub.Transaction{Status: "order placed"}).MappedStatus())
}

func TestCircleToRampChain(t *testing.T) {
	cases := map[string]string{
		"SOL":          "solana",
		"SOL-DEVNET":   "solana",
		"BASE":         "base",
		"BASE-SEPOLIA": "base",
		"ARB":          "arbitrum",
		"OP":           "optimism",
		"AVAX":         "avalanche",
		"MATIC-AMOY":   "polygon",
		"ETH":          "ethereum",
		"":             "solana",
	}
	for in, want := range cases {
		if got := circleToRampChain(in); got != want {
			t.Errorf("circleToRampChain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCalculateOfframpTransferAmounts(t *testing.T) {
	// Slippage buffer is refunded, rail fee stays charged.
	transfer, refund, err := calculateOfframpTransferAmounts(decimal.RequireFromString("0.34"), decimal.RequireFromString("0.37"), decimal.RequireFromString("0.02"))
	require.NoError(t, err)
	require.True(t, decimal.RequireFromString("0.34").Equal(transfer))
	require.True(t, decimal.RequireFromString("0.01").Equal(refund))

	// Transfer + fee exceeding the hold is rejected.
	_, _, err = calculateOfframpTransferAmounts(decimal.RequireFromString("0.34"), decimal.RequireFromString("0.35"), decimal.RequireFromString("0.02"))
	require.Error(t, err)
}

func TestCryptoSendAmount(t *testing.T) {
	require.Equal(t, 10.0, cryptoSendAmount(&ramphub.OrderResponse{ProviderDetails: ramphub.ProviderDetails{AmountToSend: 10}}, 16000, 1600))
	require.Equal(t, 10.0, cryptoSendAmount(&ramphub.OrderResponse{BestRateUsed: 1600}, 16000, 1500))
	require.Equal(t, 10.0, cryptoSendAmount(&ramphub.OrderResponse{}, 16000, 1600))
}

func TestNormalizeSide(t *testing.T) {
	require.Equal(t, "buy", normalizeSide("onramp"))
	require.Equal(t, "buy", normalizeSide("buy"))
	require.Equal(t, "sell", normalizeSide("offramp"))
	require.Equal(t, "sell", normalizeSide("sell"))
}

// TestGetBestQuoteReturnsRampHubQuote verifies the onramp quote path returns
// RampHub's best quote for the supported asset (USDC).
func TestGetBestQuoteReturnsRampHubQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ramphub.QuoteRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(ramphub.QuoteResponse{
			BestQuote: ramphub.QuoteOption{Provider: "p-" + req.Asset, Rate: 1600.0, EstimatedOutput: 6.25},
		})
	}))
	defer srv.Close()

	client := ramphub.NewClient(ramphub.Config{APIKey: "k", BaseURL: srv.URL}, zap.NewNop())
	s := NewService(nil, client, nil, nil, zap.NewNop())

	q, err := s.GetBestQuote(context.Background(), "onramp", 10000, 0, "NGN")
	require.NoError(t, err)
	require.Equal(t, "USDC", q.Asset)
	require.Equal(t, 6.25, q.EstimatedOutput)
	require.Equal(t, ProviderRampHub, q.Provider)
}

// TestGetBestQuoteErrorsWithoutProviders verifies that with no RampHub quote and
// no Paj fallback, GetBestQuote returns an error rather than a zero quote.
func TestGetBestQuoteErrorsWithoutProviders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := ramphub.NewClient(ramphub.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: 0}, zap.NewNop())
	s := NewService(nil, client, nil, nil, zap.NewNop())

	_, err := s.GetBestQuote(context.Background(), "offramp", 0, 5, "NGN")
	require.Error(t, err)
}
