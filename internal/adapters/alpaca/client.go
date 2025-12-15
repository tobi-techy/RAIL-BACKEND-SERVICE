package alpaca

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"github.com/sony/gobreaker"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

const (
	// Default timeouts and limits
	defaultTimeout    = 30 * time.Second
	maxRetries        = 3
	baseBackoff       = 1 * time.Second
	maxBackoff        = 16 * time.Second
	jitterRange       = 0.1 // 10% jitter
	defaultRetryAfter = 5 * time.Second
	maxRetryAfter     = 60 * time.Second

	// Alpaca API endpoints
	accountsEndpoint    = "/v1/accounts"
	ordersEndpoint      = "/v1/trading/accounts/%s/orders"      // account_id parameter
	assetsEndpoint      = "/v1/assets"
	positionsEndpoint   = "/v1/trading/accounts/%s/positions"   // account_id parameter
	activitiesEndpoint  = "/v1/trading/accounts/%s/activities"  // account_id parameter
	portfolioEndpoint   = "/v1/trading/accounts/%s/portfolio"   // account_id parameter
	watchlistsEndpoint  = "/v1/trading/accounts/%s/watchlists"  // account_id parameter
	newsEndpoint        = "/v1beta1/news"
)

// Config represents Alpaca API configuration
type Config struct {
	ClientID    string
	SecretKey   string
	BaseURL     string // Broker API base URL
	DataBaseURL string // Market Data API base URL
	Environment string // sandbox or production
	Timeout     time.Duration
}

// Client represents an Alpaca Broker API client
type Client struct {
	config         Config
	httpClient     *http.Client
	circuitBreaker *gobreaker.CircuitBreaker
	tokenManager   *TokenManager
	logger         *zap.Logger
}

// NewClient creates a new Alpaca API client
func NewClient(config Config, logger *zap.Logger) *Client {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}

	if config.BaseURL == "" {
		if config.Environment == "production" {
			config.BaseURL = "https://broker-api.alpaca.markets"
		} else {
			config.BaseURL = "https://broker-api.sandbox.alpaca.markets"
		}
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")

	if config.DataBaseURL == "" {
		config.DataBaseURL = "https://data.alpaca.markets"
	}
	config.DataBaseURL = strings.TrimRight(config.DataBaseURL, "/")

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
		Name:        "AlpacaBrokerAPI",
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

	tokenManager := NewTokenManager(config.ClientID, config.SecretKey, config.Environment, logger)

	return &Client{
		config:         config,
		httpClient:     httpClient,
		circuitBreaker: circuitBreaker,
		tokenManager:   tokenManager,
		logger:         logger,
	}
}

// Account Management Methods

// CreateAccount creates a new brokerage account
func (c *Client) CreateAccount(ctx context.Context, req *entities.AlpacaCreateAccountRequest) (*entities.AlpacaAccountResponse, error) {
	c.logger.Info("Creating Alpaca brokerage account",
		zap.String("email", req.Contact.EmailAddress))

	var response entities.AlpacaAccountResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", accountsEndpoint, req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Alpaca account",
			zap.String("email", req.Contact.EmailAddress),
			zap.Error(err))
		return nil, fmt.Errorf("create account failed: %w", err)
	}

	c.logger.Info("Created Alpaca account successfully",
		zap.String("account_id", response.ID),
		zap.String("account_number", response.AccountNumber),
		zap.String("status", string(response.Status)))

	return &response, nil
}

// GetAccount retrieves account details by account ID
func (c *Client) GetAccount(ctx context.Context, accountID string) (*entities.AlpacaAccountResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", accountsEndpoint, accountID)

	var response entities.AlpacaAccountResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Alpaca account",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("get account failed: %w", err)
	}

	return &response, nil
}

