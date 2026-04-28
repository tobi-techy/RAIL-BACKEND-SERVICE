package p2p

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
)

// TapIntentRequest is the body for POST /p2p/tap/intent.
type TapIntentRequest struct {
	RecipientRailtag string `json:"recipientRailtag" binding:"required"`
	Amount           string `json:"amount"           binding:"required"`
}

// TapConfirmRequest is the body for POST /p2p/tap/confirm.
type TapConfirmRequest struct {
	Nonce          string `json:"nonce"           binding:"required"`
	IdempotencyKey string `json:"idempotencyKey"  binding:"required"`
}

// TapIntent creates a server-issued transfer intent and returns a nonce.
// POST /api/v1/p2p/tap/intent  (auth only)
func (h *Handlers) TapIntent(c *gin.Context) {
	senderID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req TapIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	resp, err := h.service.CreateTapIntent(c.Request.Context(), senderID, req.RecipientRailtag, req.Amount)
	if err != nil {
		h.logger.Error("TapIntent failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": "INTENT_FAILED", "message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// TapConfirm validates the nonce and executes the transfer.
// POST /api/v1/p2p/tap/confirm  (auth + RequirePasscodeSession)
func (h *Handlers) TapConfirm(c *gin.Context) {
	senderID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED"})
		return
	}

	var req TapConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	result, err := h.service.ConfirmTapIntent(c.Request.Context(), senderID, req.Nonce, req.IdempotencyKey)
	if err != nil {
		h.logger.Error("TapConfirm failed", zap.Error(err))
		switch {
		case errors.Is(err, p2pservice.ErrTapIntentNotFound):
			c.JSON(http.StatusGone, gin.H{"error": "INTENT_EXPIRED", "message": "Transfer intent not found or expired"})
		case errors.Is(err, p2pservice.ErrTapIntentMismatch):
			c.JSON(http.StatusForbidden, gin.H{"error": "INTENT_MISMATCH", "message": "Intent parameters do not match"})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "CONFIRM_FAILED", "message": err.Error()})
		}
		return
	}

	c.JSON(http.StatusCreated, result)
}
