package circle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Config holds Circle API configuration.
type Config struct {
	APIKey            string
	BaseURL           string
	Environment       string // "sandbox" or "production"
	EntitySecret      string // 64-char hex string (32 bytes)
	PublicKeyPEM      string // Circle RSA public key for entity secret encryption
	DefaultWalletSetID string
	Timeout           time.Duration
	MaxRetries        int
}

// HTTPClient implements the Client interface via Circle REST API.
type HTTPClient struct {
	config     Config
	httpClient *http.Client
	logger     *zap.Logger
	publicKey  *rsa.PublicKey // parsed once from PEM
	pubKeyOnce sync.Once
	pubKeyErr  error

	consecutiveFailures atomic.Int64
	circuitOpenUntil    atomic.Int64
}

// NewHTTPClient creates a new Circle API client.
func NewHTTPClient(cfg Config, logger *zap.Logger) (*HTTPClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.BaseURL == "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Environment), "sandbox") {
			cfg.BaseURL = "https://api.circle.com"
		} else {
			cfg.BaseURL = "https://api.circle.com"
		}
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	c := &HTTPClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}

	// Parse RSA public key if provided.
	if cfg.PublicKeyPEM != "" {
		pk, err := parseRSAPublicKey([]byte(cfg.PublicKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("parse circle public key: %w", err)
		}
		c.publicKey = pk
	}

	return c, nil
}

// --- Entity Secret Encryption (per Circle docs: RSA-OAEP SHA-256) ---

// encryptEntitySecret produces a fresh entitySecretCiphertext for each API call.
// Algorithm: RSA-OAEP with SHA-256 hash and SHA-256 MGF1, then base64-encode.
// Reference: https://github.com/circlefin/w3s-entity-secret-sample-code/blob/master/golang/generate_entity_secret_ciphertext.go
func (c *HTTPClient) encryptEntitySecret() (string, error) {
	// Lazy-fetch public key from Circle API if not provided via config.
	if c.publicKey == nil {
		c.pubKeyOnce.Do(func() {
			pubKeyPEM, err := c.GetEntityPublicKey(context.Background())
			if err != nil {
				c.pubKeyErr = fmt.Errorf("fetch circle public key: %w", err)
				return
			}
			pk, err := parseRSAPublicKey([]byte(pubKeyPEM))
			if err != nil {
				c.pubKeyErr = fmt.Errorf("parse circle public key: %w", err)
				return
			}
			c.publicKey = pk
			c.logger.Info("Fetched Circle entity public key from API")
		})
		if c.pubKeyErr != nil {
			return "", c.pubKeyErr
		}
	}

	entitySecretBytes, err := hex.DecodeString(c.config.EntitySecret)
	if err != nil {
		return "", fmt.Errorf("decode entity secret hex: %w", err)
	}
	if len(entitySecretBytes) != 32 {
		return "", fmt.Errorf("entity secret must be 32 bytes, got %d", len(entitySecretBytes))
	}

	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, c.publicKey, entitySecretBytes, nil)
	if err != nil {
		return "", fmt.Errorf("RSA-OAEP encrypt: %w", err)
	}

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func parseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsaPub, nil
}

// --- HTTP request execution with retry + circuit breaker ---

func (c *HTTPClient) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	// Circuit breaker check
	if openUntil := c.circuitOpenUntil.Load(); openUntil > 0 {
		if time.Now().Unix() < openUntil {
			return &ErrorResponse{StatusCode: 503, Code: "circuit_open", Message: "circuit breaker open"}
		}
		c.circuitOpenUntil.Store(0)
		c.consecutiveFailures.Store(0)
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	url := c.config.BaseURL + path

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			jitter := time.Duration(mrand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http request: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			c.consecutiveFailures.Store(0)
			if result != nil && len(respBody) > 0 {
				if err := json.Unmarshal(respBody, result); err != nil {
					return fmt.Errorf("unmarshal response: %w", err)
				}
			}
			return nil
		}

		apiErr := &ErrorResponse{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(respBody, apiErr)
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}

		// 5xx → circuit breaker
		if resp.StatusCode >= 500 {
			if c.consecutiveFailures.Add(1) >= 5 {
				c.circuitOpenUntil.Store(time.Now().Add(30 * time.Second).Unix())
				c.logger.Warn("circle circuit breaker opened", zap.Int("status", resp.StatusCode))
			}
		}

		if !apiErr.IsRetryable() {
			return apiErr
		}
		lastErr = apiErr
	}

	return fmt.Errorf("circle API exhausted retries: %w", lastErr)
}

// --- Client interface implementation ---

