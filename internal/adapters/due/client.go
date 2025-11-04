package due

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sony/gobreaker"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.uber.org/zap"
)

const (
	// Default timeouts and limits
	defaultTimeout    = 30 * time.Second
	maxRetries        = 3
	baseBackoff       = 1 * time.Second
	maxBackoff        = 16 * time.Second
	jitterRange       = 0.1 // 10% jitter
	defaultRetryAfter = 5 * time.Second
	maxRetryAfter     = 60 * time.Second

	// Due API rate limits (requests per minute) - conservative defaults
	dueRateLimitRPM = 60
	rateLimitBurst  = 10
)

// Config represents Due API configuration
type Config struct {
	APIKey         string
	APISecret      string
	BaseURL        string // Due API base URL
	Environment    string // sandbox or production
	Timeout        time.Duration
	RateLimitRPM   int // Requests per minute (0 = use default)
	RateLimitBurst int // Burst capacity (0 = use default)
}

// Client represents a Due API client
type Client struct {
	config         Config
	httpClient     *http.Client
	circuitBreaker *gobreaker.CircuitBreaker
	rateLimiter    *time.Ticker
	requestTokens  chan struct{}
	logger         *zap.Logger
}

// NewClient creates a new Due API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}

	// Set default rate limits if not provided
	if config.RateLimitRPM == 0 {
		config.RateLimitRPM = dueRateLimitRPM
	}
	if config.RateLimitBurst == 0 {
		config.RateLimitBurst = rateLimitBurst
	}

	if config.BaseURL == "" {
	if config.Environment == "production" {
	config.BaseURL = "https://api.due.network"
	} else {
	config.BaseURL = "https://api.sandbox.due.network"
	}
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")

	httpClient := &http.Client{
		Timeout: config.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Initialize rate limiter
	rateLimiter := time.NewTicker(time.Minute / time.Duration(config.RateLimitRPM))
	requestTokens := make(chan struct{}, config.RateLimitBurst)

	// Fill initial burst capacity
	for i := 0; i < config.RateLimitBurst; i++ {
		requestTokens <- struct{}{}
	}

	// Token replenishment goroutine
	go func() {
		for range rateLimiter.C {
			select {
			case requestTokens <- struct{}{}:
			default:
				// Channel is full, skip this token
			}
		}
	}()

	st := gobreaker.Settings{
		Name:    "DueAPI",
		MaxRequests: 5,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))
		},
	}

	circuitBreaker := gobreaker.NewCircuitBreaker(st)

	return &Client{
		config:         config,
		httpClient:     httpClient,
		circuitBreaker: circuitBreaker,
		rateLimiter:    rateLimiter,
		requestTokens:  requestTokens,
		logger:         logger,
	}
}

// CreateVirtualAccount creates a new virtual account through the Due API
func (c *Client) CreateVirtualAccount(ctx context.Context, userID string, destination string, schemaIn string, currencyIn string, railOut string, currencyOut string) (*entities.VirtualAccount, error) {
c.logger.Info("Creating Due virtual account",
zap.String("user_id", userID),
  zap.String("destination", destination),
 zap.String("schema_in", schemaIn),
zap.String("currency_in", currencyIn),
zap.String("rail_out", railOut),
 zap.String("currency_out", currencyOut))
 
 	req := CreateVirtualAccountRequest{
 		Destination: destination,
 		SchemaIn:    schemaIn,
 		CurrencyIn:  currencyIn,
 		RailOut:     railOut,
 		CurrencyOut: currencyOut,
 		Reference:   userID, // Use userID as reference for tracking
 	}

	var response CreateVirtualAccountResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", "/v1/virtual_accounts", req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due virtual account",
			zap.String("user_id", userID),
			zap.Error(err))
		return nil, fmt.Errorf("create virtual account failed: %w", err)
	}

	// Parse userID from string to UUID
	parsedUserID, err := uuid.Parse(userID)
	if err != nil {
	c.logger.Error("Invalid user ID format",
	zap.String("user_id", userID),
	zap.Error(err))
	return nil, fmt.Errorf("invalid user ID: %w", err)
	}
	
	// Convert Due API response to our domain entity
	status := entities.VirtualAccountStatusCreating
	if response.IsActive {
	status = entities.VirtualAccountStatusActive
	}
	
	virtualAccount := &entities.VirtualAccount{
	ID:               uuid.New(),
	UserID:           parsedUserID,
	 DueAccountID:     response.Nonce, // Use nonce as the Due account identifier
 		BrokerageAccountID: "", // Will be set later during linking
 		Status:           status,
 		CreatedAt:        response.CreatedAt,
 		UpdatedAt:        response.CreatedAt, // API doesn't provide UpdatedAt, use CreatedAt
 	}

	c.logger.Info("Created Due virtual account successfully",
		zap.String("virtual_account_id", virtualAccount.ID.String()),
		zap.String("due_account_id", virtualAccount.DueAccountID),
		zap.String("status", string(virtualAccount.Status)))

	return virtualAccount, nil
}

