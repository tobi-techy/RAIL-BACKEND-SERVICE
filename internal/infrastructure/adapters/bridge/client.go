package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	emailRegex = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	phoneRegex = regexp.MustCompile(`\+?[0-9][\d\-\s()]{7,}`)
)

// sanitizeBody truncates a response body to maxLen and redacts email/phone patterns.
func sanitizeBody(body string, maxLen int) string {
	s := emailRegex.ReplaceAllString(body, "[REDACTED_EMAIL]")
	s = phoneRegex.ReplaceAllString(s, "[REDACTED_PHONE]")
	if len(s) > maxLen {
		s = s[:maxLen] + "...[truncated]"
	}
	return s
}

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

	// Generate deterministic key based on request content for true idempotency
	// Same request content = same key, ensuring retries don't create duplicates
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(endpoint))
	if len(body) > 0 {
		h.Write(body)
	}
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

	// Circuit breaker: track consecutive 5xx failures
	consecutiveFailures atomic.Int64
	circuitOpenUntil    atomic.Int64 // unix timestamp when circuit closes
}

// NewClient creates a new Bridge API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second // Production-appropriate timeout
	}
	if config.BaseURL == "" {
		if strings.EqualFold(strings.TrimSpace(config.Environment), "sandbox") {
			config.BaseURL = "https://api.sandbox.bridge.xyz"
		} else {
			config.BaseURL = "https://api.bridge.xyz"
		}
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
		logger: logger,
	}
}

// CreateCustomer creates a new customer
func (c *Client) CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*Customer, error) {
	// Debug log the request (body redacted for PII safety)
	c.logger.Info("Creating Bridge customer")

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

// DeleteCustomer permanently deletes a customer and their data from Bridge
func (c *Client) DeleteCustomer(ctx context.Context, customerID string) error {
	if err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/v0/customers/%s", url.PathEscape(customerID)), nil, nil); err != nil {
		return fmt.Errorf("delete customer failed: %w", err)
	}
	return nil
}

// UpdateCustomer updates a customer
func (c *Client) UpdateCustomer(ctx context.Context, customerID string, req *UpdateCustomerRequest) (*Customer, error) {
	var customer Customer
	if err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/v0/customers/%s", url.PathEscape(customerID)), req, &customer); err != nil {
		return nil, fmt.Errorf("update customer failed: %w", err)
	}
	return &customer, nil
}

// GetCustomerByEmail finds a customer by email address
func (c *Client) GetCustomerByEmail(ctx context.Context, email string) (*Customer, error) {
	endpoint := "/v0/customers?email=" + url.QueryEscape(email)
	var resp ListCustomersResponse
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("get customer by email failed: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, nil
	}
	return &resp.Data[0], nil
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
	c.logger.Info("Bridge KYC link response", zap.String("customer_id", customerID), zap.String("kyc_link", resp.KYCLink), zap.String("expires_at", resp.ExpiresAt))
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

// GetWalletBalance retrieves wallet balance (returns the wallet object which includes balances)
func (c *Client) GetWalletBalance(ctx context.Context, customerID, walletID string) (*WalletBalance, error) {
	var balance WalletBalance
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/wallets/%s", url.PathEscape(customerID), url.PathEscape(walletID)), nil, &balance); err != nil {
		return nil, fmt.Errorf("get wallet balance failed: %w", err)
	}
	return &balance, nil
}

// EnableCards enables cards for a developer account.
func (c *Client) EnableCards(ctx context.Context, req *EnableCardsRequest) error {
	if req == nil {
		req = &EnableCardsRequest{
			FundingStrategy: CardFundingStrategyTopUp,
		}
	}
	return c.doRequest(ctx, http.MethodPost, "/v0/cards/enable", req, nil)
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

// FreezeCardAccount freezes a card account with the given initiator and reason
func (c *Client) FreezeCardAccount(ctx context.Context, customerID, cardAccountID, initiator, reason string) (*CardAccount, error) {
	req := &FreezeCardAccountRequest{Initiator: initiator, Reason: reason}
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/freeze", url.PathEscape(customerID), url.PathEscape(cardAccountID)), req, &card); err != nil {
		return nil, fmt.Errorf("freeze card account failed: %w", err)
	}
	return &card, nil
}

