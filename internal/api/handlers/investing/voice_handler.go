package investing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	ReadBufferSize:  16384,
	WriteBufferSize: 16384,
}

const (
	maxSessionDuration = 15 * time.Minute
	idleTimeout        = 5 * time.Minute
	voiceToolTimeout   = 12 * time.Second
)

// VoiceUsageTracker tracks billable voice usage.
type VoiceUsageTracker interface {
	TrackVoice(ctx context.Context, userID uuid.UUID, seconds int) error
}

// VoiceHandler handles real-time voice sessions via AssemblyAI Voice Agent API.
type VoiceHandler struct {
	apiKey              string
	voice               string
	orchestrator        *aiservice.Orchestrator
	usage               VoiceUsageTracker
	allowAnyOrigin      bool
	allowedOriginSet    map[string]struct{}
	allowedHostSet      map[string]struct{}
	allowedHostSuffixes []string
	logger              *zap.Logger
}

func NewVoiceHandler(apiKey, voice string, orchestrator *aiservice.Orchestrator, usage VoiceUsageTracker, allowedOrigins []string, logger *zap.Logger) *VoiceHandler {
	h := &VoiceHandler{
		apiKey:           apiKey,
		voice:            voice,
		orchestrator:     orchestrator,
		usage:            usage,
		allowedOriginSet: make(map[string]struct{}),
		allowedHostSet:   make(map[string]struct{}),
		logger:           logger,
	}
	h.configureAllowedOrigins(allowedOrigins)
	return h
}

// HandleSession upgrades to WebSocket and proxies audio between client and AssemblyAI Voice Agent API.
func (h *VoiceHandler) HandleSession(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "monthly AI limit reached"})
		return
	}

	upgrader := wsUpgrader
	upgrader.CheckOrigin = h.isAllowedOrigin
	clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}
	defer clientConn.Close()

	// Connect to AssemblyAI Voice Agent API
	agentConn, err := infraai.DialRealtime(h.apiKey, h.logger)
	if err != nil {
		h.logger.Error("assemblyai voice agent dial failed", zap.Error(err))
		clientConn.WriteJSON(map[string]string{"type": "error", "message": "voice service unavailable"})
		return
	}
	defer agentConn.Close()

	// Configure session with Miriam's prompt and tools
	if err := h.configureSession(c.Request.Context(), userID, agentConn); err != nil {
		h.logger.Error("session configure failed", zap.Error(err))
		return
	}

	h.logger.Info("voice session started", zap.String("user_id", userID.String()))
	startTime := time.Now()
	var lastActivity atomic.Value
	lastActivity.Store(time.Now())

	ctx, cancel := context.WithTimeout(c.Request.Context(), maxSessionDuration)
	defer cancel()

	toolSem := make(chan struct{}, 5) // max 5 concurrent tool executions per session

	ready := make(chan struct{})
	var readyOnce sync.Once

	go func() {
		<-ctx.Done()
		agentConn.Close()
		// clientConn closed by defer — don't race with writeClientEvent
		readyOnce.Do(func() { close(ready) })
	}()

	// Client → AssemblyAI: forward audio
	go func() {
		defer cancel()
		// Block until session is ready before processing client audio
		select {
		case <-ready:
		case <-ctx.Done():
			return
		}
		for {
			messageType, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			lastActivity.Store(time.Now())
			if err := agentConn.Send(normalizeClientVoiceEvent(messageType, msg)); err != nil {
				return
			}
		}
	}()

	// Idle timeout + ping
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := agentConn.Ping(); err != nil {
					cancel()
					return
				}
				if last, ok := lastActivity.Load().(time.Time); ok && time.Since(last) > idleTimeout {
					cancel()
					return
				}
			}
		}
	}()

	// AssemblyAI → Client: handle events
	var pendingTools []pendingTool
	var pendingMu sync.Mutex

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

		raw, err := agentConn.ReadEvent()
		if err != nil {
			h.trackUsage(c.Request.Context(), userID, startTime)
			return
		}

		var event struct {
			Type      string                 `json:"type"`
			CallID    string                 `json:"call_id"`
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
			Status    string                 `json:"status"`
		}
		json.Unmarshal(raw, &event)

		switch event.Type {
		case "session.ready":
			var readyEvent struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(raw, &readyEvent)
			h.logger.Info("voice session ready",
				zap.String("user_id", userID.String()),
				zap.String("session_id", readyEvent.SessionID))
			readyOnce.Do(func() { close(ready) })
			if !h.writeClientEvent(clientConn, raw, cancel) {
				return
			}

		case "tool.call":
			// Execute tool and accumulate result. AssemblyAI expects tool.result only after reply.done.
			lastActivity.Store(time.Now())
			resultCh := make(chan string, 1)
			pendingMu.Lock()
			pendingTools = append(pendingTools, pendingTool{callID: event.CallID, result: resultCh})
			pendingMu.Unlock()

			// Acquire semaphore slot (max 5 concurrent tools)
			select {
			case toolSem <- struct{}{}:
			case <-ctx.Done():
				continue
			}

			go func(callID, name string, args map[string]interface{}) {
				defer func() { <-toolSem }()
				toolStart := time.Now()
				toolCtx, toolCancel := context.WithTimeout(ctx, voiceToolTimeout)
				defer toolCancel()

				tc := infraai.ToolCall{ID: callID, Name: name, Arguments: args}
				result, err := h.orchestrator.ExecuteToolPublic(toolCtx, userID, tc)
				if err != nil {
					h.logger.Warn("voice tool execution failed",
						zap.String("user_id", userID.String()),
						zap.String("tool", name),
						zap.Duration("duration", time.Since(toolStart)),
						zap.Error(err))
					result = map[string]interface{}{"error": "I couldn't complete that from voice right now. Try again in a moment."}
				} else {
					h.logger.Debug("voice tool execution completed",
						zap.String("user_id", userID.String()),
						zap.String("tool", name),
						zap.Duration("duration", time.Since(toolStart)))
				}
				resultJSON, _ := json.Marshal(result)
				select {
				case resultCh <- string(resultJSON):
				case <-toolCtx.Done():
					// Context cancelled — bounded wait to avoid leak
					select {
					case resultCh <- string(resultJSON):
					case <-time.After(100 * time.Millisecond):
					}
				}
			}(event.CallID, event.Name, event.Arguments)

		case "reply.done":
			lastActivity.Store(time.Now())
			var toolsToSend []pendingTool
			pendingMu.Lock()
			if event.Status == "interrupted" {
				// User barged in — discard pending tool results
				pendingTools = nil
			} else if len(pendingTools) > 0 {
				toolsToSend = append(toolsToSend, pendingTools...)
				pendingTools = nil
			}
			pendingMu.Unlock()

			for _, pt := range toolsToSend {
				select {
				case result := <-pt.result:
					if err := agentConn.Send(infraai.NewToolResult(pt.callID, result)); err != nil {
						h.logger.Warn("failed to send tool result", zap.Error(err))
					}
				case <-ctx.Done():
					return
				case <-time.After(voiceToolTimeout):
					timeoutResult := map[string]interface{}{"error": "tool timed out"}
					resultJSON, _ := json.Marshal(timeoutResult)
					if err := agentConn.Send(infraai.NewToolResult(pt.callID, string(resultJSON))); err != nil {
						h.logger.Warn("failed to send timed-out tool result", zap.Error(err))
					}
					// Drain the channel so the tool goroutine can exit
					go func(ch <-chan string, ctx context.Context) {
						select {
						case <-ch:
						case <-ctx.Done():
						case <-time.After(voiceToolTimeout):
						}
					}(pt.result, ctx)
				}
			}

			if !h.writeClientEvent(clientConn, raw, cancel) {
				return
			}

		case "session.error":
			var errEvent struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			json.Unmarshal(raw, &errEvent)
			h.logger.Error("voice agent session error",
				zap.String("user_id", userID.String()),
				zap.String("code", errEvent.Code),
				zap.String("message", errEvent.Message))
			if !h.writeClientEvent(clientConn, raw, cancel) {
				return
			}

		default:
			// Forward everything else to client (reply.audio, transcript.*, session.ready, etc.)
			if !h.writeClientEvent(clientConn, raw, cancel) {
				return
			}
		}
	}
}

