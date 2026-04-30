package cards

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/card"
)

// CardHandlers handles card-related HTTP requests
type CardHandlers struct {
	service *card.Service
	logger  *zap.Logger
}

// NewCardHandlers creates new card handlers
func NewCardHandlers(service *card.Service, logger *zap.Logger) *CardHandlers {
	return &CardHandlers{
		service: service,
		logger:  logger,
	}
}

// GetCards retrieves all cards for the authenticated user
// GET /api/v1/cards
func (h *CardHandlers) GetCards(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cards, err := h.service.GetUserCards(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to get cards", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to retrieve cards"})
		return
	}

	response := make([]entities.CardDetailsResponse, len(cards))
	for i, cardData := range cards {
		response[i] = entities.CardDetailsResponse{
			ID:           cardData.ID,
			Type:         cardData.Type,
			Status:       cardData.Status,
			Last4:        cardData.Last4,
			Expiry:       cardData.Expiry,
			CardImageURL: cardData.CardImageURL,
			Currency:     cardData.Currency,
			CreatedAt:    cardData.CreatedAt,
		}
	}

	c.JSON(http.StatusOK, entities.CardListResponse{
		Cards: response,
		Total: len(response),
	})
}

// CreateCard creates a new virtual card for the user
// POST /api/v1/cards
func (h *CardHandlers) CreateCard(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	var req entities.CreateCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	// Only virtual cards supported in MVP
	if req.Type != entities.CardTypeVirtual {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_CARD_TYPE", "message": "Only virtual cards are supported"})
		return
	}

	newCard, err := h.service.CreateVirtualCard(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to create card", zap.Error(err))
		switch err {
		case card.ErrCardAlreadyExists:
			c.JSON(http.StatusConflict, gin.H{"error": "CARD_EXISTS", "message": "User already has an active virtual card"})
		case card.ErrCustomerNotFound:
			c.JSON(http.StatusBadRequest, gin.H{"error": "CUSTOMER_NOT_FOUND", "message": "Complete onboarding before creating a card"})
		case card.ErrWalletNotFound:
			c.JSON(http.StatusBadRequest, gin.H{"error": "WALLET_NOT_FOUND", "message": "Wallet required for card creation"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to create card"})
		}
		return
	}

	c.JSON(http.StatusCreated, entities.CreateCardResponse{
		Card:    newCard,
		Message: "Virtual card created successfully",
	})
}

// GetCard retrieves a specific card
// GET /api/v1/cards/:id
func (h *CardHandlers) GetCard(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	cardData, err := h.service.GetCard(c.Request.Context(), userID, cardID)
	if err != nil {
		if err == card.ErrCardNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
			return
		}
		h.logger.Error("Failed to get card", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to retrieve card"})
		return
	}

	c.JSON(http.StatusOK, entities.CardDetailsResponse{
		ID:           cardData.ID,
		Type:         cardData.Type,
		Status:       cardData.Status,
		Last4:        cardData.Last4,
		Expiry:       cardData.Expiry,
		CardImageURL: cardData.CardImageURL,
		Currency:     cardData.Currency,
		CreatedAt:    cardData.CreatedAt,
	})
}

// FreezeCard freezes a card
// POST /api/v1/cards/:id/freeze
func (h *CardHandlers) FreezeCard(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	cardData, err := h.service.FreezeCard(c.Request.Context(), userID, cardID)
	if err != nil {
		switch err {
		case card.ErrCardNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
		case card.ErrCardCancelled:
			c.JSON(http.StatusBadRequest, gin.H{"error": "CARD_CANCELLED", "message": "Cannot freeze a cancelled card"})
		default:
			h.logger.Error("Failed to freeze card", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to freeze card"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Card frozen successfully",
		"card": entities.CardDetailsResponse{
			ID:       cardData.ID,
			Type:     cardData.Type,
			Status:   cardData.Status,
			Last4:    cardData.Last4,
			Expiry:   cardData.Expiry,
			Currency: cardData.Currency,
		},
	})
}

// UnfreezeCard unfreezes a card
// POST /api/v1/cards/:id/unfreeze
func (h *CardHandlers) UnfreezeCard(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	cardData, err := h.service.UnfreezeCard(c.Request.Context(), userID, cardID)
	if err != nil {
		switch err {
		case card.ErrCardNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
		case card.ErrCardCancelled:
			c.JSON(http.StatusBadRequest, gin.H{"error": "CARD_CANCELLED", "message": "Cannot unfreeze a cancelled card"})
		default:
			h.logger.Error("Failed to unfreeze card", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to unfreeze card"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Card unfrozen successfully",
		"card": entities.CardDetailsResponse{
			ID:       cardData.ID,
			Type:     cardData.Type,
			Status:   cardData.Status,
			Last4:    cardData.Last4,
			Expiry:   cardData.Expiry,
			Currency: cardData.Currency,
		},
	})
}

// GetCardTransactions retrieves transactions for a specific card
// GET /api/v1/cards/:id/transactions
func (h *CardHandlers) GetCardTransactions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit > 100 {
		limit = 100
	}

	txs, err := h.service.GetCardTransactions(c.Request.Context(), userID, cardID, limit, offset)
	if err != nil {
		if err == card.ErrCardNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
			return
		}
		h.logger.Error("Failed to get transactions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to retrieve transactions"})
		return
	}

	c.JSON(http.StatusOK, entities.CardTransactionListResponse{
		Transactions: derefBridgeTransactions(txs),
		Total:        len(txs),
		HasMore:      len(txs) == limit,
	})
}

// GetAllTransactions retrieves all card transactions for the user
// GET /api/v1/cards/transactions
func (h *CardHandlers) GetAllTransactions(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit > 100 {
		limit = 100
	}

	txs, err := h.service.GetUserTransactions(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("Failed to get transactions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to retrieve transactions"})
		return
	}

	c.JSON(http.StatusOK, entities.CardTransactionListResponse{
		Transactions: derefBridgeTransactions(txs),
		Total:        len(txs),
		HasMore:      len(txs) == limit,
	})
}

