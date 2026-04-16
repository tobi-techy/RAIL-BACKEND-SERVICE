package investing

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin:  func(r *http.Request) bool { return true },
	ReadBufferSize:  16384,
	WriteBufferSize: 16384,
}

const (
	maxSessionDuration = 15 * time.Minute
	idleTimeout        = 5 * time.Minute
)

// VoiceHandler handles real-time voice sessions.
type VoiceHandler struct {
	apiKey       string
	model        string
	orchestrator *aiservice.Orchestrator
	usage        aiservice.UsageTracker
	logger       *zap.Logger
}

func NewVoiceHandler(apiKey, model string, orchestrator *aiservice.Orchestrator, usage aiservice.UsageTracker, logger *zap.Logger) *VoiceHandler {
	return &VoiceHandler{apiKey: apiKey, model: model, orchestrator: orchestrator, usage: usage, logger: logger}
}

// HandleSession upgrades to WebSocket and proxies audio between client and OpenAI Realtime API.
func (h *VoiceHandler) HandleSession(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Cost ceiling check
	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "monthly AI limit reached"})
		return
	}

	// Upgrade client connection to WebSocket
	clientConn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}
	defer clientConn.Close()

	// Connect to OpenAI Realtime API
	openaiConn, err := infraai.DialRealtime(h.apiKey, h.model, h.logger)
	if err != nil {
		h.logger.Error("openai realtime dial failed", zap.Error(err))
		clientConn.WriteJSON(map[string]string{"type": "error", "message": "voice service unavailable"})
		return
	}
	defer openaiConn.Close()

	// Configure session with Miriam's prompt and tools
	if err := h.configureSession(openaiConn); err != nil {
		h.logger.Error("session configure failed", zap.Error(err))
		return
	}

	h.logger.Info("voice session started", zap.String("user_id", userID.String()))
	startTime := time.Now()
	var lastActivity atomic.Value
	lastActivity.Store(time.Now())

	ctx, cancel := context.WithTimeout(c.Request.Context(), maxSessionDuration)
	defer cancel()

	// Goroutine: client → OpenAI (forward audio + client events)
	go func() {
		defer cancel()
		for {
			_, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			lastActivity.Store(time.Now())
			// Forward raw message to OpenAI
			if err := openaiConn.Send(json.RawMessage(msg)); err != nil {
				return
			}
		}
	}()

	// Goroutine: idle timeout checker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if last, ok := lastActivity.Load().(time.Time); ok && time.Since(last) > idleTimeout {
					cancel()
					return
				}
			}
		}
	}()

	// Main loop: OpenAI → client (forward audio + handle tool calls)
	for {
		select {
		case <-ctx.Done():
			h.trackUsage(c.Request.Context(), userID, startTime)
			h.logger.Info("voice session ended",
				zap.String("user_id", userID.String()),
				zap.Duration("duration", time.Since(startTime)))
			return
		default:
		}

		raw, err := openaiConn.ReadEvent()
		if err != nil {
			h.trackUsage(c.Request.Context(), userID, startTime)
			return
		}

		var event struct {
			Type     string `json:"type"`
			CallID   string `json:"call_id"`
			Name     string `json:"name"`
			Arguments string `json:"arguments"`
		}
		json.Unmarshal(raw, &event)

		switch event.Type {
		case "response.function_call_arguments.done":
			// Tool call from OpenAI — execute it and send result back
			lastActivity.Store(time.Now())
			go h.handleToolCall(ctx, userID, openaiConn, event.CallID, event.Name, event.Arguments)

		default:
			// Forward everything else to client (audio deltas, transcripts, etc.)
			clientConn.WriteMessage(websocket.TextMessage, raw)
		}
	}
}

func (h *VoiceHandler) configureSession(conn *infraai.RealtimeClient) error {
	// Convert orchestrator tools to Realtime API format
	tools := h.orchestrator.GetTools()
	sessionTools := make([]infraai.SessionTool, len(tools))
	for i, t := range tools {
		sessionTools[i] = infraai.SessionTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}

	return conn.Send(infraai.NewSessionUpdate(aiservice.SystemPrompt, sessionTools))
}

func (h *VoiceHandler) handleToolCall(ctx context.Context, userID uuid.UUID, conn *infraai.RealtimeClient, callID, name, argsJSON string) {
	var args map[string]interface{}
	json.Unmarshal([]byte(argsJSON), &args)

	// Execute tool via existing orchestrator infrastructure
	tc := infraai.ToolCall{ID: callID, Name: name, Arguments: args}
	result, err := h.orchestrator.ExecuteToolPublic(ctx, userID, tc)
	if err != nil {
		result = map[string]interface{}{"error": err.Error()}
	}

	resultJSON, _ := json.Marshal(result)

	// Send tool result back to OpenAI
	conn.Send(infraai.NewToolResult(callID, string(resultJSON)))
	// Trigger model to continue generating after receiving tool result
	conn.Send(infraai.NewResponseCreate())
}

func (h *VoiceHandler) trackUsage(ctx context.Context, userID uuid.UUID, startTime time.Time) {
	seconds := int(time.Since(startTime).Seconds())
	if h.usage != nil && seconds > 0 {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.usage.TrackInteraction(trackCtx, userID, "gpt-4o-realtime", seconds)
	}
}
