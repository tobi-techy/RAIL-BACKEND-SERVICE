package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	config := Config{
		APIKey:      "test-api-key",
		BaseURL:     server.URL,
		Environment:  "sandbox",
		Timeout:      5 * time.Second,
		MaxRetries:   1,
	}
	logger := logger.NewLogger(nil, "test")
	client := NewClient(config, logger)
	return client, server
}

func setupTestAdapter(handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	client, server := setupTestClient(handler)
	adapter := NewAdapter(client, logger.NewLogger(nil, "test"))
	return adapter, server
}

func TestAdapter_HealthCheck(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/ping", r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	ctx := context.Background()
	err := adapter.HealthCheck(ctx)
	assert.NoError(t, err)
}

func TestAdapter_CreateCustomerWithWallet(t *testing.T) {
	customerID := "cust_123456789"
	walletID := "wal_123456789"
	walletAddress := "0x1234567890123456789012345678901234567890"

	handler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/customers":
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			
			var req CreateCustomerRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			
			assert.Equal(t, "John", req.FirstName)
			assert.Equal(t, "Doe", req.LastName)
			assert.Equal(t, "john@example.com", req.Email)
			assert.Equal(t, "+1234567890", req.Phone)
			assert.Equal(t, CustomerTypeIndividual, req.Type)
			
			customer := &Customer{
				ID:        customerID,
				FirstName: req.FirstName,
				LastName:  req.LastName,
				Email:     req.Email,
				Phone:     req.Phone,
				Status:    CustomerStatusActive,
				Type:      CustomerTypeIndividual,
				CreatedAt: time.Now(),
			}
			
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(customer)
			
		case "/customers/" + customerID + "/wallets":
			assert.Equal(t, http.MethodPost, r.Method)
			
			var req CreateWalletRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)
			
			assert.Equal(t, PaymentRailEthereum, req.Chain)
			assert.Equal(t, CurrencyUSDC, req.Currency)
			assert.Equal(t, WalletTypeUser, req.WalletType)
			
			wallet := &Wallet{
				ID:       walletID,
				CustomerID: customerID,
				Chain:    PaymentRailEthereum,
				Currency: CurrencyUSDC,
				Address:  walletAddress,
				WalletType: WalletTypeUser,
				Status:   "active",
				CreatedAt: time.Now(),
			}
			
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(wallet)
		}
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	req := &CreateCustomerWithWalletRequest{
		FirstName: "John",
		LastName:  "Doe",
		Email:     "john@example.com",
		Phone:     "+1234567890",
		Chain:     PaymentRailEthereum,
	}

	ctx := context.Background()
	response, err := adapter.CreateCustomerWithWallet(ctx, req)
	require.NoError(t, err)
	
	assert.Equal(t, customerID, response.Customer.ID)
	assert.Equal(t, "John", response.Customer.FirstName)
	assert.Equal(t, "Doe", response.Customer.LastName)
	assert.Equal(t, "john@example.com", response.Customer.Email)
	assert.Equal(t, "+1234567890", response.Customer.Phone)
	assert.Equal(t, CustomerStatusActive, response.Customer.Status)
	
	assert.Equal(t, walletID, response.Wallet.ID)
	assert.Equal(t, customerID, response.Wallet.CustomerID)
	assert.Equal(t, PaymentRailEthereum, response.Wallet.Chain)
	assert.Equal(t, CurrencyUSDC, response.Wallet.Currency)
	assert.Equal(t, walletAddress, response.Wallet.Address)
	assert.Equal(t, WalletTypeUser, response.Wallet.WalletType)
}

