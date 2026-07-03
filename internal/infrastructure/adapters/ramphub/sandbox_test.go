package ramphub

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSandboxLive exercises the real RampHub sandbox through our client. It is
// skipped unless RAMPHUB_API_KEY is set, so it never runs in normal CI.
//
// Run with:
//
//	RAMPHUB_API_KEY=sk_test_... \
//	  go test ./internal/infrastructure/adapters/ramphub/ -run TestSandboxLive -v
//
// Optional env:
//
//	RAMPHUB_BASE_URL       override base URL (defaults to https://api.ramphub.io)
//	RAMPHUB_RESOLVE_BANK   bank code to test bank-account resolution
//	RAMPHUB_RESOLVE_ACCT   account number to test bank-account resolution
func TestSandboxLive(t *testing.T) {
	apiKey := os.Getenv("RAMPHUB_API_KEY")
	if apiKey == "" {
		t.Skip("RAMPHUB_API_KEY not set; skipping live sandbox test")
	}

	client, err := NewClient(Config{
		APIKey: apiKey,
		// WebhookSecret is required by NewClient but unused by the REST calls this
		// test exercises; a placeholder keeps the sandbox smoke test runnable.
		WebhookSecret: "sandbox-test-not-used",
		BaseURL:       os.Getenv("RAMPHUB_BASE_URL"),
	}, zap.NewNop())
	require.NoError(t, err, "failed to create ramphub client")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("catalog", func(t *testing.T) {
		cat, err := client.GetCatalog(ctx)
		if err != nil {
			t.Fatalf("GetCatalog: %v", err)
		}
		t.Logf("providers: %d", len(cat.Providers))
		for _, p := range cat.Providers {
			t.Logf("  provider=%s buy=%v sell=%v", p.Name, p.SupportsBuy, p.SupportsSell)
		}
	})

	t.Run("provider_bank_list", func(t *testing.T) {
		list, err := client.GetProviderBankList(ctx, "usebread")
		if err != nil {
			t.Fatalf("GetProviderBankList: %v", err)
		}
		t.Logf("usebread banks: %d", len(list.Banks))
		if len(list.Banks) > 0 {
			t.Logf("sample bank: %s (%s)", list.Banks[0].BankName, list.Banks[0].BankCode)
		}
	})

	t.Run("onramp_quote", func(t *testing.T) {
		resp, err := client.GetQuote(ctx, QuoteRequest{
			Side:         "buy",
			FiatAmount:   10000,
			FiatCurrency: "NGN",
			Asset:        "USDC",
			Chain:        "solana",
		})
		if err != nil {
			t.Fatalf("onramp GetQuote: %v", err)
		}
		t.Logf("onramp bestQuote: provider=%s rate=%.4f estimatedOutput=%.6f (options=%d)",
			resp.BestQuote.Provider, resp.BestQuote.Rate, resp.BestQuote.EstimatedOutput, len(resp.Quotes))
		if resp.BestQuote.Rate <= 0 && resp.BestQuote.EstimatedOutput <= 0 {
			t.Fatalf("onramp quote returned empty bestQuote: %+v", resp)
		}
	})

	t.Run("offramp_quote", func(t *testing.T) {
		resp, err := client.GetQuote(ctx, QuoteRequest{
			Side:         "sell",
			TokenAmount:  5,
			FiatCurrency: "NGN",
			Asset:        "USDC",
			Chain:        "solana",
		})
		if err != nil {
			t.Fatalf("offramp GetQuote: %v", err)
		}
		t.Logf("offramp bestQuote: provider=%s rate=%.4f estimatedOutput=%.6f (options=%d)",
			resp.BestQuote.Provider, resp.BestQuote.Rate, resp.BestQuote.EstimatedOutput, len(resp.Quotes))
		if resp.BestQuote.Rate <= 0 && resp.BestQuote.EstimatedOutput <= 0 {
			t.Fatalf("offramp quote returned empty bestQuote: %+v", resp)
		}
	})

	t.Run("resolve_bank", func(t *testing.T) {
		bankCode := os.Getenv("RAMPHUB_RESOLVE_BANK")
		acct := os.Getenv("RAMPHUB_RESOLVE_ACCT")
		if bankCode == "" || acct == "" {
			t.Skip("RAMPHUB_RESOLVE_BANK / RAMPHUB_RESOLVE_ACCT not set; skipping resolve")
		}
		resolved, err := client.ResolveBankAccount(ctx, bankCode, acct, os.Getenv("RAMPHUB_RESOLVE_BANK_NAME"))
		if err != nil {
			t.Fatalf("ResolveBankAccount: %v", err)
		}
		t.Logf("resolved account name: %q (bank=%s)", resolved.AccountName, resolved.BankCode)
	})
}
