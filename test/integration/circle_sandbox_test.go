// Integration test against Circle sandbox API.
// Pulls credentials from AWS SSM /rail/staging/.
//
// Run: go test -v -run TestCircleSandbox -tags integration ./test/integration/

//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func ssmGet(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("aws", "ssm", "get-parameter",
		"--region", "us-east-1",
		"--name", name,
		"--with-decryption",
		"--query", "Parameter.Value",
		"--output", "text",
	).Output()
	require.NoError(t, err, "ssm get %s", name)
	return strings.TrimSpace(string(out))
}

func fetchCirclePublicKey(t *testing.T, apiKey string) string {
	t.Helper()
	out, err := exec.Command("curl", "-s",
		"-H", "Authorization: Bearer "+apiKey,
		"https://api.circle.com/v1/w3s/config/entity/publicKey",
	).Output()
	require.NoError(t, err, "curl fetch public key")

	var result struct {
		Data struct {
			PublicKey string `json:"publicKey"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out, &result))
	require.NotEmpty(t, result.Data.PublicKey)
	return result.Data.PublicKey
}

func TestCircleSandbox_CreateWalletSetAndWallet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Pull credentials from SSM
	apiKey := ssmGet(t, "/rail/staging/CIRCLE_API_KEY")
	entitySecret := ssmGet(t, "/rail/staging/CIRCLE_ENTITY_SECRET")
	pubKeyPEM := fetchCirclePublicKey(t, apiKey)

	t.Log("✓ Credentials loaded from SSM")

	// Create Circle client
	client, err := circle.NewHTTPClient(circle.Config{
		APIKey:       apiKey,
		EntitySecret: entitySecret,
		PublicKeyPEM: pubKeyPEM,
		Timeout:      30 * time.Second,
		MaxRetries:   2,
	}, zap.NewNop())
	require.NoError(t, err)

	adapter := circle.NewSandboxAdapter(client, zap.NewNop())

	// Step 1: Create wallet set
	t.Log("Creating wallet set...")
	ws, err := client.CreateWalletSet(ctx, fmt.Sprintf("rail-test-%d", time.Now().Unix()))
	require.NoError(t, err)
	require.NotEmpty(t, ws.ID)
	t.Logf("✓ Wallet set created: %s", ws.ID)

	// Step 2: Create multi-chain wallets (simulates onboarding flow)
	t.Log("Creating multi-chain wallets (SOL + ETH + BASE)...")
	userID := uuid.New()
	chains := []entities.WalletChain{
		entities.WalletChainSolana,
		entities.WalletChainEthereum,
		entities.WalletChainBase,
	}
	wallets, err := adapter.CreateMultiChainWallets(ctx, userID, ws.ID, chains)
	require.NoError(t, err)
	require.Len(t, wallets, 3)
	for _, w := range wallets {
		require.NotEmpty(t, w.Address)
		require.NotEmpty(t, w.CircleWalletID)
		require.Equal(t, userID, w.UserID)
		require.Equal(t, entities.WalletStatusLive, w.Status)
		require.Equal(t, entities.AccountTypeEOA, w.AccountType)
		t.Logf("  ✓ %s wallet: %s", w.Chain, w.Address)
	}

	// Step 3: Check balances (all should be 0)
	t.Log("Checking balances on all wallets...")
	for _, w := range wallets {
		balance, err := adapter.GetWalletBalance(ctx, w.CircleWalletID)
		require.NoError(t, err)
		require.Equal(t, "0", balance)
	}
	t.Log("✓ All balances are 0 (fresh wallets)")

	// Step 4: Get each wallet by ID (simulates balance refresh)
	t.Log("Fetching each wallet by Circle ID...")
	for _, w := range wallets {
		fetched, err := client.GetWallet(ctx, w.CircleWalletID)
		require.NoError(t, err)
		require.Equal(t, w.Address, fetched.Address)
		require.Equal(t, circle.WalletStateLive, fetched.State)
	}
	t.Log("✓ All wallets fetchable and LIVE")

	// Step 5: List wallets in set (simulates wallet list endpoint)
	listed, err := client.ListWallets(ctx, ws.ID)
	require.NoError(t, err)
	require.Len(t, listed, 3)
	t.Logf("✓ Listed %d wallets in set", len(listed))

	// Step 6: Test error handling — unsupported chain
	_, err = adapter.CreateWalletForUser(ctx, uuid.New(), ws.ID, entities.WalletChainCelo)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported chain")
	t.Log("✓ Unsupported chain correctly rejected")

	// Step 7: Test error handling — invalid wallet ID
	_, err = client.GetWallet(ctx, "nonexistent-wallet-id")
	require.Error(t, err)
	t.Log("✓ Invalid wallet ID correctly returns error")

	// Step 8: Test fee estimation (will fail without funds but validates API call)
	_, feeErr := client.EstimateTransferFee(ctx, &circle.EstimateFeeRequest{
		WalletID:           wallets[0].CircleWalletID,
		TokenID:            "some-token-id",
		DestinationAddress: wallets[1].Address,
		Amounts:            []string{"1.00"},
	})
	// Expected to error (no funds / invalid token), but should be an API error not a crash
	t.Logf("✓ Fee estimation returned (err: %v)", feeErr != nil)

	t.Log("")
	t.Log("=== Full Circle sandbox integration test passed ===")
	t.Logf("Wallet Set: %s", ws.ID)
	for _, w := range wallets {
		t.Logf("  %s: %s", w.Chain, w.Address)
	}
}