// CreateAccount creates a new Due account for a user
func (c *Client) CreateAccount(ctx context.Context, req CreateAccountRequest) (*Account, error) {
	c.logger.Info("Creating Due account",
		zap.String("type", req.Type),
		zap.String("name", req.Name),
		zap.String("email", req.Email),
		zap.String("country", req.Country))

	var response Account
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", "/v1/accounts", req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due account",
			zap.String("email", req.Email),
			zap.Error(err))
		return nil, fmt.Errorf("create account failed: %w", err)
	}

	c.logger.Info("Created Due account successfully",
		zap.String("account_id", response.ID),
		zap.String("status", response.Status))

	return &response, nil
	}

// CreateQuote creates a transfer quote for crypto-to-fiat conversion
func (c *Client) CreateQuote(ctx context.Context, req CreateQuoteRequest) (*CreateQuoteResponse, error) {
	c.logger.Info("Creating Due transfer quote",
		zap.String("source_rail", req.Source.Rail),
		zap.String("source_currency", req.Source.Currency),
		zap.String("source_amount", req.Source.Amount),
		zap.String("dest_rail", req.Destination.Rail),
		zap.String("dest_currency", req.Destination.Currency))

	var response CreateQuoteResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", "/v1/transfers/quote", req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due transfer quote", zap.Error(err))
		return nil, fmt.Errorf("create quote failed: %w", err)
	}

	c.logger.Info("Created Due transfer quote successfully",
		zap.String("quote_token", response.Token),
		zap.Time("expires_at", response.ExpiresAt))

	return &response, nil
}

// CreateTransfer initiates a crypto-to-fiat transfer
func (c *Client) CreateTransfer(ctx context.Context, req CreateTransferRequest) (*CreateTransferResponse, error) {
	c.logger.Info("Creating Due transfer",
		zap.String("quote", req.Quote),
		zap.String("sender", req.Sender),
		zap.String("recipient", req.Recipient),
		zap.String("memo", req.Memo))

	var response CreateTransferResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", "/v1/transfers", req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due transfer",
			zap.String("quote", req.Quote),
			zap.Error(err))
		return nil, fmt.Errorf("create transfer failed: %w", err)
	}

	c.logger.Info("Created Due transfer successfully",
		zap.String("transfer_id", response.ID),
		zap.String("status", response.Status),
		zap.Time("expires_at", response.ExpiresAt))

	return &response, nil
}

// CreateTransferIntent generates blockchain transaction data for signing
func (c *Client) CreateTransferIntent(ctx context.Context, transferID string) (*CreateTransferIntentResponse, error) {
	c.logger.Info("Creating Due transfer intent", zap.String("transfer_id", transferID))

	var response CreateTransferIntentResponse
	endpoint := fmt.Sprintf("/v1/transfers/%s/transfer_intent", transferID)
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due transfer intent",
			zap.String("transfer_id", transferID),
			zap.Error(err))
		return nil, fmt.Errorf("create transfer intent failed: %w", err)
	}

	c.logger.Info("Created Due transfer intent successfully",
		zap.String("intent_id", response.ID),
		zap.Int("signables_count", len(response.Signables)))

	return &response, nil
}

// SubmitTransferIntent submits a signed transfer intent to execute the transfer
func (c *Client) SubmitTransferIntent(ctx context.Context, req SubmitTransferIntentRequest) error {
	c.logger.Info("Submitting Due transfer intent",
		zap.String("intent_id", req.ID),
		zap.String("transfer_ref", req.Reference))

	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return nil, c.doRequestWithRetry(ctx, "POST", "/v1/transfer_intents/submit", req, nil, false)
	})

	if err != nil {
		c.logger.Error("Failed to submit Due transfer intent",
			zap.String("intent_id", req.ID),
			zap.Error(err))
		return fmt.Errorf("submit transfer intent failed: %w", err)
	}

	c.logger.Info("Submitted Due transfer intent successfully",
		zap.String("intent_id", req.ID))

	return nil
}

