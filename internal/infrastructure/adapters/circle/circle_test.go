package circle

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- Helper: generate a test RSA key pair ---

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	require.NoError(t, err)

	pubBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)

	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	return privKey, string(pemBlock)
}

func generateTestEntitySecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Entity Secret Encryption Tests ---

func TestEncryptEntitySecret(t *testing.T) {
	privKey, pubPEM := generateTestKeyPair(t)
	entitySecret := generateTestEntitySecret()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		EntitySecret: entitySecret,
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	ciphertext, err := client.encryptEntitySecret()
	require.NoError(t, err)
	assert.NotEmpty(t, ciphertext)

	// Verify we can decrypt it back with the private key
	cipherBytes, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)

	plaintext, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privKey, cipherBytes, nil)
	require.NoError(t, err)

	assert.Equal(t, entitySecret, hex.EncodeToString(plaintext))
}

func TestEncryptEntitySecret_FreshPerCall(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)
	entitySecret := generateTestEntitySecret()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		EntitySecret: entitySecret,
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	ct1, err := client.encryptEntitySecret()
	require.NoError(t, err)
	ct2, err := client.encryptEntitySecret()
	require.NoError(t, err)

	// RSA-OAEP is randomized, so two encryptions of the same plaintext must differ
	assert.NotEqual(t, ct1, ct2, "ciphertext must be fresh per call (Circle rejects reused values)")
}

func TestEncryptEntitySecret_InvalidHex(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		EntitySecret: "not-valid-hex",
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	_, err = client.encryptEntitySecret()
	assert.Error(t, err)
}

func TestEncryptEntitySecret_WrongLength(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		EntitySecret: "aabbccdd", // only 4 bytes, need 32
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	_, err = client.encryptEntitySecret()
	assert.ErrorContains(t, err, "32 bytes")
}

func TestEncryptEntitySecret_NoPubKey(t *testing.T) {
	// Without a public key PEM and no reachable API, encrypt should fail.
	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      "http://localhost:1", // unreachable
		EntitySecret: generateTestEntitySecret(),
		MaxRetries:   0,
	}, zap.NewNop())
	require.NoError(t, err)

	_, err = client.encryptEntitySecret()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fetch circle public key")
}

// --- Domain Mapping Tests ---

func TestDomainChainToCircle(t *testing.T) {
	tests := []struct {
		domain entities.WalletChain
		circle Blockchain
	}{
		{entities.WalletChainSolana, BlockchainSOL},
		{entities.WalletChainEthereum, BlockchainETH},
		{entities.WalletChainPolygon, BlockchainMATIC},
		{entities.WalletChainBase, BlockchainBASE},
		{entities.WalletChainAvalanche, BlockchainAVAX},
		{entities.WalletChainArbitrum, BlockchainARB},
		{entities.WalletChainOptimism, BlockchainOP},
	}
	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.circle, domainChainToCircle(tt.domain))
		})
	}
}

func TestCircleChainToDomain(t *testing.T) {
	tests := []struct {
		circle Blockchain
		domain entities.WalletChain
	}{
		{BlockchainSOL, entities.WalletChainSolana},
		{BlockchainSOLDevnet, entities.WalletChainSolana},
		{BlockchainETH, entities.WalletChainEthereum},
		{BlockchainETHSepolia, entities.WalletChainEthereum},
		{BlockchainMATIC, entities.WalletChainPolygon},
		{BlockchainMATICAmoy, entities.WalletChainPolygon},
		{BlockchainBASE, entities.WalletChainBase},
		{BlockchainBASESepolia, entities.WalletChainBase},
		{BlockchainAVAX, entities.WalletChainAvalanche},
		{BlockchainAVAXFuji, entities.WalletChainAvalanche},
		{BlockchainARB, entities.WalletChainArbitrum},
		{BlockchainARBSepolia, entities.WalletChainArbitrum},
		{BlockchainOP, entities.WalletChainOptimism},
		{BlockchainOPSepolia, entities.WalletChainOptimism},
	}
	for _, tt := range tests {
		t.Run(string(tt.circle), func(t *testing.T) {
			assert.Equal(t, tt.domain, circleChainToDomain(tt.circle))
		})
	}
}