type pendingTool struct {
	callID string
	result <-chan string
}

func (h *VoiceHandler) writeClientEvent(conn *websocket.Conn, raw json.RawMessage, cancel context.CancelFunc) bool {
	// Note: caller must not hold clientMu — this is the only writer path
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		h.logger.Debug("voice client write failed", zap.Error(err))
		cancel()
		return false
	}
	return true
}

func normalizeClientVoiceEvent(messageType int, msg []byte) interface{} {
	if messageType == websocket.BinaryMessage {
		return infraai.NewAudioInput(base64.StdEncoding.EncodeToString(msg))
	}

	var event struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(msg, &event); err == nil && event.Type == "input_audio_buffer.append" && event.Audio != "" {
		return infraai.NewAudioInput(event.Audio)
	}

	return json.RawMessage(msg)
}

func (h *VoiceHandler) configureSession(ctx context.Context, userID uuid.UUID, conn *infraai.RealtimeClient) error {
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

	instructions := h.orchestrator.BuildRealtimeInstructions(ctx, userID)
	greeting := h.orchestrator.BuildRealtimeGreeting(ctx, userID)
	return conn.Send(infraai.NewSessionUpdate(instructions, h.voice, greeting, sessionTools))
}

func (h *VoiceHandler) trackUsage(ctx context.Context, userID uuid.UUID, startTime time.Time) {
	seconds := int(time.Since(startTime).Seconds())
	if h.usage != nil && seconds > 0 {
		trackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.usage.TrackVoice(trackCtx, userID, seconds); err != nil {
			h.logger.Warn("failed to track voice usage", zap.Error(err))
		}
	}
}

func (h *VoiceHandler) configureAllowedOrigins(allowedOrigins []string) {
	for _, raw := range allowedOrigins {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			h.allowAnyOrigin = true
			continue
		}
		hostPattern := origin
		if strings.HasPrefix(hostPattern, "http://") || strings.HasPrefix(hostPattern, "https://") {
			parsed, err := url.Parse(hostPattern)
			if err != nil {
				continue
			}
			if parsed.Host != "" {
				normalized := strings.ToLower(strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"))
				h.allowedOriginSet[normalized] = struct{}{}
			}
			hostPattern = parsed.Hostname()
		}

		hostPattern = strings.ToLower(strings.TrimSpace(hostPattern))
		hostPattern = strings.TrimPrefix(hostPattern, "*.")
		hostPattern = strings.TrimPrefix(hostPattern, ".")
		if hostPattern == "" {
			continue
		}

		if strings.Contains(origin, "*.") {
			h.allowedHostSuffixes = append(h.allowedHostSuffixes, hostPattern)
			continue
		}

		h.allowedHostSet[hostPattern] = struct{}{}
	}
}

func (h *VoiceHandler) isAllowedOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if h.allowAnyOrigin {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}

	normalized := strings.ToLower(strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"))
	if _, ok := h.allowedOriginSet[normalized]; ok {
		return true
	}

	host := strings.ToLower(parsed.Hostname())
	if _, ok := h.allowedHostSet[host]; ok {
		return true
	}

	for _, suffix := range h.allowedHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}

	return false
}
