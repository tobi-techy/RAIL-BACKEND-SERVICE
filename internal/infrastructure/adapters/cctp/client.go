package cctp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	defaultTimeout = 30 * time.Second
	maxRetries     = 3
)

// Config represents CCTP client configuration
type Config struct {
	BaseURL     string
	Environment string // "sandbox" or "mainnet"
	Timeout     time.Duration
}

// Client represents a CCTP Iris API client
type Client struct {
	config         Config
	httpClient     *http.Client
	circuitBreaker *gobreaker.CircuitBreaker
	rateLimiter    *rate.Limiter
	logger         *zap.Logger
}

// NewClient creates a new CCTP Iris API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.BaseURL == "" {
		if config.Environment == "mainnet" {
			config.BaseURL = IrisMainnetURL
		} else {
			config.BaseURL = IrisSandboxURL
		}
	}

	cbSettings := gobreaker.Settings{
		Name:        "CCTPAPI",
		MaxRequests: 5,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("CCTP circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))
		},
	}

	return &Client{
		config:         config,
		httpClient:     &http.Client{Timeout: config.Timeout},
		circuitBreaker: gobreaker.NewCircuitBreaker(cbSettings),
		rateLimiter:    rate.NewLimiter(rate.Limit(MaxRequestsPerSecond), 1),
		logger:         logger,
	}
}

// GetAttestation fetches attestation for a burn transaction by hash
func (c *Client) GetAttestation(ctx context.Context, txHash string) (*AttestationResponse, error) {
	endpoint := fmt.Sprintf("/v2/messages?transactionHash=%s", txHash)
	var resp AttestationResponse
	if err := c.doRequest(ctx, endpoint, &resp); err != nil {
		return nil, fmt.Errorf("get attestation failed: %w", err)
	}
	if len(resp.Messages) == 0 {
		return nil, ErrNoMessages
	}
	return &resp, nil
}

// GetFees retrieves current fees for a transfer between domains
func (c *Client) GetFees(ctx context.Context, sourceDomain, destDomain uint32) (*FeesResponse, error) {
	endpoint := fmt.Sprintf("/v2/burn/USDC/fees?sourceDomain=%d&destinationDomain=%d", sourceDomain, destDomain)
	var resp FeesResponse
	if err := c.doRequest(ctx, endpoint, &resp); err != nil {
		return nil, fmt.Errorf("get fees failed: %w", err)
	}
	return &resp, nil
}

// GetPublicKeys retrieves attestation public keys for verification
func (c *Client) GetPublicKeys(ctx context.Context) (*PublicKeysResponse, error) {
	var resp PublicKeysResponse
	if err := c.doRequest(ctx, "/v2/publicKeys", &resp); err != nil {
		return nil, fmt.Errorf("get public keys failed: %w", err)
	}
	return &resp, nil
}

func (c *Client) doRequest(ctx context.Context, endpoint string, response interface{}) error {
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter: %w", err)
	}

	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return nil, c.doRequestInternal(ctx, endpoint, response)
	})
	return err
}

func (c *Client) doRequestInternal(ctx context.Context, endpoint string, response interface{}) error {
	fullURL := c.config.BaseURL + endpoint

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read body: %w", err)
			continue
		}

		// Retry on 5xx
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode >= 400 {
			var errResp ErrorResponse
			if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
				errResp.StatusCode = resp.StatusCode
				return &errResp
			}
			return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
		}

		if response != nil && len(body) > 0 {
			if err := json.Unmarshal(body, response); err != nil {
				return fmt.Errorf("unmarshal response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}