func TestCircleStateToDomainStatus(t *testing.T) {
	assert.Equal(t, entities.WalletStatusLive, circleStateToDomainStatus(WalletStateLive))
	assert.Equal(t, entities.WalletStatusFailed, circleStateToDomainStatus(WalletStateFrozen))
	assert.Equal(t, entities.WalletStatusCreating, circleStateToDomainStatus("UNKNOWN"))
}

func TestWalletToDomain(t *testing.T) {
	userID := uuid.New()
	w := Wallet{
		ID:          "circle-wallet-123",
		Address:     "0xabc123",
		Blockchain:  BlockchainETH,
		State:       WalletStateLive,
		WalletSetID: "ws-456",
		CustodyType: "DEVELOPER",
		AccountType: "EOA",
	}

	mw := walletToDomain(w, userID)

	assert.Equal(t, userID, mw.UserID)
	assert.Equal(t, entities.WalletChainEthereum, mw.Chain)
	assert.Equal(t, "0xabc123", mw.Address)
	assert.Equal(t, "circle-wallet-123", mw.CircleWalletID)
	assert.Equal(t, entities.AccountTypeEOA, mw.AccountType)
	assert.Equal(t, entities.WalletStatusLive, mw.Status)
	assert.NotEqual(t, uuid.Nil, mw.ID)
}

// --- HTTP Client Tests (mock server) ---

