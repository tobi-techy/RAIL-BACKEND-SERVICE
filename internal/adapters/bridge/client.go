package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// idempotencyKeyCtxKey is the context key for idempotency keys
type idempotencyKeyCtxKey struct{}

// WithIdempotencyKey returns a context with the specified idempotency key
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtxKey{}, key)
}

// getIdempotencyKey retrieves idempotency key from context or generates a deterministic one
func getIdempotencyKey(ctx context.Context, method, endpoint string, body []byte) string {
	// Check if caller provided an idempotency key
	if key, ok := ctx.Value(idempotencyKeyCtxKey{}).(string); ok && key != "" {
		return key
	}
	
	// Generate deterministic key based on request content for automatic retry safety
	// This ensures the same logical operation always gets the same key
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(endpoint))
	if len(body) > 0 {
		h.Write(body)
	}
	// Add a timestamp component (truncated to minute) to allow retrying failed operations
	// after a reasonable time window while still protecting against immediate duplicates
	h.Write([]byte(time.Now().UTC().Truncate(time.Minute).Format(time.RFC3339)))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// GenerateIdempotencyKey creates a new unique idempotency key for one-time use
func GenerateIdempotencyKey() string {
	return uuid.New().String()
}

// Config represents Bridge API configuration
type Config struct {
	APIKey      string
	BaseURL     string
	Environment string // "sandbox" or "production"
	Timeout     time.Duration
	MaxRetries  int
}

// Client represents a Bridge API client
type Client struct {
	config     Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a new Bridge API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.bridge.xyz"
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	return &Client{
		config:     config,
		httpClient: &http.Client{Timeout: config.Timeout},
		logger:     logger,
	}
}

// CreateCustomer creates a new customer
func (c *Client) CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*Customer, error) {
	var customer Customer
	if err := c.doRequest(ctx, http.MethodPost, "/v0/customers", req, &customer); err != nil {
		return nil, fmt.Errorf("create customer failed: %w", err)
	}
	return &customer, nil
}

// GetCustomer retrieves a customer by ID
func (c *Client) GetCustomer(ctx context.Context, customerID string) (*Customer, error) {
	var customer Customer
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s", url.PathEscape(customerID)), nil, &customer); err != nil {
		return nil, fmt.Errorf("get customer failed: %w", err)
	}
	return &customer, nil
}

// UpdateCustomer updates a customer
func (c *Client) UpdateCustomer(ctx context.Context, customerID string, req *CreateCustomerRequest) (*Customer, error) {
	var customer Customer
	if err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/v0/customers/%s", url.PathEscape(customerID)), req, &customer); err != nil {
		return nil, fmt.Errorf("update customer failed: %w", err)
	}
	return &customer, nil
}

// ListCustomers lists all customers
func (c *Client) ListCustomers(ctx context.Context, cursor string, limit int) (*ListCustomersResponse, error) {
	endpoint := "/v0/customers"
	params := url.Values{}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	var resp ListCustomersResponse
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("list customers failed: %w", err)
	}
	return &resp, nil
}

// GetKYCLink retrieves a KYC link for a customer
func (c *Client) GetKYCLink(ctx context.Context, customerID string) (*KYCLinkResponse, error) {
	var resp KYCLinkResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/kyc_link", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("get KYC link failed: %w", err)
	}
	return &resp, nil
}

// GetTOSLink retrieves a Terms of Service link for a customer
func (c *Client) GetTOSLink(ctx context.Context, customerID string) (*TOSLinkResponse, error) {
	var resp TOSLinkResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/tos_acceptance_link", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("get TOS link failed: %w", err)
	}
	return &resp, nil
}

// CreateVirtualAccount creates a virtual account for a customer
func (c *Client) CreateVirtualAccount(ctx context.Context, customerID string, req *CreateVirtualAccountRequest) (*VirtualAccount, error) {
	var va VirtualAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/virtual_accounts", url.PathEscape(customerID)), req, &va); err != nil {
		return nil, fmt.Errorf("create virtual account failed: %w", err)
	}
	return &va, nil
}

// GetVirtualAccount retrieves a virtual account
func (c *Client) GetVirtualAccount(ctx context.Context, customerID, virtualAccountID string) (*VirtualAccount, error) {
	var va VirtualAccount
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/virtual_accounts/%s", url.PathEscape(customerID), url.PathEscape(virtualAccountID)), nil, &va); err != nil {
		return nil, fmt.Errorf("get virtual account failed: %w", err)
	}
	return &va, nil
}

// ListVirtualAccounts lists virtual accounts for a customer
func (c *Client) ListVirtualAccounts(ctx context.Context, customerID string) (*ListVirtualAccountsResponse, error) {
	var resp ListVirtualAccountsResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/virtual_accounts", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list virtual accounts failed: %w", err)
	}
	return &resp, nil
}

// DeactivateVirtualAccount deactivates a virtual account
func (c *Client) DeactivateVirtualAccount(ctx context.Context, customerID, virtualAccountID string) (*VirtualAccount, error) {
	var va VirtualAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/virtual_accounts/%s/deactivate", url.PathEscape(customerID), url.PathEscape(virtualAccountID)), nil, &va); err != nil {
		return nil, fmt.Errorf("deactivate virtual account failed: %w", err)
	}
	return &va, nil
}

