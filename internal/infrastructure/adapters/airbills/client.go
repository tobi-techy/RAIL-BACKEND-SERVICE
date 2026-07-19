package airbills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL    = "https://developer.airbills.org/api/vendor/gateway"
	defaultTimeout    = 20 * time.Second
	defaultMaxRetries = 2
	maxResponseSize   = 1 << 20 // 1MB
)

// Config holds Airbills API configuration.
type Config struct {
	SecretKey     string
	BaseURL       string
	CallbackURL   string
	WebhookSecret string
	Timeout       time.Duration
	MaxRetries    int
}

// Client wraps the Airbills business gateway API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient builds an Airbills client. SecretKey is required.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("airbills: SecretKey is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}, nil
}

// WebhookSecret returns the configured callback signing secret.
func (c *Client) WebhookSecret() string { return c.cfg.WebhookSecret }

// Transact initiates a bill payment. callbackUrl defaults to the configured
// CallbackURL when the request leaves it empty. Returns a StatusError when
// Airbills responds 2xx with a non-success status code.
func (c *Client) Transact(ctx context.Context, req TransactRequest) (*TransactResult, error) {
	if req.PayWith == "" {
		req.PayWith = PayWithTransfer
	}
	if req.CallbackURL == "" {
		req.CallbackURL = c.cfg.CallbackURL
	}
	var resp TransactResponse
	if err := c.post(ctx, "/transact", req, &resp); err != nil {
		return nil, fmt.Errorf("airbills transact: %w", err)
	}
	if resp.Status != StatusSuccess {
		return nil, &StatusError{Status: resp.Status, Message: resp.Message}
	}
	return &resp.Data, nil
}

// Process fulfils a pending transaction after the on-chain payment settles.
// Status "06" (already processed) is treated as success by the caller via
// ProcessResponse.Succeeded.
func (c *Client) Process(ctx context.Context, productCode, id string) (*ProcessResponse, error) {
	var resp ProcessResponse
	if err := c.post(ctx, "/transact/process", ProcessRequest{ProductCode: productCode, ID: id}, &resp); err != nil {
		return nil, fmt.Errorf("airbills process: %w", err)
	}
	if !resp.Succeeded() {
		return &resp, &StatusError{Status: resp.Status, Message: resp.Message}
	}
	return &resp, nil
}

// ListProducts returns the provider/plan options for a product category.
// segment is one of: elect, internet, bet, cable, transport. networkId narrows
// data plans to a carrier when provided.
func (c *Client) ListProducts(ctx context.Context, segment, networkId string) ([]Product, error) {
	path := "/list/" + url.PathEscape(segment)
	if networkId != "" {
		path += "?networkId=" + url.QueryEscape(networkId)
	}
	var resp ListResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("airbills list %s: %w", segment, err)
	}
	return resp.Data, nil
}

// DetectNetwork resolves the Nigerian mobile network for a phone number so
// airtime/data purchases set the correct networkId automatically.
func (c *Client) DetectNetwork(ctx context.Context, phoneNumber string) (*NetworkCheckResponse, error) {
	path := "/network-checker?phoneNumber=" + url.QueryEscape(phoneNumber)
	var resp NetworkCheckResponse
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("airbills network-checker: %w", err)
	}
	return &resp, nil
}

// ValidateMeter confirms an electricity meter number and returns the account
// holder name to display before charging.
func (c *Client) ValidateMeter(ctx context.Context, meterNo, electId string) (*MeterValidateResponse, error) {
	var resp MeterValidateResponse
	if err := c.post(ctx, "/validate/elect", MeterValidateRequest{MeterNo: meterNo, ElectID: electId}, &resp); err != nil {
		return nil, fmt.Errorf("airbills validate meter: %w", err)
	}
	if resp.Status != StatusSuccess {
		return &resp, &StatusError{Status: resp.Status, Message: resp.Message}
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (c *Client) post(ctx context.Context, path string, body, dest interface{}) error {
	return c.do(ctx, http.MethodPost, path, body, dest)
}

func (c *Client) get(ctx context.Context, path string, dest interface{}) error {
	return c.do(ctx, http.MethodGet, path, nil, dest)
}

func (c *Client) do(ctx context.Context, method, path string, body, dest interface{}) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		payload = b
	}

	if body != nil {
		c.logger.Debug("Airbills request",
			zap.String("method", method),
			zap.String("path", path),
			zap.String("body_safe", maskDigits(string(payload))))
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("secretkey", c.cfg.SecretKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("airbills request failed: %w", err)
			c.logger.Warn("Airbills request failed", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		// Retry transient failures only. 4xx are returned immediately.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
			c.logger.Warn("Airbills retryable error", zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			c.logger.Warn("Airbills API error",
				zap.Int("status", resp.StatusCode),
				zap.String("path", path),
				zap.String("error_detail", extractErrorMessage(respBody)),
				zap.String("body_safe", maskDigits(string(respBody))))
			return &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
		}

		if dest != nil {
			if err := json.Unmarshal(respBody, dest); err != nil {
				return fmt.Errorf("unmarshal response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("airbills %s %s failed after %d attempts: %w", method, path, c.cfg.MaxRetries+1, lastErr)
}
