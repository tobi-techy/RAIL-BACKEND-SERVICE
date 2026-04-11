package investing

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	"go.uber.org/zap"
)

// ConversationHandlers handles AI conversation endpoints.
type ConversationHandlers struct {
	orchestrator *aiservice.Orchestrator
	convService  *conversationsvc.Service
	logger       *zap.Logger
}

// NewConversationHandlers creates new conversation handlers.
func NewConversationHandlers(orchestrator *aiservice.Orchestrator, convService *conversationsvc.Service, logger *zap.Logger) *ConversationHandlers {
	return &ConversationHandlers{orchestrator: orchestrator, convService: convService, logger: logger}
}

// CreateConversation handles POST /api/v1/ai/conversations
func (h *ConversationHandlers) CreateConversation(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Title string `json:"title"`
	}
	_ = c.ShouldBindJSON(&req)

	conv, err := h.convService.CreateConversation(c.Request.Context(), userID, req.Title)
	if err != nil {
		h.logger.Error("create conversation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create conversation"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": conv})
}

// ListConversations handles GET /api/v1/ai/conversations
func (h *ConversationHandlers) ListConversations(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	convs, err := h.convService.ListConversations(c.Request.Context(), userID, 20, 0)
	if err != nil {
		h.logger.Error("list conversations failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list conversations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": convs})
}

// GetConversation handles GET /api/v1/ai/conversations/:id
func (h *ConversationHandlers) GetConversation(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	conv, err := h.convService.GetConversation(c.Request.Context(), convID)
	if err != nil {
		h.logger.Error("get conversation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get conversation"})
		return
	}
	if conv == nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	msgs, err := h.convService.GetMessages(c.Request.Context(), convID, 50, 0)
	if err != nil {
		h.logger.Error("get messages failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get messages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"conversation": conv, "messages": msgs}})
}

// DeleteConversation handles DELETE /api/v1/ai/conversations/:id
func (h *ConversationHandlers) DeleteConversation(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	conv, err := h.convService.GetConversation(c.Request.Context(), convID)
	if err != nil || conv == nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	if err := h.convService.DeleteConversation(c.Request.Context(), userID, convID); err != nil {
		h.logger.Error("delete conversation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete conversation"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "conversation deleted"})
}

// ChatInConversation handles POST /api/v1/ai/conversations/:id/chat
func (h *ConversationHandlers) ChatInConversation(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return
	}

	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	conv, err := h.convService.GetConversation(c.Request.Context(), convID)
	if err != nil || conv == nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	resp, err := h.orchestrator.ChatWithConversation(c.Request.Context(), userID, conv, req.Message)
	if err != nil {
		h.logger.Error("chat failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "chat failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content":     resp.Content,
		"tool_calls":  resp.ToolCalls,
		"tokens_used": resp.TokensUsed,
		"provider":    resp.Provider,
	})
}
