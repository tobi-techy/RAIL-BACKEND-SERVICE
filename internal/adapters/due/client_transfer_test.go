package due

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestCreateQuote(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfers/quote", r.URL.Path)

		var req CreateQuoteRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "ethereum", req.Source.Rail)
		assert.Equal(t, "USDC", req.Source.Currency)
		assert.Equal(t, "1000.00", req.Source.Amount)
		assert.Equal(t, "ach", req.Destination.Rail)
		assert.Equal(t, "USD", req.Destination.Currency)

		response := CreateQuoteResponse{
			Token: "quote_token_123",
			Source: QuoteLeg{
				Rail:     "ethereum",
				Currency: "USDC",
				Amount:   "1000.00",
				Fee:      "0.00",
			},
			Destination: QuoteLeg{
				Rail:     "ach",
				Currency: "USD",
				Amount:   "997.50",
				Fee:      "2.50",
			},
			FXRate:    1.0,
			FXMarkup:  0,
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	req := CreateQuoteRequest{
		Source: QuoteSide{
			Rail:     "ethereum",
			Currency: "USDC",
			Amount:   "1000.00",
		},
		Destination: QuoteSide{
			Rail:     "ach",
			Currency: "USD",
			Amount:   "",
		},
	}

	response, err := client.CreateQuote(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "quote_token_123", response.Token)
	assert.Equal(t, "1000.00", response.Source.Amount)
	assert.Equal(t, "997.50", response.Destination.Amount)
}

func TestCreateTransfer(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfers", r.URL.Path)

		var req CreateTransferRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "quote_token_123", req.Quote)
		assert.Equal(t, "wallet_123", req.Sender)
		assert.Equal(t, "recipient_456", req.Recipient)

		response := CreateTransferResponse{
			ID:      "transfer_789",
			OwnerID: "account_123",
			Status:  "awaiting_funds",
			Source: TransferLeg{
				Amount:   "1000.00",
				Currency: "USDC",
				Rail:     "ethereum",
			},
			Destination: TransferLeg{
				Amount:   "997.50",
				Currency: "USD",
				Rail:     "ach",
			},
			FXRate:    1.0,
			FXMarkup:  0,
			TransferInstructions: TransferInstructions{
				Type: "TransferIntent",
			},
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(5 * time.Minute),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	req := CreateTransferRequest{
		Quote:     "quote_token_123",
		Sender:    "wallet_123",
		Recipient: "recipient_456",
		Memo:      "Test transfer",
	}

	response, err := client.CreateTransfer(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "transfer_789", response.ID)
	assert.Equal(t, "awaiting_funds", response.Status)
	assert.Equal(t, "TransferIntent", response.TransferInstructions.Type)
}

func TestCreateTransferIntent(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfers/transfer_789/transfer_intent", r.URL.Path)

		response := CreateTransferIntentResponse{
			Token:       "intent_token_123",
			ID:          "intent_456",
			Sender:      "0x1234567890abcdef",
			AmountIn:    "1000000000",
			To:          map[string]string{"0xabcdef1234567890": "1000000000"},
			TokenIn:     "USDC",
			TokenOut:    "USDC",
			NetworkIDIn: "ethereum",
			NetworkIDOut: "ethereum",
			GasFee:      "21000000000000000",
			Signables: []SignableTxn{
				{
					Hash: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
					Type: "EIP712",
					Data: map[string]interface{}{
						"domain": map[string]interface{}{
							"name":    "DueProtocol",
							"version": "1",
							"chainId": float64(1),
						},
					},
				},
			},
			Nonce:     "0x7b",
			Hash:      "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			Reference: "transfer_789_deposit",
			ExpiresAt: time.Now().Add(5 * time.Minute),
			CreatedAt: time.Now(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	response, err := client.CreateTransferIntent(context.Background(), "transfer_789")
	require.NoError(t, err)
	assert.Equal(t, "intent_456", response.ID)
	assert.Equal(t, "0x1234567890abcdef", response.Sender)
	assert.Len(t, response.Signables, 1)
	assert.Equal(t, "EIP712", response.Signables[0].Type)
}

func TestSubmitTransferIntent(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfer_intents/submit", r.URL.Path)

		var req SubmitTransferIntentRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "intent_456", req.ID)
		assert.Len(t, req.Signables, 1)
		assert.Equal(t, "0xsignature123", req.Signables[0].Signature)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	req := SubmitTransferIntentRequest{
		Token:      "intent_token_123",
		ID:         "intent_456",
		Sender:     "0x1234567890abcdef",
		AmountIn:   "1000000000",
		To:         map[string]string{"0xabcdef1234567890": "1000000000"},
		TokenIn:    "USDC",
		TokenOut:   "USDC",
		NetworkIDIn: "ethereum",
		NetworkIDOut: "ethereum",
		GasFee:     "21000000000000000",
		Signables: []SignableTxn{
			{
				Hash:      "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				Type:      "EIP712",
				Signature: "0xsignature123",
				Data:      map[string]interface{}{},
			},
		},
		Nonce:     "0x7b",
		Hash:      "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Reference: "transfer_789_deposit",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		CreatedAt: time.Now(),
	}

	err := client.SubmitTransferIntent(context.Background(), req)
	require.NoError(t, err)
}

func TestCreateFundingAddress(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/transfers/transfer_789/funding_address", r.URL.Path)

		response := CreateFundingAddressResponse{
			Details: FundingAddressDetails{
				Address: "0x2Fdb8B341f6c26Ee829455A9F25c83F037beb684",
				Schema:  "evm",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	response, err := client.CreateFundingAddress(context.Background(), "transfer_789")
	require.NoError(t, err)
	assert.Equal(t, "0x2Fdb8B341f6c26Ee829455A9F25c83F037beb684", response.Details.Address)
	assert.Equal(t, "evm", response.Details.Schema)
}

func TestGetTransfer(t *testing.T) {
	// Setup test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/transfers/transfer_789", r.URL.Path)

		response := GetTransferResponse{
			ID:      "transfer_789",
			OwnerID: "account_123",
			Status:  "completed",
			Source: TransferLeg{
				Amount:   "1000.00",
				Currency: "USDC",
				Rail:     "ethereum",
			},
			Destination: TransferLeg{
				Amount:   "997.50",
				Currency: "USD",
				Rail:     "ach",
			},
			FXRate:    1.0,
			FXMarkup:  0,
			CreatedAt: time.Now().Add(-10 * time.Minute),
			ExpiresAt: time.Now().Add(-5 * time.Minute),
			CompletedAt: func() *time.Time {
				t := time.Now().Add(-2 * time.Minute)
				return &t
			}(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client
	config := Config{
		BaseURL:    server.URL,
		APIKey:     "test_key",
		Environment: "sandbox",
	}
	client := NewClient(config, zaptest.NewLogger(t))

	// Test request
	response, err := client.GetTransfer(context.Background(), "transfer_789")
	require.NoError(t, err)
	assert.Equal(t, "transfer_789", response.ID)
	assert.Equal(t, "completed", response.Status)
	assert.NotNil(t, response.CompletedAt)
}
