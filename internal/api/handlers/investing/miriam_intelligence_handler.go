package investing

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

type MiriamIntelligenceHandler struct {
	service *miriamservice.Service
	logger  *zap.Logger
}

func NewMiriamIntelligenceHandler(service *miriamservice.Service, logger *zap.Logger) *MiriamIntelligenceHandler {
	return &MiriamIntelligenceHandler{service: service, logger: logger}
}

type createMiriamMandateRequest struct {
	Name               string           `json:"name"`
	MaxAmountPerAction *decimal.Decimal `json:"max_amount_per_action" binding:"required"`
	MaxAmountPerDay    *decimal.Decimal `json:"max_amount_per_day" binding:"required"`
	MinSpendBalance    decimal.Decimal  `json:"min_spend_balance"`
	MinSafeToSpend     decimal.Decimal  `json:"min_safe_to_spend"`
	CooldownMinutes    int              `json:"cooldown_minutes"`
}

func (h *MiriamIntelligenceHandler) GetState(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	state, err := h.service.GetMoneyState(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("miriam state failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get Miriam money state"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": state})
}

func (h *MiriamIntelligenceHandler) RefreshState(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	state, err := h.service.RefreshMoneyState(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("miriam state refresh failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh Miriam money state"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": state})
}

func (h *MiriamIntelligenceHandler) CreateMandate(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req createMiriamMandateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !req.MaxAmountPerAction.IsPositive() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_amount_per_action must be positive"})
		return
	}
	if !req.MaxAmountPerDay.IsPositive() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_amount_per_day must be positive"})
		return
	}
	if req.MaxAmountPerAction.GreaterThan(*req.MaxAmountPerDay) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max_amount_per_action cannot exceed max_amount_per_day"})
		return
	}
	if req.MinSpendBalance.IsNegative() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min_spend_balance must be non-negative"})
		return
	}
	if req.MinSafeToSpend.IsNegative() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min_safe_to_spend must be non-negative"})
		return
	}
	if req.CooldownMinutes < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cooldown_minutes must be non-negative"})
		return
	}
	mandate, err := h.service.CreateTransferToStashMandate(c.Request.Context(), userID, miriamservice.CreateMandateRequest{
		Name: req.Name, MaxAmountPerAction: *req.MaxAmountPerAction, MaxAmountPerDay: *req.MaxAmountPerDay,
		MinSpendBalance: req.MinSpendBalance, MinSafeToSpend: req.MinSafeToSpend, CooldownMinutes: req.CooldownMinutes,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": mandate})
}

func (h *MiriamIntelligenceHandler) ListMandates(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	mandates, err := h.service.ListMandates(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("miriam mandates failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list Miriam mandates"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": mandates})
}

func (h *MiriamIntelligenceHandler) UpdateMandateStatus(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mandate id"})
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Status != "active" && req.Status != "paused" && req.Status != "expired" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status: must be active, paused, or expired"})
		return
	}
	if err := h.service.UpdateMandateStatus(c.Request.Context(), userID, id, req.Status); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": true})
}

func (h *MiriamIntelligenceHandler) ListReceipts(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	receipts, err := h.service.ListReceipts(c.Request.Context(), userID, limit)
	if err != nil {
		h.logger.Error("miriam receipts failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list Miriam receipts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": receipts})
}

func (h *MiriamIntelligenceHandler) RecordFeedback(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	receiptID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid receipt id"})
		return
	}
	var req struct {
		Signal string `json:"signal" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Signal != "positive" && req.Signal != "negative" && req.Signal != "neutral" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signal: must be positive, negative, or neutral"})
		return
	}
	// Map user-facing signals to internal feedback enum
	signalMap := map[string]string{
		"positive": "accepted",
		"negative": "reversed",
		"neutral":  "ignored",
	}
	if err := h.service.RecordFeedback(c.Request.Context(), userID, receiptID, signalMap[req.Signal]); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"recorded": true})
}
