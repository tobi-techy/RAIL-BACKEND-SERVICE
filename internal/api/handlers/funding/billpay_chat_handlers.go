package funding

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	"go.uber.org/zap"
)

// Chat-channel bill pay (messaging and in-app chat). These sit on the normal
// protected group: session JWT or agent JWT, CSRF, Idempotency-Key on pay.
// They are NOT behind RequirePasscodeSession — chat proves identity via the
// email OTP (messaging) or the in-app confirmation card (mobile chat-first).

// ChatPayBillRequest is the HTTP body for POST /api/v1/billpay/pay.
type ChatPayBillRequest struct {
	Category      string  `json:"category" binding:"required"`
	Recipient     string  `json:"recipient" binding:"required"`
	AmountNGN     float64 `json:"amount_ngn" binding:"required,gt=0"`
	NetworkID     string  `json:"network_id"`
	ProdID        string  `json:"prod_id"`
	ElectID       string  `json:"elect_id"`
	RecipientName string  `json:"recipient_name"`
}

// PayBill handles POST /api/v1/billpay/pay
func (h *BillPayHandlers) PayBill(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req ChatPayBillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": err.Error()})
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "Idempotency key is required for bill payments"})
		return
	}
	if len(idempotencyKey) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "Idempotency key must be at most 255 characters"})
		return
	}

	result, err := h.service.PayBill(c.Request.Context(), userID, billpay.PayBillRequest{
		Category:       req.Category,
		Recipient:      req.Recipient,
		NetworkID:      req.NetworkID,
		ProdID:         req.ProdID,
		ElectID:        req.ElectID,
		AmountNGN:      req.AmountNGN,
		RecipientName:  req.RecipientName,
		IdempotencyRef: idempotencyKey,
	})
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "unsupported bill category"),
			strings.Contains(msg, "recipient is required"),
			strings.Contains(msg, "amount must be"),
			strings.Contains(msg, "exceeds the per-payment"),
			strings.Contains(msg, "duplicate payment"):
			c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": msg})
		case strings.Contains(strings.ToLower(msg), "insufficient"):
			c.JSON(http.StatusBadRequest, gin.H{"code": "INSUFFICIENT_FUNDS", "message": msg})
		default:
			h.logger.Error("bill pay failed", zap.Error(err), zap.String("user_id", userID.String()))
			c.JSON(http.StatusInternalServerError, gin.H{"code": "PAY_FAILED", "message": "Failed to pay bill"})
		}
		return
	}
	c.JSON(http.StatusOK, result)
}

// ListProviders handles GET /api/v1/billpay/providers?category=
func (h *BillPayHandlers) ListProviders(c *gin.Context) {
	if _, ok := requireUserID(c); !ok {
		return
	}
	category := strings.ToLower(strings.TrimSpace(c.Query("category")))
	products, err := h.productsForCategory(c, category, c.Query("network_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": billProductsToJSON(products)})
}

// ListDataPlans handles GET /api/v1/billpay/data-plans?network_id=
func (h *BillPayHandlers) ListDataPlans(c *gin.Context) {
	if _, ok := requireUserID(c); !ok {
		return
	}
	products, err := h.service.ListDataPlans(c.Request.Context(), c.Query("network_id"))
	if err != nil {
		h.logger.Error("list data plans failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"code": "LOOKUP_FAILED", "message": "Failed to list data plans"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plans": billProductsToJSON(products)})
}

// ListCablePackages handles GET /api/v1/billpay/cable-packages
func (h *BillPayHandlers) ListCablePackages(c *gin.Context) {
	if _, ok := requireUserID(c); !ok {
		return
	}
	products, err := h.service.ListCablePackages(c.Request.Context())
	if err != nil {
		h.logger.Error("list cable packages failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"code": "LOOKUP_FAILED", "message": "Failed to list cable packages"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"packages": billProductsToJSON(products)})
}

// DetectNetwork handles POST /api/v1/billpay/detect-network
func (h *BillPayHandlers) DetectNetwork(c *gin.Context) {
	if _, ok := requireUserID(c); !ok {
		return
	}
	var req struct {
		Phone string `json:"phone" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "phone is required"})
		return
	}
	id, name, err := h.service.DetectNetwork(c.Request.Context(), req.Phone)
	if err != nil {
		h.logger.Error("detect network failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"code": "LOOKUP_FAILED", "message": "Failed to detect network"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"network_id": id, "network": name})
}

// ValidateMeter handles POST /api/v1/billpay/validate-meter
func (h *BillPayHandlers) ValidateMeter(c *gin.Context) {
	if _, ok := requireUserID(c); !ok {
		return
	}
	var req struct {
		MeterNo string `json:"meter_no" binding:"required"`
		ElectID string `json:"elect_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_REQUEST", "message": "meter_no and elect_id are required"})
		return
	}
	name, err := h.service.ValidateMeter(c.Request.Context(), req.MeterNo, req.ElectID)
	if err != nil {
		h.logger.Error("validate meter failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"code": "LOOKUP_FAILED", "message": "Failed to validate meter"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"meter_no": req.MeterNo, "account_name": name})
}

// PaymentHistory handles GET /api/v1/billpay/history
func (h *BillPayHandlers) PaymentHistory(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	history, err := h.service.GetPaymentHistory(c.Request.Context(), userID, 20)
	if err != nil {
		h.logger.Error("bill payment history failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": "FETCH_FAILED", "message": "Failed to fetch bill history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": history})
}

func (h *BillPayHandlers) productsForCategory(c *gin.Context, category, networkID string) ([]airbills.Product, error) {
	switch category {
	case billpay.CategoryElectricity:
		return h.service.ListElectricityDiscos(c.Request.Context())
	case billpay.CategoryCable:
		return h.service.ListCablePackages(c.Request.Context())
	case billpay.CategoryBetting:
		return h.service.ListBettingProviders(c.Request.Context())
	case billpay.CategoryTransport:
		return h.service.ListTransportProviders(c.Request.Context())
	case billpay.CategoryData:
		return h.service.ListDataPlans(c.Request.Context(), networkID)
	default:
		return nil, fmt.Errorf("no provider list for category %q (use electricity, cable, betting, transport, or data)", category)
	}
}

func billProductsToJSON(products []airbills.Product) []gin.H {
	out := make([]gin.H, 0, len(products))
	for _, p := range products {
		amount := p.Amount
		if amount == 0 {
			amount = p.ProdAmount
		}
		item := gin.H{"prod_id": p.ProdID, "name": p.Name, "amount": amount}
		if p.ElectID != "" {
			item["elect_id"] = p.ElectID
		}
		if p.NetworkID != "" {
			item["network_id"] = p.NetworkID
		}
		out = append(out, item)
	}
	return out
}