func derefBridgeTransactions(txs []*entities.BridgeCardTransaction) []entities.BridgeCardTransaction {
	result := make([]entities.BridgeCardTransaction, len(txs))
	for i, tx := range txs {
		result[i] = *tx
	}
	return result
}

// GetCardEphemeralKey generates a one-time ephemeral key for PCI-compliant card detail reveal.
// POST /api/v1/cards/:id/ephemeral-key
func (h *CardHandlers) GetCardEphemeralKey(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	var req struct {
		ClientNonce string `json:"client_nonce" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "client_nonce is required"})
		return
	}

	ephemeralKey, err := h.service.GetCardEphemeralKey(c.Request.Context(), userID, cardID, req.ClientNonce)
	if err != nil {
		switch err {
		case card.ErrCardNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
		default:
			h.logger.Error("Failed to get card ephemeral key", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to generate ephemeral key"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"ephemeral_key": ephemeralKey})
}

// SetDailyLimit sets the daily spending limit for a card.
// PUT /api/v1/cards/:id/limit
func (h *CardHandlers) SetDailyLimit(c *gin.Context) {
	userID, err := common.GetUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHORIZED", "message": "User not authenticated"})
		return
	}

	cardID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_ID", "message": "Invalid card ID"})
		return
	}

	var req struct {
		LimitCents *int `json:"limit_cents"` // nil = remove limit
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": err.Error()})
		return
	}

	// SECURITY: Validate limit is positive if provided
	if req.LimitCents != nil && *req.LimitCents <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_LIMIT", "message": "Daily limit must be a positive value"})
		return
	}

	cardData, err := h.service.SetDailyLimit(c.Request.Context(), userID, cardID, req.LimitCents)
	if err != nil {
		if err == card.ErrCardNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "NOT_FOUND", "message": "Card not found"})
			return
		}
		h.logger.Error("Failed to set daily limit", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "Failed to set daily limit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"daily_limit_cents": cardData.DailyLimitCents})
}