// CreateFundingAddress creates a temporary deposit address for direct transfers
func (c *Client) CreateFundingAddress(ctx context.Context, transferID string) (*CreateFundingAddressResponse, error) {
	c.logger.Info("Creating Due funding address", zap.String("transfer_id", transferID))

	var response CreateFundingAddressResponse
	endpoint := fmt.Sprintf("/v1/transfers/%s/funding_address", transferID)
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Due funding address",
			zap.String("transfer_id", transferID),
			zap.Error(err))
		return nil, fmt.Errorf("create funding address failed: %w", err)
	}

	c.logger.Info("Created Due funding address successfully",
		zap.String("transfer_id", transferID),
		zap.String("address", response.Details.Address))

	return &response, nil
}

// GetTransfer retrieves transfer details by ID
func (c *Client) GetTransfer(ctx context.Context, transferID string) (*GetTransferResponse, error) {
	c.logger.Info("Getting Due transfer details", zap.String("transfer_id", transferID))

	var response GetTransferResponse
	endpoint := fmt.Sprintf("/v1/transfers/%s", transferID)
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Due transfer details",
			zap.String("transfer_id", transferID),
			zap.Error(err))
		return nil, fmt.Errorf("get transfer failed: %w", err)
	}

	c.logger.Info("Got Due transfer details successfully",
		zap.String("transfer_id", transferID),
		zap.String("status", response.Status))

	return &response, nil
}

// LinkWallet links a wallet address to a Due account
// Automatically formats the wallet address based on blockchain type
func (c *Client) LinkWallet(ctx context.Context, accountID, walletAddress, blockchain string) (*Wallet, error) {
	c.logger.Info("Linking wallet to Due account",
		zap.String("account_id", accountID),
		zap.String("wallet_address", walletAddress),
		zap.String("blockchain", blockchain))

	// Format wallet address according to Due API requirements
	formattedAddress, err := formatWalletAddressForDue(walletAddress, blockchain)
	if err != nil {
		c.logger.Error("Failed to format wallet address",
			zap.String("wallet_address", walletAddress),
			zap.String("blockchain", blockchain),
			zap.Error(err))
		return nil, fmt.Errorf("invalid wallet address format: %w", err)
	}

	req := LinkWalletRequest{
		Address: formattedAddress,
	}

	var response Wallet
	_, err = c.circuitBreaker.Execute(func() (interface{}, error) {
		// Create request with Due-Account-Id header
		return &response, c.doWalletRequestWithRetry(ctx, "POST", "/v1/wallets", accountID, req, &response)
	})

	if err != nil {
		c.logger.Error("Failed to link wallet to Due account",
			zap.String("account_id", accountID),
			zap.String("wallet_address", walletAddress),
			zap.String("formatted_address", formattedAddress),
			zap.Error(err))
		return nil, fmt.Errorf("link wallet failed: %w", err)
	}

	c.logger.Info("Linked wallet to Due account successfully",
		zap.String("wallet_id", response.ID),
		zap.String("account_id", response.AccountID),
		zap.String("formatted_address", formattedAddress))

	return &response, nil
}

// formatWalletAddressForDue formats wallet addresses according to Due API requirements
// Due expects addresses in the format: "evm:0x..." or "starknet:0x..."
func formatWalletAddressForDue(address, blockchain string) (string, error) {
	if address == "" {
		return "", fmt.Errorf("wallet address cannot be empty")
	}

	// If address already has a schema prefix, validate and return it
	if strings.Contains(address, ":") {
		parts := strings.Split(address, ":")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid address format: %s", address)
		}
		schema := parts[0]
		if schema != "evm" && schema != "starknet" {
			return "", fmt.Errorf("unsupported address schema: %s", schema)
		}
		return address, nil
	}

	// Determine schema based on blockchain
	var schema string
	switch strings.ToUpper(blockchain) {
	case "ETH", "ETH-SEPOLIA", "ETHEREUM", "ETHEREUM-SEPOLIA":
		schema = "evm"
	case "MATIC", "MATIC-AMOY", "POLYGON", "POLYGON-AMOY":
		schema = "evm"
	case "BASE", "BASE-SEPOLIA":
		schema = "evm"
	case "AVAX", "AVALANCHE":
		schema = "evm"
	case "STARKNET":
		schema = "starknet"
	default:
		// Default to evm for unknown EVM-compatible chains
		schema = "evm"
	}

	// Validate address format
	if !strings.HasPrefix(address, "0x") {
		return "", fmt.Errorf("invalid address format, must start with 0x: %s", address)
	}

	// Return formatted address
	return fmt.Sprintf("%s:%s", schema, address), nil
}

