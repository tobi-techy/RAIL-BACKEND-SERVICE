package unit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rail-service/rail_service/internal/adapters/due"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestClient(handler http.HandlerFunc) (*due.Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	config := due.Config{
		APIKey:    "test_key",
		AccountID: "test_account",
		BaseURL:   server.URL,
		Timeout:   5 * time.Second,
	}
	log := logger.NewLogger("test", "debug")
	client := due.NewClient(config, log)
	return client, server
}

func TestCreateAccount(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/accounts", r.URL.Path)
		assert.Equal(t, "Bearer test_key", r.Header.Get("Authorization"))
		
		resp := due.CreateAccountResponse{
			ID:      "acc_123",
			Type:    due.AccountTypeIndividual,
			Name:    "Test User",
			Email:   "test@example.com",
			Country: "US",
			Status:  "active",
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	req := &due.CreateAccountRequest{
		Type:    due.AccountTypeIndividual,
		Name:    "Test User",
		Email:   "test@example.com",
		Country: "US",
	}
	
	resp, err := client.CreateAccount(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "acc_123", resp.ID)
	assert.Equal(t, "Test User", resp.Name)
}

func TestGetWalletBalance(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/wallets/wallet_123/balance", r.URL.Path)
		
		resp := due.WalletBalanceResponse{
			Balances: []due.Balance{
				{Currency: "USDC", Amount: "1000.50", Network: "ethereum"},
				{Currency: "USD", Amount: "500.00"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	resp, err := client.GetWalletBalance(context.Background(), "wallet_123")
	require.NoError(t, err)
	assert.Len(t, resp.Balances, 2)
	assert.Equal(t, "USDC", resp.Balances[0].Currency)
	assert.Equal(t, "1000.50", resp.Balances[0].Amount)
}

func TestCreateTransferIntent(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfers/transfer_123/transfer_intent", r.URL.Path)
		
		resp := due.TransferIntentResponse{
			ID:        "intent_123",
			Status:    "pending",
			CreatedAt: time.Now(),
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	req := &due.TransferIntentRequest{
		Signature: "sig_123",
		PublicKey: "pub_123",
	}
	
	resp, err := client.CreateTransferIntent(context.Background(), "transfer_123", req)
	require.NoError(t, err)
	assert.Equal(t, "intent_123", resp.ID)
	assert.Equal(t, "pending", resp.Status)
}

func TestListVirtualAccountsValidation(t *testing.T) {
	client, server := setupTestClient(nil)
	defer server.Close()
	
	// Test nil filters
	_, err := client.ListVirtualAccounts(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "filters are required")
	
	// Test missing required fields
	_, err = client.ListVirtualAccounts(context.Background(), &due.VirtualAccountFilters{
		CurrencyIn: "USD",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestErrorResponseParsing(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := due.ErrorResponse{
			StatusCode: 400,
			Message:    "Invalid currency",
			Code:       "INVALID_CURRENCY",
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	req := &due.CreateAccountRequest{
		Type:    due.AccountTypeIndividual,
		Name:    "Test",
		Email:   "test@example.com",
		Country: "US",
	}
	
	_, err := client.CreateAccount(context.Background(), req)
	require.Error(t, err)
	
	dueErr, ok := err.(*due.ErrorResponse)
	require.True(t, ok)
	assert.Equal(t, 400, dueErr.StatusCode)
	assert.Equal(t, "Invalid currency", dueErr.Message)
	assert.Equal(t, "INVALID_CURRENCY", dueErr.Code)
}

func TestCreateVault(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/vaults", r.URL.Path)
		
		resp := due.VaultResponse{
			ID:        "vault_123",
			Address:   "0x1234567890abcdef",
			Network:   "ethereum",
			CreatedAt: time.Now(),
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	req := &due.CreateVaultRequest{
		CredentialID: "cred_123",
		Network:      "ethereum",
	}
	
	resp, err := client.CreateVault(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "vault_123", resp.ID)
	assert.Equal(t, "0x1234567890abcdef", resp.Address)
}

func TestGetFXMarkets(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/fx/markets", r.URL.Path)
		
		resp := due.FXMarketsResponse{
			Markets: []due.FXMarket{
				{From: "USD", To: "USDC", Rate: 1.0},
				{From: "EUR", To: "EURC", Rate: 1.0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}
	
	client, server := setupTestClient(handler)
	defer server.Close()
	
	resp, err := client.GetFXMarkets(context.Background())
	require.NoError(t, err)
	assert.Len(t, resp.Markets, 2)
	assert.Equal(t, "USD", resp.Markets[0].From)
}
