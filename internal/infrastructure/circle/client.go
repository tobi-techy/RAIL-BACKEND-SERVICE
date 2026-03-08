package circle

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	entitysecret "github.com/rail-service/rail_service/internal/domain/services/entity_secret"
	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker"
	"go.opentelemetry.io/otel/trace"
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
	PublicKeyPEM           string        `json:"public_key_pem"`           // Circle public key for entity secret encryption
	WalletSetID            string        `json:"wallet_set_id"`            // Default wallet set ID for wallet creation
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
		// Use environment to determine base URL
		env := strings.ToLower(strings.TrimSpace(config.Environment))
		if env == "sandbox" {
			config.BaseURL = SandboxBaseURL
		} else {
			// Default to production for "production", "mainnet", or any other value
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
	logger.Debug("Initializing Circle client entity secret service",
		zap.String("entitySecretCiphertext_length", fmt.Sprintf("%d", len(config.EntitySecretCiphertext))),
		zap.String("publicKeyPEM_length", fmt.Sprintf("%d", len(config.PublicKeyPEM))))
	entitySecretService, err := entitysecret.NewService(logger, config.EntitySecretCiphertext, config.PublicKeyPEM)
	if err != nil {
		logger.Warn("Failed to initialize entity secret service, dynamic generation will not be available",
			zap.Error(err),
			zap.String("entitySecretCiphertext_present", fmt.Sprintf("%t", config.EntitySecretCiphertext != "")),
			zap.String("publicKeyPEM_present", fmt.Sprintf("%t", config.PublicKeyPEM != "")))
		entitySecretService = nil
	}

	if strings.TrimSpace(config.EntitySecretCiphertext) == "" {
		logger.Warn("No pre-registered entity secret ciphertext configured. Dynamic generation will be used, but Circle API may reject these requests.")
	} else {
		logger.Info("Pre-registered entity secret ciphertext is configured for fallback.")
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
	entitySecretCipherText, err := c.getEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret ciphertext: %w", err)
	}

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

	// Use configured pre-registered ciphertext when available.
	entitySecretCiphertext, err := c.getEntitySecretCiphertext(ctx)
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
	attemptsMade := 0

	for attempt := 0; attempt <= maxRetries; attempt++ {
		attemptsMade = attempt + 1
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

	return fmt.Errorf("request failed after %d attempts: %w", attemptsMade, lastErr)
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

	// Inject distributed trace context
	injectTraceContext(ctx, req.Header)

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
		return attachCircleFieldErrors(apiError, circleErr.Errors)
	}

	return apiError
}

