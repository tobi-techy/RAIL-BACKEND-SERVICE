//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupIntegrationClient(t *testing.T) *bridge.Client {
	apiKey := getEnvOrSkip(t, "BRIDGE_API_KEY")
	baseURL := getEnvOrSkip(t, "BRIDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.bridge.xyz"
	}

	config := bridge.Config{
		APIKey:      apiKey,
		BaseURL:     baseURL,
		Environment: "sandbox",
		Timeout:     30 * time.Second,
		MaxRetries:  3,
	}

	zapLogger, _ := zap.NewDevelopment()

	return bridge.NewClient(config, zapLogger)
}

func TestBridgeIntegration_FullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupIntegrationClient(t)
	zapLogger := zap.NewNop()
	adapter := bridge.NewAdapter(client, zapLogger)
	ctx := context.Background()

	t.Run("Health Check", func(t *testing.T) {
		err := adapter.HealthCheck(ctx)
		require.NoError(t, err)
	})

	// Create a test customer with wallet
	customer, wallet, err := adapter.CreateCustomerWithWallet(ctx, &bridge.CreateCustomerWithWalletRequest{
		FirstName: "Test",
		LastName:  "User",
		Email:     generateTestEmail(),
		Phone:     "+12345678900",
		Chain:     bridge.PaymentRailSolana,
	})
	require.NoError(t, err)
	require.NotNil(t, customer)
	require.NotNil(t, wallet)

	t.Logf("Created customer: %s", customer.ID)
	t.Logf("Created wallet: %s", wallet.ID)

	// Test customer status
	status, err := adapter.GetCustomerStatus(ctx, customer.ID)
	require.NoError(t, err)
	require.NotNil(t, status)

	t.Logf("Customer status: %s", status.BridgeStatus)

	// Test KYC link generation
	kycLink, err := adapter.GetKYCLinkForCustomer(ctx, customer.ID)
	require.NoError(t, err)
	require.NotEmpty(t, kycLink.URL)

	t.Logf("KYC link: %s", kycLink.URL)

	// Test wallet balance
	balance, err := adapter.GetWalletBalance(ctx, customer.ID, wallet.ID)
	require.NoError(t, err)
	require.NotNil(t, balance)

	t.Logf("Wallet balance: %s %s", balance.Amount, balance.Currency)

	// Test virtual account creation
	virtualAccount, err := adapter.CreateVirtualAccountForCustomer(ctx, customer.ID, &bridge.CreateVirtualAccountRequest{})
	require.NoError(t, err)
	require.NotNil(t, virtualAccount)

	t.Logf("Virtual account: %s", virtualAccount.ID)
}

func TestBridgeIntegration_CustomerManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupIntegrationClient(t)
	ctx := context.Background()

	// Create customer
	customerReq := &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: "Integration",
		LastName:  "Test",
		Email:     generateTestEmail(),
		Phone:     "+12345678901",
	}

	customer, err := client.CreateCustomer(ctx, customerReq)
	require.NoError(t, err)
	require.NotNil(t, customer)

	t.Logf("Created customer: %s", customer.ID)

	// Get customer details
	retrievedCustomer, err := client.GetCustomer(ctx, customer.ID)
	require.NoError(t, err)
	require.NotNil(t, retrievedCustomer)
	require.Equal(t, customer.ID, retrievedCustomer.ID)
	require.Equal(t, customer.Email, retrievedCustomer.Email)

	// Update customer
	updateReq := &bridge.CreateCustomerRequest{
		FirstName: "Updated",
		LastName:  "Name",
	}

	updatedCustomer, err := client.UpdateCustomer(ctx, customer.ID, updateReq)
	require.NoError(t, err)
	require.NotNil(t, updatedCustomer)
	require.Equal(t, "Updated", updatedCustomer.FirstName)
	require.Equal(t, "Name", updatedCustomer.LastName)

	// List customers
	customers, err := client.ListCustomers(ctx, "", 10)
	require.NoError(t, err)
	require.NotNil(t, customers)
	require.True(t, len(customers.Customers) > 0)
}

