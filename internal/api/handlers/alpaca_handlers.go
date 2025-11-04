package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/adapters/alpaca"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"go.uber.org/zap"
)

// AlpacaHandlers contains handlers for Alpaca broker API operations
type AlpacaHandlers struct {
	alpacaClient *alpaca.Client
	logger       *zap.Logger
}

// NewAlpacaHandlers creates new Alpaca handlers
func NewAlpacaHandlers(alpacaClient *alpaca.Client, logger *zap.Logger) *AlpacaHandlers {
	return &AlpacaHandlers{
		alpacaClient: alpacaClient,
		logger:       logger,
	}
}

// AssetsResponse represents the paginated response for assets
type AssetsResponse struct {
	Assets     []entities.AlpacaAssetResponse `json:"assets"`
	TotalCount int                            `json:"total_count"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
}

// GetAssets retrieves all tradable assets with filtering and pagination
// @Summary Get all tradable assets
// @Description Retrieve a list of all tradable assets (stocks, ETFs) with optional filtering
// @Tags assets
// @Produce json
// @Param status query string false "Asset status filter (active, inactive)" default(active)
// @Param asset_class query string false "Asset class filter (us_equity, crypto)"
// @Param exchange query string false "Exchange filter (NASDAQ, NYSE, ARCA, BATS)"
// @Param tradable query boolean false "Filter by tradability" default(true)
// @Param fractionable query boolean false "Filter by fractional shares support"
// @Param shortable query boolean false "Filter by short selling support"
// @Param easy_to_borrow query boolean false "Filter by easy-to-borrow status"
// @Param search query string false "Search by symbol or name"
// @Param page query int false "Page number" default(1)
// @Param page_size query int false "Items per page (max 500)" default(100)
// @Success 200 {object} AssetsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets [get]
func (h *AlpacaHandlers) GetAssets(c *gin.Context) {
	// Validate and parse pagination parameters first
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		page = 1
	}

	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if err != nil || pageSize < 1 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500 // Max limit for performance
	}

	// Build query parameters for Alpaca API
	query := make(map[string]string)

	// Status filter (default: active)
	status := c.DefaultQuery("status", "active")
	query["status"] = status

	// Asset class filter
	if assetClass := c.Query("asset_class"); assetClass != "" {
		if assetClass != "us_equity" && assetClass != "crypto" {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Code:  "INVALID_ASSET_CLASS",
				Error: "Asset class must be 'us_equity' or 'crypto'",
			})
			return
		}
		query["asset_class"] = assetClass
	}

	// Exchange filter with validation
	if exchange := c.Query("exchange"); exchange != "" {
		validExchanges := map[string]bool{
			"NASDAQ": true, "NYSE": true, "ARCA": true, "BATS": true,
			"AMEX": true, "NYSEARCA": true, "OTC": true,
		}
		if !validExchanges[strings.ToUpper(exchange)] {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Code:    "INVALID_EXCHANGE",
				Error:   "Invalid exchange code",
				Details: "Valid exchanges: NASDAQ, NYSE, ARCA, BATS, AMEX, NYSEARCA, OTC",
			})
			return
		}
		query["exchange"] = strings.ToUpper(exchange)
	}

	// Boolean filters with validation
	booleanFilters := map[string]string{
		"tradable":       "tradable",
		"fractionable":   "fractionable",
		"shortable":      "shortable",
		"easy_to_borrow": "easy_to_borrow",
	}

	for paramName, queryName := range booleanFilters {
		if value := c.Query(paramName); value != "" {
			if value != "true" && value != "false" {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Code:  "INVALID_BOOLEAN_FILTER",
					Error: fmt.Sprintf("Filter '%s' must be 'true' or 'false'", paramName),
				})
				return
			}
			query[queryName] = value
		}
	}

	// Set default tradable filter for user-facing app
	if _, exists := query["tradable"]; !exists {
		query["tradable"] = "true"
	}

	h.logger.Info("Fetching assets from Alpaca",
		zap.Any("filters", query),
		zap.Int("page", page),
		zap.Int("page_size", pageSize))

	// Call Alpaca API
	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets from Alpaca",
			zap.Error(err),
			zap.Any("filters", query))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Code:  "ASSETS_FETCH_ERROR",
			Error: "Failed to retrieve assets",
		})
		return
	}

	// Apply client-side search filter if provided (since Alpaca API doesn't support search)
	searchTerm := strings.ToLower(strings.TrimSpace(c.Query("search")))
	if searchTerm != "" {
		if len(searchTerm) < 2 {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Code:  "SEARCH_TOO_SHORT",
				Error: "Search term must be at least 2 characters long",
			})
			return
		}

		filtered := make([]entities.AlpacaAssetResponse, 0, len(assets))
		for _, asset := range assets {
			symbolLower := strings.ToLower(asset.Symbol)
			nameLower := strings.ToLower(asset.Name)

			// Exact match gets priority, then prefix match, then contains
			if symbolLower == searchTerm || nameLower == searchTerm {
				filtered = append([]entities.AlpacaAssetResponse{asset}, filtered...) // prepend
			} else if strings.HasPrefix(symbolLower, searchTerm) || strings.HasPrefix(nameLower, searchTerm) {
				filtered = append([]entities.AlpacaAssetResponse{asset}, filtered...) // prepend
			} else if strings.Contains(symbolLower, searchTerm) || strings.Contains(nameLower, searchTerm) {
				filtered = append(filtered, asset)
			}
		}
		assets = filtered
	}

	// Apply pagination
	totalCount := len(assets)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start >= totalCount {
		// Return empty results for out-of-range pages
		c.JSON(http.StatusOK, AssetsResponse{
			Assets:     []entities.AlpacaAssetResponse{},
			TotalCount: totalCount,
			Page:       page,
			PageSize:   pageSize,
		})
		return
	}

	if end > totalCount {
		end = totalCount
	}

	paginatedAssets := assets[start:end]

	h.logger.Info("Successfully fetched assets",
		zap.Int("total_count", totalCount),
		zap.Int("filtered_count", len(assets)),
		zap.Int("page", page),
		zap.Int("page_size", pageSize),
		zap.Int("returned_count", len(paginatedAssets)),
		zap.String("search_term", searchTerm))

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     paginatedAssets,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	})
}

// GetAsset retrieves detailed information about a specific asset
// @Summary Get asset details
// @Description Retrieve detailed information about a specific asset by symbol or asset ID
// @Tags assets
// @Produce json
// @Param symbol_or_id path string true "Asset symbol (e.g., AAPL) or Asset ID (UUID)"
// @Success 200 {object} entities.AlpacaAssetResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets/{symbol_or_id} [get]
func (h *AlpacaHandlers) GetAsset(c *gin.Context) {
	symbolOrID := c.Param("symbol_or_id")

	if symbolOrID == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Error: "Asset symbol or ID is required",
		})
		return
	}

	// Normalize symbol to uppercase for consistency
	symbolOrID = strings.ToUpper(strings.TrimSpace(symbolOrID))

	h.logger.Info("Fetching asset details from Alpaca",
		zap.String("symbol_or_id", symbolOrID))

	// Call Alpaca API to get asset details
	asset, err := h.alpacaClient.GetAsset(c.Request.Context(), symbolOrID)
	if err != nil {
		// Check if it's an API error
		if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
			if apiErr.Code == http.StatusNotFound {
				h.logger.Warn("Asset not found",
					zap.String("symbol_or_id", symbolOrID))
				c.JSON(http.StatusNotFound, ErrorResponse{
					Code:    "ASSET_NOT_FOUND",
					Error:   "Asset not found",
					Details: symbolOrID,
				})
				return
			}
		}

		h.logger.Error("Failed to fetch asset from Alpaca",
			zap.Error(err),
			zap.String("symbol_or_id", symbolOrID))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Code:  "ASSET_FETCH_ERROR",
			Error: "Failed to retrieve asset details",
		})
		return
	}

	h.logger.Info("Successfully fetched asset details",
		zap.String("symbol", asset.Symbol),
		zap.String("name", asset.Name),
		zap.String("status", string(asset.Status)))

	c.JSON(http.StatusOK, asset)
}

// GetPopularAssets retrieves a curated list of popular/trending assets
// @Summary Get popular assets
// @Description Retrieve a curated list of popular stocks and ETFs for quick access
// @Tags assets
// @Produce json
// @Success 200 {object} AssetsResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets/popular [get]
func (h *AlpacaHandlers) GetPopularAssets(c *gin.Context) {
	// Define popular symbols (can be moved to configuration or database)
	popularSymbols := []string{
		// Tech Giants
		"AAPL", "MSFT", "GOOGL", "AMZN", "META", "NVDA", "TSLA",
		// Popular ETFs
		"SPY", "QQQ", "VOO", "VTI", "IVV",
		// Other Popular Stocks
		"NFLX", "AMD", "INTC", "DIS", "BA",
	}

	h.logger.Info("Fetching popular assets",
		zap.Int("count", len(popularSymbols)))

	assets := make([]entities.AlpacaAssetResponse, 0, len(popularSymbols))

	// Fetch each popular asset
	for _, symbol := range popularSymbols {
		asset, err := h.alpacaClient.GetAsset(c.Request.Context(), symbol)
		if err != nil {
			h.logger.Warn("Failed to fetch popular asset, skipping",
				zap.String("symbol", symbol),
				zap.Error(err))
			continue
		}

		// Only include active and tradable assets
		if asset.Status == entities.AlpacaAssetStatusActive && asset.Tradable {
			assets = append(assets, *asset)
		}
	}

	h.logger.Info("Successfully fetched popular assets",
		zap.Int("total_count", len(assets)))

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     assets,
		TotalCount: len(assets),
		Page:       1,
		PageSize:   len(assets),
	})
}

// SearchAssets searches for assets by symbol or name
// @Summary Search assets
// @Description Search for assets by symbol or company name
// @Tags assets
// @Produce json
// @Param q query string true "Search query (symbol or name)"
// @Param limit query int false "Maximum results to return" default(20)
// @Success 200 {object} AssetsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets/search [get]
func (h *AlpacaHandlers) SearchAssets(c *gin.Context) {
	searchQuery := strings.TrimSpace(c.Query("q"))

	if searchQuery == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Error: "Search query parameter 'q' is required",
		})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	h.logger.Info("Searching assets",
		zap.String("query", searchQuery),
		zap.Int("limit", limit))

	// Fetch all active tradable assets
	query := map[string]string{
		"status":   "active",
		"tradable": "true",
	}

	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets for search",
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Code:  "SEARCH_ERROR",
			Error: "Failed to search assets",
		})
		return
	}

	// Perform case-insensitive search
	searchLower := strings.ToLower(searchQuery)
	results := make([]entities.AlpacaAssetResponse, 0)

	for _, asset := range assets {
		// Check if symbol or name contains search term
		if strings.Contains(strings.ToLower(asset.Symbol), searchLower) ||
			strings.Contains(strings.ToLower(asset.Name), searchLower) {
			results = append(results, asset)

			// Stop when we reach the limit
			if len(results) >= limit {
				break
			}
		}
	}

	h.logger.Info("Search completed",
		zap.String("query", searchQuery),
		zap.Int("results_count", len(results)))

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     results,
		TotalCount: len(results),
		Page:       1,
		PageSize:   len(results),
	})
}

// GetAssetsByExchange retrieves assets by exchange
// @Summary Get assets by exchange
// @Description Retrieve assets listed on a specific exchange
// @Tags assets
// @Produce json
// @Param exchange path string true "Exchange code (NASDAQ, NYSE, ARCA, BATS)"
// @Param page query int false "Page number" default(1)
// @Param page_size query int false "Items per page" default(50)
// @Success 200 {object} AssetsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets/exchange/{exchange} [get]
func (h *AlpacaHandlers) GetAssetsByExchange(c *gin.Context) {
	exchange := strings.ToUpper(strings.TrimSpace(c.Param("exchange")))

	if exchange == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Error: "Exchange parameter is required",
		})
		return
	}

	// Validate exchange (optional but recommended)
	validExchanges := map[string]bool{
		"NASDAQ": true,
		"NYSE":   true,
		"ARCA":   true,
		"BATS":   true,
		"AMEX":   true,
	}

	if !validExchanges[exchange] {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:    "INVALID_EXCHANGE",
			Error:   "Invalid exchange code",
			Details: "Valid exchanges: NASDAQ, NYSE, ARCA, BATS, AMEX",
		})
		return
	}

	h.logger.Info("Fetching assets by exchange",
		zap.String("exchange", exchange))

	// Build query for Alpaca
	query := map[string]string{
		"status":   "active",
		"tradable": "true",
		"exchange": exchange,
	}

	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets by exchange",
			zap.Error(err),
			zap.String("exchange", exchange))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Code:  "ASSETS_FETCH_ERROR",
			Error: "Failed to retrieve assets",
		})
		return
	}

	// Apply pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}

	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	totalCount := len(assets)
	start := (page - 1) * pageSize
	end := start + pageSize

	if start >= totalCount {
		c.JSON(http.StatusOK, AssetsResponse{
			Assets:     []entities.AlpacaAssetResponse{},
			TotalCount: totalCount,
			Page:       page,
			PageSize:   pageSize,
		})
		return
	}

	if end > totalCount {
		end = totalCount
	}

	paginatedAssets := assets[start:end]

	h.logger.Info("Successfully fetched assets by exchange",
		zap.String("exchange", exchange),
		zap.Int("total_count", totalCount),
		zap.Int("returned_count", len(paginatedAssets)))

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     paginatedAssets,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	})
}

// AssetDetailResponse represents comprehensive asset details with position
type AssetDetailResponse struct {
	// Basic Asset Information
	AssetInfo AssetInfo `json:"asset_info"`

	// Position Information (if user holds this asset)
	Position *PositionInfo `json:"position,omitempty"`

	// Trading Information
	TradingInfo TradingInfo `json:"trading_info"`

	// Market Context
	MarketContext MarketContext `json:"market_context"`

	// Related News (latest 5)
	RecentNews []NewsItem `json:"recent_news,omitempty"`

	// Metadata
	Metadata ResponseMetadata `json:"metadata"`
}

// AssetInfo contains core asset details
type AssetInfo struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Name         string `json:"name"`
	Class        string `json:"class"`
	Exchange     string `json:"exchange"`
	Status       string `json:"status"`
	Tradable     bool   `json:"tradable"`
	Marginable   bool   `json:"marginable"`
	Shortable    bool   `json:"shortable"`
	EasyToBorrow bool   `json:"easy_to_borrow"`
	Fractionable bool   `json:"fractionable"`
}

// PositionInfo contains user's position in this asset
type PositionInfo struct {
	Quantity            string `json:"quantity"`
	AvgEntryPrice       string `json:"avg_entry_price"`
	MarketValue         string `json:"market_value"`
	CostBasis           string `json:"cost_basis"`
	UnrealizedPL        string `json:"unrealized_pl"`
	UnrealizedPLPercent string `json:"unrealized_pl_percent"`
	CurrentPrice        string `json:"current_price"`
	Side                string `json:"side"` // long or short
	QtyAvailable        string `json:"qty_available"`
}

// TradingInfo contains trading-specific information
type TradingInfo struct {
	MinOrderSize      *string `json:"min_order_size,omitempty"`
	MinTradeIncrement *string `json:"min_trade_increment,omitempty"`
	PriceIncrement    *string `json:"price_increment,omitempty"`
	SupportsMarket    bool    `json:"supports_market_orders"`
	SupportsLimit     bool    `json:"supports_limit_orders"`
	SupportsStop      bool    `json:"supports_stop_orders"`
	ExtendedHours     bool    `json:"extended_hours_trading"`
}

// MarketContext provides market-related context
type MarketContext struct {
	IsMarketOpen    bool       `json:"is_market_open"`
	NextMarketOpen  *time.Time `json:"next_market_open,omitempty"`
	NextMarketClose *time.Time `json:"next_market_close,omitempty"`
	Timezone        string     `json:"timezone"`
}

// NewsItem represents a news article
type NewsItem struct {
	ID        int       `json:"id"`
	Headline  string    `json:"headline"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// ResponseMetadata contains response metadata
type ResponseMetadata struct {
	Timestamp   time.Time `json:"timestamp"`
	RequestID   string    `json:"request_id,omitempty"`
	CacheStatus string    `json:"cache_status,omitempty"`
}

// GetAssetDetails retrieves comprehensive asset details with position data
// @Summary Get comprehensive asset details
// @Description Retrieve complete asset information including position data, trading info, and recent news
// @Tags assets
// @Produce json
// @Param symbol path string true "Asset symbol (e.g., AAPL)"
// @Param account_id query string false "Account ID to fetch position data"
// @Param include_news query boolean false "Include recent news articles" default(true)
// @Success 200 {object} AssetDetailResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/assets/{symbol}/details [get]
func (h *AlpacaHandlers) GetAssetDetails(c *gin.Context) {
	symbol := strings.ToUpper(strings.TrimSpace(c.Param("symbol")))

	if symbol == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Error: "Asset symbol is required",
		})
		return
	}

	// Get user ID from context (for position data)
	userID, exists := c.Get("user_id")
	var userUUID uuid.UUID
	if exists {
		if uid, ok := userID.(uuid.UUID); ok {
			userUUID = uid
		}
	}

	// Optional: account ID from query params
	accountID := c.Query("account_id")
	includeNews := c.DefaultQuery("include_news", "true") == "true"

	h.logger.Info("Fetching comprehensive asset details",
		zap.String("symbol", symbol),
		zap.String("account_id", accountID),
		zap.Bool("include_news", includeNews))

	// 1. Fetch asset information
	asset, err := h.alpacaClient.GetAsset(c.Request.Context(), symbol)
	if err != nil {
		if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
			if apiErr.Code == http.StatusNotFound {
				c.JSON(http.StatusNotFound, ErrorResponse{
					Code:    "ASSET_NOT_FOUND",
					Error:   "Asset not found",
					Details: symbol,
				})
				return
			}
		}
		h.logger.Error("Failed to fetch asset details",
			zap.String("symbol", symbol),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Code:  "ASSET_FETCH_ERROR",
			Error: "Failed to retrieve asset details",
		})
		return
	}

	// 2. Build asset info
	assetInfo := AssetInfo{
		ID:           asset.ID,
		Symbol:       asset.Symbol,
		Name:         asset.Name,
		Class:        string(asset.Class),
		Exchange:     asset.Exchange,
		Status:       string(asset.Status),
		Tradable:     asset.Tradable,
		Marginable:   asset.Marginable,
		Shortable:    asset.Shortable,
		EasyToBorrow: asset.EasyToBorrow,
		Fractionable: asset.Fractionable,
	}

	// 3. Build trading info
	tradingInfo := TradingInfo{
		SupportsMarket: true, // Alpaca supports these by default
		SupportsLimit:  true,
		SupportsStop:   true,
		ExtendedHours:  false, // Can be configured
	}

	if asset.MinOrderSize != nil {
		minOrder := asset.MinOrderSize.String()
		tradingInfo.MinOrderSize = &minOrder
	}
	if asset.MinTradeIncrement != nil {
		minTrade := asset.MinTradeIncrement.String()
		tradingInfo.MinTradeIncrement = &minTrade
	}
	if asset.PriceIncrement != nil {
		priceInc := asset.PriceIncrement.String()
		tradingInfo.PriceIncrement = &priceInc
	}

	// 4. Fetch position data (if account ID provided and user authenticated)
	var positionInfo *PositionInfo
	if accountID != "" && userUUID != uuid.Nil {
		position, err := h.alpacaClient.GetPosition(c.Request.Context(), accountID, symbol)
		if err == nil {
			// User has a position in this asset
			positionInfo = &PositionInfo{
				Quantity:            position.Qty.String(),
				AvgEntryPrice:       position.AvgEntryPrice.String(),
				MarketValue:         position.MarketValue.String(),
				CostBasis:           position.CostBasis.String(),
				UnrealizedPL:        position.UnrealizedPL.String(),
				UnrealizedPLPercent: position.UnrealizedPLPC.String(),
				CurrentPrice:        position.CurrentPrice.String(),
				Side:                position.Side,
				QtyAvailable:        position.QtyAvailable.String(),
			}
		} else {
			// No position or error fetching - log but don't fail
			h.logger.Debug("No position found for asset",
				zap.String("symbol", symbol),
				zap.String("account_id", accountID))
		}
	}

	// 5. Build market context
	marketContext := buildMarketContext()

	// 6. Fetch recent news (optional)
	var recentNews []NewsItem
	if includeNews {
		newsReq := &entities.AlpacaNewsRequest{
			Symbols: []string{symbol},
			Limit:   5,
			Sort:    "DESC",
		}

		newsResp, err := h.alpacaClient.GetNews(c.Request.Context(), newsReq)
		if err == nil && len(newsResp.News) > 0 {
			for _, article := range newsResp.News {
				recentNews = append(recentNews, NewsItem{
					ID:        article.ID,
					Headline:  article.Headline,
					Summary:   article.Summary,
					Source:    article.Source,
					URL:       article.URL,
					CreatedAt: article.CreatedAt,
				})
			}
		} else if err != nil {
			h.logger.Warn("Failed to fetch news for asset",
				zap.String("symbol", symbol),
				zap.Error(err))
		}
	}

	// 7. Build response
	response := AssetDetailResponse{
		AssetInfo:     assetInfo,
		Position:      positionInfo,
		TradingInfo:   tradingInfo,
		MarketContext: marketContext,
		RecentNews:    recentNews,
		Metadata: ResponseMetadata{
			Timestamp:   time.Now(),
			RequestID:   c.GetString("request_id"),
			CacheStatus: "miss",
		},
	}

	h.logger.Info("Successfully fetched comprehensive asset details",
		zap.String("symbol", symbol),
		zap.Bool("has_position", positionInfo != nil),
		zap.Int("news_count", len(recentNews)))

	c.JSON(http.StatusOK, response)
}