// ListAccounts lists all accounts
func (c *Client) ListAccounts(ctx context.Context, query map[string]string) ([]entities.AlpacaAccountResponse, error) {
	endpoint := accountsEndpoint
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Add(k, v)
		}
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	var response []entities.AlpacaAccountResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to list Alpaca accounts", zap.Error(err))
		return nil, fmt.Errorf("list accounts failed: %w", err)
	}

	return response, nil
}

// Trading Methods

// CreateOrder creates a new order for an account
func (c *Client) CreateOrder(ctx context.Context, accountID string, req *entities.AlpacaCreateOrderRequest) (*entities.AlpacaOrderResponse, error) {
	endpoint := fmt.Sprintf(ordersEndpoint, accountID)

	c.logger.Info("Creating Alpaca order",
		zap.String("account_id", accountID),
		zap.String("symbol", req.Symbol),
		zap.String("side", string(req.Side)),
		zap.String("type", string(req.Type)))

	var response entities.AlpacaOrderResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "POST", endpoint, req, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to create Alpaca order",
			zap.String("account_id", accountID),
			zap.String("symbol", req.Symbol),
			zap.Error(err))
		return nil, fmt.Errorf("create order failed: %w", err)
	}

	c.logger.Info("Created Alpaca order successfully",
		zap.String("order_id", response.ID),
		zap.String("status", string(response.Status)))

	return &response, nil
}

// GetOrder retrieves an order by ID
func (c *Client) GetOrder(ctx context.Context, accountID, orderID string) (*entities.AlpacaOrderResponse, error) {
	endpoint := fmt.Sprintf(ordersEndpoint+"/%s", accountID, orderID)

	var response entities.AlpacaOrderResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Alpaca order",
			zap.String("account_id", accountID),
			zap.String("order_id", orderID),
			zap.Error(err))
		return nil, fmt.Errorf("get order failed: %w", err)
	}

	return &response, nil
}

// ListOrders lists all orders for an account
func (c *Client) ListOrders(ctx context.Context, accountID string, query map[string]string) ([]entities.AlpacaOrderResponse, error) {
	endpoint := fmt.Sprintf(ordersEndpoint, accountID)
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Add(k, v)
		}
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	var response []entities.AlpacaOrderResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to list Alpaca orders",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("list orders failed: %w", err)
	}

	return response, nil
}

// CancelOrder cancels an order
func (c *Client) CancelOrder(ctx context.Context, accountID, orderID string) error {
	endpoint := fmt.Sprintf(ordersEndpoint+"/%s", accountID, orderID)

	c.logger.Info("Canceling Alpaca order",
		zap.String("account_id", accountID),
		zap.String("order_id", orderID))

	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return nil, c.doRequestWithRetry(ctx, "DELETE", endpoint, nil, nil, false)
	})

	if err != nil {
		c.logger.Error("Failed to cancel Alpaca order",
			zap.String("account_id", accountID),
			zap.String("order_id", orderID),
			zap.Error(err))
		return fmt.Errorf("cancel order failed: %w", err)
	}

	c.logger.Info("Canceled Alpaca order successfully",
		zap.String("order_id", orderID))

	return nil
}

// Asset Methods

// GetAsset retrieves an asset by symbol or ID
func (c *Client) GetAsset(ctx context.Context, symbolOrID string) (*entities.AlpacaAssetResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", assetsEndpoint, symbolOrID)

	var response entities.AlpacaAssetResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Alpaca asset",
			zap.String("symbol_or_id", symbolOrID),
			zap.Error(err))
		return nil, fmt.Errorf("get asset failed: %w", err)
	}

	return &response, nil
}

// ListAssets lists all assets
func (c *Client) ListAssets(ctx context.Context, query map[string]string) ([]entities.AlpacaAssetResponse, error) {
	endpoint := assetsEndpoint
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Add(k, v)
		}
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	var response []entities.AlpacaAssetResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to list Alpaca assets", zap.Error(err))
		return nil, fmt.Errorf("list assets failed: %w", err)
	}

	return response, nil
}

// Position Methods