func (c *HTTPClient) Ping(ctx context.Context) error {
	var resp struct{}
	return c.doRequest(ctx, http.MethodGet, "/ping", nil, &resp)
}

func (c *HTTPClient) GetEntityPublicKey(ctx context.Context) (string, error) {
	var resp apiResponse[EntityPublicKeyData]
	if err := c.doRequest(ctx, http.MethodGet, "/v1/w3s/config/entity/publicKey", nil, &resp); err != nil {
		return "", err
	}
	return resp.Data.PublicKey, nil
}

func (c *HTTPClient) CreateWalletSet(ctx context.Context, name string) (*WalletSet, error) {
	ciphertext, err := c.encryptEntitySecret()
	if err != nil {
		return nil, err
	}

	req := CreateWalletSetRequest{
		IdempotencyKey:         uuid.New().String(),
		Name:                   name,
		EntitySecretCiphertext: ciphertext,
	}

	var resp apiResponse[WalletSetData]
	if err := c.doRequest(ctx, http.MethodPost, "/v1/w3s/developer/walletSets", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.WalletSet, nil
}

func (c *HTTPClient) CreateWallets(ctx context.Context, walletSetID string, blockchains []Blockchain, count int, metadata []WalletMetadata) ([]Wallet, error) {
	ciphertext, err := c.encryptEntitySecret()
	if err != nil {
		return nil, err
	}

	req := CreateWalletsRequest{
		IdempotencyKey:         uuid.New().String(),
		EntitySecretCiphertext: ciphertext,
		WalletSetID:            walletSetID,
		Blockchains:            blockchains,
		Count:                  count,
		AccountType:            "EOA",
		Metadata:               metadata,
	}

	var resp apiResponse[WalletsData]
	if err := c.doRequest(ctx, http.MethodPost, "/v1/w3s/developer/wallets", req, &resp); err != nil {
		return nil, err
	}
	return resp.Data.Wallets, nil
}

func (c *HTTPClient) GetWallet(ctx context.Context, walletID string) (*Wallet, error) {
	var resp apiResponse[WalletData]
	if err := c.doRequest(ctx, http.MethodGet, "/v1/w3s/wallets/"+walletID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Wallet, nil
}

func (c *HTTPClient) ListWallets(ctx context.Context, walletSetID string) ([]Wallet, error) {
	var resp apiResponse[WalletsData]
	path := "/v1/w3s/wallets?walletSetId=" + walletSetID
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data.Wallets, nil
}

func (c *HTTPClient) GetTokenBalance(ctx context.Context, walletID string) ([]TokenBalance, error) {
	var resp apiResponse[TokenBalancesData]
	if err := c.doRequest(ctx, http.MethodGet, "/v1/w3s/wallets/"+walletID+"/balances", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data.TokenBalances, nil
}

// GetUSDCTokenID looks up the USDC token UUID from a wallet's balances.
func (c *HTTPClient) GetUSDCTokenID(ctx context.Context, walletID string) (string, error) {
	balances, err := c.GetTokenBalance(ctx, walletID)
	if err != nil {
		return "", err
	}
	for _, b := range balances {
		if strings.EqualFold(b.Token.Symbol, "USDC") {
			return b.Token.ID, nil
		}
	}
	return "", fmt.Errorf("no USDC token found in wallet %s", walletID)
}

func (c *HTTPClient) CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*Transaction, error) {
	ciphertext, err := c.encryptEntitySecret()
	if err != nil {
		return nil, err
	}

	// Copy to avoid mutating caller's struct.
	outReq := *req
	outReq.EntitySecretCiphertext = ciphertext
	if outReq.IdempotencyKey == "" {
		outReq.IdempotencyKey = uuid.New().String()
	}

	var resp apiResponse[TransactionData]
	if err := c.doRequest(ctx, http.MethodPost, "/v1/w3s/developer/transactions/transfer", outReq, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Transaction, nil
}

func (c *HTTPClient) GetTransaction(ctx context.Context, txID string) (*Transaction, error) {
	var resp apiResponse[TransactionData]
	if err := c.doRequest(ctx, http.MethodGet, "/v1/w3s/transactions/"+txID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Transaction, nil
}

func (c *HTTPClient) EstimateTransferFee(ctx context.Context, req *EstimateFeeRequest) (*FeeEstimate, error) {
	var resp apiResponse[FeeEstimate]
	if err := c.doRequest(ctx, http.MethodPost, "/v1/w3s/developer/transactions/transfer/estimateFee", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// Ensure HTTPClient implements Client.
var _ Client = (*HTTPClient)(nil)
