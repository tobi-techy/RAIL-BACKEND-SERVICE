package circle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker"
	"github.com/rail-service/rail_service/internal/domain/entities"
	entitysecret "github.com/rail-service/rail_service/internal/domain/services/entity_secret"
	"go.uber.org/zap"
)

const (
	// Circle API URLs
	ProductionBaseURL = "https://api.circle.com"
	SandboxBaseURL    = "https://api-sandbox.circle.com"

	// Timeouts and limits
	defaultTimeout    = 30 * time.Second
	maxRetries        = 5
	baseBackoff       = 1 * time.Second
	maxBackoff        = 32 * time.Second
	jitterRange       = 0.1 // 10% jitter
	defaultRetryAfter = 5 * time.Second
	maxRetryAfter     = 60 * time.Second
)

// Config represents Circle API configuration
type Config struct {
	APIKey                 string        `json:"api_key"`
	BaseURL                string        `json:"base_url"`
	Environment            string        `json:"environment"` // "sandbox" or "production"
	Timeout                time.Duration `json:"timeout"`
	WalletSetsEndpoint     string        `json:"wallet_sets_endpoint"`
	WalletsEndpoint        string        `json:"wallets_endpoint"`
	PublicKeyEndpoint      string        `json:"public_key_endpoint"`
	BalancesEndpoint       string        `json:"balances_endpoint"`
	TransferEndpoint       string        `json:"transfer_endpoint"`
	EntitySecretCiphertext string        `json:"entity_secret_ciphertext"` // Pre-registered ciphertext from Circle Dashboard
}

// Client represents a Circle API client
type Client struct {
	config              Config
	httpClient          *http.Client
	circuitBreaker      *gobreaker.CircuitBreaker
	logger              *zap.Logger
	entitySecretService *entitysecret.Service
}

// NewClient creates a new Circle API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}

	if config.BaseURL == "" {
		if config.Environment == "mainnet" {
			config.BaseURL = ProductionBaseURL
		} else {
			// Default to production URL for both testnet and mainnet
			// Circle Wallet API uses the same base URL for both environments
			config.BaseURL = ProductionBaseURL
		}
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")

	if config.WalletSetsEndpoint == "" {
		config.WalletSetsEndpoint = "/v1/w3s/developer/walletSets"
	}
	if config.WalletsEndpoint == "" {
		config.WalletsEndpoint = "/v1/w3s/developer/wallets"
	}
	if config.PublicKeyEndpoint == "" {
		config.PublicKeyEndpoint = "/v1/w3s/config/entity/publicKey"
	}
	if config.BalancesEndpoint == "" {
		config.BalancesEndpoint = "/v1/w3s/wallets"
	}
	if config.TransferEndpoint == "" {
		config.TransferEndpoint = "/v1/w3s/developer/transactions/transfer"
	}

	httpClient := &http.Client{
		Timeout: config.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	st := gobreaker.Settings{
		Name:        "CircleAPI",
		MaxRequests: 5,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()))
		},
	}

	circuitBreaker := gobreaker.NewCircuitBreaker(st)

	// Initialize entity secret service for dynamic ciphertext generation (fallback only)
	entitySecretService := entitysecret.NewService(logger)

	if strings.TrimSpace(config.EntitySecretCiphertext) == "" {
		logger.Warn("No pre-registered entity secret ciphertext configured. Dynamic generation will be used, but Circle API may reject these requests.")
	} else {
		logger.Info("Using pre-registered entity secret ciphertext from configuration.")
	}

	return &Client{
		config:              config,
		httpClient:          httpClient,
		circuitBreaker:      circuitBreaker,
		logger:              logger,
		entitySecretService: entitySecretService,
	}
}