func TestBridgeIntegration_WalletOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupIntegrationClient(t)
	ctx := context.Background()

	// First create a customer
	customer, err := client.CreateCustomer(ctx, &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: "Wallet",
		LastName:  "Test",
		Email:     generateTestEmail(),
		Phone:     "+12345678902",
	})
	require.NoError(t, err)

	// Create wallets for supported chain(s)
	testChains := []bridge.PaymentRail{
		bridge.PaymentRailSolana,
	}

	for _, chain := range testChains {
		walletReq := &bridge.CreateWalletRequest{
			Chain:      chain,
			Currency:   bridge.CurrencyUSDC,
			WalletType: bridge.WalletTypeUser,
		}

		wallet, err := client.CreateWallet(ctx, customer.ID, walletReq)
		require.NoError(t, err)
		require.NotNil(t, wallet)

		t.Logf("Created wallet for %s: %s", chain, wallet.ID)

		// Get wallet balance
		balance, err := client.GetWalletBalance(ctx, customer.ID, wallet.ID)
		require.NoError(t, err)
		require.NotNil(t, balance)

		t.Logf("Wallet balance for %s: %s %s", chain, balance.Amount, balance.Currency)
	}

	// List wallets
	wallets, err := client.ListWallets(ctx, customer.ID)
	require.NoError(t, err)
	require.NotNil(t, wallets)
	require.True(t, len(wallets.Wallets) >= len(testChains))
}

func TestBridgeIntegration_TransferOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupIntegrationClient(t)
	ctx := context.Background()

	// Create a customer
	customer, err := client.CreateCustomer(ctx, &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: "Transfer",
		LastName:  "Test",
		Email:     generateTestEmail(),
		Phone:     "+12345678903",
	})
	require.NoError(t, err)

	// Create first wallet
	wallet1, err := client.CreateWallet(ctx, customer.ID, &bridge.CreateWalletRequest{
		Chain:      bridge.PaymentRailSolana,
		Currency:   bridge.CurrencyUSDC,
		WalletType: bridge.WalletTypeUser,
	})
	require.NoError(t, err)

	// Create second wallet for transfer destination
	wallet2, err := client.CreateWallet(ctx, customer.ID, &bridge.CreateWalletRequest{
		Chain:      bridge.PaymentRailSolana,
		Currency:   bridge.CurrencyUSDC,
		WalletType: bridge.WalletTypeUser,
	})
	require.NoError(t, err)

	// Note: This test shows transfer creation but won't actually execute
	// since the wallets will have 0 balance in sandbox
	transferReq := &bridge.CreateTransferRequest{
		SourceWalletID:      wallet1.ID,
		DestinationWalletID: wallet2.ID,
		Amount:              "1.00", // Minimum amount
		Currency:            bridge.CurrencyUSDC,
	}

	transfer, err := client.CreateTransfer(ctx, transferReq)
	if err != nil {
		t.Logf("Transfer creation failed (expected for empty wallets): %v", err)
	} else {
		t.Logf("Created transfer: %s", transfer.ID)

		// Get transfer details
		retrievedTransfer, err := client.GetTransfer(ctx, transfer.ID)
		require.NoError(t, err)
		require.Equal(t, transfer.ID, retrievedTransfer.ID)

		// List transfers
		transfers, err := client.ListTransfers(ctx, customer.ID)
		require.NoError(t, err)
		require.True(t, len(transfers.Transfers) > 0)
	}
}

func TestBridgeIntegration_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := setupIntegrationClient(t)
	ctx := context.Background()

	// Test invalid customer creation
	invalidReq := &bridge.CreateCustomerRequest{
		Type:      bridge.CustomerTypeIndividual,
		FirstName: "Invalid",
		Email:     "invalid-email", // Invalid email
	}

	_, err := client.CreateCustomer(ctx, invalidReq)
	require.Error(t, err)

	// Test getting non-existent customer
	_, err = client.GetCustomer(ctx, "non_existent_customer")
	require.Error(t, err)

	// Test creating wallet for non-existent customer
	walletReq := &bridge.CreateWalletRequest{
		Chain:      bridge.PaymentRailSolana,
		Currency:   bridge.CurrencyUSDC,
		WalletType: bridge.WalletTypeUser,
	}

	_, err = client.CreateWallet(ctx, "non_existent_customer", walletReq)
	require.Error(t, err)
}