// GetPosition retrieves a position by symbol
func (c *Client) GetPosition(ctx context.Context, accountID, symbol string) (*entities.AlpacaPositionResponse, error) {
	endpoint := fmt.Sprintf(positionsEndpoint+"/%s", accountID, symbol)

	var response entities.AlpacaPositionResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get Alpaca position",
			zap.String("account_id", accountID),
			zap.String("symbol", symbol),
			zap.Error(err))
		return nil, fmt.Errorf("get position failed: %w", err)
	}

	return &response, nil
}

// ListPositions lists all positions for an account
func (c *Client) ListPositions(ctx context.Context, accountID string) ([]entities.AlpacaPositionResponse, error) {
	endpoint := fmt.Sprintf(positionsEndpoint, accountID)

	var response []entities.AlpacaPositionResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to list Alpaca positions",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("list positions failed: %w", err)
	}

	return response, nil
}

// Market Data Methods

// GetNews fetches news articles from the market data API
func (c *Client) GetNews(ctx context.Context, req *entities.AlpacaNewsRequest) (*entities.AlpacaNewsResponse, error) {
	endpoint := newsEndpoint
	params := url.Values{}

	if len(req.Symbols) > 0 {
		params.Add("symbols", strings.Join(req.Symbols, ","))
	}
	if req.Start != nil {
		params.Add("start", req.Start.Format(time.RFC3339))
	}
	if req.End != nil {
		params.Add("end", req.End.Format(time.RFC3339))
	}
	if req.Limit > 0 {
		params.Add("limit", fmt.Sprintf("%d", req.Limit))
	}
	if req.Sort != "" {
		params.Add("sort", req.Sort)
	}
	if req.IncludeContent {
		params.Add("include_content", "true")
	}
	if req.ExcludeContentless {
		params.Add("exclude_contentless", "true")
	}
	if req.PageToken != "" {
		params.Add("page_token", req.PageToken)
	}

	if len(params) > 0 {
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	c.logger.Info("Fetching Alpaca news",
		zap.Strings("symbols", req.Symbols),
		zap.Int("limit", req.Limit))

	var response entities.AlpacaNewsResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, true)
	})

	if err != nil {
		c.logger.Error("Failed to fetch Alpaca news", zap.Error(err))
		return nil, fmt.Errorf("get news failed: %w", err)
	}

	c.logger.Info("Fetched Alpaca news successfully",
		zap.Int("count", len(response.News)))

	return &response, nil
}

// HTTP helper methods

