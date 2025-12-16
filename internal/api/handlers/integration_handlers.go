package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
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
}

// NewIntegrationHandlers creates new integration handlers
func NewIntegrationHandlers(
	alpacaClient *alpaca.Client,
	_ interface{}, // Deprecated: Due service removed
	_ string,      // Deprecated: Due webhook secret removed
	notificationService *services.NotificationService,
	logger *logger.Logger,
) *IntegrationHandlers {
	return &IntegrationHandlers{
		alpacaClient:        alpacaClient,
		notificationService: notificationService,
		logger:              logger.Zap(),
	}
}

// ===== ALPACA HANDLERS =====

type AssetsResponse struct {
	Assets     []entities.AlpacaAssetResponse `json:"assets"`
	TotalCount int                            `json:"total_count"`
	Page       int                            `json:"page"`
	PageSize   int                            `json:"page_size"`
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

	c.JSON(http.StatusOK, AssetsResponse{
		Assets:     assets[start:end],
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

	c.JSON(http.StatusOK, asset)
}

// Note: Due handlers have been removed. Virtual accounts are now handled by Bridge.
// See bridge_webhook_handlers.go for Bridge webhook handling.