func TestCreateWalletSet(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/developer/walletSets", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var body CreateWalletSetRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "test-set", body.Name)
		assert.NotEmpty(t, body.IdempotencyKey)
		assert.NotEmpty(t, body.EntitySecretCiphertext)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(apiResponse[WalletSetData]{
			Data: WalletSetData{WalletSet: WalletSet{ID: "ws-new-123", Name: "test-set"}},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	ws, err := client.CreateWalletSet(context.Background(), "test-set")
	require.NoError(t, err)
	assert.Equal(t, "ws-new-123", ws.ID)
	assert.Equal(t, "test-set", ws.Name)
}

func TestCreateWallets(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/developer/wallets", r.URL.Path)

		var body CreateWalletsRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "ws-123", body.WalletSetID)
		assert.Equal(t, []Blockchain{BlockchainSOL, BlockchainETH}, body.Blockchains)
		assert.Equal(t, 1, body.Count)
		assert.Equal(t, "SCA", body.AccountType)
		assert.NotEmpty(t, body.EntitySecretCiphertext)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(apiResponse[WalletsData]{
			Data: WalletsData{Wallets: []Wallet{
				{ID: "w-sol", Address: "So1abc", Blockchain: BlockchainSOL, State: WalletStateLive, WalletSetID: "ws-123"},
				{ID: "w-eth", Address: "0xdef", Blockchain: BlockchainETH, State: WalletStateLive, WalletSetID: "ws-123"},
			}},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	wallets, err := client.CreateWallets(context.Background(), "ws-123", []Blockchain{BlockchainSOL, BlockchainETH}, 1, nil)
	require.NoError(t, err)
	assert.Len(t, wallets, 2)
	assert.Equal(t, "w-sol", wallets[0].ID)
	assert.Equal(t, BlockchainSOL, wallets[0].Blockchain)
	assert.Equal(t, "w-eth", wallets[1].ID)
}

func TestCreateTransfer(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/developer/transactions/transfer", r.URL.Path)

		var body CreateTransferRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "w-123", body.WalletID)
		assert.Equal(t, "tok-usdc", body.TokenID)
		assert.Equal(t, "0xdest", body.DestinationAddress)
		assert.Equal(t, []string{"10.00"}, body.Amounts)
		assert.NotEmpty(t, body.EntitySecretCiphertext)
		assert.NotEmpty(t, body.IdempotencyKey)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(apiResponse[TransactionData]{
			Data: TransactionData{Transaction: Transaction{
				ID:    "tx-abc",
				State: TransactionStateInitiated,
			}},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	tx, err := client.CreateTransfer(context.Background(), &CreateTransferRequest{
		WalletID:           "w-123",
		TokenID:            "tok-usdc",
		DestinationAddress: "0xdest",
		Amounts:            []string{"10.00"},
		Fee:                &FeeConfig{Type: "level", Config: FeeConfigLevel{FeeLevel: "MEDIUM"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tx-abc", tx.ID)
	assert.Equal(t, TransactionStateInitiated, tx.State)
}

func TestSignTransaction(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/developer/sign/transaction", r.URL.Path)

		var body SignTransactionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "w-sol", body.WalletID)
		assert.Equal(t, "raw-base64", body.RawTransaction)
		assert.Equal(t, "Deposit USDC into Reflect yield", body.Memo)
		assert.NotEmpty(t, body.EntitySecretCiphertext)

		json.NewEncoder(w).Encode(apiResponse[SignedTransactionData]{
			Data: SignedTransactionData{
				Signature:         "sig",
				SignedTransaction: "signed-base64",
				TxHash:            "tx-hash",
			},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	signed, err := client.SignTransaction(context.Background(), &SignTransactionRequest{
		WalletID:       "w-sol",
		RawTransaction: "raw-base64",
		Memo:           "Deposit USDC into Reflect yield",
	})
	require.NoError(t, err)
	assert.Equal(t, "signed-base64", signed.SignedTransaction)
	assert.Equal(t, "tx-hash", signed.TxHash)
}

func TestGetTokenBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/wallets/w-123/balances", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		json.NewEncoder(w).Encode(apiResponse[TokenBalancesData]{
			Data: TokenBalancesData{TokenBalances: []TokenBalance{
				{Token: TokenInfo{Symbol: "USDC", Decimals: 6}, Amount: "42.50"},
				{Token: TokenInfo{Symbol: "ETH", Decimals: 18}, Amount: "0.01"},
			}},
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:  "test-key",
		BaseURL: server.URL,
	}, zap.NewNop())
	require.NoError(t, err)

	balances, err := client.GetTokenBalance(context.Background(), "w-123")
	require.NoError(t, err)
	assert.Len(t, balances, 2)
	assert.Equal(t, "USDC", balances[0].Token.Symbol)
	assert.Equal(t, "42.50", balances[0].Amount)
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{
			StatusCode: 400,
			Code:       "invalid_entity_secret_ciphertext",
			Message:    "Entity secret ciphertext is invalid",
		})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:  "test-key",
		BaseURL: server.URL,
	}, zap.NewNop())
	require.NoError(t, err)

	_, err = client.GetWallet(context.Background(), "bad-id")
	require.Error(t, err)

	var apiErr *ErrorResponse
	require.ErrorAs(t, err, &apiErr)
	assert.True(t, apiErr.IsBadCiphertext())
	assert.False(t, apiErr.IsRetryable())
}

func TestCircuitBreaker(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{StatusCode: 500, Message: "server error"})
	}))
	defer server.Close()

	client, err := NewHTTPClient(Config{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		MaxRetries: 0, // no retries, just count raw calls
	}, zap.NewNop())
	require.NoError(t, err)

	// 5 failures should open the circuit
	for i := 0; i < 5; i++ {
		_ = client.doRequest(context.Background(), "GET", "/test", nil, nil)
	}

	// 6th call should be rejected by circuit breaker without hitting server
	prevCount := callCount
	err = client.doRequest(context.Background(), "GET", "/test", nil, nil)
	require.Error(t, err)
	assert.Equal(t, prevCount, callCount, "circuit breaker should prevent server call")
	assert.Contains(t, err.Error(), "circuit")
}

// --- Adapter Integration Test (with mock) ---

func TestAdapterCreateWalletForUser(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(apiResponse[WalletsData]{
			Data: WalletsData{Wallets: []Wallet{
				{ID: "cw-123", Address: "0xwallet", Blockchain: BlockchainETH, State: WalletStateLive},
			}},
		})
	}))
	defer server.Close()

	httpClient, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
		PublicKeyPEM: pubPEM,
	}, zap.NewNop())
	require.NoError(t, err)

	adapter := NewAdapter(httpClient, zap.NewNop())
	userID := uuid.New()

	mw, err := adapter.CreateWalletForUser(context.Background(), userID, "ws-1", entities.WalletChainEthereum)
	require.NoError(t, err)
	assert.Equal(t, userID, mw.UserID)
	assert.Equal(t, entities.WalletChainEthereum, mw.Chain)
	assert.Equal(t, "0xwallet", mw.Address)
	assert.Equal(t, "cw-123", mw.CircleWalletID)
	assert.Equal(t, entities.WalletStatusLive, mw.Status)
}

