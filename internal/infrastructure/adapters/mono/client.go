package mono

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Config holds Mono API configuration.
type Config struct {
	APIKey      string
	Environment string // "sandbox" or "production"
	BaseURL     string
	Timeout     time.Duration
	MaxRetries  int
}

// HTTPClient implements the Client interface via the Mono REST API.
type HTTPClient struct {
	config     Config
	httpClient *http.Client
	logger     *zap.Logger

	consecutiveFailures atomic.Int64
	circuitOpenUntil    atomic.Int64
}

// NewHTTPClient creates a new Mono API client.
func NewHTTPClient(cfg Config, logger *zap.Logger) *HTTPClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.withmono.com"
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	return &HTTPClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logger,
	}
}

// --- HTTP request execution with retry + circuit breaker ---

func (c *HTTPClient) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	// Circuit breaker check
	if openUntil := c.circuitOpenUntil.Load(); openUntil > 0 {
		if time.Now().Unix() < openUntil {
			return &ErrorResponse{StatusCode: 503, Code: "circuit_open", Message: "circuit breaker open"}
		}
		c.circuitOpenUntil.Store(0)
		c.consecutiveFailures.Store(0)
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	requestURL := c.config.BaseURL + path

	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, requestURL, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("mono-sec-key", c.config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http request: %w", err)
			c.consecutiveFailures.Add(1)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response: %w", err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			c.consecutiveFailures.Store(0)
			if result != nil && len(respBody) > 0 {
				if err := json.Unmarshal(respBody, result); err != nil {
					return fmt.Errorf("unmarshal response: %w (body: %s)", err, string(respBody))
				}
			}
			return nil
		}

		// Error response
		errResp := &ErrorResponse{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(respBody, errResp) // best-effort parse
		if errResp.Message == "" {
			errResp.Message = string(respBody)
		}

		if errResp.IsRetryable() && attempt < c.config.MaxRetries {
			lastErr = errResp
			continue
		}

		// Trip circuit breaker on consecutive failures
		if c.consecutiveFailures.Add(1) >= 5 {
			c.circuitOpenUntil.Store(time.Now().Add(30 * time.Second).Unix())
			c.logger.Warn("Mono circuit breaker tripped",
				zap.Int("consecutive_failures", int(c.consecutiveFailures.Load())))
		}

		return errResp
	}

	return lastErr
}

// --- Account Linking ---

func (c *HTTPClient) InitiateLinking(ctx context.Context, req *InitiateLinkingRequest) (*InitiateLinkingResponse, error) {
	var resp monoResponse[InitiateLinkingResponse]
	if err := c.doRequest(ctx, http.MethodPost, "/v2/accounts/initiate", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *HTTPClient) ExchangeCode(ctx context.Context, code string) (*ExchangeTokenResponse, error) {
	var resp monoResponse[ExchangeTokenResponse]
	if err := c.doRequest(ctx, http.MethodPost, "/v2/accounts/auth", &ExchangeTokenRequest{Code: code}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// --- Account Data ---

func (c *HTTPClient) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	var resp monoResponse[AccountDetailsResponse]
	if err := c.doRequest(ctx, http.MethodGet, "/v2/accounts/"+accountID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Account, nil
}

func (c *HTTPClient) GetTransactions(ctx context.Context, accountID string, query *TransactionListQuery) ([]Transaction, error) {
	path := buildTransactionsPath(accountID, query)
	// The Mono API returns data as a bare JSON array, not wrapped in an object.
	var resp monoResponse[[]Transaction]
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *HTTPClient) GetIncome(ctx context.Context, accountID string) (*IncomeAnalysis, error) {
	var resp monoResponse[IncomeResponse]
	if err := c.doRequest(ctx, http.MethodGet, "/v2/accounts/"+accountID+"/income", nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Income, nil
}

// InitiateIncomeAnalysis triggers the async income analysis. The response
// body is empty (data: null) — results come via the mono.events.account_income
// webhook. periodMonths limits analysis to N months (0 = all history).
func (c *HTTPClient) InitiateIncomeAnalysis(ctx context.Context, accountID string, periodMonths int) error {
	path := "/v2/accounts/" + accountID + "/income"
	if periodMonths > 0 {
		path += "?period=" + strconv.Itoa(periodMonths)
	}
	// The income endpoint returns a 200 with data: null when initiating.
	var resp monoResponse[json.RawMessage]
	return c.doRequest(ctx, http.MethodGet, path, nil, &resp)
}

func (c *HTTPClient) GetIdentity(ctx context.Context, accountID string) (*Identity, error) {
	var resp monoResponse[IdentityResponse]
	if err := c.doRequest(ctx, http.MethodGet, "/v2/accounts/"+accountID+"/identity", nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data.Identity, nil
}

func (c *HTTPClient) UnlinkAccount(ctx context.Context, accountID string) error {
	var resp monoResponse[UnlinkResponse]
	if err := c.doRequest(ctx, http.MethodPost, "/v2/accounts/"+accountID+"/unlink", nil, &resp); err != nil {
		return err
	}
	return nil
}

// --- DirectPay ---

func (c *HTTPClient) InitiatePayment(ctx context.Context, req *InitiatePaymentRequest) (*InitiatePaymentResponse, error) {
	var resp monoResponse[InitiatePaymentResponse]
	if err := c.doRequest(ctx, http.MethodPost, "/v2/payments/initiate", req, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *HTTPClient) VerifyPayment(ctx context.Context, reference string) (*PaymentVerifyResponse, error) {
	path := "/v2/payments/verify?reference=" + url.QueryEscape(reference)
	var resp monoResponse[PaymentVerifyResponse]
	if err := c.doRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// --- Helpers ---

func buildTransactionsPath(accountID string, query *TransactionListQuery) string {
	path := "/v2/accounts/" + accountID + "/transactions"
	if query == nil {
		return path
	}

	params := url.Values{}
	if query.Start != "" {
		params.Set("start", query.Start)
	}
	if query.End != "" {
		params.Set("end", query.End)
	}
	if query.Type != "" {
		params.Set("type", query.Type)
	}
	if !query.Paginate {
		params.Set("paginate", "false")
	}
	if query.Limit > 0 {
		params.Set("limit", strconv.Itoa(query.Limit))
	}
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return path
}
