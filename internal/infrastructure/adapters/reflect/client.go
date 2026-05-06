package reflect

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

const (
	USDCPlusIndex = 0 // USDC+ stablecoin index on Reflect
	prodBaseURL   = "https://prod.api.reflect.money"
	usdcMint      = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	slippageBPS   = 10 // 0.1% slippage tolerance
)

// Client interacts with the Reflect Money REST API and submits signed Solana transactions.
type Client struct {
	baseURL         string
	apiKey          string
	solanaRPC       string
	owner           string
	privateKey      ed25519.PrivateKey
	stablecoinIndex int
	httpClient      *http.Client
	logger          *zap.Logger
}

// NewClient creates a Reflect client. privateKeyBase58 is optional for flows where
// Circle signs generated transactions with a user wallet.
func NewClient(baseURL, apiKey, solanaRPC, ownerPubkey, privateKeyBase58 string, stablecoinIndex int, logger *zap.Logger) (*Client, error) {
	if baseURL == "" {
		baseURL = prodBaseURL
	}
	if solanaRPC == "" {
		return nil, fmt.Errorf("reflect: solana_rpc is required")
	}
	var privateKey ed25519.PrivateKey
	if privateKeyBase58 != "" {
		keyBytes, err := base58.Decode(privateKeyBase58)
		if err != nil {
			return nil, fmt.Errorf("decode private key: %w", err)
		}
		if len(keyBytes) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("invalid private key length: got %d, want %d", len(keyBytes), ed25519.PrivateKeySize)
		}
		privateKey = ed25519.PrivateKey(keyBytes)
	}
	return &Client{
		baseURL:         baseURL,
		apiKey:          apiKey,
		solanaRPC:       solanaRPC,
		owner:           ownerPubkey,
		privateKey:      privateKey,
		stablecoinIndex: stablecoinIndex,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
	}, nil
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// Health checks if the Reflect API is operational.
// GET /health
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.get(ctx, "/health", nil, &resp); err != nil {
		return nil, fmt.Errorf("reflect health: %w", err)
	}
	return &resp, nil
}

// GetExchangeRate returns the current exchange rate for the configured stablecoin as a decimal.
// Uses GET /stablecoin/{index}/exchange-rate
// The API returns base_usd_value_bps where 10000 = $1.00 (e.g. 10043 = $1.0043).
// We convert to a decimal rate: bps / 10000.
func (c *Client) GetExchangeRate(ctx context.Context) (decimal.Decimal, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			BaseUSDValueBPS    int64 `json:"base_usd_value_bps"`
			ReceiptUSDValueBPS int64 `json:"receipt_usd_value_bps"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/stablecoin/%d/exchange-rate", c.stablecoinIndex)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return decimal.Zero, fmt.Errorf("reflect exchange rate: %w", err)
	}
	if !resp.Success || resp.Data.BaseUSDValueBPS == 0 {
		return decimal.Zero, fmt.Errorf("reflect: invalid exchange rate response for index %d", c.stablecoinIndex)
	}
	// base_usd_value_bps: 10000 = $1.00
	rate := decimal.NewFromInt(resp.Data.BaseUSDValueBPS).Div(decimal.NewFromInt(10000))
	return rate, nil
}

// GetAPY returns the current APY for the configured stablecoin.
// Uses GET /stablecoin/{index}/apy
func (c *Client) GetAPY(ctx context.Context) (decimal.Decimal, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Index int             `json:"index"`
			APY   decimal.Decimal `json:"apy"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/stablecoin/%d/apy", c.stablecoinIndex)
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return decimal.Zero, fmt.Errorf("reflect apy: %w", err)
	}
	if !resp.Success {
		return decimal.Zero, fmt.Errorf("reflect: apy request failed for index %d", c.stablecoinIndex)
	}
	return resp.Data.APY, nil
}

