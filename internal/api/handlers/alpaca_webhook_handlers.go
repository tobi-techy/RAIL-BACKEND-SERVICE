package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	alpacaService "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AlpacaWebhookHandlers handles Alpaca webhook events
type AlpacaWebhookHandlers struct {
	eventProcessor *alpacaService.EventProcessor
	logger         *logger.Logger
}

func NewAlpacaWebhookHandlers(eventProcessor *alpacaService.EventProcessor, logger *logger.Logger) *AlpacaWebhookHandlers {
	return &AlpacaWebhookHandlers{
		eventProcessor: eventProcessor,
		logger:         logger,
	}
}

// HandleTradeUpdate handles trade/order update webhooks from Alpaca
// POST /api/v1/webhooks/alpaca/trade
func (h *AlpacaWebhookHandlers) HandleTradeUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Parse the event
	var event struct {
		Event string          `json:"event"`
		Order json.RawMessage `json:"order"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse webhook", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Store event for processing
	eventID := uuid.New().String()
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "trade_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	// Process immediately for order fills
	if event.Event == "fill" || event.Event == "partial_fill" {
		var orderData struct {
			ID             string `json:"id"`
			Symbol         string `json:"symbol"`
			Side           string `json:"side"`
			FilledQty      string `json:"filled_qty"`
			FilledAvgPrice string `json:"filled_avg_price"`
			Status         string `json:"status"`
			FilledAt       string `json:"filled_at"`
		}
		if err := json.Unmarshal(event.Order, &orderData); err == nil {
			fillEvent := &entities.AlpacaOrderFillEvent{
				OrderID: orderData.ID,
				Symbol:  orderData.Symbol,
				Side:    orderData.Side,
				Status:  orderData.Status,
			}
			// Parse decimal values
			if qty, err := parseDecimal(orderData.FilledQty); err == nil {
				fillEvent.FilledQty = qty
			}
			if price, err := parseDecimal(orderData.FilledAvgPrice); err == nil {
				fillEvent.FilledAvgPrice = price
			}
			if t, err := parseTime(orderData.FilledAt); err == nil {
				fillEvent.FilledAt = t
			}

			if err := h.eventProcessor.ProcessOrderFill(c.Request.Context(), fillEvent); err != nil {
				h.logger.Error("Failed to process order fill", "error", err, "order_id", orderData.ID)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleAccountUpdate handles account status update webhooks
// POST /api/v1/webhooks/alpaca/account
func (h *AlpacaWebhookHandlers) HandleAccountUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var event struct {
		AccountID     string `json:"account_id"`
		AccountNumber string `json:"account_number"`
		Status        string `json:"status"`
		StatusFrom    string `json:"status_from"`
		Reason        string `json:"reason"`
		At            string `json:"at"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse account webhook", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Store event
	eventID := uuid.New().String()
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "account_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	// Process account update
	accountEvent := &entities.AlpacaAccountEvent{
		AccountID: event.AccountID,
		Status:    entities.AlpacaAccountStatus(event.Status),
		Reason:    event.Reason,
	}
	if t, err := parseTime(event.At); err == nil {
		accountEvent.UpdatedAt = t
	}

	if err := h.eventProcessor.ProcessAccountUpdate(c.Request.Context(), accountEvent); err != nil {
		h.logger.Error("Failed to process account update", "error", err, "account_id", event.AccountID)
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleTransferUpdate handles transfer/funding update webhooks
// POST /api/v1/webhooks/alpaca/transfer
func (h *AlpacaWebhookHandlers) HandleTransferUpdate(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Store event for processing
	eventID := uuid.New().String()
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "transfer_update", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	h.logger.Info("Transfer webhook received", "body", string(body))
	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// HandleNonTradeActivity handles non-trade activity webhooks (dividends, fees, etc.)
// POST /api/v1/webhooks/alpaca/nta
func (h *AlpacaWebhookHandlers) HandleNonTradeActivity(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Store event for processing
	eventID := uuid.New().String()
	if err := h.eventProcessor.StoreEvent(c.Request.Context(), "nta", eventID, body, nil, nil); err != nil {
		h.logger.Error("Failed to store event", "error", err)
	}

	h.logger.Info("NTA webhook received", "body", string(body))
	c.JSON(http.StatusOK, gin.H{"status": "received"})
}
