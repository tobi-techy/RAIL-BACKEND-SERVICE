package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	
	// Due
	dueService          *services.DueService
	dueWebhookSecret    string
	notificationService *services.NotificationService
	
	logger *zap.Logger
}

// NewIntegrationHandlers creates new integration handlers
func NewIntegrationHandlers(
	alpacaClient *alpaca.Client,
	dueService *services.DueService,
	dueWebhookSecret string,
	notificationService *services.NotificationService,
	logger *logger.Logger,
) *IntegrationHandlers {
	return &IntegrationHandlers{
		alpacaClient:        alpacaClient,
		dueService:          dueService,
		dueWebhookSecret:    dueWebhookSecret,
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

// ===== DUE HANDLERS =====

type CreateDueAccountRequest struct {
	Name    string `json:"name" binding:"required"`
	Email   string `json:"email" binding:"required,email"`
	Country string `json:"country" binding:"required,len=2"`
}

func (h *IntegrationHandlers) CreateDueAccount(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateDueAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dueAccountID, err := h.dueService.CreateDueAccount(c.Request.Context(), userID.(uuid.UUID), req.Email, req.Name, req.Country)
	if err != nil {
		h.logger.Error("Failed to create Due account", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create Due account"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":        "Due account created successfully",
		"due_account_id": dueAccountID,
	})
}

func (h *IntegrationHandlers) HandleDueWebhook(c *gin.Context) {
	// Read raw body for signature verification
	rawBody, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Verify webhook signature if secret is configured
	signature := c.GetHeader("X-Due-Signature")
	if !h.verifyDueSignature(signature, rawBody) {
		h.logger.Warn("Invalid Due webhook signature")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
		return
	}

	eventType, ok := event["type"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing event type"})
		return
	}

	h.logger.Info("Received Due webhook", zap.String("type", eventType))

	switch eventType {
	case "virtual_account.deposit", "deposit.received", "deposit.confirmed":
		h.handleVirtualAccountDeposit(c, event)
	case "transfer.completed":
		h.handleTransferCompleted(c, event)
	case "transfer.failed":
		h.handleTransferFailed(c, event)
	case "kyc.status_changed":
		h.handleKYCStatusChanged(c, event)
	default:
		h.logger.Info("Unhandled webhook event type", zap.String("type", eventType))
	}

	c.JSON(http.StatusOK, gin.H{"message": "webhook processed"})
}

func (h *IntegrationHandlers) verifyDueSignature(signature string, body []byte) bool {
	// Skip verification if no secret configured (dev mode)
	if h.dueWebhookSecret == "" {
		return true
	}
	
	mac := hmac.New(sha256.New, []byte(h.dueWebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (h *IntegrationHandlers) handleVirtualAccountDeposit(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid virtual account deposit webhook data")
		return
	}

	virtualAccountID, _ := data["id"].(string)
	amount, _ := data["amount"].(string)
	currency, _ := data["currency"].(string)
	nonce, _ := data["nonce"].(string)
	transactionID, _ := data["transactionId"].(string)

	if err := h.dueService.HandleVirtualAccountDeposit(c.Request.Context(), virtualAccountID, amount, currency, transactionID, nonce); err != nil {
		h.logger.Error("Failed to handle virtual account deposit", zap.Error(err))
		return
	}

	h.logger.Info("Virtual account deposit processed successfully", zap.String("transaction_id", transactionID))
}

func (h *IntegrationHandlers) handleTransferStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid transfer webhook data")
		return
	}

	transferID, _ := data["id"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("Transfer status changed", zap.String("transfer_id", transferID), zap.String("status", status))
}

func (h *IntegrationHandlers) handleTransferCompleted(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid transfer completed webhook data")
		return
	}

	transferID, _ := data["id"].(string)
	h.logger.Info("Transfer completed", zap.String("transfer_id", transferID))
}

func (h *IntegrationHandlers) handleTransferFailed(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid transfer failed webhook data")
		return
	}

	transferID, _ := data["id"].(string)
	reason, _ := data["reason"].(string)
	h.logger.Info("Transfer failed", zap.String("transfer_id", transferID), zap.String("reason", reason))
}

func (h *IntegrationHandlers) handleKYCStatusChanged(c *gin.Context, event map[string]interface{}) {
	data, ok := event["data"].(map[string]interface{})
	if !ok {
		h.logger.Error("Invalid KYC webhook data")
		return
	}

	accountID, _ := data["accountId"].(string)
	status, _ := data["status"].(string)

	h.logger.Info("KYC status changed", zap.String("account_id", accountID), zap.String("status", status))
}