// ListWallets retrieves all wallets linked to a Due account
func (c *Client) ListWallets(ctx context.Context, accountID string) ([]Wallet, error) {
	c.logger.Info("Listing wallets for Due account",
		zap.String("account_id", accountID))

	var response []Wallet
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doWalletRequestWithRetry(ctx, "GET", "/v1/wallets", accountID, nil, &response)
	})

	if err != nil {
		c.logger.Error("Failed to list wallets for Due account",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("list wallets failed: %w", err)
	}

	c.logger.Info("Listed wallets for Due account successfully",
		zap.String("account_id", accountID),
		zap.Int("wallet_count", len(response)))

	return response, nil
}

// GetAccount retrieves account details by ID
func (c *Client) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	c.logger.Info("Getting Due account details",
		zap.String("account_id", accountID))

	var response Account
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", "/v1/accounts/"+accountID, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Due account details",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	c.logger.Info("Got Due account details successfully",
		zap.String("account_id", response.ID),
		zap.String("status", response.Status))

	return &response, nil
}

// doWalletRequestWithRetry performs an HTTP request with exponential backoff retry for wallet endpoints
func (c *Client) doWalletRequestWithRetry(ctx context.Context, method, endpoint, accountID string, body, response interface{}) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(attempt)
			c.logger.Info("Retrying Due wallet API request",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Acquire rate limit token
		select {
		case <-c.requestTokens:
			// Token acquired, proceed
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.config.Timeout):
			return fmt.Errorf("rate limit token acquisition timeout")
		}

		err := c.doWalletRequest(ctx, method, endpoint, accountID, body, response)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			c.logger.Warn("Non-retryable error encountered",
				zap.Error(err))
			return err
		}

		c.logger.Warn("Retryable error encountered",
			zap.Error(err),
			zap.Int("attempt", attempt))
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doWalletRequest performs a single HTTP request for wallet endpoints (requires Due-Account-Id header)
func (c *Client) doWalletRequest(ctx context.Context, method, endpoint, accountID string, body, response interface{}) error {
	baseURL := c.config.BaseURL
	fullURL := baseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Due-Account-Id", accountID)

	// Due API authentication
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		// Or use API key/secret headers if that's the auth method
		req.Header.Set("X-API-Key", c.config.APIKey)
		if c.config.APISecret != "" {
			req.Header.Set("X-API-Secret", c.config.APISecret)
		}
	}

	c.logger.Debug("Sending Due wallet API request",
		zap.String("method", method),
		zap.String("url", fullURL),
		zap.String("account_id", accountID))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Due wallet API response",
		zap.Int("status_code", resp.StatusCode),
		zap.Int("body_size", len(respBody)))

	// Check for error responses
	if resp.StatusCode >= 400 {
		var apiErr DueErrorResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Message != "" {
			apiErr.Code = resp.StatusCode

			// Handle rate limiting specifically
			if resp.StatusCode == http.StatusTooManyRequests {
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if seconds, err := strconv.Atoi(retryAfter); err == nil {
						c.logger.Warn("Rate limited by Due API",
							zap.Int("retry_after_seconds", seconds),
							zap.String("endpoint", endpoint))
					}
				}
			}

			return &apiErr
		}
		return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse response if a response object is provided
	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// doRequestWithRetry performs an HTTP request with exponential backoff retry
