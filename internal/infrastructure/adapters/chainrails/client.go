package chainrails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	APIKey        string
	WebhookSecret string
	BaseURL       string
	Timeout       time.Duration
	MaxRetries    int
	RetryDelay    time.Duration
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

// APIError preserves ChainRails HTTP status codes so callers can distinguish
// permanent request failures from retryable provider failures.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("chainrails returned %d: %s", e.StatusCode, e.Body)
}

func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= http.StatusInternalServerError
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

// Config returns the client configuration.
func (c *Client) Config() Config { return c.config }

// --- Intent creation (direct API integration) ---

type CreateIntentRequest struct {
	Sender           string                 `json:"sender"`
	Amount           string                 `json:"amount"`
	AmountSymbol     string                 `json:"amountSymbol"`
	TokenIn          string                 `json:"tokenIn"`
	SourceChain      string                 `json:"source_chain"`
	DestinationChain string                 `json:"destination_chain"`
	Recipient        string                 `json:"recipient"`
	RefundAddress    string                 `json:"refund_address"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

type CreateIntentResponse struct {
	ID                       int                    `json:"id"`
	ClientID                 string                 `json:"client_id"`
	Sender                   string                 `json:"sender"`
	InitialAmount            string                 `json:"initialAmount"`
	FeesInUSD                string                 `json:"fees_in_usd"`
	AppFeeInUSD              string                 `json:"app_fee_in_usd"`
	TotalAmountInUSD         string                 `json:"total_amount_in_usd"`
	TotalAmountInAssetToken  string                 `json:"total_amount_in_asset_token"`
	FeesInAssetToken         string                 `json:"fees_in_asset_token"`
	AppFeeInAssetToken       string                 `json:"app_fee_in_asset_token"`
	AssetTokenSymbol         string                 `json:"asset_token_symbol"`
	AssetTokenDecimals       int                    `json:"asset_token_decimals"`
	Slippage                 string                 `json:"slippage"`
	TokenIn                  string                 `json:"tokenIn"`
	TokenOut                 string                 `json:"tokenOut"`
	IntentAddress            string                 `json:"intent_address"`
	DestinationIntentAddress string                 `json:"destination_intent_address"`
	SourceChain              string                 `json:"source_chain"`
	DestinationChain         string                 `json:"destination_chain"`
	Recipient                string                 `json:"recipient"`
	RefundAddress            string                 `json:"refund_address"`
	Relayer                  string                 `json:"relayer"`
	Coordinator              string                 `json:"coordinator"`
	Bridger                  string                 `json:"bridger"`
	BridgeExtraData          string                 `json:"bridgeExtraData"`
	IntentNonce              int64                  `json:"intent_nonce"`
	IntentStatus             string                 `json:"intent_status"`
	TxHash                   string                 `json:"tx_hash"`
	NeedsRelay               bool                   `json:"needs_relay"`
	RelayerClaimed           bool                   `json:"relayer_claimed"`
	PaymasterUsed            bool                   `json:"paymaster_used"`
	Mode                     string                 `json:"mode"`
	ExpiresAt                string                 `json:"expires_at"`
	Metadata                 map[string]interface{} `json:"metadata"`
	CreatedAt                string                 `json:"created_at"`
	UpdatedAt                string                 `json:"updated_at"`
}

// --- Session creation (server-side, keeps API key private) ---

type CreateSessionRequest struct {
	Recipient        string `json:"recipient"`
	TokenOut         string `json:"tokenOut"`
	DestinationChain string `json:"destinationChain"`
	Amount           string `json:"amount"`
}

type CreateSessionResponse struct {
	SessionToken string `json:"sessionToken"`
	SessionId    string `json:"sessionId"`
	ExpiresAt    string `json:"expiresAt"`
}

// CreateSessionRaw returns the raw ChainRails session response bytes.
func (c *Client) CreateSessionRaw(ctx context.Context, req *CreateSessionRequest) (json.RawMessage, error) {
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

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/modal/sessions", bytes.NewReader(body))
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
		resp.Body.Close()

		if err != nil {
			lastErr = fmt.Errorf("read response body: %w", err)
			c.logger.Warn("ChainRails response read failed",
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue
		}

		if len(respBody) == maxResponseSize {
			lastErr = fmt.Errorf("response too large (>%d bytes)", maxResponseSize)
			c.logger.Warn("ChainRails response too large",
				zap.Int("attempt", attempt+1),
				zap.Int("max_size", maxResponseSize))
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
			c.logger.Warn("ChainRails session retryable error",
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt+1),
			)
			continue
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			c.logger.Error("chainrails session creation failed",
				zap.Int("status", resp.StatusCode),
				zap.String("body", string(respBody)),
			)
			return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
		}

		var session CreateSessionResponse
		if err := json.Unmarshal(respBody, &session); err != nil {
			return nil, fmt.Errorf("unmarshal session response: %w", err)
		}
		return respBody, nil
	}

	return nil, fmt.Errorf("chainrails session failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
}

// CreateIntent creates a cross-chain transfer intent with ChainRails
func (c *Client) CreateIntent(ctx context.Context, req *CreateIntentRequest) (*CreateIntentResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal intent request: %w", err)
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

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/intents", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create http request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("chainrails intent request failed: %w", err)
			c.logger.Warn("ChainRails intent attempt failed",
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue
		}

		// Read response body with size limit
		limitedReader := io.LimitReader(resp.Body, maxResponseSize)
		respBody, err := io.ReadAll(limitedReader)
		resp.Body.Close()

		if err != nil {
			lastErr = fmt.Errorf("read response body: %w", err)
			c.logger.Warn("ChainRails response read failed",
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue
		}

		if len(respBody) == maxResponseSize {
			lastErr = fmt.Errorf("response too large (>%d bytes)", maxResponseSize)
			c.logger.Warn("ChainRails response too large",
				zap.Int("attempt", attempt+1),
				zap.Int("max_size", maxResponseSize))
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
			c.logger.Warn("ChainRails intent retryable error",
				zap.Int("status", resp.StatusCode),
				zap.Int("attempt", attempt+1),
			)
			continue
		}

		if resp.StatusCode != http.StatusCreated {
			c.logger.Error("chainrails intent creation failed",
				zap.Int("status", resp.StatusCode),
				zap.String("body", string(respBody)),
			)
			return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
		}

		var intent CreateIntentResponse
		if err := json.Unmarshal(respBody, &intent); err != nil {
			return nil, fmt.Errorf("unmarshal intent response: %w", err)
		}
		return &intent, nil
	}

	return nil, fmt.Errorf("chainrails intent failed after %d attempts: %w", c.config.MaxRetries+1, lastErr)
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

// GetIntentStatus polls an intent by its on-chain address.
// Per the ChainRails docs, GET /intents/{id} takes the numeric intent ID
// while address lookup lives at GET /intents/address/{address}. All callers
// pass an intent address, so this must hit the address route — the by-ID
// route 404s for addresses and polling recovery could never observe a
// terminal state otherwise.
// GetIntentByID fetches an intent by its numeric ID (GET /intents/{id}).
// Used when only the numeric ID is known (e.g. parsed out of a transfer
// reference like "circle-cr:<tx>:<id>").
func (c *Client) GetIntentByID(ctx context.Context, intentID int) (*IntentStatus, error) {
	intentURL := fmt.Sprintf("%s/intents/%d", c.config.BaseURL, intentID)
	return c.getIntent(ctx, intentURL)
}

func (c *Client) GetIntentStatus(ctx context.Context, intentAddress string) (*IntentStatus, error) {
	intentURL := fmt.Sprintf("%s/intents/address/%s", c.config.BaseURL, url.PathEscape(intentAddress))
	status, err := c.getIntent(ctx, intentURL)
	if err == nil {
		return status, nil
	}
	// Route fallback: if the address route 404s (docs vs prod mismatch),
	// retry the legacy by-ID-shaped route once before giving up — every
	// poll site (withdrawals, sweeps, autosweep, stuck-funds) depends on
	// observing a terminal state, and a hard 404 would stall them all.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		legacy := fmt.Sprintf("%s/intents/%s", c.config.BaseURL, url.PathEscape(intentAddress))
		if legacyStatus, lerr := c.getIntent(ctx, legacy); lerr == nil {
			if c.logger != nil {
				c.logger.Warn("chainrails address route 404, legacy route answered (verify docs)",
					zap.String("address", intentAddress))
			}
			return legacyStatus, nil
		}
	}
	return nil, err
}

// TriggerIntentProcessing manually kicks a funded-but-unstarted intent.
// Per the ChainRails docs this is the required fallback when the indexer
// misses the funding event — and it is mandatory on testnets, which have no
// indexing support at all.
func (c *Client) TriggerIntentProcessing(ctx context.Context, intentAddress string) error {
	if c == nil || c.httpClient == nil {
		return fmt.Errorf("chainrails trigger-processing skipped: nil client")
	}
	intentURL := fmt.Sprintf("%s/intents/%s/trigger-processing", c.config.BaseURL, url.PathEscape(intentAddress))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, intentURL, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("chainrails trigger-processing failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode != http.StatusOK {
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	return nil
}

// RefundIntentResult is the outcome of POST /intents/{address}/refund.
type RefundIntentResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	TxHash  string `json:"txHash"`
}

// RefundExpiredIntent manually refunds an expired intent — the documented
// escape hatch for recovering stuck funds when automation fails.
func (c *Client) RefundExpiredIntent(ctx context.Context, intentAddress string) (*RefundIntentResult, error) {
	intentURL := fmt.Sprintf("%s/intents/%s/refund", c.config.BaseURL, url.PathEscape(intentAddress))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, intentURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chainrails refund failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	var result RefundIntentResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode refund result: %w", err)
	}
	return &result, nil
}

// IsTestnetChain reports whether a chain name targets a testnet.
// Testnet intents have no indexer support and must be trigger-processed
// after funding (see TriggerIntentProcessing). Matches the full vocabulary
// callers may pass: ChainRails names (BASE_TESTNET) and Circle-style names
// (BASE-SEPOLIA) from the Blend path, plus AMOY/FUJI/DEVNET families.
func IsTestnetChain(chain string) bool {
	up := strings.ToUpper(chain)
	for _, marker := range []string{"TESTNET", "SEPOLIA", "AMOY", "FUJI", "DEVNET"} {
		if strings.Contains(up, marker) {
			return true
		}
	}
	return false
}

func (c *Client) getIntent(ctx context.Context, url string) (*IntentStatus, error) {
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
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var status IntentStatus
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, fmt.Errorf("decode intent status: %w", err)
	}
	return &status, nil
}