// UnfreezeCardAccount unfreezes a card account with the given initiator
func (c *Client) UnfreezeCardAccount(ctx context.Context, customerID, cardAccountID, initiator string) (*CardAccount, error) {
	req := &UnfreezeCardAccountRequest{Initiator: initiator}
	var card CardAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/unfreeze", url.PathEscape(customerID), url.PathEscape(cardAccountID)), req, &card); err != nil {
		return nil, fmt.Errorf("unfreeze card account failed: %w", err)
	}
	return &card, nil
}

// CreateCardPINUpdateURL returns a signed URL for the customer to update their card PIN
func (c *Client) CreateCardPINUpdateURL(ctx context.Context, customerID, cardAccountID string) (*CardPINUpdateURLResponse, error) {
	var resp CardPINUpdateURLResponse
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/pin", url.PathEscape(customerID), url.PathEscape(cardAccountID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("create card PIN update URL failed: %w", err)
	}
	return &resp, nil
}

// CreateCardEphemeralKey creates a one-time ephemeral key for revealing card details to the frontend
func (c *Client) CreateCardEphemeralKey(ctx context.Context, customerID, cardAccountID, clientNonce string) (*EphemeralKeyResponse, error) {
	req := &EphemeralKeyRequest{ClientNonce: clientNonce}
	var resp EphemeralKeyResponse
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/card_accounts/%s/ephemeral_keys", url.PathEscape(customerID), url.PathEscape(cardAccountID)), req, &resp); err != nil {
		return nil, fmt.Errorf("create card ephemeral key failed: %w", err)
	}
	return &resp, nil
}

// GetCardStatement downloads a card statement PDF for the given period (e.g. "2025-01")
func (c *Client) GetCardStatement(ctx context.Context, customerID, cardAccountID, period string) ([]byte, error) {
	path := fmt.Sprintf("/v0/customers/%s/card_accounts/%s/statements/%s.pdf", url.PathEscape(customerID), url.PathEscape(cardAccountID), url.PathEscape(period))
	var raw []byte
	if err := c.doRequest(ctx, http.MethodPost, path, nil, &raw); err != nil {
		return nil, fmt.Errorf("get card statement failed: %w", err)
	}
	return raw, nil
}

// CreateExternalAccount registers a bank account as an ACH payout destination
func (c *Client) CreateExternalAccount(ctx context.Context, customerID string, req *CreateExternalAccountRequest) (*ExternalAccount, error) {
	var acct ExternalAccount
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/external_accounts", url.PathEscape(customerID)), req, &acct); err != nil {
		return nil, fmt.Errorf("create external account failed: %w", err)
	}
	return &acct, nil
}

// GetExternalAccount retrieves an external account
func (c *Client) GetExternalAccount(ctx context.Context, customerID, externalAccountID string) (*ExternalAccount, error) {
	var acct ExternalAccount
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/external_accounts/%s", url.PathEscape(customerID), url.PathEscape(externalAccountID)), nil, &acct); err != nil {
		return nil, fmt.Errorf("get external account failed: %w", err)
	}
	return &acct, nil
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

// ListTransfersByTemplateID fetches all transfer instances created from a static template
func (c *Client) ListTransfersByTemplateID(ctx context.Context, templateID string) (*ListTransfersResponse, error) {
	var resp ListTransfersResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/transfers?template_id=%s", url.QueryEscape(templateID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list transfers by template failed: %w", err)
	}
	return &resp, nil
}