// CreateWalletSet creates a new developer-controlled wallet set using pre-registered Entity Secret Ciphertext
func (c *Client) CreateWalletSet(ctx context.Context, name string, _ string) (*entities.CircleWalletSetResponse, error) {

	var entitySecretCipherText string
	var err error

	entitySecretCipherText, err = c.entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret ciphertext: %w", err)
	}

	// Entity secret service already returns base64-encoded ciphertext
	c.logger.Warn("Using dynamically generated entity secret ciphertext - Circle API may reject this request")

	request := entities.CircleWalletSetRequest{
		IdempotencyKey:         uuid.NewString(),
		EntitySecretCiphertext: entitySecretCipherText,
		Name:                   name,
	}

	c.logger.Info("Creating developer-controlled wallet set",
		zap.String("walletSetName", name))

	var response entities.CircleWalletSetResponse
	_, err = c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", c.config.WalletSetsEndpoint, request, &response)
	})

	if err != nil {
		c.logger.Error("Failed to create developer-controlled wallet set",
			zap.String("name", name),
			zap.Error(err))
		return nil, fmt.Errorf("create wallet set failed: %w", err)
	}

	fmt.Printf("response to create wallet set: %+v\n", response)

	c.logger.Info("Created developer-controlled wallet set successfully",
		zap.String("name", name),
		zap.String("walletSetId", response.WalletSet.ID))

	return &response, nil
}

// GetWalletSet retrieves a wallet set by ID
func (c *Client) GetWalletSet(ctx context.Context, walletSetID string) (*entities.CircleWalletSetResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", c.config.WalletSetsEndpoint, walletSetID)

	var response entities.CircleWalletSetResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
	})

	if err != nil {
		c.logger.Error("Failed to get wallet set",
			zap.String("walletSetId", walletSetID),
			zap.Error(err))
		return nil, fmt.Errorf("get wallet set failed: %w", err)
	}

	return &response, nil
}

// CreateWallet creates a new developer-controlled wallet using dynamic Entity Secret Ciphertext
func (c *Client) CreateWallet(ctx context.Context, req entities.CircleWalletCreateRequest) (*entities.CircleWalletCreateResponse, error) {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = uuid.NewString()
	}

	// Generate a new unique entity secret ciphertext for this request
	entitySecretCiphertext, err := c.entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret ciphertext: %w", err)
	}

	// Entity secret service already returns base64-encoded ciphertext
	req.EntitySecretCiphertext = entitySecretCiphertext

	c.logger.Info("Creating developer-controlled wallet",
		zap.String("walletSetId", req.WalletSetID),
		zap.Strings("blockchains", req.Blockchains),
		zap.String("accountType", req.AccountType),
		zap.Int("count", req.Count))

	var response entities.CircleWalletCreateResponse
	_, err = c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", c.config.WalletsEndpoint, req, &response)
	})

	if err != nil {
		c.logger.Error("Failed to create developer-controlled wallet",
			zap.String("walletSetId", req.WalletSetID),
			zap.Strings("blockchains", req.Blockchains),
			zap.String("accountType", req.AccountType),
			zap.Int("count", req.Count),
			zap.Error(err))
		return nil, fmt.Errorf("create wallet failed: %w", err)
	}

	c.logger.Info("Created developer-controlled wallet successfully",
		zap.String("walletSetId", req.WalletSetID),
		zap.String("walletId", response.Wallet.ID),
		zap.Strings("blockchains", req.Blockchains))

	return &response, nil
}

// GetWallet retrieves a wallet by ID
func (c *Client) GetWallet(ctx context.Context, walletID string) (*entities.CircleWalletCreateResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", c.config.WalletsEndpoint, walletID)

	var response entities.CircleWalletCreateResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
	})

	if err != nil {
		c.logger.Error("Failed to get wallet",
			zap.String("walletId", walletID),
			zap.Error(err))
		return nil, fmt.Errorf("get wallet failed: %w", err)
	}

	return &response, nil
}

// addJitter adds random jitter to a duration to prevent thundering herd
func addJitter(duration time.Duration) time.Duration {
	// Generate random number between -1 and 1
	randomBytes := make([]byte, 8)
	rand.Read(randomBytes)
	randomFloat := float64(randomBytes[0]) / 255.0 // Normalize to 0-1
	randomFloat = randomFloat*2 - 1                // Convert to -1 to 1

	jitter := time.Duration(float64(duration) * jitterRange * randomFloat)
	return duration + jitter
}

