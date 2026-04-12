package investing

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
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

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	convs, err := h.convService.ListConversations(c.Request.Context(), userID, limit, offset)
	if err != nil {
		h.logger.Error("list conversations failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list conversations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": convs})
}

// getConversationForUser loads a conversation and verifies ownership.
// Returns the conversation or writes an error response and returns nil.
func (h *ConversationHandlers) getConversationForUser(c *gin.Context, userID uuid.UUID) *entities.AIConversation {
	convID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return nil
	}

	conv, err := h.convService.GetConversation(c.Request.Context(), convID)
	if err != nil {
		h.logger.Error("get conversation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil
	}
	if conv == nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return nil
	}
	return conv
}

// GetConversation handles GET /api/v1/ai/conversations/:id
func (h *ConversationHandlers) GetConversation(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	conv := h.getConversationForUser(c, userID)
	if conv == nil {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	msgs, err := h.convService.GetMessages(c.Request.Context(), conv.ID, limit, offset)
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

	conv := h.getConversationForUser(c, userID)
	if conv == nil {
		return
	}

	if err := h.convService.DeleteConversation(c.Request.Context(), userID, conv.ID); err != nil {
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

	conv := h.getConversationForUser(c, userID)
	if conv == nil {
		return
	}

	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	if len(req.Message) > entities.MaxChatMessageLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message too long", "max_length": entities.MaxChatMessageLength})
		return
	}

	// Cost ceiling check
	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"content":      "You've been chatting a lot this month! Your AI assistant will be back at full power next month 💡",
				"over_ceiling": true,
				"tokens_used":  0,
			},
		})
		return
	}

	resp, err := h.orchestrator.ChatWithConversation(c.Request.Context(), userID, conv, req.Message)
	if err != nil {
		h.logger.Error("chat failed", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"content":     "I'm having a moment — try again in a few seconds 🔄",
				"tokens_used": 0,
				"fallback":    true,
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"content":     resp.Content,
			"cards":       resp.Cards,
			"tool_calls":  resp.ToolCalls,
			"tokens_used": resp.TokensUsed,
			"provider":    resp.Provider,
		},
	})
}