// CreateLiquidationAddress creates a liquidation address for a customer
func (c *Client) CreateLiquidationAddress(ctx context.Context, customerID string, req *CreateLiquidationAddressRequest) (*LiquidationAddress, error) {
	var la LiquidationAddress
	if err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/v0/customers/%s/liquidation_addresses", url.PathEscape(customerID)), req, &la); err != nil {
		return nil, fmt.Errorf("create liquidation address failed: %w", err)
	}
	return &la, nil
}

// ListLiquidationAddresses lists liquidation addresses for a customer
func (c *Client) ListLiquidationAddresses(ctx context.Context, customerID string) (*ListLiquidationAddressesResponse, error) {
	var resp ListLiquidationAddressesResponse
	if err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/v0/customers/%s/liquidation_addresses", url.PathEscape(customerID)), nil, &resp); err != nil {
		return nil, fmt.Errorf("list liquidation addresses failed: %w", err)
	}
	return &resp, nil
}

// GetDrains retrieves the drain history for a liquidation address
func (c *Client) GetDrains(ctx context.Context, customerID, liquidationAddressID string) (*ListDrainsResponse, error) {
	var resp ListDrainsResponse
	path := fmt.Sprintf("/v0/customers/%s/liquidation_addresses/%s/drains", url.PathEscape(customerID), url.PathEscape(liquidationAddressID))
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("get drains failed: %w", err)
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

	// Use incoming context as parent, with client timeout as ceiling.
	// This ensures proper cancellation propagation while preventing unbounded waits.
	reqCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		// Circuit breaker: if open, fail fast
		if openUntil := c.circuitOpenUntil.Load(); openUntil > 0 && time.Now().Unix() < openUntil {
			return fmt.Errorf("bridge API circuit breaker open: too many consecutive 5xx errors")
		}

		if attempt > 0 {
			baseBackoff := time.Duration(1<<(attempt-1)) * time.Second
			jitter := time.Duration(rand.Float64() * 0.5 * float64(baseBackoff))
			backoff := baseBackoff/2 + jitter
			select {
			case <-reqCtx.Done():
				return reqCtx.Err()
			case <-time.After(backoff):
			}
			c.logger.Debug("Retrying Bridge API request", zap.Int("attempt", attempt), zap.String("method", method), zap.String("url", fullURL), zap.Duration("backoff", backoff))
		}

		req, err := http.NewRequestWithContext(reqCtx, method, fullURL, bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Api-Key", c.config.APIKey)
		if method == http.MethodPost {
			idempotencyKey := getIdempotencyKey(ctx, method, endpoint, reqBody)
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		// Inject distributed trace context
		injectTraceContext(ctx, req.Header)

		c.logger.Debug("Sending Bridge API request", zap.String("method", method), zap.String("url", fullURL))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue // Retry on network errors
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB max
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			continue
		}

		c.logger.Debug("Received Bridge API response", zap.Int("status_code", resp.StatusCode), zap.Int("body_size", len(respBody)))

		// Retry on 5xx errors
		if resp.StatusCode >= 500 {
			failures := c.consecutiveFailures.Add(1)
			if failures >= 5 {
				c.circuitOpenUntil.Store(time.Now().Add(30 * time.Second).Unix())
				c.logger.Warn("Bridge API circuit breaker opened", zap.Int64("consecutive_failures", failures))
			}
			lastErr = fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, sanitizeBody(string(respBody), 200))
			continue
		}

		// Reset circuit breaker on any non-5xx response
		c.consecutiveFailures.Store(0)
		c.circuitOpenUntil.Store(0)

		if resp.StatusCode >= 400 {
			var errResp ErrorResponse
			if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Message != "" {
				errResp.StatusCode = resp.StatusCode
				return &errResp
			}
			return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, sanitizeBody(string(respBody), 200))
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

// injectTraceContext injects OpenTelemetry trace context into HTTP headers
func injectTraceContext(ctx context.Context, headers http.Header) {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return
	}

	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()
	flags := "00"
	if span.SpanContext().IsSampled() {
		flags = "01"
	}
	headers.Set("traceparent", "00-"+traceID+"-"+spanID+"-"+flags)
}


