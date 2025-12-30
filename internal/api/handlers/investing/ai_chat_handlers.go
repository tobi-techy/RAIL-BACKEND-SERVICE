package investing

import (
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"net/http"

	"github.com/gin-gonic/gin"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/logger"
)

// AIChatHandlers handles AI chat endpoints
type AIChatHandlers struct {
	orchestrator *aiservice.Orchestrator
	logger       *logger.Logger
}

// NewAIChatHandlers creates new AI chat handlers
func NewAIChatHandlers(orchestrator *aiservice.Orchestrator, logger *logger.Logger) *AIChatHandlers {
	return &AIChatHandlers{orchestrator: orchestrator, logger: logger}
}

// ChatRequest represents a chat message request
type ChatRequest struct {
	Message string       `json:"message" binding:"required"`
	History []ai.Message `json:"history,omitempty"`
}

// Chat handles POST /api/v1/ai/chat
func (h *AIChatHandlers) Chat(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID, req.Message, req.History)
	if err != nil {
		h.logger.Error("Chat failed", "error", err, "user_id", userID.String())
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

// GetWrapped handles GET /api/v1/ai/wrapped
func (h *AIChatHandlers) GetWrapped(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	cards, err := h.orchestrator.GenerateWrappedCards(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to generate wrapped cards", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate wrapped"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// QuickInsight handles GET /api/v1/ai/quick-insight
func (h *AIChatHandlers) QuickInsight(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	insightType := c.DefaultQuery("type", "performance")
	var prompt string
	switch insightType {
	case "performance":
		prompt = "Give me a quick one-sentence summary of my portfolio performance this week"
	case "top_mover":
		prompt = "What's my best performing stock this week in one sentence?"
	case "streak":
		prompt = "How's my investing streak going?"
	default:
		prompt = "Give me a quick portfolio update"
	}

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID, prompt, nil)
	if err != nil {
		h.logger.Error("Quick insight failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get insight"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"type":    insightType,
		"insight": resp.Content,
	})
}

// GetSuggestedQuestions handles GET /api/v1/ai/suggestions
func (h *AIChatHandlers) GetSuggestedQuestions(c *gin.Context) {
	_, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	suggestions := []string{
		"How did my portfolio do this week?",
		"What's my best performing stock?",
		"Show me my investment streak",
		"What news affects my holdings?",
		"How diversified is my portfolio?",
	}

	c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
}

// WrappedCard represents a Spotify-Wrapped style card (for response typing)
type WrappedCard struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Content string                 `json:"content"`
	Data    map[string]interface{} `json:"data,omitempty"`
}