// shouldRetry determines if a request should be retried based on the error
func (c *Client) shouldRetry(err error) bool {
	// Don't retry on context cancellation
	if err == context.Canceled || err == context.DeadlineExceeded {
		return false
	}

	// Prefer behavior-based retry checks for typed Circle errors.
	type retryableError interface {
		IsRetryable() bool
	}
	var retriable retryableError
	if errors.As(err, &retriable) {
		return retriable.IsRetryable()
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

func attachCircleFieldErrors(apiErr error, fieldErrors []entities.CircleFieldError) error {
	switch e := apiErr.(type) {
	case entities.CircleValidationError:
		e.Errors = fieldErrors
		return e
	case entities.CircleAuthError:
		e.Errors = fieldErrors
		return e
	case entities.CircleRateLimitError:
		e.Errors = fieldErrors
		return e
	case entities.CircleConflictError:
		e.Errors = fieldErrors
		return e
	case entities.CircleServerError:
		e.Errors = fieldErrors
		return e
	case entities.CircleAPIError:
		e.Errors = fieldErrors
		return e
	default:
		return apiErr
	}
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

// GenerateDepositAddress creates a dev-controlled wallet on the given chain and
// returns its on-chain address. For testnet chains the caller should pass the
// Circle WalletChain constant (e.g. MATIC-AMOY).
func (c *Client) GenerateDepositAddress(ctx context.Context, chain entities.WalletChain, userID uuid.UUID) (string, error) {
	if !chain.IsValid() {
		return "", fmt.Errorf("unsupported chain: %s", chain)
	}

	// Determine wallet set ID from config.
	walletSetID := c.config.WalletSetsEndpoint // fallback
	// The caller is expected to have set a default wallet set ID on the funding
	// service; the Circle client itself doesn't store it. We accept it via the
	// config field DefaultWalletSetID if present.
	if c.config.WalletSetID != "" {
		walletSetID = c.config.WalletSetID
	}

	accountType := "EOA"
	if chain == entities.WalletChainSOLDevnet || chain == entities.WalletChainSolana {
		accountType = "EOA" // Solana uses EOA-equivalent
	}

	resp, err := c.CreateWallet(ctx, entities.CircleWalletCreateRequest{
		IdempotencyKey: fmt.Sprintf("deposit-%s-%s", userID.String(), string(chain)),
		Blockchains:    []string{string(chain)},
		Count:          1,
		AccountType:    accountType,
		WalletSetID:    walletSetID,
	})
	if err != nil {
		return "", fmt.Errorf("circle create wallet for deposit address: %w", err)
	}

	// Extract address from response — prefer the Addresses array, fall back to Address field.
	if len(resp.Wallet.Addresses) > 0 {
		return resp.Wallet.Addresses[0].Address, nil
	}
	if resp.Wallet.Address != "" {
		return resp.Wallet.Address, nil
	}

	return "", fmt.Errorf("circle wallet created but no address returned (wallet_id=%s)", resp.Wallet.ID)
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
	if req.WalletID == "" {
		return nil, fmt.Errorf("wallet ID is required")
	}
	if req.TokenID == "" {
		return nil, fmt.Errorf("token ID is required")
	}

	resolvedTokenID, err := c.resolveTransferTokenID(ctx, req.WalletID, req.TokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve token ID: %w", err)
	}
	req.TokenID = resolvedTokenID

	if req.FeeLevel == "" {
		req.FeeLevel = "MEDIUM"
	}

	// Use configured pre-registered ciphertext when available.
	entitySecretCiphertext, err := c.getEntitySecretCiphertext(ctx)
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

func (c *Client) resolveTransferTokenID(ctx context.Context, walletID, tokenHint string) (string, error) {
	if _, err := uuid.Parse(tokenHint); err == nil {
		return tokenHint, nil
	}

	balances, err := c.GetWalletBalances(ctx, walletID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch wallet balances for token lookup: %w", err)
	}

	for _, tb := range balances.TokenBalances {
		if strings.EqualFold(tb.Token.ID, tokenHint) {
			return tb.Token.ID, nil
		}
		if strings.EqualFold(tb.Token.Symbol, tokenHint) {
			return tb.Token.ID, nil
		}
		if strings.EqualFold(tb.Token.Name, tokenHint) {
			return tb.Token.ID, nil
		}
	}

	return "", fmt.Errorf("token %q not present in wallet %s balances", tokenHint, walletID)
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

	entitySecretCiphertext, err := c.getEntitySecretCiphertext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to generate entity secret: %w", err)
	}

	// Resolve the actual USDC token ID from the wallet's balances
	tokenID, err := c.resolveTransferTokenID(ctx, req.WalletID, "USDC")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve USDC token for wallet %s: %w", req.WalletID, err)
	}

	// Build CCTP transfer request using Circle's developer wallet transfer endpoint
	// CCTP mintRecipient must be 32-byte hex: EVM addresses (20 bytes) need left-zero-padding.
	mintRecipient := req.MintRecipient
	if addr := strings.TrimPrefix(mintRecipient, "0x"); len(addr) == 40 {
		mintRecipient = "0x" + fmt.Sprintf("%064s", addr)
	}

	transferReq := map[string]interface{}{
		"idempotencyKey":         req.IdempotencyKey,
		"entitySecretCiphertext": entitySecretCiphertext,
		"walletId":               req.WalletID,
		"amounts":                []string{req.Amount.String()},
		"destinationAddress":     mintRecipient,
		"tokenId":                tokenID,
		"destinationDomain":      req.DestDomain,
		"feeLevel":               "MEDIUM",
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

func (c *Client) getEntitySecretCiphertext(ctx context.Context) (string, error) {
	// Try dynamic generation first
	if c.entitySecretService != nil {
		ciphertext, err := c.entitySecretService.GenerateEntitySecretCiphertext(ctx)
		if err == nil {
			c.logger.Debug("Using dynamically generated entity secret ciphertext",
				zap.String("ciphertext_length", fmt.Sprintf("%d", len(ciphertext))))
			return ciphertext, nil
		}
		c.logger.Warn("Dynamic ciphertext generation failed, trying fallback", zap.Error(err))
	}
	// Fall back to pre-registered ciphertext from config
	if ct := strings.TrimSpace(c.config.EntitySecretCiphertext); ct != "" {
		c.logger.Info("Using pre-registered entity secret ciphertext from configuration",
			zap.String("ciphertext_length", fmt.Sprintf("%d", len(ct))))
		return ct, nil
	}
	return "", fmt.Errorf("entity secret service not initialized and no pre-registered ciphertext configured: check CIRCLE_PUBLIC_KEY_PEM and CIRCLE_ENTITY_SECRET_CIPHERTEXT")
}

// GetCCTPTransaction retrieves the status of a CCTP/transfer transaction
func (c *Client) GetCCTPTransaction(ctx context.Context, transactionID string) (*entities.CCTPTransactionStatus, error) {
	endpoints := []string{
		fmt.Sprintf("/v1/w3s/transactions/%s", transactionID),
		fmt.Sprintf("/v1/w3s/developer/transactions/%s", transactionID),
	}

	var lastErr error
	for idx, endpoint := range endpoints {
		var response map[string]interface{}
		_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
			return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
		})
		if err != nil {
			lastErr = err
			if isCircleNotFoundError(err) && idx < len(endpoints)-1 {
				c.logger.Warn("Circle transaction lookup endpoint returned 404, trying fallback endpoint",
					zap.String("transactionId", transactionID),
					zap.String("endpoint", endpoint))
				continue
			}
			c.logger.Error("Failed to get CCTP transaction",
				zap.String("transactionId", transactionID),
				zap.String("endpoint", endpoint),
				zap.Error(err))
			return nil, fmt.Errorf("failed to get transaction: %w", err)
		}

		status := parseCCTPTransactionStatus(transactionID, response)
		c.logger.Info("Retrieved CCTP transaction status",
			zap.String("transactionId", status.ID),
			zap.String("status", status.Status),
			zap.String("txHash", status.TxHash),
			zap.String("endpoint", endpoint))
		return status, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("transaction lookup failed without specific error")
	}
	return nil, fmt.Errorf("failed to get transaction: %w", lastErr)
}

func parseCCTPTransactionStatus(transactionID string, response map[string]interface{}) *entities.CCTPTransactionStatus {
	// Handle nested data structure
	data, ok := response["data"].(map[string]interface{})
	if !ok {
		data = response
	}
	txData, ok := data["transaction"].(map[string]interface{})
	if !ok {
		txData = data
	}
	return parseTransactionStatusFromMap(transactionID, txData)
}

func isCircleNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	var circleErr entities.CircleAPIError
	if errors.As(err, &circleErr) {
		return circleErr.Code == http.StatusNotFound
	}

	var legacyErr entities.CircleErrorResponse
	if errors.As(err, &legacyErr) {
		return legacyErr.Code == http.StatusNotFound
	}

	// Defensive fallback for wrapped/non-typed errors.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http 404") ||
		strings.Contains(msg, "error 404") ||
		strings.Contains(msg, "status 404") ||
		strings.Contains(msg, "\"code\":404")
}

// FindRecentOutboundTransfer searches recent outbound transfer transactions for a wallet.
// This is used as a recovery path when a persisted transfer ID can no longer be fetched directly.
func (c *Client) FindRecentOutboundTransfer(
	ctx context.Context,
	walletID, destinationAddress string,
	amount decimal.Decimal,
	since time.Time,
) (*entities.CCTPTransactionStatus, error) {
	query := url.Values{}
	query.Set("walletIds", walletID)
	query.Set("txType", "OUTBOUND")
	query.Set("operation", "TRANSFER")
	query.Set("pageSize", "50")
	if !since.IsZero() {
		query.Set("from", since.UTC().Format(time.RFC3339))
	}

	endpoint := "/v1/w3s/transactions?" + query.Encode()

	var response map[string]interface{}
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list outbound transactions: %w", err)
	}

	data, ok := response["data"].(map[string]interface{})
	if !ok {
		data = response
	}

	rawList, ok := data["transactions"].([]interface{})
	if !ok || len(rawList) == 0 {
		return nil, nil
	}

	targetAddress := strings.TrimSpace(strings.ToLower(destinationAddress))
	var best *entities.CCTPTransactionStatus
	var bestTime time.Time

	for _, item := range rawList {
		txMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		txType := strings.ToUpper(strings.TrimSpace(getString(txMap, "transactionType", "txType")))
		if txType != "" && txType != "OUTBOUND" {
			continue
		}

		state := strings.ToUpper(strings.TrimSpace(getString(txMap, "state")))
		if state == "FAILED" || state == "REJECTED" || state == "CANCELLED" {
			continue
		}

		if targetAddress != "" {
			dest := strings.TrimSpace(strings.ToLower(getString(txMap, "destinationAddress")))
			if dest == "" {
				if nestedDest, ok := txMap["destination"].(map[string]interface{}); ok {
					dest = strings.TrimSpace(strings.ToLower(getString(nestedDest, "address")))
				}
			}
			if dest != "" && dest != targetAddress {
				continue
			}
		}

		txAmount, ok := getTransactionAmount(txMap)
		if !ok || !txAmount.Equal(amount) {
			continue
		}

		txStatus := parseTransactionStatusFromMap("", txMap)
		txTime := parseTransactionTimestamp(txMap)
		if best == nil || txTime.After(bestTime) {
			best = txStatus
			bestTime = txTime
		}
	}

	if best != nil {
		c.logger.Info("Matched outbound transfer from transaction history fallback",
			zap.String("walletId", walletID),
			zap.String("transactionId", best.ID),
			zap.String("status", best.Status),
			zap.String("txHash", best.TxHash))
	}

	return best, nil
}

