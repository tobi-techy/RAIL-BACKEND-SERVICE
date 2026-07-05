package ramphub

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
	defaultBaseURL    = "https://api.ramphub.io"
	defaultTimeout    = 15 * time.Second
	defaultMaxRetries = 2
	maxResponseSize   = 1 << 20 // 1MB
)

// Config holds RampHub API configuration.
type Config struct {
	APIKey        string
	BaseURL       string
	WebhookSecret string
	Timeout       time.Duration
	MaxRetries    int
	Sandbox       bool // mirrors RampHubConfig.Sandbox — when true, sandbox webhook events are accepted
}

// Client wraps the RampHub developer API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if cfg.APIKey == "" {
		logger.Error("RampHub APIKey is required but was not provided")
		return nil, fmt.Errorf("ramphub: APIKey is required")
	}
	if cfg.WebhookSecret == "" {
		logger.Error("RampHub WebhookSecret is required but was not provided")
		return nil, fmt.Errorf("ramphub: WebhookSecret is required for webhook authentication")
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

// WebhookSecret returns the configured webhook signing secret.
func (c *Client) WebhookSecret() string { return c.cfg.WebhookSecret }

// IsSandbox reports whether this client is configured for sandbox mode. When
// true, webhook events with livemode:false are processed; in production (false)
// they are rejected to prevent sandbox events from affecting real accounts.
func (c *Client) IsSandbox() bool { return c.cfg.Sandbox }

// GetQuote fetches provider routes for a corridor. The response includes
// RampHub's recommended BestQuote plus all options.
func (c *Client) GetQuote(ctx context.Context, req QuoteRequest) (*QuoteResponse, error) {
	var resp QuoteResponse
	if err := c.post(ctx, "/api/developer/quotes", req, &resp); err != nil {
		return nil, fmt.Errorf("ramphub get quote: %w", err)
	}
	return &resp, nil
}

// CreateOrder creates a routed buy or sell order.
func (c *Client) CreateOrder(ctx context.Context, req OrderRequest) (*OrderResponse, error) {
	var resp OrderResponse
	raw, err := c.postCapture(ctx, "/api/developer/orders", req, &resp)
	if err != nil {
		return nil, fmt.Errorf("ramphub create order: %w", err)
	}
	// Providers return the pay-in account in different shapes (Paycrest's
	// virtualAccount vs UseBread's nested data.deposit); PayInAccount normalizes
	// them. If we still can't extract an account for a buy order, log the raw
	// providerDetails so a new/unknown provider shape can be added quickly.
	if resp.Side == "buy" {
		if num, _, bank := resp.PayInAccount(); num == "" || bank == "" {
			c.logger.Warn("ramphub buy order returned no recognizable pay-in account",
				zap.String("transaction_id", resp.TransactionID),
				zap.String("provider", resp.SelectedProvider),
				zap.String("provider_details_status", resp.ProviderDetails.Status),
				zap.String("provider_details_raw", extractProviderDetailsRaw(raw)))
		}
	}
	return &resp, nil
}

// GetOrderIntent returns the active payment window for a customer/token/network.
func (c *Client) GetOrderIntent(ctx context.Context, customerID, token, network string) (*OrderIntent, error) {
	path := fmt.Sprintf("/api/developer/orders/intent?customerId=%s&token=%s&network=%s",
		url.QueryEscape(customerID), url.QueryEscape(token), url.QueryEscape(network))
	var resp OrderIntent
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("ramphub get order intent: %w", err)
	}
	return &resp, nil
}

// MonitorStatus syncs a transaction with the provider and returns the latest
// server truth. Used for polling, the status endpoint, and recovery workers.
func (c *Client) MonitorStatus(ctx context.Context, transactionID string) (*Transaction, error) {
	var resp Transaction
	if err := c.post(ctx, "/api/developer/orders/"+url.PathEscape(transactionID)+"/monitor-status", struct{}{}, &resp); err != nil {
		return nil, fmt.Errorf("ramphub monitor-status: %w", err)
	}
	return &resp, nil
}

// GetCatalog returns the providers, assets, and endpoints supported by RampHub.
func (c *Client) GetCatalog(ctx context.Context) (*Catalog, error) {
	var resp Catalog
	if err := c.get(ctx, "/api/developer/catalog", &resp); err != nil {
		return nil, fmt.Errorf("ramphub get catalog: %w", err)
	}
	return &resp, nil
}