// Mint generates a mint transaction via Reflect API, signs it, and submits to Solana.
// amount is in USDC (6 decimal places). Internally converted to micro-USDC integer units.
// Uses POST /stablecoin/mint
func (c *Client) Mint(ctx context.Context, amount decimal.Decimal) (string, error) {
	if c.owner == "" {
		return "", fmt.Errorf("reflect mint: owner wallet is required for local signing")
	}
	if len(c.privateKey) == 0 {
		return "", fmt.Errorf("reflect mint: private key is required for local signing")
	}
	serializedTx, err := c.GenerateMintTransaction(ctx, amount, c.owner, c.owner)
	if err != nil {
		return "", err
	}
	if err := c.validateReflectUserMintTransaction(ctx, serializedTx, c.owner, amount, nil); err != nil {
		return "", fmt.Errorf("reflect validate mint tx: %w", err)
	}
	txHash, err := c.signAndSubmit(ctx, serializedTx)
	if err != nil {
		return "", fmt.Errorf("reflect submit mint tx: %w", err)
	}
	c.logger.Info("Reflect mint submitted", zap.String("amount", amount.String()), zap.String("tx", txHash))
	return txHash, nil
}

// GenerateMintTransaction asks Reflect to build an unsigned mint transaction for the provided signer.
func (c *Client) GenerateMintTransaction(ctx context.Context, amount decimal.Decimal, signer, feePayer string) (string, error) {
	microAmount := amount.Truncate(6).Mul(decimal.NewFromInt(1_000_000)).IntPart()
	if microAmount <= 0 {
		return "", fmt.Errorf("reflect mint: amount %s is below minimum (1 micro-USDC)", amount.String())
	}
	if signer == "" {
		return "", fmt.Errorf("reflect mint: signer is required")
	}
	if feePayer == "" {
		feePayer = signer
	}
	// Apply slippage: floor at 1 to avoid sending minimumReceived=0 which disables protection.
	minReceived := microAmount * (10000 - slippageBPS) / 10000
	if minReceived <= 0 {
		minReceived = 1
	}

	body := map[string]any{
		"stablecoinIndex": c.stablecoinIndex,
		"depositAmount":   microAmount,
		"signer":          signer,
		"feePayer":        feePayer,
		"collateralMint":  usdcMint,
		"minimumReceived": minReceived,
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Transaction string `json:"transaction"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/stablecoin/mint", body, &resp); err != nil {
		return "", fmt.Errorf("reflect generate mint tx: %w", err)
	}
	if !resp.Success || resp.Data.Transaction == "" {
		return "", fmt.Errorf("reflect returned empty mint transaction")
	}
	return resp.Data.Transaction, nil
}

// Burn generates a burn/redeem transaction via Reflect API, signs it, and submits to Solana.
// amount is in USDC+ tokens (6 decimal places). Internally converted to micro-unit integer.
// Uses POST /stablecoin/burn
func (c *Client) Burn(ctx context.Context, amount decimal.Decimal) (string, error) {
	if c.owner == "" {
		return "", fmt.Errorf("reflect burn: owner wallet is required for local signing")
	}
	if len(c.privateKey) == 0 {
		return "", fmt.Errorf("reflect burn: private key is required for local signing")
	}
	serializedTx, err := c.GenerateBurnTransaction(ctx, amount, c.owner, c.owner)
	if err != nil {
		return "", err
	}
	if err := c.validateReflectUserBurnTransaction(ctx, serializedTx, c.owner, nil); err != nil {
		return "", fmt.Errorf("reflect validate burn tx: %w", err)
	}
	txHash, err := c.signAndSubmit(ctx, serializedTx)
	if err != nil {
		return "", fmt.Errorf("reflect submit burn tx: %w", err)
	}
	c.logger.Info("Reflect burn submitted", zap.String("amount", amount.String()), zap.String("tx", txHash))
	return txHash, nil
}

// GenerateBurnTransaction asks Reflect to build an unsigned burn transaction for the provided signer.
func (c *Client) GenerateBurnTransaction(ctx context.Context, amount decimal.Decimal, signer, feePayer string) (string, error) {
	microAmount := amount.Truncate(6).Mul(decimal.NewFromInt(1_000_000)).IntPart()
	if microAmount <= 0 {
		return "", fmt.Errorf("reflect burn: amount %s is below minimum (1 micro-USDC)", amount.String())
	}
	if signer == "" {
		return "", fmt.Errorf("reflect burn: signer is required")
	}
	if feePayer == "" {
		feePayer = signer
	}
	minReceived := microAmount * (10000 - slippageBPS) / 10000
	if minReceived <= 0 {
		minReceived = 1
	}

	body := map[string]any{
		"stablecoinIndex": c.stablecoinIndex,
		"depositAmount":   microAmount,
		"signer":          signer,
		"feePayer":        feePayer,
		"collateralMint":  usdcMint,
		"minimumReceived": minReceived,
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Transaction string `json:"transaction"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/stablecoin/burn", body, &resp); err != nil {
		return "", fmt.Errorf("reflect generate burn tx: %w", err)
	}
	if !resp.Success || resp.Data.Transaction == "" {
		return "", fmt.Errorf("reflect returned empty burn transaction")
	}
	return resp.Data.Transaction, nil
}

