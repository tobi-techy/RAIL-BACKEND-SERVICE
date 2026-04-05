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
	defaultBaseURL = "https://api.chainrails.io/api/v1"
	defaultTimeout = 15 * time.Second
)

type Config struct {
	APIKey         string
	WebhookSecret  string
	BaseURL        string
	Timeout        time.Duration
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/auth/session", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chainrails session request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chainrails returned %d: %s", resp.StatusCode, string(body))
	}

	var status IntentStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode intent status: %w", err)
	}
	return &status, nil
}