// calculateBackoff calculates exponential backoff with jitter
func calculateBackoff(attempt int, retryAfter *time.Duration) time.Duration {
	var baseDelay time.Duration

	if retryAfter != nil {
		baseDelay = *retryAfter
		if baseDelay > maxRetryAfter {
			baseDelay = maxRetryAfter
		}
	} else {
		// Exponential backoff: 2^attempt * baseBackoff
		exponent := math.Pow(2, float64(attempt))
		baseDelay = time.Duration(exponent) * baseBackoff
		if baseDelay > maxBackoff {
			baseDelay = maxBackoff
		}
	}

	return addJitter(baseDelay)
}

// doRequestWithRetry performs HTTP request with exponential backoff retry and jitter
func (c *Client) doRequestWithRetry(ctx context.Context, method, endpoint string, requestBody, responseBody interface{}) error {
	var lastErr error
	requestID := uuid.NewString()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Check if the last error was a rate limit error with Retry-After
			var retryAfter *time.Duration
			if circleErr, ok := lastErr.(entities.CircleAPIError); ok {
				if circleErr.GetRetryAfter() > 0 {
					ra := circleErr.GetRetryAfter()
					retryAfter = &ra
				}
			}

			backoff := calculateBackoff(attempt-1, retryAfter)

			c.logger.Info("Retrying Circle API request",
				zap.String("request_id", requestID),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
				zap.String("method", method),
				zap.String("endpoint", endpoint))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		err := c.doRequest(ctx, method, endpoint, requestBody, responseBody, requestID)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !c.shouldRetry(err) {
			c.logger.Warn("Not retrying Circle API request due to error type",
				zap.String("request_id", requestID),
				zap.Error(err),
				zap.String("method", method),
				zap.String("endpoint", endpoint))
			break
		}

		c.logger.Warn("Circle API request failed, will retry",
			zap.String("request_id", requestID),
			zap.Error(err),
			zap.Int("attempt", attempt+1),
			zap.Int("maxRetries", maxRetries),
			zap.String("method", method),
			zap.String("endpoint", endpoint))
	}

	return fmt.Errorf("request failed after %d attempts: %w", maxRetries+1, lastErr)
}