// SubmitSignedTransaction submits a base64-encoded signed Solana transaction to the configured RPC.
func (c *Client) SubmitSignedTransaction(ctx context.Context, signedTransaction string) (string, error) {
	return c.submitBase64Transaction(ctx, signedTransaction)
}

// signAndSubmit decodes a base64 serialized Solana transaction, signs it, and submits via RPC.
// It locates Rail's wallet in the transaction's account list to write the signature
// into the correct slot, handling multi-signer transactions safely.
func (c *Client) signAndSubmit(ctx context.Context, serializedTx string) (string, error) {
	txBytes, err := base64.StdEncoding.DecodeString(serializedTx)
	if err != nil {
		return "", fmt.Errorf("decode transaction: %w", err)
	}
	if len(txBytes) < 3 {
		return "", fmt.Errorf("transaction too short: %d bytes", len(txBytes))
	}

	// Detect versioned (v0) vs legacy transaction.
	offset := 0
	if txBytes[0]&0x80 != 0 {
		offset = 1
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

	signerSlot, err := c.findSignerSlot(message, sigCount)
	if err != nil {
		c.logger.Warn("Could not locate signer slot, defaulting to slot 0", zap.Error(err))
		signerSlot = 0
	}

	sig := ed25519.Sign(c.privateKey, message)
	slotStart := offset + 1 + signerSlot*64
	copy(txBytes[slotStart:slotStart+64], sig)

	encoded := base64.StdEncoding.EncodeToString(txBytes)
	return c.submitBase64Transaction(ctx, encoded)
}

func (c *Client) submitBase64Transaction(ctx context.Context, encoded string) (string, error) {
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

// findSignerSlot scans the message account list to find the index of our owner pubkey
// among the required signers. Returns an error if not found.
func (c *Client) findSignerSlot(message []byte, sigCount int) (int, error) {
	ownerBytes, err := base58.Decode(c.owner)
	if err != nil || len(ownerBytes) != 32 {
		return 0, fmt.Errorf("invalid owner pubkey")
	}
	// Message header: 3 bytes, then compact-u16 account count, then 32-byte accounts.
	if len(message) < 4 {
		return 0, fmt.Errorf("message too short")
	}
	pos := 3
	accountCount := int(message[pos])
	if message[pos]&0x80 != 0 {
		if pos+1 >= len(message) {
			return 0, fmt.Errorf("truncated compact-u16")
		}
		accountCount = int(message[pos]&0x7f) | int(message[pos+1])<<7
		pos++
	}
	pos++
	for i := 0; i < accountCount && i < sigCount; i++ {
		if pos+32 > len(message) {
			break
		}
		if string(message[pos:pos+32]) == string(ownerBytes) {
			return i, nil
		}
		pos += 32
	}
	return 0, fmt.Errorf("owner pubkey not found in signer accounts")
}

func (c *Client) get(ctx context.Context, path string, params map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	return c.doHTTP(req, out)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doHTTP(req, out)
}

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		c.logger.Error("Reflect API error",
			zap.String("path", req.URL.Path),
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(body)),
		)
		return fmt.Errorf("%s %s returned %d: %s", req.Method, req.URL.Path, resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
	}
	return nil
}
