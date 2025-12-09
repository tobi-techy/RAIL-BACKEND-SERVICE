package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/recipient"
	"go.uber.org/zap"
)

// RecipientHandlers handles recipient API endpoints
type RecipientHandlers struct {
	service *recipient.Service
	logger  *zap.Logger
}

// NewRecipientHandlers creates new recipient handlers
func NewRecipientHandlers(service *recipient.Service, logger *zap.Logger) *RecipientHandlers {
	return &RecipientHandlers{service: service, logger: logger}
}

// CreateRecipientRequest represents the request to create a recipient
type CreateRecipientRequest struct {
	Name      string `json:"name" binding:"required"`
	Schema    string `json:"schema" binding:"required,oneof=evm solana"`
	Address   string `json:"address" binding:"required"`
	IsDefault bool   `json:"is_default"`
}

// Create creates a new withdrawal recipient
// POST /api/v1/recipients
func (h *RecipientHandlers) Create(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req CreateRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.service.Create(c.Request.Context(), &recipient.CreateRequest{
		UserID:    userID.(uuid.UUID),
		Name:      req.Name,
		Schema:    req.Schema,
		Address:   req.Address,
		IsDefault: req.IsDefault,
	})
	if err != nil {
		h.logger.Error("Failed to create recipient", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create recipient"})
		return
	}

	c.JSON(http.StatusCreated, result)
}

// List returns all recipients for the user
// GET /api/v1/recipients
func (h *RecipientHandlers) List(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	recipients, err := h.service.GetByUserID(c.Request.Context(), userID.(uuid.UUID))
	if err != nil {
		h.logger.Error("Failed to list recipients", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list recipients"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"recipients": recipients})
}

// Get returns a specific recipient
// GET /api/v1/recipients/:id
func (h *RecipientHandlers) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipient id"})
		return
	}

	result, err := h.service.GetByID(c.Request.Context(), id)
	if err != nil || result == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "recipient not found"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// SetDefault sets a recipient as the default
// PUT /api/v1/recipients/:id/default
func (h *RecipientHandlers) SetDefault(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	recipientID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipient id"})
		return
	}

	if err := h.service.SetDefault(c.Request.Context(), userID.(uuid.UUID), recipientID); err != nil {
		h.logger.Error("Failed to set default recipient", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set default"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "default recipient updated"})
}

// Delete removes a recipient
// DELETE /api/v1/recipients/:id
func (h *RecipientHandlers) Delete(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recipient id"})
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		h.logger.Error("Failed to delete recipient", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete recipient"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "recipient deleted"})
}
