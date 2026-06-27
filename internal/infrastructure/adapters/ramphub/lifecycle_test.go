package ramphub

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSandboxLifecycle drives real sandbox transactions end-to-end at the client
// level: create buy + sell orders, poll monitor-status to a terminal state,
// exercise the active payment-window intent + conflict/override, and error paths.
// Skipped unless RAMPHUB_API_KEY is set.
//
//	RAMPHUB_API_KEY=rh_test_... go test ./internal/infrastructure/adapters/ramphub/ -run TestSandboxLifecycle -v -timeout 8m
func TestSandboxLifecycle(t *testing.T) {
	apiKey := os.Getenv("RAMPHUB_API_KEY")
	if apiKey == "" {
		t.Skip("RAMPHUB_API_KEY not set; skipping live sandbox lifecycle test")
	}
	client, err := NewClient(Config{APIKey: apiKey, BaseURL: os.Getenv("RAMPHUB_BASE_URL")}, zap.NewNop())
	require.NoError(t, err, "failed to create ramphub client")
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	cust := fmt.Sprintf("rail-lc-%d", time.Now().Unix())
	wallet := "6dNVKr8e6Xy2bWv7iZ1pDcaKf5o2pVQ8YtUDf2L3aaa"

	// --- 1. Buy order ---
	buy, err := client.CreateOrder(ctx, OrderRequest{
		Side: "buy", Amount: 7.05, FiatAmount: 10000, FiatCurrency: "NGN",
		Asset: "USDC", Chain: "solana", WalletAddress: wallet, ExternalCustomerID: cust,
	})
	if err != nil {
		t.Fatalf("create buy: %v", err)
	}
	t.Logf("BUY created: tx=%s provider=%s rate=%.2f status=%s",
		buy.TransactionID, buy.SelectedProvider, buy.BestRateUsed, buy.ProviderDetails.Status)
	if buy.ProviderDetails.VirtualAccount != nil {
		va := buy.ProviderDetails.VirtualAccount
		t.Logf("  pay-in account: %s / %s / %s  amountToPay=%.2f",
			va.AccountName, va.AccountNumber, va.BankName, buy.ProviderDetails.AmountToPay)
	} else {
		t.Errorf("BUY missing virtualAccount in providerDetails")
	}

	// --- 2. Order intent (active payment window) ---
	intent, err := client.GetOrderIntent(ctx, cust, "USDC", "solana")
	if err != nil {
		t.Logf("GetOrderIntent error (may be expected if window not exposed): %v", err)
	} else {
		t.Logf("INTENT: tx=%s status=%s depositAddress=%q expiresAt=%s",
			intent.TransactionID, intent.Status, intent.DepositAddress, intent.ExpiresAt)
	}

	// --- 3. Active-intent conflict + override ---
	_, confErr := client.CreateOrder(ctx, OrderRequest{
		Side: "buy", Amount: 7.05, FiatAmount: 10000, FiatCurrency: "NGN",
		Asset: "USDC", Chain: "solana", WalletAddress: wallet, ExternalCustomerID: cust,
	})
	if confErr == nil {
		t.Logf("second buy succeeded without override (sandbox may not enforce intent here)")
	} else if IsActiveIntentConflict(confErr) {
		t.Logf("CONFLICT detected as expected: %v", confErr)
		ovr, ovrErr := client.CreateOrder(ctx, OrderRequest{
			Side: "buy", Amount: 7.05, FiatAmount: 10000, FiatCurrency: "NGN",
			Asset: "USDC", Chain: "solana", WalletAddress: wallet, ExternalCustomerID: cust,
			OverrideActiveIntent: true,
		})
		if ovrErr != nil {
			t.Errorf("override retry failed: %v", ovrErr)
		} else {
			t.Logf("OVERRIDE succeeded: tx=%s", ovr.TransactionID)
		}
	} else {
		t.Logf("second buy failed with non-conflict error: %v", confErr)
	}

	// --- 4. Poll buy to terminal ---
	final := pollUntilTerminal(ctx, t, client, buy.TransactionID, 3*time.Minute)
	t.Logf("BUY terminal: status=%s completed=%v terminal=%v -> mapped=%s",
		final.Status, final.Completed, final.Terminal, final.MappedStatus())

	// --- 5. Sell order (separate customer to avoid intent coupling) ---
	sellCust := cust + "-sell"
	sell, err := client.CreateOrder(ctx, OrderRequest{
		Side: "sell", Amount: 5, FiatCurrency: "NGN", Asset: "USDC", Chain: "solana",
		BankCode: "100004", AccountNumber: "8050201494", AccountName: "Test User", ExternalCustomerID: sellCust,
	})
	if err != nil {
		t.Fatalf("create sell: %v", err)
	}
	t.Logf("SELL created: tx=%s provider=%s ourCryptoAddress=%q amountToSend=%.4f",
		sell.TransactionID, sell.SelectedProvider, sell.OurCryptoAddress, sell.ProviderDetails.AmountToSend)
	if sell.OurCryptoAddress == "" {
		t.Errorf("SELL missing ourCryptoAddress")
	}
	sellFinal := pollUntilTerminal(ctx, t, client, sell.TransactionID, 3*time.Minute)
	t.Logf("SELL terminal: status=%s completed=%v terminal=%v -> mapped=%s",
		sellFinal.Status, sellFinal.Completed, sellFinal.Terminal, sellFinal.MappedStatus())

	// --- 6. Error path: invalid bank resolve ---
	if _, rErr := client.ResolveBankAccount(ctx, "000000", "0000000000"); rErr != nil {
		t.Logf("invalid bank resolve rejected as expected: %v", rErr)
	} else {
		t.Logf("invalid bank resolve unexpectedly succeeded")
	}
}

// pollUntilTerminal polls monitor-status every 5s until terminal/completed or
// the deadline, logging each transition.
func pollUntilTerminal(ctx context.Context, t *testing.T, client *Client, txID string, max time.Duration) *Transaction {
	t.Helper()
	deadline := time.Now().Add(max)
	var last string
	var tx *Transaction
	for time.Now().Before(deadline) {
		var err error
		tx, err = client.MonitorStatus(ctx, txID)
		if err != nil {
			t.Logf("  monitor-status error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if tx.Status != last {
			t.Logf("  [%s] status=%s terminal=%v completed=%v", time.Now().Format("15:04:05"), tx.Status, tx.Terminal, tx.Completed)
			last = tx.Status
		}
		if tx.Terminal || tx.Completed {
			return tx
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("  poll timed out after %s (last status=%s)", max, last)
	return nil // unreachable; t.Fatalf stops the test
}