// buildMarketContext creates market context information
func buildMarketContext() MarketContext {
	// Simple implementation - can be enhanced with actual market hours API
	now := time.Now().In(time.FixedZone("EST", -5*3600))
	weekday := now.Weekday()
	hour := now.Hour()

	// US market hours: 9:30 AM - 4:00 PM EST, Monday-Friday
	isMarketOpen := weekday >= time.Monday && weekday <= time.Friday &&
		((hour == 9 && now.Minute() >= 30) || (hour > 9 && hour < 16))

	marketContext := MarketContext{
		IsMarketOpen: isMarketOpen,
		Timezone:     "America/New_York",
	}

	// Calculate next market open/close (simplified)
	if !isMarketOpen {
		// Calculate next market open
		nextOpen := time.Date(now.Year(), now.Month(), now.Day(), 9, 30, 0, 0, now.Location())
		if now.After(nextOpen) || weekday == time.Saturday || weekday == time.Sunday {
			// Move to next business day
			if weekday == time.Friday && now.Hour() >= 16 {
				nextOpen = nextOpen.AddDate(0, 0, 3) // Monday
			} else if weekday == time.Saturday {
				nextOpen = nextOpen.AddDate(0, 0, 2) // Monday
			} else if weekday == time.Sunday {
				nextOpen = nextOpen.AddDate(0, 0, 1) // Monday
			} else {
				nextOpen = nextOpen.AddDate(0, 0, 1) // Next day
			}
		}
		marketContext.NextMarketOpen = &nextOpen
	} else {
		// Market is open, calculate next close
		nextClose := time.Date(now.Year(), now.Month(), now.Day(), 16, 0, 0, 0, now.Location())
		marketContext.NextMarketClose = &nextClose
	}

	return marketContext
}