func (c *Client) doRequestWithRetry(ctx context.Context, method, endpoint string, body, response interface{}, useDataAPI bool) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := calculateBackoff(attempt)
			c.logger.Info("Retrying Due API request",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Acquire rate limit token
		select {
		case <-c.requestTokens:
			// Token acquired, proceed
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.config.Timeout):
			return fmt.Errorf("rate limit token acquisition timeout")
		}

		err := c.doRequest(ctx, method, endpoint, body, response, useDataAPI)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			c.logger.Warn("Non-retryable error encountered",
				zap.Error(err))
			return err
		}

		c.logger.Warn("Retryable error encountered",
			zap.Error(err),
			zap.Int("attempt", attempt))
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doRequest performs a single HTTP request
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body, response interface{}, useDataAPI bool) error {
	baseURL := c.config.BaseURL
	if useDataAPI {
		baseURL = c.config.BaseURL // Due API doesn't have separate data API
	}

	fullURL := baseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Due API authentication
	if c.config.APIKey != "" {
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	if c.config.APISecret != "" {
	req.Header.Set("Due-Account-Id", c.config.APISecret) // APISecret used for account ID
	}

	c.logger.Debug("Sending Due API request",
		zap.String("method", method),
		zap.String("url", fullURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Due API response",
		zap.Int("status_code", resp.StatusCode),
		zap.Int("body_size", len(respBody)))

	// Check for error responses
	if resp.StatusCode >= 400 {
		var apiErr DueErrorResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Message != "" {
			apiErr.Code = resp.StatusCode

			// Handle rate limiting specifically
			if resp.StatusCode == http.StatusTooManyRequests {
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if seconds, err := strconv.Atoi(retryAfter); err == nil {
						c.logger.Warn("Rate limited by Due API",
							zap.Int("retry_after_seconds", seconds),
							zap.String("endpoint", endpoint))
					}
				}
			}

			return &apiErr
		}
		return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	// Parse response if a response object is provided
	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// Close gracefully shuts down the client and cleans up resources
func (c *Client) Close() error {
	if c.rateLimiter != nil {
		c.rateLimiter.Stop()
	}
	c.logger.Info("Due client closed")
	return nil
}

// GetMetrics returns circuit breaker and client metrics for monitoring
func (c *Client) GetMetrics() map[string]interface{} {
	counts := c.circuitBreaker.Counts()
	return map[string]interface{}{
		"circuit_breaker_state":         c.circuitBreaker.State().String(),
		"requests_total":                counts.Requests,
		"consecutive_successes":         counts.ConsecutiveSuccesses,
		"consecutive_failures":          counts.ConsecutiveFailures,
		"total_successes":               counts.TotalSuccesses,
		"total_failures":                counts.TotalFailures,
		"rate_limiter_tokens_available": len(c.requestTokens),
		"rate_limiter_burst_capacity":   cap(c.requestTokens),
		"client_timeout_seconds":        c.config.Timeout.Seconds(),
		"environment":                   c.config.Environment,
	}
}

// calculateBackoff calculates exponential backoff with jitter
func calculateBackoff(attempt int) time.Duration {
	// Calculate exponential backoff: baseBackoff * 2^(attempt-1)
	backoff := float64(baseBackoff) * math.Pow(2, float64(attempt-1))

	// Apply max backoff limit
	if backoff > float64(maxBackoff) {
		backoff = float64(maxBackoff)
	}

	// Add jitter (±10%)
	jitter := backoff * jitterRange * (2*getRandomFloat() - 1)
	backoff += jitter

	return time.Duration(backoff)
}

// getRandomFloat returns a random float between 0 and 1
func getRandomFloat() float64 {
	return float64(time.Now().UnixNano()%1000) / 1000.0
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Due API errors
	if apiErr, ok := err.(*DueErrorResponse); ok {
		// Retry on rate limits and server errors
		switch apiErr.Code {
		case http.StatusTooManyRequests:
			return true // Rate limited, worth retrying after backoff
		case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true // Server errors, worth retrying
		case http.StatusRequestTimeout:
			return true // Request timeout, worth retrying
		default:
			return false // Client errors (4xx except 429) should not be retried
		}
	}

	// Retry on network errors and timeouts
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection closed") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "no such host")
}

// ListVirtualAccounts retrieves all virtual accounts for the account
 func (c *Client) ListVirtualAccounts(ctx context.Context, filters map[string]string) ([]VirtualAccountSummary, error) {
 	c.logger.Info("Listing Due virtual accounts")

 	var response ListVirtualAccountsResponse
 	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
 		return &response, c.doRequestWithRetry(ctx, "GET", "/v1/virtual_accounts", nil, &response, false)
 	})

 	if err != nil {
 		c.logger.Error("Failed to list Due virtual accounts", zap.Error(err))
 		return nil, fmt.Errorf("list virtual accounts failed: %w", err)
 	}

 	c.logger.Info("Listed Due virtual accounts successfully", zap.Int("count", len(response.VirtualAccounts)))

 	return response.VirtualAccounts, nil
 }

// GetVirtualAccount retrieves details for a specific virtual account by reference
 func (c *Client) GetVirtualAccount(ctx context.Context, reference string) (*GetVirtualAccountResponse, error) {
 	c.logger.Info("Getting Due virtual account", zap.String("reference", reference))

 	var response GetVirtualAccountResponse
 	endpoint := "/v1/virtual_accounts/" + reference
 	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
 		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
 	})

 	if err != nil {
 		c.logger.Error("Failed to get Due virtual account", zap.String("reference", reference), zap.Error(err))
 		return nil, fmt.Errorf("get virtual account failed: %w", err)
 	}

 	c.logger.Info("Got Due virtual account successfully", zap.String("reference", reference))

 	return &response, nil
 	}