// doRequest performs a single HTTP request
func (c *Client) doRequest(ctx context.Context, method, endpoint string, requestBody, responseBody interface{}, requestID string) error {
	url := c.config.BaseURL + endpoint

	var reqBody io.Reader
	if requestBody != nil {
		jsonData, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Stack-Service/1.0")
	req.Header.Set("X-Request-ID", requestID)

	c.logger.Debug("Making Circle API request",
		zap.String("request_id", requestID),
		zap.String("method", method),
		zap.String("url", url),
		zap.Any("headers", req.Header))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Circle API response",
		zap.String("request_id", requestID),
		zap.String("method", method),
		zap.String("url", url),
		zap.Int("statusCode", resp.StatusCode),
		zap.String("body", string(body)))

	// Handle error responses
	if resp.StatusCode >= 400 {
		return c.handleErrorResponse(resp.StatusCode, body, requestID)
	}

	// Unmarshal successful response
	if responseBody != nil && len(body) > 0 {
		if err := json.Unmarshal(body, responseBody); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// handleErrorResponse processes Circle API error responses and returns typed errors
func (c *Client) handleErrorResponse(statusCode int, body []byte, requestID string) error {
	// Parse Retry-After header if present
	var retryAfter *time.Duration
	// Note: In a real implementation, you'd get this from the response headers
	// For now, we'll use default values based on status code

	var circleErr entities.CircleErrorResponse
	if err := json.Unmarshal(body, &circleErr); err != nil {
		// If we can't parse the error response, create a generic error
		message := fmt.Sprintf("HTTP %d: %s", statusCode, string(body))
		return entities.NewCircleAPIError(statusCode, message, requestID, retryAfter)
	}

	// Set default retry-after for rate limits
	if statusCode == 429 {
		defaultRetry := defaultRetryAfter
		retryAfter = &defaultRetry
	}

	// Create typed error
	apiError := entities.NewCircleAPIError(statusCode, circleErr.Message, requestID, retryAfter)

	// Add field errors if present
	if len(circleErr.Errors) > 0 {
		if circleAPIErr, ok := apiError.(entities.CircleAPIError); ok {
			circleAPIErr.Errors = circleErr.Errors
			return circleAPIErr
		}
	}

	return apiError
}

// shouldRetry determines if a request should be retried based on the error
func (c *Client) shouldRetry(err error) bool {
	// Don't retry on context cancellation
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	// Check if it's a Circle API error
	if circleErr, ok := err.(entities.CircleAPIError); ok {
		return circleErr.IsRetryable()
	}

	// Check legacy CircleErrorResponse for backward compatibility
	if circleErr, ok := err.(entities.CircleErrorResponse); ok {
		// Don't retry on client errors (4xx), except for rate limiting and timeouts
		if circleErr.Code >= 400 && circleErr.Code < 500 {
			return circleErr.Code == 429 || circleErr.Code == 408
		}
		// Retry on server errors (5xx)
		return circleErr.Code >= 500
	}

	// Retry on network errors
	return true
}

// HealthCheck performs a health check against Circle API
func (c *Client) HealthCheck(ctx context.Context) error {
	// Use a simple GET request to wallet sets to check connectivity
	endpoint := c.config.WalletSetsEndpoint

	req, err := http.NewRequestWithContext(ctx, "GET", c.config.BaseURL+endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("circle API health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("circle API health check failed with status %d", resp.StatusCode)
	}

	c.logger.Info("Circle API health check successful", zap.Int("statusCode", resp.StatusCode))
	return nil
}

// GenerateDepositAddress generates a deposit address for the specified chain and user
func (c *Client) GenerateDepositAddress(ctx context.Context, chain entities.WalletChain, userID uuid.UUID) (string, error) {
	// For MVP, we'll simulate address generation based on chain type
	// In production, this would call Circle's actual deposit address generation API
	return "hello", nil
}

// ValidateDeposit validates a deposit transaction using Circle's validation service
func (c *Client) ValidateDeposit(ctx context.Context, txHash string, amount decimal.Decimal) (bool, error) {
	c.logger.Info("Validating deposit",
		zap.String("tx_hash", txHash),
		zap.String("amount", amount.String()))

	// For MVP, we'll simulate validation
	// In production, this would call Circle's transaction validation API

	// Simple validation: check if amount is positive and txHash is not empty
	if amount.IsZero() || amount.IsNegative() {
		c.logger.Warn("Invalid deposit amount",
			zap.String("tx_hash", txHash),
			zap.String("amount", amount.String()))
		return false, nil
	}

	if txHash == "" {
		c.logger.Warn("Empty transaction hash", zap.String("tx_hash", txHash))
		return false, nil
	}

	// For demo purposes, reject transactions with "invalid" in the hash
	if len(txHash) > 7 && txHash[:7] == "invalid" {
		c.logger.Warn("Invalid transaction detected", zap.String("tx_hash", txHash))
		return false, nil
	}

	c.logger.Info("Deposit validation successful",
		zap.String("tx_hash", txHash),
		zap.String("amount", amount.String()))

	return true, nil
}

// ConvertToUSD converts stablecoin amount to USD buying power
func (c *Client) ConvertToUSD(ctx context.Context, amount decimal.Decimal, token entities.Stablecoin) (decimal.Decimal, error) {
	c.logger.Info("Converting to USD",
		zap.String("amount", amount.String()),
		zap.String("token", string(token)))

	// For MVP, we'll use fixed conversion rates
	// In production, this would call Circle's price oracle or conversion API

	var conversionRate decimal.Decimal
	switch token {
	case entities.StablecoinUSDC:
		// USDC is pegged 1:1 to USD
		conversionRate = decimal.NewFromInt(1)
	default:
		return decimal.Zero, fmt.Errorf("unsupported token: %s", token)
	}

	usdAmount := amount.Mul(conversionRate)

	c.logger.Info("Conversion to USD completed",
		zap.String("original_amount", amount.String()),
		zap.String("token", string(token)),
		zap.String("usd_amount", usdAmount.String()),
		zap.String("conversion_rate", conversionRate.String()))

	return usdAmount, nil
}

// GetEntityPublicKey retrieves the entity's public key (optional if using pre-registered ciphertext)
func (c *Client) GetEntityPublicKey(ctx context.Context) (string, error) {
	c.logger.Info("Retrieving entity public key")

	var response map[string]interface{}
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", c.config.PublicKeyEndpoint, nil, &response)
	})

	if err != nil {
		c.logger.Error("Failed to get entity public key", zap.Error(err))
		return "", fmt.Errorf("get entity public key failed: %w", err)
	}

	// Extract public key from response
	if publicKey, ok := response["publicKey"].(string); ok {
		c.logger.Info("Retrieved entity public key successfully")
		return publicKey, nil
	}

	return "", fmt.Errorf("public key not found in response")
}

// GetWalletBalances retrieves token balances for a specific wallet
// tokenAddress is optional - if provided, filters results to only that token
func (c *Client) GetWalletBalances(ctx context.Context, walletID string, tokenAddress ...string) (*entities.CircleWalletBalancesResponse, error) {
	endpoint := fmt.Sprintf("%s/%s/balances", c.config.BalancesEndpoint, walletID)
	
	// Add tokenAddress query parameter if provided
	if len(tokenAddress) > 0 && tokenAddress[0] != "" {
		endpoint = fmt.Sprintf("%s?tokenAddress=%s", endpoint, tokenAddress[0])
		c.logger.Info("Getting wallet balances", 
			zap.String("walletId", walletID),
			zap.String("tokenAddress", tokenAddress[0]),
			zap.String("endpoint", endpoint),
			zap.String("fullURL", c.config.BaseURL+endpoint))
	} else {
		c.logger.Info("Getting wallet balances", 
			zap.String("walletId", walletID),
			zap.String("endpoint", endpoint),
			zap.String("fullURL", c.config.BaseURL+endpoint))
	}

	var response entities.CircleWalletBalancesResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
	})
     
	
	if err != nil {
		c.logger.Error("Failed to get wallet balances",
			zap.String("walletId", walletID),
			zap.Error(err))
		return nil, fmt.Errorf("get wallet balances failed: %w", err)
	}

	c.logger.Info("Retrieved wallet balances successfully",
		zap.String("walletId", walletID),
		zap.Int("tokenCount", len(response.TokenBalances)),
		zap.String("usdcBalance", response.GetUSDCBalance()))
		c.logger.Info("log the response", zap.Any("response", response))

	return &response, nil
}