// CreateWallet creates a custodial wallet for a customer
func (c *Client) CreateWallet(ctx context.Context, customerID string, req *CreateWalletRequest) (*Wallet, error) {
	var wallet Wallet
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/wallets", url.PathEscape(customerID)), req, &wallet); err != nil {
		return nil, fmt.Errorf("create wallet failed: %w", err)
	}
	return &wallet, nil
}

// GetWallet retrieves a wallet
func (c *Client) GetWallet(ctx context.Context, customerID, walletID string) (*Wallet, error) {
	var wallet Wallet
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/wallets/%s", url.PathEscape(customerID), url.PathEscape(walletID)), nil, &wallet); err != nil {
		return nil, fmt.Errorf("get wallet failed: %w", err)
	}
	return &wallet, nil
}

// ListWallets lists wallets for a customer
func (c *Client) ListWallets(ctx context.Context, customerID string) (*ListWalletsResponse, error) {
	var resp ListWalletsResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/wallets", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list wallets failed: %w", err)
	}
	return &resp, nil
}

// GetWalletBalance retrieves wallet balance
func (c *Client) GetWalletBalance(ctx context.Context, customerID, walletID string) (*WalletBalance, error) {
	var balance WalletBalance
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/wallets/%s/balance", url.PathEscape(customerID), url.PathEscape(walletID)), nil, &balance); err != nil {
		return nil, fmt.Errorf("get wallet balance failed: %w", err)
	}
	return &balance, nil
}

// CreateCardAccount creates a card account for a customer
func (c *Client) CreateCardAccount(ctx context.Context, customerID string, req *CreateCardAccountRequest) (*CardAccount, error) {
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts", url.PathEscape(customerID)), req, &card); err != nil {
		return nil, fmt.Errorf("create card account failed: %w", err)
	}
	return &card, nil
}

// GetCardAccount retrieves a card account
func (c *Client) GetCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error) {
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/card_accounts/%s", url.PathEscape(customerID), url.PathEscape(cardAccountID)), nil, &card); err != nil {
		return nil, fmt.Errorf("get card account failed: %w", err)
	}
	return &card, nil
}

// FreezeCardAccount freezes a card account
func (c *Client) FreezeCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error) {
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/freeze", url.PathEscape(customerID), url.PathEscape(cardAccountID)), nil, &card); err != nil {
		return nil, fmt.Errorf("freeze card account failed: %w", err)
	}
	return &card, nil
}

// UnfreezeCardAccount unfreezes a card account
func (c *Client) UnfreezeCardAccount(ctx context.Context, customerID, cardAccountID string) (*CardAccount, error) {
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/unfreeze", url.PathEscape(customerID), url.PathEscape(cardAccountID)), nil, &card); err != nil {
		return nil, fmt.Errorf("unfreeze card account failed: %w", err)
	}
	return &card, nil
}

// CreateTransfer creates a transfer
func (c *Client) CreateTransfer(ctx context.Context, req *CreateTransferRequest) (*Transfer, error) {
	var transfer Transfer
	if err := c.doRequest(ctx, http.MethodPost, "/v0/transfers", req, &transfer); err != nil {
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}
	return &transfer, nil
}

// GetTransfer retrieves a transfer
func (c *Client) GetTransfer(ctx context.Context, transferID string) (*Transfer, error) {
	var transfer Transfer
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/transfers/%s", url.PathEscape(transferID)), nil, &transfer); err != nil {
		return nil, fmt.Errorf("get transfer failed: %w", err)
	}
	return &transfer, nil
}

// ListTransfers lists transfers for a customer
func (c *Client) ListTransfers(ctx context.Context, customerID string) (*ListTransfersResponse, error) {
	var resp ListTransfersResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/transfers", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list transfers failed: %w", err)
	}
	return &resp, nil
}

// Ping tests connectivity to the Bridge API
func (c *Client) Ping(ctx context.Context) error {
	// Use list customers with limit 1 as a health check
	_, err := c.ListCustomers(ctx, "", 1)
	return err
}

// doRequest performs an HTTP request to the Bridge API with retry logic
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body, response interface{}) error {
	fullURL := c.config.BaseURL + endpoint

	var reqBody []byte
	if body != nil {
		var err error
		reqBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s...
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			c.logger.Debug("Retrying Bridge API request", zap.Int("attempt", attempt), zap.String("method", method), zap.String("url", fullURL))
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Api-Key", c.config.APIKey)
		if method == http.MethodPost || method == http.MethodPut {
			idempotencyKey := getIdempotencyKey(ctx, method, endpoint, reqBody)
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		c.logger.Debug("Sending Bridge API request", zap.String("method", method), zap.String("url", fullURL))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue // Retry on network errors
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			continue
		}

		c.logger.Debug("Received Bridge API response", zap.Int("status_code", resp.StatusCode), zap.Int("body_size", len(respBody)))

		// Retry on 5xx errors
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
			continue
		}

		if resp.StatusCode >= 400 {
			var errResp ErrorResponse
			if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Message != "" {
				errResp.StatusCode = resp.StatusCode
				return &errResp
			}
			return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
		}

		if response != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, response); err != nil {
				return fmt.Errorf("failed to unmarshal response: %w", err)
			}
		}

		return nil
	}

	return lastErr
}

// Config returns the client configuration
func (c *Client) Config() Config {
	return c.config
}
