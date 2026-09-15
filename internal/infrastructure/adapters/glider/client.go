package glider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// DefaultBaseURL is the Glider V2 production endpoint.
const DefaultBaseURL = "https://api.glider.fi/v2"

// Config configures the Glider HTTP client. The API key is a tenant secret and
// must only ever live on the backend.
type Config struct {
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	MaxRetries    int
	CorrelationID string // optional override, mainly for tests
}

// Client is a thin HTTP client for the Glider V2 API.
type Client struct {
	baseURL       string
	apiKey        string
	http          *http.Client
	log           *zap.Logger
	maxRetries    int
	correlationID string
}

// NewClient builds a Glider API client. A missing API key is not fatal at
// construction time so a disabled deployment can still boot; every call will
// fail with 401 until a key is configured.
func NewClient(cfg Config, log *zap.Logger) *Client {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(cfg.APIKey),
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     60 * time.Second,
			},
		},
		log:           log,
		maxRetries:    cfg.MaxRetries,
		correlationID: strings.TrimSpace(cfg.CorrelationID),
	}
}

// envelope is the Glider V2 response envelope. nextCursor lives beside data,
// never inside it.
type envelope struct {
	Success    bool            `json:"success"`
	Data       json.RawMessage `json:"data"`
	NextCursor *string         `json:"nextCursor"`
	Error      *struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
}

type requestOptions struct {
	// retry marks a call as safe to repeat. Only reads and calls with a
	// documented idempotency anchor (enroll flowId, withdrawal nonce) may set
	// this; an unanchored write must never be retried automatically.
	retry bool
}