// TransferFunds transfers funds between accounts using developer-controlled wallets
func (c *Client) TransferFunds(ctx context.Context, req entities.CircleTransferRequest) (map[string]interface{}, error) {
	// Generate a new unique entity secret ciphertext for this request
	entitySecretCiphertext, err := c.entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret ciphertext: %w", err)
	}

	// Entity secret service already returns base64-encoded ciphertext
	req.EntitySecretCiphertext = entitySecretCiphertext

	c.logger.Info("Transferring funds",
		zap.String("walletId", req.WalletID),
		zap.Strings("amounts", req.Amounts),
		zap.String("tokenId", req.TokenID))

	var response map[string]interface{}
	_, err = c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", c.config.TransferEndpoint, req, &response)
	})

	if err != nil {
		c.logger.Error("Failed to transfer funds",
			zap.String("walletId", req.WalletID),
			zap.Strings("amounts", req.Amounts),
			zap.Error(err))
		return nil, fmt.Errorf("transfer funds failed: %w", err)
	}

	c.logger.Info("Transfer completed successfully",
		zap.String("walletId", req.WalletID),
		zap.Strings("amounts", req.Amounts))

	return response, nil
}

// GetMetrics returns circuit breaker metrics for monitoring
func (c *Client) GetMetrics() map[string]interface{} {
	counts := c.circuitBreaker.Counts()
	return map[string]interface{}{
		"circuit_breaker_state": c.circuitBreaker.State().String(),
		"requests":              counts.Requests,
		"consecutive_successes": counts.ConsecutiveSuccesses,
		"consecutive_failures":  counts.ConsecutiveFailures,
		"total_successes":       counts.TotalSuccesses,
		"total_failures":        counts.TotalFailures,
	}
}

