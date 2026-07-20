package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL    = "https://api.useoval.com"
	defaultTimeout    = 20 * time.Second
	defaultMaxRetries = 2
	maxResponseSize   = 1 << 20 // 1MB
)

// Config holds Graph API configuration.
type Config struct {
	APIKey        string
	BaseURL       string
	WebhookSecret string
	Timeout       time.Duration
	MaxRetries    int
}

// Client wraps the Graph (useoval.com) core API.
type Client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient constructs a Graph API client. APIKey is required.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("graph: APIKey is required")
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

// ── People ───────────────────────────────────────────────────────

// CreatePerson creates a person (identity) on Graph.
func (c *Client) CreatePerson(ctx context.Context, req *CreatePersonRequest) (*Person, error) {
	var resp envelope[Person]
	if err := c.do(ctx, http.MethodPost, "/person", req, &resp); err != nil {
		return nil, fmt.Errorf("graph create person: %w", err)
	}
	return &resp.Data, nil
}

// FetchPerson retrieves a person by ID.
func (c *Client) FetchPerson(ctx context.Context, personID string) (*Person, error) {
	var resp envelope[Person]
	if err := c.do(ctx, http.MethodGet, "/person/"+url.PathEscape(personID), nil, &resp); err != nil {
		return nil, fmt.Errorf("graph fetch person: %w", err)
	}
	return &resp.Data, nil
}

// UpgradePersonKYC upgrades a person's KYC level (e.g. secondary → primary ID).
func (c *Client) UpgradePersonKYC(ctx context.Context, personID string, req *UpgradePersonKYCRequest) (*Person, error) {
	var resp envelope[Person]
	path := "/person/" + url.PathEscape(personID) + "/kyc"
	if err := c.do(ctx, http.MethodPatch, path, req, &resp); err != nil {
		return nil, fmt.Errorf("graph upgrade person kyc: %w", err)
	}
	return &resp.Data, nil
}

// ── Documents ────────────────────────────────────────────────────

// CreateDocument attaches a document to an existing person or business.
func (c *Client) CreateDocument(ctx context.Context, req *CreateDocumentRequest) (*EntityDocument, error) {
	var resp envelope[EntityDocument]
	if err := c.do(ctx, http.MethodPost, "/entity_document", req, &resp); err != nil {
		return nil, fmt.Errorf("graph create document: %w", err)
	}
	return &resp.Data, nil
}

// ── Bank Accounts ────────────────────────────────────────────────

// CreateBankAccount provisions a virtual bank account for a person.
func (c *Client) CreateBankAccount(ctx context.Context, req *CreateBankAccountRequest) (*BankAccount, error) {
	var resp envelope[BankAccount]
	if err := c.do(ctx, http.MethodPost, "/bank_account", req, &resp); err != nil {
		return nil, fmt.Errorf("graph create bank account: %w", err)
	}
	return &resp.Data, nil
}

// FetchBankAccount retrieves a bank account by ID.
func (c *Client) FetchBankAccount(ctx context.Context, accountID string) (*BankAccount, error) {
	var resp envelope[BankAccount]
	if err := c.do(ctx, http.MethodGet, "/bank_account/"+url.PathEscape(accountID), nil, &resp); err != nil {
		return nil, fmt.Errorf("graph fetch bank account: %w", err)
	}
	return &resp.Data, nil
}

// ListBankAccounts lists the bank accounts owned by a person.
func (c *Client) ListBankAccounts(ctx context.Context, personID string) ([]BankAccount, error) {
	var resp ListBankAccountsResponse
	path := "/bank_account?person_id=" + url.QueryEscape(personID)
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("graph list bank accounts: %w", err)
	}
	return resp.Data, nil
}

// ── Rates & Conversions ──────────────────────────────────────────

// FetchRate returns the exchange rate for a currency pair (e.g. NGN → USDC).
func (c *Client) FetchRate(ctx context.Context, source, target string) (*Rate, error) {
	var resp envelope[Rate]
	path := fmt.Sprintf("/rate?source=%s&target=%s", url.QueryEscape(source), url.QueryEscape(target))
	if err := c.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("graph fetch rate: %w", err)
	}
	return &resp.Data, nil
}

// CreateConversion converts a balance from one currency to another.
func (c *Client) CreateConversion(ctx context.Context, req *CreateConversionRequest) (*Conversion, error) {
	var resp envelope[Conversion]
	if err := c.do(ctx, http.MethodPost, "/conversion", req, &resp); err != nil {
		return nil, fmt.Errorf("graph create conversion: %w", err)
	}
	return &resp.Data, nil
}

// ── HTTP transport ───────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body, dest interface{}) error {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyBytes = b
	}

	if c.logger != nil {
		var bodySafe string
		if len(bodyBytes) > 0 {
			bodySafe = digitRun.ReplaceAllString(string(bodyBytes), "***")
		}
		c.logger.Debug("Graph request",
			zap.String("method", method),
			zap.String("path", path),
			zap.String("body_safe", bodySafe))
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, ... with jitter to avoid thundering herd.
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff + jitter):
			}
		}

		var reader io.Reader
		if bodyBytes != nil {
			reader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, reader)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("graph request failed: %w", err)
			if c.logger != nil {
				c.logger.Warn("Graph request failed", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
			}
			continue
		}

		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read response: %w", readErr)
			continue
		}

		// Retry transient failures only. Respect Retry-After header.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
			if c.logger != nil {
				c.logger.Warn("Graph retryable error", zap.Int("status", resp.StatusCode), zap.Int("attempt", attempt+1))
			}
			// If Retry-After is present, override the backoff on next iteration.
			if ra := resp.Header.Get("Retry-After"); ra != "" && attempt < c.cfg.MaxRetries {
				var retryDur time.Duration
				// Try numeric seconds first (e.g. "30").
				if secs, err := time.ParseDuration(ra + "s"); err == nil {
					retryDur = secs
				} else if t, err := time.Parse(time.RFC1123, ra); err == nil {
					// Fall back to HTTP-date format (e.g. "Wed, 21 Oct 2015 07:28:00 GMT").
					retryDur = time.Until(t)
				}
				if retryDur > 0 {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(retryDur):
					}
				}
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if c.logger != nil {
				c.logger.Warn("Graph API error",
					zap.Int("status", resp.StatusCode),
					zap.String("path", path),
					zap.String("error_detail", extractErrorMessage(respBody)))
			}
			return &APIError{StatusCode: resp.StatusCode, Body: string(respBody), Path: path}
		}

		if dest != nil {
			if err := json.Unmarshal(respBody, dest); err != nil {
				return fmt.Errorf("unmarshal response: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("graph %s %s failed after %d attempts: %w", method, path, c.cfg.MaxRetries+1, lastErr)
}
