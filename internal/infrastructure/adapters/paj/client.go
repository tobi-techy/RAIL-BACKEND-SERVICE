package paj

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL    = "https://api.paj.cash"
	defaultTimeout    = 15 * time.Second
	defaultMaxRetries = 2
	maxResponseSize   = 1 << 20 // 1MB
)

// Config holds Paj API configuration.
type Config struct {
	APIKey        string
	BaseURL       string
	WebhookURL    string // Rail's webhook endpoint URL for order status updates
	WalletAddress string // Rail's USDC custody wallet address (onramp recipient)
	TokenMint     string // USDC mint address on Solana
	Chain         string // "SOLANA"
	Timeout       time.Duration
	MaxRetries    int
}

// Client wraps the Paj Cash ramp API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.Chain == "" {
		cfg.Chain = "SOLANA"
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}
}

// --- Session management ---

type InitiateResponse struct {
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type DeviceSignature struct {
	UUID    string `json:"uuid"`
	Device  string `json:"device"`
	OS      string `json:"os,omitempty"`
	Browser string `json:"browser,omitempty"`
	IP      string `json:"ip,omitempty"`
}

type VerifyResponse struct {
	Recipient string `json:"recipient"`
	IsActive  bool   `json:"isActive"`
	ExpiresAt string `json:"expiresAt"`
	Token     string `json:"token"`
}

// Initiate sends an OTP to the user's email or phone.
func (c *Client) Initiate(ctx context.Context, emailOrPhone string) (*InitiateResponse, error) {
	var body interface{}
	if isPhone(emailOrPhone) {
		body = map[string]string{"phone": emailOrPhone}
	} else {
		body = map[string]string{"email": emailOrPhone}
	}
	var resp InitiateResponse
	if err := c.post(ctx, "/pub/initiate", body, nil, &resp); err != nil {
		return nil, fmt.Errorf("paj initiate: %w", err)
	}
	return &resp, nil
}

// Verify confirms the OTP and returns a session token.
func (c *Client) Verify(ctx context.Context, emailOrPhone, otp string, device DeviceSignature) (*VerifyResponse, error) {
	var body map[string]interface{}
	if isPhone(emailOrPhone) {
		body = map[string]interface{}{"phone": emailOrPhone, "otp": otp, "device": device}
	} else {
		body = map[string]interface{}{"email": emailOrPhone, "otp": otp, "device": device}
	}
	var resp VerifyResponse
	if err := c.post(ctx, "/pub/verify", body, nil, &resp); err != nil {
		return nil, fmt.Errorf("paj verify: %w", err)
	}
	return &resp, nil
}

func isPhone(s string) bool {
	if len(s) < 7 {
		return false
	}
	for i, c := range s {
		if c == '+' && i == 0 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// --- Rates ---

type RateResponse struct {
	OnRampRate  Rate `json:"onRampRate"`
	OffRampRate Rate `json:"offRampRate"`
}

type Rate struct {
	BaseCurrency   string  `json:"baseCurrency"`
	TargetCurrency string  `json:"targetCurrency"`
	IsActive       bool    `json:"isActive"`
	Rate           float64 `json:"rate"`
	Type           string  `json:"type"`
}

func (c *Client) GetRates(ctx context.Context) (*RateResponse, error) {
	var resp RateResponse
	if err := c.get(ctx, "/pub/rate", nil, &resp); err != nil {
		return nil, fmt.Errorf("paj get rates: %w", err)
	}
	return &resp, nil
}

// --- Banks ---

type Bank struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Logo    string `json:"logo"`
	Country string `json:"country"`
}

type ResolvedAccount struct {
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	Bank          Bank   `json:"bank"`
}

type SavedBankAccount struct {
	ID            string `json:"id"`
	AccountName   string `json:"accountName"`
	AccountNumber string `json:"accountNumber"`
	Bank          string `json:"bank"`
}

func (c *Client) GetBanks(ctx context.Context, sessionToken string) ([]Bank, error) {
	var resp []Bank
	if err := c.get(ctx, "/pub/bank", &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj get banks: %w", err)
	}
	return resp, nil
}

func (c *Client) ResolveBankAccount(ctx context.Context, sessionToken, bankID, accountNumber string) (*ResolvedAccount, error) {
	path := fmt.Sprintf("/pub/bank-account/confirm/?bankId=%s&accountNumber=%s", bankID, accountNumber)
	var resp ResolvedAccount
	if err := c.get(ctx, path, &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj resolve bank: %w", err)
	}
	return &resp, nil
}

func (c *Client) AddBankAccount(ctx context.Context, sessionToken, bankID, accountNumber string) (*SavedBankAccount, error) {
	payload := map[string]string{"bankId": bankID, "accountNumber": accountNumber}
	var resp SavedBankAccount
	if err := c.post(ctx, "/pub/bank-account", payload, &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj add bank: %w", err)
	}
	return &resp, nil
}

func (c *Client) GetBankAccounts(ctx context.Context, sessionToken string) ([]SavedBankAccount, error) {
	var resp []SavedBankAccount
	if err := c.get(ctx, "/pub/bank-account", &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj get bank accounts: %w", err)
	}
	return resp, nil
}

// --- Onramp (NGN → USDC) ---

type CreateOnrampOrderRequest struct {
	FiatAmount     float64 `json:"fiatAmount"`
	Currency       string  `json:"currency"`
	Recipient      string  `json:"recipient"` // wallet address to receive USDC
	Mint           string  `json:"mint"`      // USDC mint address
	Chain          string  `json:"chain"`
	WebhookURL     string  `json:"webhookURL"`
	BusinessUSDCFee float64 `json:"businessUSDCFee,omitempty"`
}

type OnrampOrder struct {
	ID            string  `json:"id"`
	AccountNumber string  `json:"accountNumber"`
	AccountName   string  `json:"accountName"`
	FiatAmount    float64 `json:"fiatAmount"`
	Amount        float64 `json:"amount"` // token amount to receive
	Bank          string  `json:"bank"`
	Rate          float64 `json:"rate"`
	Recipient     string  `json:"recipient"`
	Currency      string  `json:"currency"`
	Mint          string  `json:"mint"`
	Fee           float64 `json:"fee"`
}

func (c *Client) CreateOnrampOrder(ctx context.Context, sessionToken string, fiatAmount float64, currency string) (*OnrampOrder, error) {
	req := CreateOnrampOrderRequest{
		FiatAmount: fiatAmount,
		Currency:   currency,
		Recipient:  c.cfg.WalletAddress,
		Mint:       c.cfg.TokenMint,
		Chain:      c.cfg.Chain,
		WebhookURL: c.cfg.WebhookURL,
	}
	var resp OnrampOrder
	if err := c.post(ctx, "/pub/onramp", req, &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj create onramp order: %w", err)
	}
	return &resp, nil
}

// --- Offramp (USDC → NGN) ---

type CreateOfframpOrderRequest struct {
	Bank            string  `json:"bank"`
	AccountNumber   string  `json:"accountNumber"`
	Currency        string  `json:"currency"`
	FiatAmount      float64 `json:"fiatAmount,omitempty"`
	Amount          float64 `json:"amount,omitempty"` // token amount (alternative to fiatAmount)
	Mint            string  `json:"mint"`
	Chain           string  `json:"chain"`
	WebhookURL      string  `json:"webhookURL"`
	BusinessUSDCFee float64 `json:"businessUSDCFee,omitempty"`
}

type OfframpOrder struct {
	ID         string  `json:"id"`
	Address    string  `json:"address"` // Paj deposit address for USDC
	Mint       string  `json:"mint"`
	Currency   string  `json:"currency"`
	Amount     float64 `json:"amount"`     // token amount to send
	FiatAmount float64 `json:"fiatAmount"` // NGN the user will receive
	Rate       float64 `json:"rate"`
	Fee        float64 `json:"fee"`
}

func (c *Client) CreateOfframpOrder(ctx context.Context, sessionToken, bankID, accountNumber string, fiatAmount float64, currency string) (*OfframpOrder, error) {
	req := CreateOfframpOrderRequest{
		Bank:          bankID,
		AccountNumber: accountNumber,
		Currency:      currency,
		FiatAmount:    fiatAmount,
		Mint:          c.cfg.TokenMint,
		Chain:         c.cfg.Chain,
		WebhookURL:    c.cfg.WebhookURL,
	}
	var resp OfframpOrder
	if err := c.post(ctx, "/pub/offramp", req, &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj create offramp order: %w", err)
	}
	return &resp, nil
}

// --- Transaction status (for polling / webhook verification) ---

type PajTransaction struct {
	ID              string  `json:"id"`
	Address         string  `json:"address"`
	Signature       string  `json:"signature,omitempty"`
	Mint            string  `json:"mint"`
	Currency        string  `json:"currency"`
	Amount          float64 `json:"amount"`
	USDCAmount      float64 `json:"usdcAmount"`
	FiatAmount      float64 `json:"fiatAmount"`
	Sender          string  `json:"sender"`
	Recipient       string  `json:"recipient"`
	Rate            float64 `json:"rate"`
	Status          string  `json:"status"`          // INIT, PAID, COMPLETED, FAILED
	TransactionType string  `json:"transactionType"` // ON_RAMP, OFF_RAMP
	CreatedAt       string  `json:"createdAt"`
}

// GetTransaction fetches order status from Paj. Used to verify webhook payloads
// and for polling fallback since Paj has no webhook signature verification.
func (c *Client) GetTransaction(ctx context.Context, sessionToken, orderID string) (*PajTransaction, error) {
	var resp PajTransaction
	if err := c.get(ctx, "/pub/transactions/"+orderID, &sessionToken, &resp); err != nil {
		return nil, fmt.Errorf("paj get transaction: %w", err)
	}
	return &resp, nil
}

// --- Webhook types (Paj posts to webhookURL per-order, no signature) ---

type WebhookPayload struct {
	ID              string  `json:"id"`
	Address         string  `json:"address"`
	Signature       string  `json:"signature,omitempty"`
	Mint            string  `json:"mint"`
	Currency        string  `json:"currency"`
	Amount          float64 `json:"amount"`
	USDCAmount      float64 `json:"usdcAmount"`
	FiatAmount      float64 `json:"fiatAmount"`
	Sender          string  `json:"sender"`
	Recipient       string  `json:"recipient"`
	Rate            float64 `json:"rate"`
	Status          string  `json:"status"`          // INIT, PAID, COMPLETED, FAILED
	TransactionType string  `json:"transactionType"` // ON_RAMP, OFF_RAMP
}

// --- HTTP helpers ---

func (c *Client) post(ctx context.Context, path string, body interface{}, sessionToken *string, dest interface{}) error {
	return c.do(ctx, http.MethodPost, path, body, sessionToken, dest)
}

func (c *Client) get(ctx context.Context, path string, sessionToken *string, dest interface{}) error {
	return c.do(ctx, http.MethodGet, path, nil, sessionToken, dest)
}

func (c *Client) do(ctx context.Context, method, path string, body interface{}, sessionToken *string, dest interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			// Re-create reader for retry
			if body != nil {
				b, _ := json.Marshal(body)
				bodyReader = bytes.NewReader(b)
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, bodyReader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.cfg.APIKey)
		if sessionToken != nil {
			req.Header.Set("Authorization", "Bearer "+*sessionToken)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("paj request failed: %w", err)
			c.logger.Warn("Paj request failed", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("paj returned %d: %s", resp.StatusCode, string(respBody))
			c.logger.Warn("Paj retryable error", zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			c.logger.Error("Paj API error", zap.Int("status", resp.StatusCode), zap.String("body", string(respBody)), zap.String("path", path))
			return fmt.Errorf("paj returned %d: %s", resp.StatusCode, string(respBody))
		}

		if dest != nil {
			if err := json.Unmarshal(respBody, dest); err != nil {
				return fmt.Errorf("unmarshal response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("paj %s %s failed after %d attempts: %w", method, path, c.cfg.MaxRetries+1, lastErr)
}