// InitiateCCTPBurn initiates a CCTP burn transaction on Polygon to bridge USDC to another chain
func (c *Client) InitiateCCTPBurn(ctx context.Context, req *entities.CCTPBurnRequest) (*entities.CCTPBurnResponse, error) {
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.NewString()
	}

	entitySecretCiphertext, err := c.entitySecretService.GenerateEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret: %w", err)
	}

	// Build CCTP transfer request using Circle's developer wallet transfer endpoint
	transferReq := map[string]interface{}{
		"idempotencyKey":         req.IdempotencyKey,
		"entitySecretCiphertext": entitySecretCiphertext,
		"walletId":               req.WalletID,
		"amounts":                []string{req.Amount.String()},
		"destinationAddress":     req.MintRecipient,
		"tokenId":                "usdc-polygon",
	}

	c.logger.Info("Initiating CCTP burn",
		zap.String("walletId", req.WalletID),
		zap.String("amount", req.Amount.String()),
		zap.Uint32("destDomain", req.DestDomain),
		zap.String("mintRecipient", req.MintRecipient))

	var response map[string]interface{}
	_, err = c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", c.config.TransferEndpoint, transferReq, &response)
	})

	if err != nil {
		c.logger.Error("Failed to initiate CCTP burn",
			zap.String("walletId", req.WalletID),
			zap.Error(err))
		return nil, fmt.Errorf("cctp burn failed: %w", err)
	}

	result := &entities.CCTPBurnResponse{Status: "pending"}
	if id, ok := response["id"].(string); ok {
		result.TransactionID = id
	}
	if txHash, ok := response["txHash"].(string); ok {
		result.TxHash = txHash
	}

	c.logger.Info("CCTP burn initiated",
		zap.String("transactionId", result.TransactionID),
		zap.String("txHash", result.TxHash))

	return result, nil
}

// GetCCTPTransaction retrieves the status of a CCTP/transfer transaction
func (c *Client) GetCCTPTransaction(ctx context.Context, transactionID string) (*entities.CCTPTransactionStatus, error) {
	endpoint := fmt.Sprintf("/v1/w3s/developer/transactions/%s", transactionID)

	var response map[string]interface{}
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
	})

	if err != nil {
		c.logger.Error("Failed to get CCTP transaction",
			zap.String("transactionId", transactionID),
			zap.Error(err))
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	// Handle nested data structure
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		data = response
	}
	txData, ok := data["transaction"].(map[string]interface{})
	if !ok {
		txData = data
	}

	status := &entities.CCTPTransactionStatus{
		ID:     transactionID,
		Status: "pending",
	}

	if id, ok := txData["id"].(string); ok {
		status.ID = id
	}
	if txHash, ok := txData["txHash"].(string); ok {
		status.TxHash = txHash
	}
	if state, ok := txData["state"].(string); ok {
		status.Status = state
	}
	if chain, ok := txData["blockchain"].(string); ok {
		status.Chain = chain
	}
	if confirmDate, ok := txData["firstConfirmDate"].(string); ok {
		if t, err := time.Parse(time.RFC3339, confirmDate); err == nil {
			status.ConfirmedAt = &t
		}
	}

	c.logger.Info("Retrieved CCTP transaction status",
		zap.String("transactionId", status.ID),
		zap.String("status", status.Status),
		zap.String("txHash", status.TxHash))

	return status, nil
}