func TestAdapter_GetKYCLinkForCustomer(t *testing.T) {
	customerID := "cust_123456789"
	kycURL := "https://kyc.bridge.xyz/verify?token=abc123"

	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/customers/"+customerID+"/kyc", r.URL.Path)
		
		kycLink := &KYCLinkResponse{
			URL:      kycURL,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(kycLink)
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	ctx := context.Background()
	response, err := adapter.GetKYCLinkForCustomer(ctx, customerID)
	require.NoError(t, err)
	
	assert.Equal(t, kycURL, response.URL)
	assert.NotNil(t, response.ExpiresAt)
}

func TestAdapter_GetWalletBalance(t *testing.T) {
	customerID := "cust_123456789"
	walletID := "wal_123456789"
	amount := "100.50"

	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/customers/"+customerID+"/wallets/"+walletID+"/balance", r.URL.Path)
		
		balance := &WalletBalance{
			Amount:   amount,
			Currency: CurrencyUSDC,
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balance)
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	ctx := context.Background()
	response, err := adapter.GetWalletBalance(ctx, customerID, walletID)
	require.NoError(t, err)
	
	assert.Equal(t, amount, response.Amount)
	assert.Equal(t, CurrencyUSDC, response.Currency)
}

func TestAdapter_CreateVirtualAccountForCustomer(t *testing.T) {
	customerID := "cust_123456789"
	accountID := "va_123456789"

	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/customers/"+customerID+"/virtual-accounts", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		
		var req CreateVirtualAccountRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		
		virtualAccount := &VirtualAccount{
			ID:       accountID,
			CustomerID: customerID,
			Status:   VirtualAccountStatusActivated,
			CreatedAt: time.Now(),
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(virtualAccount)
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	req := &CreateVirtualAccountRequest{}
	ctx := context.Background()
	response, err := adapter.CreateVirtualAccountForCustomer(ctx, customerID, req)
	require.NoError(t, err)
	
	assert.Equal(t, accountID, response.ID)
	assert.Equal(t, customerID, response.CustomerID)
	assert.Equal(t, VirtualAccountStatusActivated, response.Status)
}

func TestAdapter_TransferFunds(t *testing.T) {
	transferID := "transfer_123456789"

	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/transfers", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		
		var req CreateTransferRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		
		assert.Equal(t, "wallet_source_123", req.SourceWalletID)
		assert.Equal(t, "wallet_dest_123", req.DestinationWalletID)
		assert.Equal(t, "10.50", req.Amount)
		assert.Equal(t, CurrencyUSDC, req.Currency)
		
		transfer := &Transfer{
			ID:                 transferID,
			SourceWalletID:     req.SourceWalletID,
			DestinationWalletID: req.DestinationWalletID,
			Amount:             req.Amount,
			Currency:           req.Currency,
			Status:             TransferStatusPending,
			CreatedAt:          time.Now(),
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(transfer)
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	req := &CreateTransferRequest{
		SourceWalletID:     "wallet_source_123",
		DestinationWalletID: "wallet_dest_123",
		Amount:             "10.50",
		Currency:           CurrencyUSDC,
	}

	ctx := context.Background()
	response, err := adapter.TransferFunds(ctx, req)
	require.NoError(t, err)
	
	assert.Equal(t, transferID, response.ID)
	assert.Equal(t, "wallet_source_123", response.SourceWalletID)
	assert.Equal(t, "wallet_dest_123", response.DestinationWalletID)
	assert.Equal(t, "10.50", response.Amount)
	assert.Equal(t, CurrencyUSDC, response.Currency)
	assert.Equal(t, TransferStatusPending, response.Status)
}

func TestAdapter_GetCustomerStatus(t *testing.T) {
	customerID := "cust_123456789"

	handler := func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/customers/"+customerID, r.URL.Path)
		
		customer := &Customer{
			ID:     customerID,
			Status: CustomerStatusUnderReview,
			Type:   CustomerTypeIndividual,
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(customer)
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	ctx := context.Background()
	response, err := adapter.GetCustomerStatus(ctx, customerID)
	require.NoError(t, err)
	
	assert.Equal(t, customerID, response.CustomerID)
	assert.Equal(t, CustomerStatusUnderReview, response.BridgeStatus)
	assert.Equal(t, "processing", string(response.KYCStatus)) // CustomerStatusUnderReview -> KYCProcessing
	assert.Equal(t, "kyc_pending", string(response.OnboardingStatus)) // CustomerStatusUnderReview -> OnboardingStatusKYCPending
}

func TestAdapter_HealthCheck_Failure(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "service unavailable"}`))
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	ctx := context.Background()
	err := adapter.HealthCheck(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bridge health check failed")
}

func TestAdapter_CreateCustomerWithWallet_Error(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid email"}`))
	}

	adapter, server := setupTestAdapter(handler)
	defer server.Close()

	req := &CreateCustomerWithWalletRequest{
		FirstName: "Invalid",
		Email:     "invalid-email",
	}

	ctx := context.Background()
	response, err := adapter.CreateCustomerWithWallet(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "create customer failed")
}