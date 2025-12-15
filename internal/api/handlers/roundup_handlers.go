package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"go.uber.org/zap"
)

// RoundupHandlers handles round-up API endpoints
type RoundupHandlers struct {
	service *roundup.Service
	logger  *zap.Logger
}

// NewRoundupHandlers creates new round-up handlers
func NewRoundupHandlers(service *roundup.Service, logger *zap.Logger) *RoundupHandlers {
	return &RoundupHandlers{service: service, logger: logger}
}

// GetSettings handles GET /api/v1/roundups/settings
func (h *RoundupHandlers) GetSettings(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	settings, err := h.service.GetSettings(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get settings", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get settings"})
		return
	}

	c.JSON(http.StatusOK, settings)
}

// UpdateSettingsRequest represents the request body for updating settings
type UpdateSettingsRequest struct {
	Enabled            *bool    `json:"enabled,omitempty"`
	Multiplier         *float64 `json:"multiplier,omitempty"`
	Threshold          *float64 `json:"threshold,omitempty"`
	AutoInvestEnabled  *bool    `json:"auto_invest_enabled,omitempty"`
	AutoInvestBasketID *string  `json:"auto_invest_basket_id,omitempty"`
	AutoInvestSymbol   *string  `json:"auto_invest_symbol,omitempty"`
}

// UpdateSettings handles PUT /api/v1/roundups/settings
func (h *RoundupHandlers) UpdateSettings(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Convert to service request
	svcReq := &roundup.UpdateSettingsRequest{
		Enabled:           req.Enabled,
		AutoInvestEnabled: req.AutoInvestEnabled,
		AutoInvestSymbol:  req.AutoInvestSymbol,
	}

	if req.Multiplier != nil {
		m := decimal.NewFromFloat(*req.Multiplier)
		svcReq.Multiplier = &m
	}
	if req.Threshold != nil {
		t := decimal.NewFromFloat(*req.Threshold)
		svcReq.Threshold = &t
	}
	if req.AutoInvestBasketID != nil {
		id, err := uuid.Parse(*req.AutoInvestBasketID)
		if err == nil {
			svcReq.AutoInvestBasketID = &id
		}
	}

	settings, err := h.service.UpdateSettings(c.Request.Context(), userID, svcReq)
	if err != nil {
		h.logger.Error("Failed to update settings", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, settings)
}

// ProcessTransactionRequest represents a transaction to process for round-up
type ProcessTransactionRequest struct {
	Amount       float64 `json:"amount" binding:"required,gt=0"`
	SourceType   string  `json:"source_type" binding:"required,oneof=card bank manual"`
	SourceRef    *string `json:"source_ref,omitempty"`
	MerchantName *string `json:"merchant_name,omitempty"`
}

// ProcessTransaction handles POST /api/v1/roundups/transactions
func (h *RoundupHandlers) ProcessTransaction(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req ProcessTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	tx, err := h.service.ProcessTransaction(c.Request.Context(), &roundup.ProcessTransactionRequest{
		UserID:       userID,
		Amount:       decimal.NewFromFloat(req.Amount),
		SourceType:   entities.RoundupSourceType(req.SourceType),
		SourceRef:    req.SourceRef,
		MerchantName: req.MerchantName,
	})
	if err != nil {
		h.logger.Error("Failed to process transaction", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, tx)
}

// GetTransactions handles GET /api/v1/roundups/transactions
func (h *RoundupHandlers) GetTransactions(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	txs, err := h.service.GetTransactions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get transactions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get transactions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transactions": txs})
}

// GetSummary handles GET /api/v1/roundups/summary
func (h *RoundupHandlers) GetSummary(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	summary, err := h.service.GetSummary(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get summary", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get summary"})
		return
	}

	c.JSON(http.StatusOK, summary)
}

// CollectRoundups handles POST /api/v1/roundups/collect
func (h *RoundupHandlers) CollectRoundups(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.service.CollectPendingRoundups(c.Request.Context(), userID); err != nil {
		h.logger.Error("Failed to collect roundups", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "roundups collected successfully"})
}

// CalculatePreview handles POST /api/v1/roundups/preview - preview round-up without saving
func (h *RoundupHandlers) CalculatePreview(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Amount float64 `json:"amount" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	settings, err := h.service.GetSettings(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get settings"})
		return
	}

	amount := decimal.NewFromFloat(req.Amount)
	rounded, spareChange, multiplied := entities.CalculateRoundup(amount, settings.Multiplier)

	c.JSON(http.StatusOK, gin.H{
		"original_amount":   amount.String(),
		"rounded_amount":    rounded.String(),
		"spare_change":      spareChange.String(),
		"multiplied_amount": multiplied.String(),
		"multiplier":        settings.Multiplier.String(),
	})
}
