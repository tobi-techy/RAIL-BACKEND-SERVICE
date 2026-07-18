// Package eval exposes a gated endpoint for evaluating Miriam from the terminal
// test harness — synchronous chat with latency, token, and quality metadata.
// It runs Miriam for an arbitrary user id, so it must stay disabled in production
// and is guarded by a shared token.
package eval

import (
	"context"
	"crypto/hmac"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	"go.uber.org/zap"
)

type Handler struct {
	orchestrator  aiservice.ChatEngine
	convService   *conversationsvc.Service
	autopilot     *aiservice.AutopilotService
	anomalyEngine *aiservice.AnomalyEngine
	anomalyStore  aiservice.AnomalyStore
	token         string
	logger        *zap.Logger
}

func NewHandler(orchestrator aiservice.ChatEngine, convService *conversationsvc.Service, token string, logger *zap.Logger) *Handler {
	return &Handler{orchestrator: orchestrator, convService: convService, token: token, logger: logger}
}

func (h *Handler) SetAutopilot(svc *aiservice.AutopilotService) {
	h.autopilot = svc
}

func (h *Handler) SetAnomalyEngine(engine *aiservice.AnomalyEngine, store aiservice.AnomalyStore) {
	h.anomalyEngine = engine
	h.anomalyStore = store
}

type chatRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	Message        string `json:"message" binding:"required"`
	ConversationID string `json:"conversation_id"`
	ToneMode       string `json:"tone_mode"`
}

type pendingActionView struct {
	Action      string `json:"action"`
	Description string `json:"description"`
}

type qualityView struct {
	Pass     bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
}

type chatResponse struct {
	Content        string             `json:"content"`
	ConversationID string             `json:"conversation_id"`
	Provider       string             `json:"provider"`
	Tokens         int                `json:"tokens"`
	LatencyMs      int64              `json:"latency_ms"`
	PendingAction  *pendingActionView `json:"pending_action,omitempty"`
	Quality        qualityView        `json:"quality"`
}

type autopilotRequest struct {
	UserID string `json:"user_id"` // optional: run for a single user
}

type autopilotResponse struct {
	Phase        string                  `json:"phase"`
	UsersScanned int                     `json:"users_scanned"`
	AlertsSent   int                     `json:"alerts_sent"`
	Metrics      aiservice.AutopilotMetrics `json:"metrics"`
	Summary      string                  `json:"summary"`
}

func (h *Handler) requireToken(c *gin.Context) bool {
	if h.token == "" || !hmac.Equal([]byte(c.GetHeader("X-Eval-Token")), []byte(h.token)) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	return true
}

// Chat handles POST /v1/eval/miriam.
func (h *Handler) Chat(c *gin.Context) {
	if !h.requireToken(c) {
		return
	}

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	conv, err := h.getOrCreateConversation(c.Request.Context(), uid, req.ConversationID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	start := time.Now()
	resp, err := h.orchestrator.ChatWithConversationWithOptions(
		c.Request.Context(), uid, conv, req.Message, aiservice.ChatOptions{ToneMode: req.ToneMode},
	)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	verdict := aiservice.CheckResponseQuality(resp.Content)
	out := chatResponse{
		Content:        resp.Content,
		ConversationID: conv.ID.String(),
		Provider:       resp.Provider,
		Tokens:         resp.TokensUsed,
		LatencyMs:      latency,
		Quality:        qualityView{Pass: verdict.Pass, Failures: verdict.Failures},
	}
	if resp.PendingAction != nil {
		out.PendingAction = &pendingActionView{
			Action:      resp.PendingAction.Action,
			Description: resp.PendingAction.Description,
		}
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) AutopilotMorning(c *gin.Context) {
	h.runAutopilot(c, "morning")
}

func (h *Handler) AutopilotMidday(c *gin.Context) {
	h.runAutopilot(c, "midday")
}

func (h *Handler) AutopilotEvening(c *gin.Context) {
	h.runAutopilot(c, "evening")
}

type anomalyEvalRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type anomalyEvalResponse struct {
	Results  []aiservice.AnomalyResult `json:"results"`
	Count    int                       `json:"count"`
	Summary  string                    `json:"summary"`
}

func (h *Handler) AnomalyScan(c *gin.Context) {
	if !h.requireToken(c) {
		return
	}
	if h.anomalyEngine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "anomaly engine not available"})
		return
	}

	var req anomalyEvalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	now := time.Now().UTC()
	results := h.anomalyEngine.RunAllChecks(c.Request.Context(), uid, now)

	// Store results so orchestrator can reference them
	if h.anomalyStore != nil && len(results) > 0 {
		_ = h.anomalyStore.Set(c.Request.Context(), uid, results, 24*time.Hour)
	}

	c.JSON(http.StatusOK, anomalyEvalResponse{
		Results: results,
		Count:   len(results),
		Summary: fmt.Sprintf("Anomaly scan complete. Found %d issue(s).", len(results)),
	})
}

func (h *Handler) runAutopilot(c *gin.Context, phase string) {
	if !h.requireToken(c) {
		return
	}
	if h.autopilot == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "autopilot not available"})
		return
	}

	var req autopilotRequest
	_ = c.ShouldBindJSON(&req)

	ctx := c.Request.Context()

	switch phase {
	case "morning":
		h.autopilot.RunMorningScan(ctx)
	case "midday":
		h.autopilot.RunMiddayCheck(ctx)
	case "evening":
		h.autopilot.RunEveningReview(ctx)
	}

	m := h.autopilot.Metrics()
	c.JSON(http.StatusOK, autopilotResponse{
		Phase:   phase,
		Metrics: m,
		Summary: fmt.Sprintf("Autopilot %s scan complete.", phase),
	})
}

func (h *Handler) getOrCreateConversation(ctx context.Context, userID uuid.UUID, conversationID string) (*entities.AIConversation, error) {
	if h.convService == nil {
		return nil, fmt.Errorf("conversation service unavailable")
	}
	if conversationID == "" {
		return h.convService.CreateConversation(ctx, userID, "")
	}
	convID, err := uuid.Parse(conversationID)
	if err != nil {
		return nil, fmt.Errorf("invalid conversation_id")
	}
	conv, err := h.convService.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.UserID != userID {
		return nil, fmt.Errorf("conversation not found")
	}
	return conv, nil
}
