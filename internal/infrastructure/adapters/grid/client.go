package grid

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"
)

const (
	defaultBaseURL = "https://api.squads.so"
	defaultTimeout = 30 * time.Second
	maxRetries     = 3
)

// Config represents Grid API configuration
type Config struct {
	APIKey      string
	Environment string // "sandbox" or "mainnet"
	BaseURL     string
	Timeout     time.Duration
}

// Client represents a Grid API client
type Client struct {
	config         Config
	httpClient     *http.Client
	circuitBreaker *gobreaker.CircuitBreaker
	logger         *zap.Logger
}

// NewClient creates a new Grid API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}

	httpClient := &http.Client{Timeout: config.Timeout}

	cbSettings := gobreaker.Settings{
		Name:        "GridAPI",
		MaxRequests: 5,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("Grid circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))
		},
	}

	return &Client{
		config:         config,
		httpClient:     httpClient,
		circuitBreaker: gobreaker.NewCircuitBreaker(cbSettings),
		logger:         logger,
	}
}

// CreateAccount initiates account creation by sending OTP to email
func (c *Client) CreateAccount(ctx context.Context, email string) (*AccountCreationResponse, error) {
	req := CreateAccountRequest{Email: email}
	var resp AccountCreationResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/grid/v1/accounts", req, &resp); err != nil {
		return nil, fmt.Errorf("create account failed: %w", err)
	}
	return &resp, nil
}

// VerifyOTP verifies the OTP and creates the account
func (c *Client) VerifyOTP(ctx context.Context, email, otp string, sessionSecrets *SessionSecrets) (*Account, error) {
	req := VerifyOTPRequest{
		Email:          email,
		Code:           otp,
		SessionSecrets: sessionSecrets,
	}
	var resp VerifyOTPResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/grid/v1/accounts/verify", req, &resp); err != nil {
		return nil, fmt.Errorf("verify OTP failed: %w", err)
	}
	return &Account{
		Address: resp.Address,
		Email:   resp.Email,
		Status:  resp.Status,
	}, nil
}

// GetAccount retrieves account details by address
func (c *Client) GetAccount(ctx context.Context, address string) (*Account, error) {
	var resp Account
	endpoint := fmt.Sprintf("/api/grid/v1/accounts/%s", url.PathEscape(address))
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("get account failed: %w", err)
	}
	return &resp, nil
}

// GetAccountBalances retrieves account balances
func (c *Client) GetAccountBalances(ctx context.Context, address string) (*Balances, error) {
	var resp Balances
	endpoint := fmt.Sprintf("/api/grid/v1/accounts/%s/balances", url.PathEscape(address))
	if err := c.doRequest(ctx, http.MethodGet, endpoint, nil, &resp); err != nil {
		return nil, fmt.Errorf("get balances failed: %w", err)
	}
	return &resp, nil
}

// GenerateSessionSecrets generates Ed25519 keypairs for transaction signing
func (c *Client) GenerateSessionSecrets() (*SessionSecrets, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 keypair: %w", err)
	}
	return &SessionSecrets{
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}, nil
}

// Ping tests connectivity to the Grid API
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check failed: status %d", resp.StatusCode)
	}
	return nil
}

// doRequest performs an HTTP request with circuit breaker and retry logic
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body, response interface{}) error {
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return nil, c.doRequestInternal(ctx, method, endpoint, body, response)
	})
	return err
}

func (c *Client) doRequestInternal(ctx context.Context, method, endpoint string, body, response interface{}) error {
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
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			c.logger.Debug("Retrying Grid API request",
				zap.Int("attempt", attempt),
				zap.String("method", method),
				zap.String("url", fullURL))
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
		req.Header.Set("x-grid-environment", c.config.Environment)

		c.logger.Debug("Sending Grid API request",
			zap.String("method", method),
			zap.String("url", fullURL))

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			continue
		}

		c.logger.Debug("Received Grid API response",
			zap.Int("status_code", resp.StatusCode),
			zap.Int("body_size", len(respBody)))

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
