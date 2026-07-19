package funding

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"go.uber.org/zap"
)

// BillPayHandlers exposes the Airbills bill-payment webhook. Bill purchases
// themselves are driven through Miriam's tool layer; this handler only receives
// asynchronous fulfillment callbacks from Airbills.
type BillPayHandlers struct {
	service *billpay.Service
	logger  *zap.Logger
}

func NewBillPayHandlers(service *billpay.Service, logger *zap.Logger) *BillPayHandlers {
	return &BillPayHandlers{service: service, logger: logger}
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