// doRequestWithRetry performs an HTTP request with exponential backoff retry
func (c *Client) doRequestWithRetry(ctx context.Context, method, endpoint string, body, response interface{}, useDataAPI bool) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			var backoff time.Duration
			if alpacaErr, ok := lastErr.(AlpacaError); ok && alpacaErr.IsRetryable() {
				backoff = alpacaErr.RetryAfter()
			} else {
				backoff = calculateBackoff(attempt)
			}

			c.logger.Info("Retrying Alpaca API request",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := c.doRequest(ctx, method, endpoint, body, response, useDataAPI)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if alpacaErr, ok := err.(AlpacaError); ok {
			if !alpacaErr.IsRetryable() {
				c.logger.Warn("Non-retryable Alpaca error", zap.Error(err))
				return err
			}
		} else if !isRetryableError(err) {
			c.logger.Warn("Non-retryable error", zap.Error(err))
			return err
		}

		c.logger.Warn("Retryable error encountered",
			zap.Error(err),
			zap.Int("attempt", attempt))
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doRequest performs a single HTTP request
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body, response interface{}, useDataAPI bool) error {
	baseURL := c.config.BaseURL
	if useDataAPI {
		baseURL = c.config.DataBaseURL
	}

	fullURL := baseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	
	// Authentication
	if useDataAPI {
		// Market Data API uses header-based auth
		req.Header.Set("APCA-API-KEY-ID", c.config.ClientID)
		req.Header.Set("APCA-API-SECRET-KEY", c.config.SecretKey)
	} else {
		// Broker API uses OAuth2 Bearer token
		token, err := c.tokenManager.GetValidToken(ctx)
		if err != nil {
			return fmt.Errorf("failed to get access token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	c.logger.Debug("Sending Alpaca API request",
		zap.String("method", method),
		zap.String("url", fullURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Received Alpaca API response",
		zap.Int("status_code", resp.StatusCode),
		zap.Int("body_size", len(respBody)))

	// Check for error responses
	if resp.StatusCode >= 400 {
		return parseAlpacaError(resp.StatusCode, respBody)
	}

	// Parse response if a response object is provided
	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// calculateBackoff calculates exponential backoff with jitter
func calculateBackoff(attempt int) time.Duration {
	// Calculate exponential backoff: baseBackoff * 2^(attempt-1)
	backoff := float64(baseBackoff) * math.Pow(2, float64(attempt-1))

	// Apply max backoff limit
	if backoff > float64(maxBackoff) {
		backoff = float64(maxBackoff)
	}

	// Add jitter (±10%)
	jitter := backoff * jitterRange * (2*getRandomFloat() - 1)
	backoff += jitter

	return time.Duration(backoff)
}

// getRandomFloat returns a random float between 0 and 1
func getRandomFloat() float64 {
	return float64(time.Now().UnixNano()%1000) / 1000.0
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for Alpaca API errors
	if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
		// Retry on rate limits and server errors
		return apiErr.Code == http.StatusTooManyRequests ||
			apiErr.Code >= 500
	}

	// Retry on network errors and timeouts
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF")
}


// Market Data API methods

// GetLatestQuote returns the latest quote for a symbol
func (c *Client) GetLatestQuote(ctx context.Context, symbol string) (*entities.MarketQuote, error) {
	endpoint := fmt.Sprintf("/v2/stocks/%s/quotes/latest", symbol)
	var resp struct {
		Quote struct {
			AskPrice  float64   `json:"ap"`
			AskSize   int       `json:"as"`
			BidPrice  float64   `json:"bp"`
			BidSize   int       `json:"bs"`
			Timestamp time.Time `json:"t"`
		} `json:"quote"`
	}

	if err := c.doDataRequest(ctx, "GET", endpoint, nil, &resp); err != nil {
		return nil, err
	}

	// Get trade for last price
	tradeEndpoint := fmt.Sprintf("/v2/stocks/%s/trades/latest", symbol)
	var tradeResp struct {
		Trade struct {
			Price     float64   `json:"p"`
			Size      int       `json:"s"`
			Timestamp time.Time `json:"t"`
		} `json:"trade"`
	}
	_ = c.doDataRequest(ctx, "GET", tradeEndpoint, nil, &tradeResp)

	return &entities.MarketQuote{
		Symbol:    symbol,
		Price:     decimal.NewFromFloat(tradeResp.Trade.Price),
		Bid:       decimal.NewFromFloat(resp.Quote.BidPrice),
		Ask:       decimal.NewFromFloat(resp.Quote.AskPrice),
		Timestamp: resp.Quote.Timestamp,
	}, nil
}

// GetLatestQuotes returns latest quotes for multiple symbols
func (c *Client) GetLatestQuotes(ctx context.Context, symbols []string) (map[string]*entities.MarketQuote, error) {
	endpoint := "/v2/stocks/quotes/latest?symbols=" + url.QueryEscape(strings.Join(symbols, ","))
	var resp struct {
		Quotes map[string]struct {
			AskPrice  float64   `json:"ap"`
			BidPrice  float64   `json:"bp"`
			Timestamp time.Time `json:"t"`
		} `json:"quotes"`
	}

	if err := c.doDataRequest(ctx, "GET", endpoint, nil, &resp); err != nil {
		return nil, err
	}

	// Get trades for prices
	tradeEndpoint := "/v2/stocks/trades/latest?symbols=" + url.QueryEscape(strings.Join(symbols, ","))
	var tradeResp struct {
		Trades map[string]struct {
			Price     float64   `json:"p"`
			Timestamp time.Time `json:"t"`
		} `json:"trades"`
	}
	_ = c.doDataRequest(ctx, "GET", tradeEndpoint, nil, &tradeResp)

	result := make(map[string]*entities.MarketQuote)
	for sym, q := range resp.Quotes {
		quote := &entities.MarketQuote{
			Symbol:    sym,
			Bid:       decimal.NewFromFloat(q.BidPrice),
			Ask:       decimal.NewFromFloat(q.AskPrice),
			Timestamp: q.Timestamp,
		}
		if t, ok := tradeResp.Trades[sym]; ok {
			quote.Price = decimal.NewFromFloat(t.Price)
		}
		result[sym] = quote
	}

	return result, nil
}

// Account Activities Methods

// GetAccountActivities retrieves account activities (trades, dividends, etc.)
func (c *Client) GetAccountActivities(ctx context.Context, accountID string, query map[string]string) ([]entities.AlpacaActivityResponse, error) {
	endpoint := fmt.Sprintf(activitiesEndpoint, accountID)
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Add(k, v)
		}
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	var response []entities.AlpacaActivityResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get account activities",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("get account activities failed: %w", err)
	}

	return response, nil
}

// GetPortfolioHistory retrieves portfolio performance history
func (c *Client) GetPortfolioHistory(ctx context.Context, accountID string, query map[string]string) (*entities.AlpacaPortfolioHistoryResponse, error) {
	endpoint := fmt.Sprintf(portfolioEndpoint+"/history", accountID)
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Add(k, v)
		}
		endpoint = fmt.Sprintf("%s?%s", endpoint, params.Encode())
	}

	var response entities.AlpacaPortfolioHistoryResponse
	_, err := c.circuitBreaker.Execute(func() (interface{}, error) {
		return &response, c.doRequestWithRetry(ctx, "GET", endpoint, nil, &response, false)
	})

	if err != nil {
		c.logger.Error("Failed to get portfolio history",
			zap.String("account_id", accountID),
			zap.Error(err))
		return nil, fmt.Errorf("get portfolio history failed: %w", err)
	}

	return &response, nil
}

