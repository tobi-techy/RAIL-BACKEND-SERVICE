package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
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
	// Build query parameters for Alpaca API
	query := make(map[string]string)

	// Status filter (default: active)
	status := c.DefaultQuery("status", "active")
	if status != "" {
		query["status"] = status
	}

	// Asset class filter
	if assetClass := c.Query("asset_class"); assetClass != "" {
		query["asset_class"] = assetClass
	}

	// Exchange filter
	if exchange := c.Query("exchange"); exchange != "" {
		query["exchange"] = exchange
	}

	// Tradable filter (default: true for user-facing app)
	tradable := c.DefaultQuery("tradable", "true")
	if tradable != "" {
		query["tradable"] = tradable
	}

	// Fractionable filter
	if fractionable := c.Query("fractionable"); fractionable != "" {
		query["fractionable"] = fractionable
	}

	// Shortable filter
	if shortable := c.Query("shortable"); shortable != "" {
		query["shortable"] = shortable
	}

	// Easy to borrow filter
	if easyToBorrow := c.Query("easy_to_borrow"); easyToBorrow != "" {
		query["easy_to_borrow"] = easyToBorrow
	}

	h.logger.Info("Fetching assets from Alpaca",
		zap.Any("filters", query))

	// Call Alpaca API
	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets from Alpaca",
			zap.Error(err),
			zap.Any("filters", query))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "ASSETS_FETCH_ERROR",
			Message: "Failed to retrieve assets",
		})
		return
	}

	// Apply client-side search filter if provided
	searchTerm := strings.ToLower(c.Query("search"))
	if searchTerm != "" {
		filtered := make([]entities.AlpacaAssetResponse, 0)
		for _, asset := range assets {
			if strings.Contains(strings.ToLower(asset.Symbol), searchTerm) ||
				strings.Contains(strings.ToLower(asset.Name), searchTerm) {
				filtered = append(filtered, asset)
			}
		}
		assets = filtered
	}

	// Apply pagination (client-side since Alpaca returns all results)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}

	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if pageSize < 1 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500 // Max limit for performance
	}

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
		zap.Int("page", page),
		zap.Int("page_size", pageSize),
		zap.Int("returned_count", len(paginatedAssets)))

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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Message: "Asset symbol or ID is required",
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
				c.JSON(http.StatusNotFound, entities.ErrorResponse{
					Code:  "ASSET_NOT_FOUND",
					Message: "Asset not found",
					Details: map[string]interface{}{"symbol": symbolOrID},
				})
				return
			}
		}

		h.logger.Error("Failed to fetch asset from Alpaca",
			zap.Error(err),
			zap.String("symbol_or_id", symbolOrID))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "ASSET_FETCH_ERROR",
			Message: "Failed to retrieve asset details",
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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Message: "Search query parameter 'q' is required",
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
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "SEARCH_ERROR",
			Message: "Failed to search assets",
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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_PARAMETER",
			Message: "Exchange parameter is required",
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
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:  "INVALID_EXCHANGE",
			Message: "Invalid exchange code",
			Details: map[string]interface{}{"message": "Valid exchanges: NASDAQ, NYSE, ARCA, BATS, AMEX"},
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
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:  "ASSETS_FETCH_ERROR",
			Message: "Failed to retrieve assets",
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
