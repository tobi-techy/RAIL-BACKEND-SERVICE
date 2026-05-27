package common

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// IntegrationHandlers consolidates all external service integration handlers
type IntegrationHandlers struct {
	// Alpaca
	alpacaClient *alpaca.Client

	// Notification
	notificationService *services.NotificationService

	logger *zap.Logger
	logos  *assetLogoResolver
}

// NewIntegrationHandlers creates new integration handlers
func NewIntegrationHandlers(
	alpacaClient *alpaca.Client,
	notificationService *services.NotificationService,
	logger *logger.Logger,
) *IntegrationHandlers {
	return &IntegrationHandlers{
		alpacaClient:        alpacaClient,
		notificationService: notificationService,
		logger:              logger.Zap(),
		logos:               newAssetLogoResolverFromEnv(logger.Zap()),
	}
}

// ===== ALPACA HANDLERS =====

type AssetsResponse struct {
	Assets     []entities.AlpacaAssetResponse `json:"assets"`
	TotalCount int                            `json:"total_count"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
}

const assetLogoCacheTTL = 24 * time.Hour

type assetLogoResolver struct {
	provider string
	baseURL  string
	token    string
	size     int
	format   string
	theme    string

	mu    sync.RWMutex
	cache map[string]cachedAssetLogo
}

type cachedAssetLogo struct {
	url       *string
	fetchedAt time.Time
}

func (h *IntegrationHandlers) GetAssets(c *gin.Context) {
	query := make(map[string]string)
	status := c.DefaultQuery("status", "active")
	if status != "" {
		query["status"] = status
	}
	if assetClass := c.Query("asset_class"); assetClass != "" {
		query["asset_class"] = assetClass
	}
	if exchange := c.Query("exchange"); exchange != "" {
		query["exchange"] = exchange
	}
	tradable := c.DefaultQuery("tradable", "true")
	if tradable != "" {
		query["tradable"] = tradable
	}
	if fractionable := c.Query("fractionable"); fractionable != "" {
		query["fractionable"] = fractionable
	}
	if shortable := c.Query("shortable"); shortable != "" {
		query["shortable"] = shortable
	}
	if easyToBorrow := c.Query("easy_to_borrow"); easyToBorrow != "" {
		query["easy_to_borrow"] = easyToBorrow
	}

	assets, err := h.alpacaClient.ListAssets(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("Failed to fetch assets", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ASSETS_FETCH_ERROR",
			Message: "Failed to retrieve assets",
		})
		return
	}

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

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "100"))
	if pageSize < 1 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
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

	pageAssets := enrichAssetMetadata(assets[start:end], h.logos)
	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     pageAssets,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	})
}

func (h *IntegrationHandlers) GetAsset(c *gin.Context) {
	symbolOrID := strings.ToUpper(strings.TrimSpace(c.Param("symbol_or_id")))
	if symbolOrID == "" {
		c.JSON(http.StatusBadRequest, entities.ErrorResponse{
			Code:    "INVALID_PARAMETER",
			Message: "Asset symbol or ID is required",
		})
		return
	}

	asset, err := h.alpacaClient.GetAsset(c.Request.Context(), symbolOrID)
	if err != nil {
		if apiErr, ok := err.(*entities.AlpacaErrorResponse); ok {
			if apiErr.Code == http.StatusNotFound {
				c.JSON(http.StatusNotFound, entities.ErrorResponse{
					Code:    "ASSET_NOT_FOUND",
					Message: "Asset not found",
					Details: map[string]interface{}{"symbol": symbolOrID},
				})
				return
			}
		}
		h.logger.Error("Failed to fetch asset", zap.Error(err))
		c.JSON(http.StatusInternalServerError, entities.ErrorResponse{
			Code:    "ASSET_FETCH_ERROR",
			Message: "Failed to retrieve asset details",
		})
		return
	}

	asset.Description = buildAssetDescription(*asset)
	asset.LogoURL = h.logos.Resolve(asset.Symbol)
	c.JSON(http.StatusOK, asset)
}

func enrichAssetMetadata(assets []entities.AlpacaAssetResponse, logos *assetLogoResolver) []entities.AlpacaAssetResponse {
	enriched := make([]entities.AlpacaAssetResponse, len(assets))
	copy(enriched, assets)
	for i := range enriched {
		enriched[i].Description = buildAssetDescription(enriched[i])
		enriched[i].LogoURL = logos.Resolve(enriched[i].Symbol)
	}
	return enriched
}

func buildAssetDescription(asset entities.AlpacaAssetResponse) string {
	typeLabel := "stock"
	nameLower := strings.ToLower(asset.Name)
	if strings.Contains(nameLower, " etf") || strings.Contains(nameLower, "exchange traded fund") || strings.Contains(nameLower, " fund") || strings.HasSuffix(nameLower, " etf") {
		typeLabel = "ETF"
	}

	description := asset.Name + " is a " + typeLabel + " listed on " + asset.Exchange + "."
	if asset.Tradable {
		description += " Tradable"
	} else {
		description += " Not currently tradable"
	}
	if asset.Fractionable {
		description += " and supports fractional investing."
	} else {
		description += "."
	}
	return description
}

func newAssetLogoResolverFromEnv(logger *zap.Logger) *assetLogoResolver {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("MARKET_LOGO_PROVIDER")))
	if provider == "" {
		provider = "logo_dev"
	}

	baseURL := strings.TrimSpace(os.Getenv("MARKET_LOGO_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://img.logo.dev"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	token := strings.TrimSpace(os.Getenv("LOGO_DEV_PUBLISHABLE_KEY"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("LOGO_DEV_TOKEN"))
	}

	size := 128
	if rawSize := strings.TrimSpace(os.Getenv("LOGO_DEV_SIZE")); rawSize != "" {
		if parsed, err := strconv.Atoi(rawSize); err == nil && parsed > 0 {
			size = parsed
		}
	}

	format := strings.ToLower(strings.TrimSpace(os.Getenv("LOGO_DEV_FORMAT")))
	if format == "" {
		format = "png"
	}
	theme := strings.ToLower(strings.TrimSpace(os.Getenv("LOGO_DEV_THEME")))
	if theme == "" {
		theme = "auto"
	}

	if token == "" && logger != nil {
		logger.Warn("Asset logo provider token not configured; logo_url will be omitted",
			zap.String("provider", provider))
	}

	return &assetLogoResolver{
		provider: provider,
		baseURL:  baseURL,
		token:    token,
		size:     size,
		format:   format,
		theme:    theme,
		cache:    make(map[string]cachedAssetLogo),
	}
}

func (r *assetLogoResolver) Resolve(symbol string) *string {
	if r == nil {
		return nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(symbol))
	if normalized == "" {
		return nil
	}

	now := time.Now().UTC()
	r.mu.RLock()
	if cached, ok := r.cache[normalized]; ok && now.Sub(cached.fetchedAt) < assetLogoCacheTTL {
		r.mu.RUnlock()
		return cloneAssetLogoPtr(cached.url)
	}
	r.mu.RUnlock()

	var resolved *string
	switch r.provider {
	case "logo_dev":
		resolved = r.resolveLogoDevURL(normalized)
	default:
		resolved = nil
	}

	r.mu.Lock()
	r.cache[normalized] = cachedAssetLogo{url: resolved, fetchedAt: now}
	r.mu.Unlock()

	return cloneAssetLogoPtr(resolved)
}

func (r *assetLogoResolver) resolveLogoDevURL(symbol string) *string {
	if strings.TrimSpace(r.token) == "" {
		return nil
	}
	values := url.Values{}
	values.Set("token", r.token)
	values.Set("size", strconv.Itoa(r.size))
	values.Set("format", r.format)
	values.Set("theme", r.theme)

	u := r.baseURL + "/ticker/" + url.PathEscape(symbol) + "?" + values.Encode()
	return &u
}

func cloneAssetLogoPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}


