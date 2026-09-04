package investing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
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

// sseKeepaliveInterval paces the comment heartbeat on the chat stream. Short
// enough to stay under common proxy idle timeouts and to keep the client's
// typing indicator alive through a long tool chain.
const sseKeepaliveInterval = 10 * time.Second

// sseStream serializes every write to a Server-Sent Events response. Gin's
// ResponseWriter is not safe for concurrent use and the keepalive heartbeat
// writes from its own goroutine alongside the orchestrator's emit callback, so
// all writes funnel through one mutex.
type sseStream struct {
	mu sync.Mutex
	w  gin.ResponseWriter
}

// writeRaw emits a pre-formatted SSE payload (including its trailing blank
// line) and flushes.
func (s *sseStream) writeRaw(payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.w, payload); err != nil {
		return
	}
	s.w.Flush()
}

// event marshals and emits one stream event. Marshal failures are dropped:
// callers that need a guaranteed-renderable fallback build the payload
// themselves and use writeRaw.
func (s *sseStream) event(e aiservice.StreamEvent) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.writeRaw("data: " + string(data) + "\n\n")
}

// keepalive starts a comment heartbeat and returns a stop function. SSE
// comment lines are ignored by EventSource clients but defeat proxy buffering
// and idle timeouts during the dead air before the first token.
func (s *sseStream) keepalive(ctx context.Context, every time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.writeRaw(": ka\n\n")
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// nudgeLimiters provides per-user rate limiting for nudge endpoints (2 req/30s, burst 3).
var nudgeLimiters sync.Map

func init() {
	go func() {
		for range time.NewTicker(10 * time.Minute).C {
			aiChatLimiters.Range(func(key, _ interface{}) bool {
				aiChatLimiters.Delete(key)
				return true
			})
			nudgeLimiters.Range(func(key, _ interface{}) bool {
				nudgeLimiters.Delete(key)
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

func getNudgeLimiter(userID string) *rate.Limiter {
	if v, ok := nudgeLimiters.Load(userID); ok {
		return v.(*rate.Limiter)
	}
	// 2 requests per 30 seconds, burst of 3
	l := rate.NewLimiter(rate.Limit(2.0/30.0), 3)
	actual, _ := nudgeLimiters.LoadOrStore(userID, l)
	return actual.(*rate.Limiter)
}

// AIChatHandlers handles AI chat endpoints
type AIChatHandlers struct {
	orchestrator aiservice.ChatEngine
	convService  *conversationsvc.Service
	logger       *logger.Logger
}

// NewAIChatHandlers creates new AI chat handlers
func NewAIChatHandlers(orchestrator aiservice.ChatEngine, convService *conversationsvc.Service, logger *logger.Logger) *AIChatHandlers {
	return &AIChatHandlers{orchestrator: orchestrator, convService: convService, logger: logger}
}

// ChatRequest represents a chat message request
type ChatRequest struct {
	Message            string       `json:"message" binding:"required"`
	History            []ai.Message `json:"history,omitempty"`
	TransactionContext *TxContext   `json:"transaction_context,omitempty"`
	ConversationID     string       `json:"conversation_id,omitempty"`
	ToneMode           string       `json:"tone_mode,omitempty"` // "gentle", "direct", or "hard"
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
var injectionPatterns = regexp.MustCompile(`(?i)(ignore previous instructions|ignore all instructions|you are now|system:|SYSTEM OVERRIDE|<\|im_start\|>|<\|im_end\|>|\[INST\]|\[/INST\]|forget everything|new instructions|<\|endoftext\|>|### System|### Human|### Assistant|<system>|</system>|ADMIN OVERRIDE|do not follow|disregard|pretend you are|act as if|roleplay as|bypass|override safety|jailbreak)`)

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
			"message": "You're sending messages too fast — take a breather and try again in a minute",
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
			"content":      "You've hit your monthly AI limit. You can still check balances and transactions in the app tabs — Miriam will be back at full power next month.",
			"over_ceiling": true,
		})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Writer.Flush()

	stream := &sseStream{w: c.Writer}
	// Hold the connection and the client's typing indicator through context
	// assembly, tool chains, and model latency — all of which can pass with no
	// token to send.
	stopKeepalive := stream.keepalive(c.Request.Context(), sseKeepaliveInterval)
	defer stopKeepalive()

	err = func() error {
		message := req.TransactionContext.toPromptPrefix() + sanitizeUserMessage(req.Message)
		emitFn := stream.event

		if req.ConversationID != "" && h.convService != nil {
			convID, parseErr := uuid.Parse(req.ConversationID)
			if parseErr != nil {
				stream.event(aiservice.StreamEvent{Type: "error", Content: "Invalid conversation ID"})
				return nil
			}
			conv, convErr := h.convService.GetConversation(c.Request.Context(), convID)
			if convErr != nil || conv == nil || conv.UserID != userID {
				stream.event(aiservice.StreamEvent{Type: "error", Content: "Conversation not found"})
				return nil
			}
			// Inject conversation_id into done event so frontend can track it
			convEmit := func(event aiservice.StreamEvent) {
				if event.Type == "done" {
					if m, ok := event.Data.(map[string]interface{}); ok {
						m["conversation_id"] = conv.ID.String()
					}
				}
				emitFn(event)
			}
			return h.orchestrator.ChatStreamInConversationWithOptions(c.Request.Context(), userID, conv, message, aiservice.ChatOptions{ToneMode: req.ToneMode}, convEmit)
		}

		// Auto-create conversation server-side if frontend didn't provide one.
		// This prevents orphaned messages that can't be continued.
		if h.convService != nil {
			conv, convErr := h.convService.CreateConversation(c.Request.Context(), userID, "")
			if convErr == nil && conv != nil {
				// Wrap emitFn to inject conversation_id into the done event
				originalEmit := emitFn
				emitFn = func(event aiservice.StreamEvent) {
					if event.Type == "done" {
						if m, ok := event.Data.(map[string]interface{}); ok {
							m["conversation_id"] = conv.ID.String()
						}
					}
					originalEmit(event)
				}
				return h.orchestrator.ChatStreamInConversationWithOptions(c.Request.Context(), userID, conv, message, aiservice.ChatOptions{ToneMode: req.ToneMode}, emitFn)
			}
		}

		return h.orchestrator.ChatStreamWithOptions(c.Request.Context(), userID, message, req.History, aiservice.ChatOptions{ToneMode: req.ToneMode}, emitFn)
	}()

	// The turn is over one way or another: no more dead air to cover, and the
	// heartbeat must not race the terminal events below.
	stopKeepalive()

	if err != nil {
		if c.Request.Context().Err() != nil {
			// Client disconnected — expected for streaming, don't log as error
			h.logger.Info("Stream chat client disconnected", "user_id", userID.String())
			return
		}

		// Cost-ceiling errors get a dedicated friendly stream event so the
		// user understands *why* the reply stopped, not just that it did.
		if ex, ok := ai.IsExceeded(err); ok {
			h.logger.Info("Stream chat refused — user over cost ceiling",
				"user_id", userID.String(),
				"scope", ex.Scope,
				"spent_usd", ex.SpentUSD,
				"limit_usd", ex.LimitUSD,
			)
			stream.event(aiservice.StreamEvent{
				Type:    "error",
				Content: friendlyCeilingMessage(ex),
			})
			return
		}

		h.logger.Error("Stream chat failed", "error", err, "user_id", userID.String())
		// Emit a visible error event. error_after_partial is silently dropped by the
		// client when no tokens were streamed (e.g. a tool call ran but the follow-up
		// completion failed), which left the user staring at nothing. A plain "error"
		// event is always rendered.
		errEvent, marshalErr := json.Marshal(aiservice.StreamEvent{Type: "error", Content: "I couldn't finish that one — mind trying again?"})
		if marshalErr != nil {
			// Never leave the stream without a renderable error event — fall back to
			// a hardcoded, known-valid JSON payload.
			h.logger.Error("failed to marshal stream error event", "error", marshalErr, "user_id", userID.String())
			errEvent = []byte(`{"type":"error","content":"I couldn't finish that one — mind trying again?"}`)
		}
		stream.writeRaw("data: " + string(errEvent) + "\n\n")
	}

	// Signal end of stream
	stream.writeRaw("data: [DONE]\n\n")
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
			"message": "You're sending messages too fast — take a breather and try again in a minute",
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
			"content":      "You've hit your monthly AI limit. You can still check balances and transactions in the app tabs — Miriam will be back at full power next month.",
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

		resp, err := h.orchestrator.ChatWithConversationWithOptions(c.Request.Context(), userID, conv, message, aiservice.ChatOptions{ToneMode: req.ToneMode})
		if err != nil {
			// Cost ceiling → HTTP 429 with a human message + reset hint.
			if ex, ok := ai.IsExceeded(err); ok {
				h.logger.Info("Chat refused — user over cost ceiling",
					"user_id", userID.String(),
					"scope", ex.Scope,
					"spent_usd", ex.SpentUSD,
					"limit_usd", ex.LimitUSD,
				)
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error":       "cost_ceiling_exceeded",
					"scope":       ex.Scope,
					"limit_usd":   ex.LimitUSD,
					"spent_usd":   ex.SpentUSD,
					"message":     friendlyCeilingMessage(ex),
					"retry_after": nextResetUnix(ex.Scope),
				})
				return
			}

			h.logger.Error("Chat failed", "error", err, "user_id", userID.String())

			msg := "I'm having a moment — try again in a few seconds"
			var provErr *ai.ProviderError
			if errors.As(err, &provErr) && provErr.Code == ai.ErrorCodeAuthentication {
				msg = "My brain is having trouble connecting right now — our team has been notified"
			}

			c.JSON(http.StatusOK, gin.H{
				"content":     msg,
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
	resp, err := h.orchestrator.ChatInContextWithOptions(c.Request.Context(), userID, uuid.Nil, message, req.History, aiservice.ChatOptions{ToneMode: req.ToneMode})
	if err != nil {
		// Cost ceiling → HTTP 429 with a human message + reset hint.
		if ex, ok := ai.IsExceeded(err); ok {
			h.logger.Info("Chat refused — user over cost ceiling (fallback path)",
				"user_id", userID.String(),
				"scope", ex.Scope,
				"spent_usd", ex.SpentUSD,
				"limit_usd", ex.LimitUSD,
			)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "cost_ceiling_exceeded",
				"scope":       ex.Scope,
				"limit_usd":   ex.LimitUSD,
				"spent_usd":   ex.SpentUSD,
				"message":     friendlyCeilingMessage(ex),
				"retry_after": nextResetUnix(ex.Scope),
			})
			return
		}

		h.logger.Error("Chat failed", "error", err, "user_id", userID.String())

		msg := "I'm having a moment — try again in a few seconds"
		var provErr *ai.ProviderError
		if errors.As(err, &provErr) && provErr.Code == ai.ErrorCodeAuthentication {
			msg = "My brain is having trouble connecting right now — our team has been notified"
		}

		c.JSON(http.StatusOK, gin.H{
			"content":     msg,
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
		c.JSON(http.StatusOK, gin.H{"type": "performance", "insight": "You've hit your monthly AI limit. You can still check balances and transactions in the app tabs — Miriam will be back at full power next month.", "over_ceiling": true})
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

	// Use a detached context so client disconnect doesn't cancel the AI call.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := h.orchestrator.Chat(ctx, userID, prompt, nil)
	if err != nil {
		h.logger.Warn("Quick insight failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusOK, gin.H{
			"type":    insightType,
			"insight": "",
			"error":   "temporarily_unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"type":    insightType,
		"insight": resp.Content,
	})
}

// FinancialHealth handles GET /api/v1/ai/financial-health.
func (h *AIChatHandlers) FinancialHealth(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	args := map[string]interface{}{}
	if period := strings.TrimSpace(c.Query("period")); period != "" {
		if aiservice.IsFinancialAuditPeriod(period) {
			args["period"] = period
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'period' query parameter"})
			return
		}
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "financial-health-http",
		Name:      aiservice.ToolGetFinancialHealth,
		Arguments: args,
	})
	if err != nil {
		h.logger.Error("financial health failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get financial health"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// FinancialAudit handles GET /api/v1/ai/financial-audit.
func (h *AIChatHandlers) FinancialAudit(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	args := map[string]interface{}{}
	if period := strings.TrimSpace(c.Query("period")); period != "" {
		if aiservice.IsFinancialAuditPeriod(period) {
			args["period"] = period
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'period' query parameter"})
			return
		}
	}
	if intensity := strings.TrimSpace(c.Query("intensity")); intensity != "" {
		switch intensity {
		case "gentle", "direct", "hard":
			args["intensity"] = intensity
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'intensity' query parameter"})
			return
		}
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "financial-audit-http",
		Name:      aiservice.ToolGetFinancialAudit,
		Arguments: args,
	})
	if err != nil {
		h.logger.Error("financial audit failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get financial audit"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// CashFlowForecast handles GET /api/v1/ai/cash-flow-forecast.
func (h *AIChatHandlers) CashFlowForecast(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "cash-flow-forecast-http",
		Name:      aiservice.ToolGetCashFlowForecast,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("cash flow forecast failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get cash flow forecast"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// FinancialPlan handles GET /api/v1/ai/financial-plan.
func (h *AIChatHandlers) FinancialPlan(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "financial-plan-http",
		Name:      aiservice.ToolGetFinancialPlan,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("financial plan failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get financial plan"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// ActionReceipts handles GET /api/v1/ai/action-receipts.
func (h *AIChatHandlers) ActionReceipts(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:   "action-receipts-http",
		Name: aiservice.ToolGetActionReceipts,
		Arguments: map[string]interface{}{
			"limit": float64(5),
		},
	})
	if err != nil {
		h.logger.Error("action receipts failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get action receipts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// FinancialAdvice handles GET /api/v1/ai/financial-advice.
func (h *AIChatHandlers) FinancialAdvice(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	args := map[string]interface{}{}
	if intent := strings.TrimSpace(c.Query("intent")); intent != "" {
		args["intent"] = intent
	}
	if amount := strings.TrimSpace(c.Query("amount")); amount != "" {
		parsed, parseErr := strconv.ParseFloat(amount, 64)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'amount' query parameter: must be a valid number"})
			return
		}
		args["proposed_amount"] = parsed
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "financial-advice-http",
		Name:      aiservice.ToolGetFinancialAdvice,
		Arguments: args,
	})
	if err != nil {
		h.logger.Error("financial advice failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get financial advice"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// FinancialTimeline handles GET /api/v1/ai/financial-timeline.
func (h *AIChatHandlers) FinancialTimeline(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	args := map[string]interface{}{}
	if days := strings.TrimSpace(c.Query("days")); days != "" {
		parsed, parseErr := strconv.Atoi(days)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'days' query parameter: must be a valid integer"})
			return
		}
		args["days"] = parsed
	}
	if limit := strings.TrimSpace(c.Query("limit")); limit != "" {
		parsed, parseErr := strconv.Atoi(limit)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'limit' query parameter: must be a valid integer"})
			return
		}
		args["limit"] = parsed
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "financial-timeline-http",
		Name:      aiservice.ToolGetFinancialTimeline,
		Arguments: args,
	})
	if err != nil {
		h.logger.Error("financial timeline failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get financial timeline"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

// MiriamBrief handles GET /api/v1/ai/miriam-brief.
func (h *AIChatHandlers) MiriamBrief(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	result, err := h.orchestrator.ExecuteToolPublic(c.Request.Context(), userID, ai.ToolCall{
		ID:        "miriam-brief-http",
		Name:      aiservice.ToolGetMiriamBrief,
		Arguments: map[string]interface{}{},
	})
	if err != nil {
		h.logger.Error("miriam brief failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get miriam brief"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
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

// GetProactiveOpener handles GET /api/v1/ai/proactive-opener
// Returns a personalized greeting, suggestions, and action chips
// based on the user's financial state, replacing the static empty-state greeting.
func (h *AIChatHandlers) GetProactiveOpener(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	opener := h.orchestrator.GetProactiveOpener(c.Request.Context(), userID)
	c.JSON(http.StatusOK, opener)
}

// GetConversationStarters handles GET /api/v1/ai/starters
// Returns AI-generated contextual conversation starters based on the user's financial state.
func (h *AIChatHandlers) GetConversationStarters(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	starters := h.orchestrator.GetConversationStarters(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{"starters": starters})
}

// WrappedCard represents a Spotify-Wrapped style card (for response typing)
type WrappedCard struct {
	Type    string                 `json:"type"`
	Title   string                 `json:"title"`
	Content string                 `json:"content"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// friendlyCeilingMessage turns a CostGuard ExceededError into the message the
// user sees. Tone matches Miriam's persona (warm, no jargon, no scare quotes).
func friendlyCeilingMessage(ex *ai.ExceededError) string {
	if ex == nil {
		return "Miriam's had a busy day — try again in a bit."
	}
	switch ex.Scope {
	case "daily":
		return "You've hit today's chat limit with me — your limit resets at midnight UTC. You can still check balances, transactions, and automations in the app."
	case "monthly":
		return "You've hit your monthly chat limit — your limit resets at the start of next month. The app's tabs (Balances, Transactions, Automations) still work the same."
	default:
		return "Miriam's had a busy day — try again in a bit."
	}
}

// nextResetUnix returns the unix timestamp (seconds) when the user's counter
// will reset, so clients can show a "resets in 4h 12m" countdown. Daily resets
// at next UTC midnight; monthly resets at the 1st of next month UTC.
func nextResetUnix(scope string) int64 {
	now := time.Now().UTC()
	switch scope {
	case "daily":
		tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		return tomorrow.Unix()
	case "monthly":
		firstNext := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		return firstNext.Unix()
	default:
		return now.Add(1 * time.Hour).Unix()
	}
}

// Nudge handles POST /api/v1/ai/nudge — lightweight ambient nudge for Miriam.
func (h *AIChatHandlers) Nudge(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if !getNudgeLimiter(userID.String()).Allow() {
		c.JSON(http.StatusTooManyRequests, gin.H{"show": false, "severity": "info"})
		return
	}

	var req aiservice.NudgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "screen field is required"})
		return
	}
	if req.Screen == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "screen field is required"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := h.orchestrator.GenerateNudge(ctx, userID, req)
	if err != nil {
		h.logger.Warn("nudge generation failed", "error", err, "user_id", userID.String())
		c.JSON(http.StatusOK, gin.H{"show": false, "severity": "info"})
		return
	}

	c.JSON(http.StatusOK, resp)
}