// GetBars returns historical OHLCV bars
func (c *Client) GetBars(ctx context.Context, symbol, timeframe string, start, end time.Time) ([]*entities.MarketBar, error) {
	endpoint := fmt.Sprintf("/v2/stocks/%s/bars?timeframe=%s&start=%s&end=%s",
		symbol, timeframe, start.Format(time.RFC3339), end.Format(time.RFC3339))

	var resp struct {
		Bars []struct {
			Open      float64   `json:"o"`
			High      float64   `json:"h"`
			Low       float64   `json:"l"`
			Close     float64   `json:"c"`
			Volume    int64     `json:"v"`
			Timestamp time.Time `json:"t"`
		} `json:"bars"`
	}

	if err := c.doDataRequest(ctx, "GET", endpoint, nil, &resp); err != nil {
		return nil, err
	}

	bars := make([]*entities.MarketBar, len(resp.Bars))
	for i, b := range resp.Bars {
		bars[i] = &entities.MarketBar{
			Symbol:    symbol,
			Open:      decimal.NewFromFloat(b.Open),
			High:      decimal.NewFromFloat(b.High),
			Low:       decimal.NewFromFloat(b.Low),
			Close:     decimal.NewFromFloat(b.Close),
			Volume:    b.Volume,
			Timestamp: b.Timestamp,
		}
	}

	return bars, nil
}

// doDataRequest makes a request to the Market Data API
func (c *Client) doDataRequest(ctx context.Context, method, endpoint string, body, response interface{}) error {
	fullURL := c.config.DataBaseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("APCA-API-KEY-ID", c.config.ClientID)
	req.Header.Set("APCA-API-SECRET-KEY", c.config.SecretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	if response != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return nil
}
