package investing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/logger"
	"golang.org/x/time/rate"
)

// aiChatLimiters provides per-user rate limiting for AI chat endpoints (10 req/min).
// Entries are evicted every 10 minutes to prevent unbounded memory growth.
var aiChatLimiters sync.Map

func init() {
	go func() {
		for range time.NewTicker(10 * time.Minute).C {
			aiChatLimiters.Range(func(key, _ interface{}) bool {
				aiChatLimiters.Delete(key)
				return true
			})
		}
	}()
}

func getAIChatLimiter(userID string) *rate.Limiter {
	if v, ok := aiChatLimiters.Load(userID); ok {
		return v.(*rate.Limiter)
	}
	// 10 requests per minute = 1 every 6 seconds, burst of 10
	l := rate.NewLimiter(rate.Limit(10.0/60.0), 10)
	actual, _ := aiChatLimiters.LoadOrStore(userID, l)
	return actual.(*rate.Limiter)
}

// AIChatHandlers handles AI chat endpoints
type AIChatHandlers struct {
	orchestrator *aiservice.Orchestrator
	convService  *conversationsvc.Service
	logger       *logger.Logger
}

// NewAIChatHandlers creates new AI chat handlers
func NewAIChatHandlers(orchestrator *aiservice.Orchestrator, convService *conversationsvc.Service, logger *logger.Logger) *AIChatHandlers {
	return &AIChatHandlers{orchestrator: orchestrator, convService: convService, logger: logger}
}

// ChatRequest represents a chat message request
type ChatRequest struct {
	Message            string       `json:"message" binding:"required"`
	History            []ai.Message `json:"history,omitempty"`
	TransactionContext *TxContext   `json:"transaction_context,omitempty"`
	ConversationID     string       `json:"conversation_id,omitempty"`
}

// TxContext provides context about a specific transaction the user tapped on.
type TxContext struct {
	Type     string `json:"type"` // "card", "withdrawal", "deposit", "p2p"
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
	prefix := fmt.Sprintf("[The user is asking about a specific %s transaction: %s %s", sanitizeField(tc.Type), sanitizeField(tc.Amount), sanitizeField(tc.Currency))
	if tc.Merchant != "" {
		prefix += " at " + sanitizeField(tc.Merchant)
	}
	if tc.Date != "" {
		prefix += " on " + sanitizeField(tc.Date)
	}
	if tc.Status != "" {
		prefix += " (status: " + sanitizeField(tc.Status) + ")"
	}
	prefix += "]\n\n"
	return prefix
}

// Prompt injection patterns to strip from user input.
var injectionPatterns = regexp.MustCompile(`(?i)(ignore previous instructions|you are now|system:|SYSTEM OVERRIDE|<\|im_start\|>|<\|im_end\|>|\[INST\]|\[/INST\])`)

var (
	errInvalidConversationID = errors.New("invalid conversation id")
	errConversationNotFound  = errors.New("conversation not found")
)

// sanitizeUserMessage strips control characters and prompt injection patterns.
func sanitizeUserMessage(msg string) string {
	// Strip control characters (keep newline, tab)
	msg = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, msg)
	msg = injectionPatterns.ReplaceAllString(msg, "")
	return strings.TrimSpace(msg)
}

// sanitizeField strips injection patterns from transaction context fields.
func sanitizeField(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return injectionPatterns.ReplaceAllString(s, "")
}

func (h *AIChatHandlers) getOrCreateConversation(ctx context.Context, userID uuid.UUID, conversationID string) (*entities.AIConversation, error) {
	if h.convService == nil {
		return nil, nil
	}

	if conversationID == "" {
		return h.convService.CreateConversation(ctx, userID, "")
	}

	convID, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, errInvalidConversationID
	}

	conv, err := h.convService.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.UserID != userID {
		return nil, errConversationNotFound
	}
	return conv, nil
}

// ChatStream handles POST /api/v1/ai/chat/stream (SSE)
func (h *AIChatHandlers) ChatStream(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if !getAIChatLimiter(userID.String()).Allow() {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":   "rate_limit_exceeded",
			"message": "You're sending messages too fast — take a breather and try again in a minute 🕐",
		})
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

	err = func() error {
		message := req.TransactionContext.toPromptPrefix() + sanitizeUserMessage(req.Message)
		emitFn := func(event aiservice.StreamEvent) {
			data, _ := json.Marshal(event)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		}

		if req.ConversationID != "" && h.convService != nil {
			convID, parseErr := uuid.Parse(req.ConversationID)
			if parseErr != nil {
				errEvent, _ := json.Marshal(aiservice.StreamEvent{Type: "error", Content: "Invalid conversation ID"})
				fmt.Fprintf(c.Writer, "data: %s\n\n", errEvent)
				c.Writer.Flush()
				return nil
			}
			conv, convErr := h.convService.GetConversation(c.Request.Context(), convID)
			if convErr != nil || conv == nil || conv.UserID != userID {
				errEvent, _ := json.Marshal(aiservice.StreamEvent{Type: "error", Content: "Conversation not found"})
				fmt.Fprintf(c.Writer, "data: %s\n\n", errEvent)
				c.Writer.Flush()
				return nil
			}
			return h.orchestrator.ChatStreamInConversation(c.Request.Context(), userID, conv, message, emitFn)
		}

		return h.orchestrator.ChatStream(c.Request.Context(), userID, message, req.History, emitFn)
	}()

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

	if !getAIChatLimiter(userID.String()).Allow() {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":   "rate_limit_exceeded",
			"message": "You're sending messages too fast — take a breather and try again in a minute 🕐",
		})
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
			"content":      "You've been chatting a lot this month! Your AI assistant will be back at full power next month. In the meantime, check your Station for balances and the spending tab for insights 💡",
			"over_ceiling": true,
			"tokens_used":  0,
		})
		return
	}

	message := req.TransactionContext.toPromptPrefix() + sanitizeUserMessage(req.Message)

	// Prefer persisted conversations so action confirmations and usage accounting are reliable.
	if h.convService != nil {
		conv, err := h.getOrCreateConversation(c.Request.Context(), userID, req.ConversationID)
		if err != nil {
			switch {
			case errors.Is(err, errInvalidConversationID):
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
			case errors.Is(err, errConversationNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			default:
				h.logger.Error("Failed to resolve conversation", "error", err, "user_id", userID.String())
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve conversation"})
			}
			return
		}

		resp, err := h.orchestrator.ChatWithConversation(c.Request.Context(), userID, conv, message)
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
			"content":         resp.Content,
			"cards":           resp.Cards,
			"tool_calls":      resp.ToolCalls,
			"tokens_used":     resp.TokensUsed,
			"provider":        resp.Provider,
			"pending_action":  resp.PendingAction,
			"conversation_id": conv.ID.String(),
		})
		return
	}

	// Fallback when conversation persistence is unavailable.
	resp, err := h.orchestrator.ChatInContext(c.Request.Context(), userID, uuid.Nil, message, req.History)
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

	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusOK, gin.H{"type": "performance", "insight": "Your AI assistant will be back at full power next month 💡", "over_ceiling": true})
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