func parseTransactionStatusFromMap(defaultID string, txData map[string]interface{}) *entities.CCTPTransactionStatus {
	status := &entities.CCTPTransactionStatus{
		ID:     defaultID,
		Status: "pending",
	}

	if id, ok := txData["id"].(string); ok {
		status.ID = id
	}
	if txHash, ok := txData["txHash"].(string); ok {
		status.TxHash = txHash
	} else if txHash, ok := txData["transactionHash"].(string); ok {
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
	if reason, ok := txData["errorReason"].(string); ok {
		status.ErrorReason = reason
	}

	return status
}

func parseTransactionTimestamp(txData map[string]interface{}) time.Time {
	for _, key := range []string{"createDate", "create_date", "updateDate", "update_date"} {
		if raw := strings.TrimSpace(getString(txData, key)); raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func getTransactionAmount(txData map[string]interface{}) (decimal.Decimal, bool) {
	if rawAmounts, ok := txData["amounts"].([]interface{}); ok && len(rawAmounts) > 0 {
		if amountStr, ok := rawAmounts[0].(string); ok {
			if d, err := decimal.NewFromString(strings.TrimSpace(amountStr)); err == nil {
				return d, true
			}
		}
	}
	if amountObj, ok := txData["amount"].(map[string]interface{}); ok {
		if amountStr := strings.TrimSpace(getString(amountObj, "amount", "value")); amountStr != "" {
			if d, err := decimal.NewFromString(amountStr); err == nil {
				return d, true
			}
		}
	}
	return decimal.Zero, false
}

func getString(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// injectTraceContext injects OpenTelemetry trace context into HTTP headers
func injectTraceContext(ctx context.Context, headers http.Header) {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return
	}

	traceID := span.SpanContext().TraceID().String()
	spanID := span.SpanContext().SpanID().String()
	flags := "00"
	if span.SpanContext().IsSampled() {
		flags = "01"
	}
	headers.Set("traceparent", "00-"+traceID+"-"+spanID+"-"+flags)
}