// GetProviderBankList returns the supported payout banks for a single provider.
func (c *Client) GetProviderBankList(ctx context.Context, provider string) (*ProviderBankList, error) {
	var resp ProviderBankList
	if err := c.get(ctx, "/api/developer/provider-bank-list/"+url.PathEscape(provider), &resp); err != nil {
		return nil, fmt.Errorf("ramphub get provider bank list: %w", err)
	}
	return &resp, nil
}

// ResolveBankAccount validates payout bank details before creating a sell order.
// bankName is optional but recommended: RampHub bank codes are provider-scoped,
// and the name lets RampHub's directory conversion disambiguate the institution.
func (c *Client) ResolveBankAccount(ctx context.Context, bankCode, accountNumber, bankName string) (*ResolvedAccount, error) {
	body := map[string]string{"bankCode": bankCode, "accountNumber": accountNumber}
	if bankName != "" {
		body["bankName"] = bankName
	}
	var resp ResolvedAccount
	if err := c.post(ctx, "/api/developer/bank-accounts/resolve", body, &resp); err != nil {
		return nil, fmt.Errorf("ramphub resolve bank: %w", err)
	}
	// RampHub can return 200 with success:false (or an empty name) when the
	// directory lookup fails — treat that as a resolution failure, not a match.
	if !resp.Success || resp.AccountName == "" {
		return nil, ErrAccountResolveFailed
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (c *Client) post(ctx context.Context, path string, body, dest interface{}) error {
	_, err := c.do(ctx, http.MethodPost, path, body, dest)
	return err
}

// postCapture is post that also returns the raw response body for diagnostics.
func (c *Client) postCapture(ctx context.Context, path string, body, dest interface{}) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, body, dest)
}

func (c *Client) get(ctx context.Context, path string, dest interface{}) error {
	_, err := c.do(ctx, http.MethodGet, path, nil, dest)
	return err
}

// extractProviderDetailsRaw pulls just the providerDetails object out of a raw
// order response as a compact JSON string, for diagnostic logging. Returns ""
// when it can't be isolated so callers never log the full (PII-bearing) body.
func extractProviderDetailsRaw(raw []byte) string {
	var envelope struct {
		ProviderDetails json.RawMessage `json:"providerDetails"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.ProviderDetails) == 0 {
		return ""
	}
	return string(envelope.ProviderDetails)
}

func (c *Client) do(ctx context.Context, method, path string, body, dest interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	// Create PII-safe request body for debug logging
	var bodySafe string
	if body != nil {
		if b, err := json.Marshal(body); err == nil {
			bodySafe = digitRun.ReplaceAllString(string(b), "***")
		}
	}
	c.logger.Debug("RampHub request",
		zap.String("method", method),
		zap.String("path", path),
		zap.String("body_safe", bodySafe))

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			if body != nil {
				b, _ := json.Marshal(body)
				bodyReader = bytes.NewReader(b)
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.cfg.APIKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ramphub request failed: %w", err)
			c.logger.Warn("RampHub request failed", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
			continue
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		// Retry transient failures only. 4xx (including the active-intent
		// conflict) are returned immediately so the service can react.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
			c.logger.Warn("RampHub retryable error", zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Body may contain PII / credentials — log only the machine error code,
			// not the full body; callers have the complete APIError.
			c.logger.Warn("RampHub API error",
				zap.Int("status", resp.StatusCode),
				zap.String("path", path),
				zap.String("error_code", extractErrorCode(respBody)),
				zap.String("error_detail", extractErrorMessage(respBody)),
				zap.String("field_errors", extractErrorDetails(respBody)),
				zap.String("body_safe", digitRun.ReplaceAllString(string(respBody), "***")))
			return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
		}

		if dest != nil {
			if err := json.Unmarshal(respBody, dest); err != nil {
				return nil, fmt.Errorf("unmarshal response: %w", err)
			}
		}
		return respBody, nil
	}
	return nil, fmt.Errorf("ramphub %s %s failed after %d attempts: %w", method, path, c.cfg.MaxRetries+1, lastErr)
}
