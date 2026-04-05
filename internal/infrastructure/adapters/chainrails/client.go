package chainrails

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBaseURL    = "https://api.chainrails.io/api/v1"
	defaultTimeout    = 15 * time.Second
	defaultMaxRetries = 3
	defaultRetryDelay = 1 * time.Second
)

type Config struct {
	APIKey         string
	WebhookSecret  string
	BaseURL        string
	Timeout        time.Duration
	MaxRetries     int
	RetryDelay     time.Duration
	// DestinationChain is the chain where Rail's Bridge wallet lives (e.g. "BASE_MAINNET").
	DestinationChain string
	// SettlementToken is the token Rail accepts (e.g. "USDC").
	SettlementToken string
}

type Client struct {
	config     Config
	httpClient *http.Client
	logger     *zap.Logger
}

func NewClient(cfg Config, logger *zap.Logger) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaultRetryDelay
	}
	if cfg.SettlementToken == "" {
		cfg.SettlementToken = "USDC"
	}
	return &Client{
		config:     cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
		logger:     logger,
	}
}

// --- Session creation (server-side, keeps API key private) ---

type CreateSessionRequest struct {
	Amount           string `json:"amount"`
	Recipient        string `json:"recipient"`
	DestinationChain string `json:"destinationChain"`
	Token            string `json:"token"`
}

type CreateSessionResponse struct {
	SessionToken string `json:"session_token"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// CreateSession generates a payment session token for the frontend PaymentModal.
func (c *Client) CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error) {
	if req.DestinationChain == "" {
		req.DestinationChain = c.config.DestinationChain
	}
	if req.Token == "" {
		req.Token = c.config.SettlementToken
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal session request: %w", err)
	}

	const maxResponseSize = 10 * 1024 * 1024 // 10MB
	var lastErr error
	
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.config.RetryDelay * time.Duration(attempt)):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/auth/session", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create http request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("chainrails session request failed: %w", err)
			c.logger.Warn("ChainRails session attempt failed", 
				zap.Int("attempt", attempt+1), 
				zap.Error(err))
			continue
		}

		// Read response body with size limit
		limitedReader := io.LimitReader(resp.Body, maxResponseSize)
		respBody, err := io.ReadAll(limitedReader)
		resp.Body.Close() // Close immediately, not deferred
		
		if err != nil {
			lastErr = fmt.Errorf("read response body: %w", err)
			c.logger.Warn("ChainRails response read failed",
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue
		}

		// Check if response was truncated due to size limit
		if len(respBody) == maxResponseSize {
			lastErr = fmt.Errorf("response too large (>%d bytes)", maxResponseSize)
			c.logger.Warn("ChainRails response too large",
				zap.Int("attempt", attempt+1),
				zap.Int("max_size", maxResponseSize))
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("chainrails returned %d: %s", resp.StatusCode, string(respBody))
			c.logger.Warn("ChainRails session retryable error",
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt+1),
			)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			c.logger.Error("chainrails session creation failed",
				zap.Int("status", resp.StatusCode),
				zap.String("body", string(respBody)),
			)
			return nil, fmt.Errorf("chainrails returned %d: %s", resp.StatusCode, string(respBody))
		}

		var session CreateSessionResponse
		if err := json.Unmarshal(respBody, &session); err != nil {
			return nil, fmt.Errorf("unmarshal session response: %w", err)
		}
		return &session, nil
	}

	return nil, fmt.Errorf("chainrails session failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}

// --- Intent status (optional polling fallback) ---

type IntentStatus struct {
	ID               int    `json:"id"`
	IntentAddress    string `json:"intent_address"`
	Status           string `json:"intent_status"`
	TxHash           string `json:"tx_hash"`
	Amount           string `json:"initialAmount"`
	SourceChain      string `json:"source_chain"`
	DestinationChain string `json:"destination_chain"`
	Recipient        string `json:"recipient"`
	Sender           string `json:"sender"`
}

func (c *Client) GetIntentStatus(ctx context.Context, intentAddress string) (*IntentStatus, error) {
	url := fmt.Sprintf("%s/intents/%s", c.config.BaseURL, intentAddress)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chainrails get intent failed: %w", err)
	}

	const maxResponseSize = 1024 * 1024 // 1MB for error responses
	limitedReader := io.LimitReader(resp.Body, maxResponseSize)
	respBody, err := io.ReadAll(limitedReader)
	resp.Body.Close()
	
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chainrails returned %d: %s", resp.StatusCode, string(respBody))
	}

	var status IntentStatus
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, fmt.Errorf("decode intent status: %w", err)
	}
	return &status, nil
}
