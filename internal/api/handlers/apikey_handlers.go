package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/apikey"
	"go.uber.org/zap"
)

// APIKeyHandlers manages API key operations
type APIKeyHandlers struct {
	apikeyService *apikey.Service
	logger        *zap.Logger
}

// NewAPIKeyHandlers creates a new APIKeyHandlers instance
func NewAPIKeyHandlers(apikeyService *apikey.Service, logger *zap.Logger) *APIKeyHandlers {
	return &APIKeyHandlers{
		apikeyService: apikeyService,
		logger:        logger,
	}
}

// CreateAPIKey handles POST /api/v1/security/api-keys
// Creates a new API key for the authenticated user
func (h *APIKeyHandlers) CreateAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	var req apikey.CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	req.UserID = &userID

	response, err := h.apikeyService.CreateAPIKey(ctx, &req)
	if err != nil {
		h.logger.Error("Failed to create API key", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to create API key")
		return
	}

	SendCreated(c, response)
}

// ListAPIKeys handles GET /api/v1/security/api-keys
// Lists all API keys for the authenticated user
func (h *APIKeyHandlers) ListAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	keys, err := h.apikeyService.ListAPIKeys(ctx, &userID)
	if err != nil {
		h.logger.Error("Failed to list API keys", zap.Error(err), zap.String("user_id", userID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to list API keys")
		return
	}

	SendSuccess(c, gin.H{"api_keys": keys})
}

// RevokeAPIKey handles DELETE /api/v1/security/api-keys/:id
// Revokes a specific API key
func (h *APIKeyHandlers) RevokeAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		SendBadRequest(c, ErrCodeInvalidID, "Invalid key ID")
		return
	}

	if err := h.apikeyService.RevokeAPIKey(ctx, keyID, &userID); err != nil {
		h.logger.Error("Failed to revoke API key",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("key_id", keyID.String()))
		SendInternalError(c, ErrCodeInternalError, err.Error())
		return
	}

	SendSuccess(c, gin.H{"message": "API key revoked"})
}

// UpdateAPIKey handles PUT /api/v1/security/api-keys/:id
// Updates an existing API key
func (h *APIKeyHandlers) UpdateAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		SendBadRequest(c, ErrCodeInvalidID, "Invalid key ID")
		return
	}

	var req struct {
		Name   string   `json:"name" binding:"required"`
		Scopes []string `json:"scopes" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SendBadRequest(c, ErrCodeInvalidRequest, MsgInvalidRequest)
		return
	}

	if err := h.apikeyService.UpdateAPIKey(ctx, keyID, req.Name, req.Scopes, &userID); err != nil {
		h.logger.Error("Failed to update API key",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("key_id", keyID.String()))
		SendInternalError(c, ErrCodeInternalError, err.Error())
		return
	}

	SendSuccess(c, gin.H{"message": "API key updated"})
}

// GetAPIKey handles GET /api/v1/security/api-keys/:id
// Returns details of a specific API key
func (h *APIKeyHandlers) GetAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	userID, err := getUserID(c)
	if err != nil {
		SendUnauthorized(c, "User ID not found")
		return
	}

	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		SendBadRequest(c, ErrCodeInvalidID, "Invalid key ID")
		return
	}

	keys, err := h.apikeyService.ListAPIKeys(ctx, &userID)
	if err != nil {
		h.logger.Error("Failed to get API key",
			zap.Error(err),
			zap.String("user_id", userID.String()),
			zap.String("key_id", keyID.String()))
		SendInternalError(c, ErrCodeInternalError, "Failed to get API key")
		return
	}

	for _, key := range keys {
		if key.ID == keyID {
			SendSuccess(c, key)
			return
		}
	}

	SendNotFound(c, ErrCodeNotFound, "API key not found")
}

// AdminAPIKeyHandlers manages admin API key operations
type AdminAPIKeyHandlers struct {
	apikeyService *apikey.Service
	logger        *zap.Logger
}

// NewAdminAPIKeyHandlers creates a new AdminAPIKeyHandlers instance
func NewAdminAPIKeyHandlers(apikeyService *apikey.Service, logger *zap.Logger) *AdminAPIKeyHandlers {
	return &AdminAPIKeyHandlers{
		apikeyService: apikeyService,
		logger:        logger,
	}
}

// AdminListAPIKeys handles GET /api/v1/admin/api-keys
// Lists all API keys (admin access)
func (h *AdminAPIKeyHandlers) AdminListAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()

	// Parse optional user_id filter
	var userID *uuid.UUID
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		parsed, err := uuid.Parse(userIDStr)
		if err != nil {
			SendBadRequest(c, ErrCodeInvalidUserID, "Invalid user ID")
			return
		}
		userID = &parsed
	}

	keys, err := h.apikeyService.ListAPIKeys(ctx, userID)
	if err != nil {
		h.logger.Error("Failed to list API keys", zap.Error(err))
		SendInternalError(c, ErrCodeInternalError, "Failed to list API keys")
		return
	}

	SendSuccess(c, gin.H{"api_keys": keys})
}

// AdminRevokeAPIKey handles DELETE /api/v1/admin/api-keys/:id
// Revokes any API key (admin access)
func (h *AdminAPIKeyHandlers) AdminRevokeAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	keyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		SendBadRequest(c, ErrCodeInvalidID, "Invalid key ID")
		return
	}

	// Pass nil for userID to bypass ownership check (admin privilege)
	if err := h.apikeyService.RevokeAPIKey(ctx, keyID, nil); err != nil {
		h.logger.Error("Failed to revoke API key",
			zap.Error(err),
			zap.String("key_id", keyID.String()))
		SendInternalError(c, ErrCodeInternalError, err.Error())
		return
	}

	SendSuccess(c, gin.H{"message": "API key revoked"})
}

// AdminGetAPIKeyStats handles GET /api/v1/admin/api-keys/stats
// Returns statistics about API key usage
func (h *AdminAPIKeyHandlers) AdminGetAPIKeyStats(c *gin.Context) {
	ctx := c.Request.Context()

	// Get all API keys for stats
	keys, err := h.apikeyService.ListAPIKeys(ctx, nil)
	if err != nil {
		h.logger.Error("Failed to get API key stats", zap.Error(err))
		SendInternalError(c, ErrCodeInternalError, "Failed to get API key stats")
		return
	}

	// Calculate stats
	totalKeys := len(keys)
	activeKeys := 0
	for _, key := range keys {
		if key.IsActive {
			activeKeys++
		}
	}

	SendSuccess(c, gin.H{
		"total_keys":   totalKeys,
		"active_keys":  activeKeys,
		"revoked_keys": totalKeys - activeKeys,
	})
}
