package funding

import (
	"database/sql"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"go.uber.org/zap"
)

// BillPayHandlers exposes the Airbills bill-payment webhook and user-facing
// management endpoints. Bill purchases are driven through Miriam's tool layer;
// this handler receives asynchronous fulfillment callbacks and supports
// beneficiaries / mandate management.
type BillPayHandlers struct {
	service *billpay.Service
	logger  *zap.Logger
}

func NewBillPayHandlers(service *billpay.Service, logger *zap.Logger) *BillPayHandlers {
	return &BillPayHandlers{service: service, logger: logger}
}

// GetOrder returns the status and details of a single Airbills bill payment.
// GET /v1/billpay/orders/:id
func (h *BillPayHandlers) GetOrder(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "order ID required"})
		return
	}
	order, err := h.service.GetOrder(c.Request.Context(), userID, orderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"code": "ORDER_NOT_FOUND", "message": "Order not found"})
			return
		}
		h.logger.Error("failed to get airbills order", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FETCH_FAILED", "message": "Failed to fetch order"})
		return
	}
	c.JSON(http.StatusOK, order)
}

// ListBeneficiaries returns the user's saved bill-payment beneficiaries.
// GET /v1/billpay/beneficiaries?category=...
func (h *BillPayHandlers) ListBeneficiaries(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	category := c.Query("category")
	beneficiaries, err := h.service.ListBeneficiaries(c.Request.Context(), userID, category)
	if err != nil {
		h.logger.Error("failed to list bill beneficiaries", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FETCH_FAILED", "message": "Failed to fetch beneficiaries"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"beneficiaries": beneficiaries})
}

// SaveBeneficiary saves a new bill-payment beneficiary.
// POST /v1/billpay/beneficiaries
func (h *BillPayHandlers) SaveBeneficiary(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Label         string `json:"label" binding:"required"`
		Category      string `json:"category" binding:"required"`
		Recipient     string `json:"recipient" binding:"required"`
		NetworkID     string `json:"network_id"`
		ProdID        string `json:"prod_id"`
		ElectID       string `json:"elect_id"`
		RecipientName string `json:"recipient_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "label, category, and recipient are required"})
		return
	}
	b, err := h.service.SaveBeneficiary(c.Request.Context(), userID, billpay.Beneficiary{
		Label:         req.Label,
		Category:      req.Category,
		Recipient:     req.Recipient,
		NetworkID:     req.NetworkID,
		ProdID:        req.ProdID,
		ElectID:       req.ElectID,
		RecipientName: req.RecipientName,
	})
	if err != nil {
		h.logger.Error("failed to save bill beneficiary", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": "SAVE_FAILED", "message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, b)
}

// GetMandate returns the active mandate for a bill category.
// GET /v1/billpay/mandates/:category
func (h *BillPayHandlers) GetMandate(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	category := c.Param("category")
	m, err := h.service.GetMandate(c.Request.Context(), userID, category)
	if err != nil {
		h.logger.Error("failed to get bill mandate", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FETCH_FAILED", "message": "Failed to fetch mandate"})
		return
	}
	if m == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "MANDATE_NOT_FOUND", "message": "No active mandate for this category"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// SetMandate creates or updates a bill-pay mandate for a category.
// PUT /v1/billpay/mandates/:category
func (h *BillPayHandlers) SetMandate(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	category := c.Param("category")
	var req struct {
		PerPaymentCapNGN float64 `json:"per_payment_cap_ngn" binding:"required,gt=0"`
		DailyCapNGN      float64 `json:"daily_cap_ngn" binding:"gte=0"`
		AllowAuto        bool    `json:"allow_auto"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "per_payment_cap_ngn must be positive"})
		return
	}
	m, err := h.service.SetMandate(c.Request.Context(), userID, billpay.Mandate{
		Category:         category,
		PerPaymentCapNGN: req.PerPaymentCapNGN,
		DailyCapNGN:      req.DailyCapNGN,
		AllowAuto:        req.AllowAuto,
	})
	if err != nil {
		h.logger.Error("failed to set bill mandate", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"code": "SET_FAILED", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}

// HandleWebhook processes an Airbills fulfillment callback.
// POST /v1/webhooks/airbills
func (h *BillPayHandlers) HandleWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		h.logger.Error("airbills webhook: failed to read body", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	signature := c.GetHeader("x-airbills-signature")
	if err := h.service.HandleCallback(c.Request.Context(), body, signature); err != nil {
		h.logger.Warn("airbills webhook processing failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "callback rejected"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