func TestAdapterCreateWalletForUserReusesExistingWalletByRefID(t *testing.T) {
	userID := uuid.New()
	var createCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " /v1/w3s/wallets":
			assert.Equal(t, userID.String(), r.URL.Query().Get("refId"))
			json.NewEncoder(w).Encode(apiResponse[WalletsData]{
				Data: WalletsData{Wallets: []Wallet{
					{
						ID:          "existing-wallet",
						Address:     "0xexisting",
						Blockchain:  BlockchainETH,
						State:       WalletStateLive,
						WalletSetID: "ws-1",
						AccountType: "SCA",
					},
				}},
			})
		case http.MethodPost + " /v1/w3s/developer/wallets":
			createCalls++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(apiResponse[WalletsData]{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	httpClient, err := NewHTTPClient(Config{
		APIKey:       "test-key",
		BaseURL:      server.URL,
		EntitySecret: generateTestEntitySecret(),
	}, zap.NewNop())
	require.NoError(t, err)

	adapter := NewAdapter(httpClient, zap.NewNop())
	mw, err := adapter.CreateWalletForUser(context.Background(), userID, "ws-1", entities.WalletChainEthereum)
	require.NoError(t, err)
	assert.Equal(t, "existing-wallet", mw.CircleWalletID)
	assert.Equal(t, entities.AccountTypeSCA, mw.AccountType)
	assert.Zero(t, createCalls)
}

func TestAdapterCreateMultiChainWalletsRejectsUnsupportedMixedChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/w3s/wallets", r.URL.Path)
		json.NewEncoder(w).Encode(apiResponse[WalletsData]{Data: WalletsData{Wallets: nil}})
	}))
	defer server.Close()

	httpClient, err := NewHTTPClient(Config{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
	require.NoError(t, err)

	adapter := NewAdapter(httpClient, zap.NewNop())
	_, err = adapter.CreateMultiChainWallets(context.Background(), uuid.New(), "ws-1", []entities.WalletChain{
		entities.WalletChainSolana,
		entities.WalletChainCelo,
	})
	assert.ErrorContains(t, err, "unsupported chain for Circle: CELO")
}

func TestAdapterGetWalletBalance_USDC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse[TokenBalancesData]{
			Data: TokenBalancesData{TokenBalances: []TokenBalance{
				{Token: TokenInfo{Symbol: "USDC"}, Amount: "100.50"},
				{Token: TokenInfo{Symbol: "ETH"}, Amount: "0.001"},
			}},
		})
	}))
	defer server.Close()

	httpClient, err := NewHTTPClient(Config{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
	require.NoError(t, err)

	adapter := NewAdapter(httpClient, zap.NewNop())
	balance, err := adapter.GetWalletBalance(context.Background(), "w-1")
	require.NoError(t, err)
	assert.Equal(t, "100.50", balance)
}

func TestAdapterGetWalletBalance_NoUSDC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse[TokenBalancesData]{
			Data: TokenBalancesData{TokenBalances: []TokenBalance{
				{Token: TokenInfo{Symbol: "ETH"}, Amount: "1.0"},
			}},
		})
	}))
	defer server.Close()

	httpClient, err := NewHTTPClient(Config{APIKey: "test-key", BaseURL: server.URL}, zap.NewNop())
	require.NoError(t, err)

	adapter := NewAdapter(httpClient, zap.NewNop())
	balance, err := adapter.GetWalletBalance(context.Background(), "w-1")
	require.NoError(t, err)
	assert.Equal(t, "0", balance)
}

func TestAdapterUnsupportedChain(t *testing.T) {
	adapter := NewAdapter(nil, zap.NewNop())
	_, err := adapter.CreateWalletForUser(context.Background(), uuid.New(), "ws-1", entities.WalletChainCelo)
	assert.ErrorContains(t, err, "unsupported chain")
}
