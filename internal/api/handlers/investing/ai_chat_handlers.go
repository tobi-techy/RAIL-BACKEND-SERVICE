package investing

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
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
	Message            string       `json:"message" binding:"required"`
	History            []ai.Message `json:"history,omitempty"`
	TransactionContext *TxContext   `json:"transaction_context,omitempty"`
}

// TxContext provides context about a specific transaction the user tapped on.
type TxContext struct {
	Type     string `json:"type"`     // "card", "withdrawal", "deposit", "p2p"
	Amount   string `json:"amount"`
	Currency string `json:"currency,omitempty"`
	Merchant string `json:"merchant,omitempty"`
	Date     string `json:"date,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (tc *TxContext) toPromptPrefix() string {
	if tc == nil {
		return ""
	}
	prefix := fmt.Sprintf("[The user is asking about a specific %s transaction: %s %s", tc.Type, tc.Amount, tc.Currency)
	if tc.Merchant != "" {
		prefix += " at " + tc.Merchant
	}
	if tc.Date != "" {
		prefix += " on " + tc.Date
	}
	if tc.Status != "" {
		prefix += " (status: " + tc.Status + ")"
	}
	prefix += "]\n\n"
	return prefix
}

// ChatStream handles POST /api/v1/ai/chat/stream (SSE)
func (h *AIChatHandlers) ChatStream(c *gin.Context) {
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

	if len(req.Message) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message too long", "max_length": 2000})
		return
	}

	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{
			"content":      "You've been chatting a lot this month! Your AI assistant will be back at full power next month 💡",
			"over_ceiling": true,
		})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()

	err = h.orchestrator.ChatStream(c.Request.Context(), userID, req.Message, req.History, func(event aiservice.StreamEvent) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		c.Writer.Flush()
	})

	if err != nil {
		h.logger.Error("Stream chat failed", "error", err, "user_id", userID.String())
		errEvent, _ := json.Marshal(aiservice.StreamEvent{Type: "error", Content: "Something went wrong — try again 🔄"})
		fmt.Fprintf(c.Writer, "data: %s\n\n", errEvent)
		c.Writer.Flush()
	}
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

	if len(req.Message) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message too long", "max_length": 2000})
		return
	}

	// Cost ceiling check — degrade gracefully instead of blocking
	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{
			"content":       "You've been chatting a lot this month! Your AI assistant will be back at full power next month. In the meantime, check your Station for balances and the spending tab for insights 💡",
			"over_ceiling":  true,
			"tokens_used":   0,
		})
		return
	}

	resp, err := h.orchestrator.Chat(c.Request.Context(), userID, req.TransactionContext.toPromptPrefix()+req.Message, req.History)
	if err != nil {
		h.logger.Error("Chat failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusOK, gin.H{
			"content":     "I'm having a moment — try again in a few seconds 🔄",
			"tokens_used": 0,
			"fallback":    true,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content":        resp.Content,
		"cards":          resp.Cards,
		"tool_calls":     resp.ToolCalls,
		"tokens_used":    resp.TokensUsed,
		"provider":       resp.Provider,
		"pending_action": resp.PendingAction,
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
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	suggestions := h.orchestrator.GetPersonalizedSuggestions(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
}

// WrappedCard represents a Spotify-Wrapped style card (for response typing)
type WrappedCard struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Content string                 `json:"content"`
	Data    map[string]interface{} `json:"data,omitempty"`
}