// Whoami returns the tenant identity and granted scopes.
func (c *Client) Whoami(ctx context.Context) (*entities.GliderIdentity, error) {
	var out entities.GliderIdentity
	if err := c.do(ctx, http.MethodGet, "/whoami", nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateStrategy creates a provider strategy with its initial allocation.
func (c *Client) CreateStrategy(ctx context.Context, in entities.GliderStrategyInput) (*entities.GliderStrategy, error) {
	var out entities.GliderStrategy
	if err := c.do(ctx, http.MethodPost, "/strategies", in, &out, requestOptions{}); err != nil {
		return nil, err
	}
	return &out, nil
}

// PublishStrategyVersion publishes a new allocation version. Enrolled
// portfolios re-target on their next scheduled rebalance.
func (c *Client) PublishStrategyVersion(ctx context.Context, strategyID string, in entities.GliderStrategyInput) (*entities.GliderStrategy, error) {
	var out entities.GliderStrategy
	path := fmt.Sprintf("/strategies/%s/versions", strategyID)
	if err := c.do(ctx, http.MethodPost, path, in, &out, requestOptions{}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetStrategy reads a strategy template.
func (c *Client) GetStrategy(ctx context.Context, strategyID string) (*entities.GliderStrategy, error) {
	var out entities.GliderStrategy
	if err := c.do(ctx, http.MethodGet, "/strategies/"+strategyID, nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetStrategySchedule changes a strategy's rebalance cadence, which fans out to
// every enrolled portfolio.
func (c *Client) SetStrategySchedule(ctx context.Context, strategyID, frequency string) error {
	body := map[string]string{"type": "interval", "frequency": frequency}
	return c.do(ctx, http.MethodPut, "/strategies/"+strategyID+"/schedule", body, nil, requestOptions{})
}

// DiscoverStrategies lists public, mirrorable strategies.
func (c *Client) DiscoverStrategies(ctx context.Context, collection, cursor string, limit int) ([]entities.GliderDiscoveredStrategy, string, error) {
	query := "?collection=" + collection
	if cursor != "" {
		query += "&cursor=" + cursor
	}
	if limit > 0 {
		query += "&limit=" + strconv.Itoa(limit)
	}
	var out struct {
		Strategies []entities.GliderDiscoveredStrategy `json:"strategies"`
	}
	next, err := c.doWithCursor(ctx, http.MethodGet, "/discovery/strategies"+query, nil, &out, requestOptions{retry: true})
	if err != nil {
		return nil, "", err
	}
	return out.Strategies, next, nil
}

// PrepareEnrollment runs enroll stage 1 and returns the wallet authorization
// payload the portfolio owner must sign.
func (c *Client) PrepareEnrollment(ctx context.Context, in entities.GliderEnrollSignatureInput) (*entities.GliderEnrollAuthorization, error) {
	var out entities.GliderEnrollAuthorization
	if err := c.do(ctx, http.MethodPost, "/enroll/signature", in, &out, requestOptions{}); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitEnrollment runs enroll stage 2. Idempotent on flowId, so a retry with
// the same body replays the original response.
func (c *Client) SubmitEnrollment(ctx context.Context, in entities.GliderEnrollSubmitInput) (*entities.GliderPortfolio, error) {
	var out entities.GliderPortfolio
	if err := c.do(ctx, http.MethodPost, "/enroll", in, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPortfolio reads one portfolio's state, including its schedule.
func (c *Client) GetPortfolio(ctx context.Context, portfolioID string) (*entities.GliderPortfolio, error) {
	var out entities.GliderPortfolio
	if err := c.do(ctx, http.MethodGet, "/portfolios/"+portfolioID, nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPortfolios lists the tenant's portfolios.
func (c *Client) ListPortfolios(ctx context.Context) ([]entities.GliderPortfolio, error) {
	var out struct {
		Portfolios []entities.GliderPortfolio `json:"portfolios"`
	}
	if _, err := c.doWithCursor(ctx, http.MethodGet, "/portfolios", nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return out.Portfolios, nil
}

// GetPositions reads live balances and USD values for a portfolio.
func (c *Client) GetPositions(ctx context.Context, portfolioID string) (*entities.GliderPositions, error) {
	var out entities.GliderPositions
	if err := c.do(ctx, http.MethodGet, "/portfolios/"+portfolioID+"/positions", nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	out.PortfolioID = portfolioID
	out.AsOf = time.Now().UTC()
	return &out, nil
}

// StartPortfolio resumes provider automation.
func (c *Client) StartPortfolio(ctx context.Context, portfolioID string) error {
	return c.do(ctx, http.MethodPost, "/portfolios/"+portfolioID+"/start", map[string]any{}, nil, requestOptions{})
}

// StopPortfolio pauses provider automation.
func (c *Client) StopPortfolio(ctx context.Context, portfolioID string) error {
	return c.do(ctx, http.MethodPost, "/portfolios/"+portfolioID+"/stop", map[string]any{}, nil, requestOptions{})
}

// TriggerRebalance dispatches an out-of-band rebalance. A portfolio-level
// cooldown returns 429 with Retry-After.
func (c *Client) TriggerRebalance(ctx context.Context, portfolioID string) (*entities.GliderOperationHandle, error) {
	var out entities.GliderOperationHandle
	if err := c.do(ctx, http.MethodPost, "/portfolios/"+portfolioID+"/rebalance", map[string]any{}, &out, requestOptions{}); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetOperation polls one async provider operation.
func (c *Client) GetOperation(ctx context.Context, portfolioID, operationID string) (*entities.GliderOperationState, error) {
	var out entities.GliderOperationState
	path := fmt.Sprintf("/portfolios/%s/operations/%s", portfolioID, operationID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// PrepareWithdrawal runs withdrawal stage 1 and returns the authorization the
// portfolio owner must sign.
func (c *Client) PrepareWithdrawal(ctx context.Context, portfolioID string, in entities.GliderWithdrawSignatureInput) (*entities.GliderWithdrawAuthorization, error) {
	var out entities.GliderWithdrawAuthorization
	path := "/portfolios/" + portfolioID + "/withdraw/signature"
	if err := c.do(ctx, http.MethodPost, path, in, &out, requestOptions{}); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitWithdrawal runs withdrawal stage 2. Idempotent on the authorization
// nonce carried inside message.
func (c *Client) SubmitWithdrawal(ctx context.Context, portfolioID string, in entities.GliderWithdrawSubmitInput) (*entities.GliderOperationHandle, error) {
	var out entities.GliderOperationHandle
	if err := c.do(ctx, http.MethodPost, "/portfolios/"+portfolioID+"/withdraw", in, &out, requestOptions{retry: true}); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs one provider call, decoding the response envelope.
func (c *Client) do(ctx context.Context, method, path string, body any, out any, opts requestOptions) error {
	_, err := c.doWithCursor(ctx, method, path, body, out, opts)
	return err
}

// doWithCursor performs one provider call and also returns nextCursor.
func (c *Client) doWithCursor(ctx context.Context, method, path string, body any, out any, opts requestOptions) (string, error) {
	if c.apiKey == "" {
		return "", &APIError{
			StatusCode: http.StatusUnauthorized,
			Code:       "API_401",
			Message:    "glider api key is not configured",
		}
	}

	attempts := 1
	if opts.retry && c.maxRetries > 0 {
		attempts += c.maxRetries
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 500 * time.Millisecond
			if apiErr, ok := AsAPIError(lastErr); ok && apiErr.RetryAfter > 0 {
				delay = apiErr.RetryAfter
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
		next, err := c.attempt(ctx, method, path, body, out)
		if err == nil {
			return next, nil
		}
		lastErr = err
		if !IsRetryableError(err) {
			return "", err
		}
		c.log.Warn("glider call retryable failure",
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}
	return "", lastErr
}

// attempt issues a single HTTP request.
func (c *Client) attempt(ctx context.Context, method, path string, body any, out any) (string, error) {
	correlationID := c.correlationID
	if correlationID == "" {
		correlationID = uuid.NewString()
	}

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("glider: encode request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return "", fmt.Errorf("glider: build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Correlation-Id", correlationID)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %s %s: %v", ErrProviderUnavailable, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("%w: read body: %v", ErrProviderUnavailable, err)
	}

	var env envelope
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			// A non-JSON body on an error status is still an error.
			if resp.StatusCode >= 400 {
				return "", &APIError{
					StatusCode:    resp.StatusCode,
					Message:       truncate(string(raw), 240),
					CorrelationID: correlationID,
				}
			}
			return "", fmt.Errorf("glider: decode response: %w", err)
		}
	}

	if resp.StatusCode >= 400 || (env.Error != nil && env.Error.Code != "") {
		apiErr := &APIError{
			StatusCode:    resp.StatusCode,
			CorrelationID: correlationID,
		}
		if env.Error != nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
			apiErr.Details = env.Error.Details
		}
		if apiErr.Message == "" {
			apiErr.Message = truncate(string(raw), 240)
		}
		apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
		return "", apiErr
	}

	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return "", fmt.Errorf("glider: decode data: %w", err)
		}
	}

	next := ""
	if env.NextCursor != nil {
		next = *env.NextCursor
	}
	return next, nil
}

// parseRetryAfter understands both delay-seconds and HTTP-date forms.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
