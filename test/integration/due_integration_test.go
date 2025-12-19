//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIntegrationClient(t *testing.T) *due.Client {
	apiKey := os.Getenv("DUE_API_KEY")
	accountID := os.Getenv("DUE_ACCOUNT_ID")
	baseURL := os.Getenv("DUE_BASE_URL")
	
	if apiKey == "" || accountID == "" {
		t.Skip("DUE_API_KEY and DUE_ACCOUNT_ID required for integration tests")
	}
	
	if baseURL == "" {
		baseURL = "https://api.sandbox.due.network"
	}
	
	config := due.Config{
		APIKey:    apiKey,
		AccountID: accountID,
		BaseURL:   baseURL,
		Timeout:   30 * time.Second,
	}
	
	log := logger.New("debug", "integration-test")
	return due.NewClient(config, log)
}

func TestIntegration_AccountFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Create account
	req := &due.CreateAccountRequest{
		Type:    due.AccountTypeIndividual,
		Name:    "Integration Test User",
		Email:   "integration-test@example.com",
		Country: "US",
	}
	
	account, err := client.CreateAccount(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, account.ID)
	assert.Equal(t, "Integration Test User", account.Name)
	
	// Get account
	retrieved, err := client.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	assert.Equal(t, account.ID, retrieved.ID)
}

func TestIntegration_WalletFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Link wallet
	req := &due.LinkWalletRequest{
		Address: "evm:0x1234567890123456789012345678901234567890",
	}
	
	wallet, err := client.LinkWallet(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, wallet.ID)
	
	// Get wallet balance
	balance, err := client.GetWalletBalance(ctx, wallet.ID)
	require.NoError(t, err)
	assert.NotNil(t, balance)
}

func TestIntegration_RecipientFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Create recipient
	req := &due.CreateRecipientRequest{
		Name: "Test Recipient",
		Details: due.RecipientDetails{
			Schema:  "evm",
			Address: "0x1234567890123456789012345678901234567890",
		},
		IsExternal: false,
	}
	
	recipient, err := client.CreateRecipient(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, recipient.ID)
	
	// List recipients
	list, err := client.ListRecipients(ctx, 10, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, list.Data)
	
	// Get recipient
	retrieved, err := client.GetRecipient(ctx, recipient.ID)
	require.NoError(t, err)
	assert.Equal(t, recipient.ID, retrieved.ID)
}

func TestIntegration_VirtualAccountFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Create recipient first
	recipientReq := &due.CreateRecipientRequest{
		Name: "VA Test Recipient",
		Details: due.RecipientDetails{
			Schema:  "evm",
			Address: "0x1234567890123456789012345678901234567890",
		},
		IsExternal: false,
	}
	
	recipient, err := client.CreateRecipient(ctx, recipientReq)
	require.NoError(t, err)
	
	// Create virtual account
	vaReq := &due.CreateVirtualAccountRequest{
		Destination: recipient.ID,
		SchemaIn:    "bank_us",
		CurrencyIn:  "USD",
		RailOut:     "ethereum",
		CurrencyOut: "USDC",
		Reference:   "test_va_001",
	}
	
	va, err := client.CreateVirtualAccount(ctx, vaReq)
	require.NoError(t, err)
	assert.NotEmpty(t, va.Nonce)
	assert.True(t, va.IsActive)
}

func TestIntegration_ChannelsAndQuote(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Get channels
	channels, err := client.GetChannels(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, channels.Channels)
}

func TestIntegration_FXMarkets(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Get FX markets
	markets, err := client.GetFXMarkets(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, markets.Markets)
	
	// Create FX quote
	quoteReq := &due.FXQuoteRequest{
		From:   "USD",
		To:     "USDC",
		Amount: "100",
	}
	
	quote, err := client.CreateFXQuote(ctx, quoteReq)
	require.NoError(t, err)
	assert.NotEmpty(t, quote.Total)
}

func TestIntegration_VaultFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Initialize credentials
	initReq := &due.InitCredentialsRequest{
		Email: "vault-test@example.com",
	}
	
	initResp, err := client.InitializeVaultCredentials(ctx, initReq)
	require.NoError(t, err)
	assert.NotEmpty(t, initResp.ChallengeID)
	
	// Note: Full vault flow requires WebAuthn attestation
	// which can't be automated in tests
}

func TestIntegration_WebhookFlow(t *testing.T) {
	client := setupIntegrationClient(t)
	ctx := context.Background()
	
	// Create webhook endpoint
	req := &due.CreateWebhookRequest{
		URL:         "https://example.com/webhooks/due",
		Events:      []string{"transfer.completed", "transfer.failed"},
		Description: "Integration test webhook",
	}
	
	webhook, err := client.CreateWebhookEndpoint(ctx, req)
	require.NoError(t, err)
	assert.NotEmpty(t, webhook.ID)
	assert.NotEmpty(t, webhook.Secret)
	
	// List webhooks
	list, err := client.ListWebhookEndpoints(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, list.Data)
	
	// Delete webhook
	err = client.DeleteWebhookEndpoint(ctx, webhook.ID)
	require.NoError(t, err)
}
