package lulo

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mr-tron/base58"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const USDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// Client interacts with the Lulo yield API and submits signed Solana transactions.
type Client struct {
	baseURL    string
	apiKey     string
	solanaRPC  string
	owner      string            // base58 pubkey
	privateKey ed25519.PrivateKey // 64-byte ed25519 key
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a Lulo client.
// privateKeyBase58 is the 64-byte ed25519 private key encoded in base58.
func NewClient(baseURL, apiKey, solanaRPC, ownerPubkey, privateKeyBase58 string, logger *zap.Logger) (*Client, error) {
	keyBytes, err := base58.Decode(privateKeyBase58)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key length: got %d, want %d", len(keyBytes), ed25519.PrivateKeySize)
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		solanaRPC:  solanaRPC,
		owner:      ownerPubkey,
		privateKey: ed25519.PrivateKey(keyBytes),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}, nil
}

// PoolInfo represents the response from pool.getPools.
type PoolInfo struct {
	Regular   PoolType `json:"regular"`
	Protected PoolType `json:"protected"`
}

// PoolType represents a single pool's data.
type PoolType struct {
	Type  string  `json:"type"`
	APY   float64 `json:"apy"`
	Price float64 `json:"price"` // cumulative yield factor (e.g. 1.026 = 2.6% earned)
}

// AccountInfo represents the response from account.getAccount.
type AccountInfo struct {
	TotalValue     decimal.Decimal `json:"totalValue"`
	InterestEarned decimal.Decimal `json:"interestEarned"`
	DepositedValue decimal.Decimal `json:"depositedValue"`
}

// Deposit generates a deposit transaction via Lulo API, signs it, and submits to Solana.
func (c *Client) Deposit(ctx context.Context, amount decimal.Decimal, poolType string) (string, error) {
	body := map[string]any{
		"owner":      c.owner,
		"feePayer":   c.owner,
		"mintAddress": USDCMint,
	}
	// Round to 6 decimals (USDC precision) before float conversion to minimize precision loss.
	rounded := amount.Truncate(6)
	if poolType == "protected" {
		body["protectedAmount"], _ = rounded.Float64()
	} else {
		body["regularAmount"], _ = rounded.Float64()
	}

	var resp struct {
		Transaction string `json:"transaction"`
	}
	if err := c.luloPost(ctx, "/v1/generate.transactions.deposit", body, &resp); err != nil {
		return "", fmt.Errorf("generate deposit tx: %w", err)
	}
	if resp.Transaction == "" {
		return "", fmt.Errorf("lulo returned empty deposit transaction")
	}

	txHash, err := c.signAndSubmit(ctx, resp.Transaction)
	if err != nil {
		return "", fmt.Errorf("submit deposit tx: %w", err)
	}

	c.logger.Info("Lulo deposit submitted", zap.String("amount", amount.String()), zap.String("tx", txHash))
	return txHash, nil
}

// Withdraw generates a withdraw transaction via Lulo API, signs it, and submits to Solana.
func (c *Client) Withdraw(ctx context.Context, amount decimal.Decimal, poolType string) (string, error) {
	body := map[string]any{
		"owner":      c.owner,
		"feePayer":   c.owner,
		"mintAddress": USDCMint,
	}
	rounded := amount.Truncate(6)
	if poolType == "protected" {
		body["protectedAmount"], _ = rounded.Float64()
	} else {
		body["regularAmount"], _ = rounded.Float64()
	}

	var resp struct {
		Transaction string `json:"transaction"`
	}
	if err := c.luloPost(ctx, "/v1/generate.transactions.withdraw", body, &resp); err != nil {
		return "", fmt.Errorf("generate withdraw tx: %w", err)
	}
	if resp.Transaction == "" {
		return "", fmt.Errorf("lulo returned empty withdraw transaction")
	}

	txHash, err := c.signAndSubmit(ctx, resp.Transaction)
	if err != nil {
		return "", fmt.Errorf("submit withdraw tx: %w", err)
	}

	c.logger.Info("Lulo withdrawal submitted", zap.String("amount", amount.String()), zap.String("tx", txHash))
	return txHash, nil
}

// GetAccount returns the current account state for the owner wallet.
// Returns zero-value AccountInfo if the account doesn't exist yet (no deposits made).
func (c *Client) GetAccount(ctx context.Context) (*AccountInfo, error) {
	var info AccountInfo
	if err := c.luloGet(ctx, "/v1/account.getAccount", map[string]string{"owner": c.owner}, &info); err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return &info, nil
}

// GetPools returns current pool APY and price data.
func (c *Client) GetPools(ctx context.Context) (*PoolInfo, error) {
	var info PoolInfo
	if err := c.luloGet(ctx, "/v1/pool.getPools", nil, &info); err != nil {
		return nil, fmt.Errorf("get pools: %w", err)
	}
	return &info, nil
}

// GetRates returns current yield rates.
func (c *Client) GetRates(ctx context.Context) (map[string]map[string]float64, error) {
	var rates map[string]map[string]float64
	if err := c.luloGet(ctx, "/v1/rates.getRates", nil, &rates); err != nil {
		return nil, fmt.Errorf("get rates: %w", err)
	}
	return rates, nil
}

// signAndSubmit decodes a base64 serialized Solana transaction, signs it, and submits via RPC.
func (c *Client) signAndSubmit(ctx context.Context, serializedTx string) (string, error) {
	txBytes, err := base64.StdEncoding.DecodeString(serializedTx)
	if err != nil {
		return "", fmt.Errorf("decode transaction: %w", err)
	}

	if len(txBytes) < 3 {
		return "", fmt.Errorf("transaction too short: %d bytes", len(txBytes))
	}

	// Detect versioned (v0) vs legacy transaction.
	// Versioned transactions have a high bit set on the first byte (0x80 = v0).
	offset := 0
	if txBytes[0]&0x80 != 0 {
		offset = 1 // skip version prefix byte
	}

	sigCount := int(txBytes[offset])
	if sigCount == 0 {
		return "", fmt.Errorf("transaction has zero signatures")
	}
	messageStart := offset + 1 + sigCount*64
	if messageStart >= len(txBytes) {
		return "", fmt.Errorf("invalid transaction: sigCount=%d exceeds length=%d", sigCount, len(txBytes))
	}

	message := txBytes[messageStart:]
	sig := ed25519.Sign(c.privateKey, message)

	// Place our signature in the first signature slot.
	firstSigStart := offset + 1
	copy(txBytes[firstSigStart:firstSigStart+64], sig)

	encoded := base64.StdEncoding.EncodeToString(txBytes)

	// Submit via Solana RPC sendTransaction.
	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "sendTransaction",
		"params": []any{
			encoded,
			map[string]any{
				"encoding":            "base64",
				"skipPreflight":       false,
				"preflightCommitment": "confirmed",
			},
		},
	}

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.postJSON(ctx, c.solanaRPC, rpcReq, &rpcResp); err != nil {
		return "", fmt.Errorf("solana rpc: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("solana rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// luloGet makes an authenticated GET request to the Lulo API.
func (c *Client) luloGet(ctx context.Context, path string, params map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	return c.doHTTP(req, out)
}

// luloPost makes an authenticated POST request to the Lulo API.
func (c *Client) luloPost(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return c.doHTTP(req, out)
}

// postJSON makes a generic JSON POST (used for Solana RPC).
func (c *Client) postJSON(ctx context.Context, url string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doHTTP(req, out)
}

func (c *Client) doHTTP(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		c.logger.Error("HTTP error",
			zap.String("url", req.URL.String()),
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(respBody)),
		)
		return fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(respBody))
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
	}
	return nil
}
