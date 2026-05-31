package investing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rail-service/rail_service/internal/api/handlers/common"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/shopspring/decimal"
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
	voiceSampleRateHz  = 24000

	voiceSessionTicketTTL        = 60 * time.Second
	maxVoiceFrameBytes           = 256 * 1024
	maxVoiceAudioFrameBytes      = 128 * 1024
	maxVoiceAudioBytesPerSecond  = 128 * 1024
	maxVoiceAudioBytesPerSession = 25 * 1024 * 1024
)

// VoiceUsageTracker tracks billable voice usage.
type VoiceUsageTracker interface {
	TrackVoice(ctx context.Context, userID uuid.UUID, seconds int) error
}

// VoiceConversationPersister creates and records voice conversations.
type VoiceConversationPersister interface {
	CreateConversation(ctx context.Context, userID uuid.UUID, title string) (*entities.AIConversation, error)
	RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error
	UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error
	DeleteConversation(ctx context.Context, userID, convID uuid.UUID) error
}

type voiceRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]int // userID -> count
	resetAt  time.Time
}

var globalVoiceRateLimiter = &voiceRateLimiter{
	attempts: make(map[string]int),
	resetAt:  time.Now().Add(1 * time.Hour),
}

func (rl *voiceRateLimiter) allow(userID string, maxPerHour int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if time.Now().After(rl.resetAt) {
		rl.attempts = make(map[string]int)
		rl.resetAt = time.Now().Add(1 * time.Hour)
	}
	rl.attempts[userID]++
	return rl.attempts[userID] <= maxPerHour
}

// VoiceHandler handles real-time voice sessions via ElevenLabs Conversational AI API.
type VoiceHandler struct {
	apiKey              string
	agentID             string
	tokenSecret         string
	webhookSecret       string
	pidginVoiceID       string
	orchestrator        *aiservice.Orchestrator
	usage               VoiceUsageTracker
	convService         VoiceConversationPersister
	allowAnyOrigin      bool
	allowedOriginSet    map[string]struct{}
	allowedHostSet      map[string]struct{}
	allowedHostSuffixes []string
	ttsConfig           *infraai.ELTTSConfig
	logger              *zap.Logger
}

func NewVoiceHandler(apiKey, agentID, tokenSecret, webhookSecret, pidginVoiceID string, orchestrator *aiservice.Orchestrator, usage VoiceUsageTracker, convService VoiceConversationPersister, allowedOrigins []string, logger *zap.Logger, ttsConfig *infraai.ELTTSConfig) *VoiceHandler {
	h := &VoiceHandler{
		apiKey:           apiKey,
		agentID:          agentID,
		tokenSecret:      tokenSecret,
		webhookSecret:    webhookSecret,
		pidginVoiceID:    pidginVoiceID,
		orchestrator:     orchestrator,
		usage:            usage,
		convService:      convService,
		allowedOriginSet: make(map[string]struct{}),
		allowedHostSet:   make(map[string]struct{}),
		ttsConfig:        ttsConfig,
		logger:           logger,
	}
	h.configureAllowedOrigins(allowedOrigins)
	return h
}

func (h *VoiceHandler) IssueSessionToken(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	token, expiresAt, err := auth.GenerateVoiceSessionToken(userID, h.tokenSecret, voiceSessionTicketTTL)
	if err != nil {
		h.logger.Error("voice session token issue failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "voice session unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":      token,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// CheckELHealth validates ElevenLabs connectivity by attempting to fetch a signed URL.
func (h *VoiceHandler) CheckELHealth(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	healthy, err := infraai.CheckELConnectivity(ctx, h.apiKey, h.agentID)
	if err != nil || !healthy {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  "elevenlabs unreachable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// IssueSignedURL returns an agent ID and dynamic variables for the ElevenLabs Conversational AI agent.
// Used by the ElevenLabs React Native SDK to establish a direct WebRTC connection.
func (h *VoiceHandler) IssueSignedURL(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	signedURL, err := infraai.FetchElevenLabsSignedURL(h.apiKey, h.agentID)
	if err != nil {
		h.logger.Error("failed to fetch signed URL", zap.Error(err), zap.String("user_id", userID.String()))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "voice service unavailable"})
		return
	}
	dynamicVars := h.orchestrator.BuildRealtimeDynamicVars(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{
		"signed_url":        signedURL,
		"agent_id":          h.agentID,
		"dynamic_variables": dynamicVars,
	})
}

// GetProactiveInsight returns a contextual insight for the current user to inject into the voice agent.
// GET /v1/ai/voice/proactive-insight
func (h *VoiceHandler) GetProactiveInsight(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	insight := h.orchestrator.GetProactiveVoiceInsight(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{"insight": insight})
}

// HandleToolExecution executes a voice tool call from the ElevenLabs client SDK and returns the result.
// POST /v1/ai/voice/execute-tool
func (h *VoiceHandler) HandleToolExecution(c *gin.Context) {
	userID, err := common.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		ToolName   string                 `json:"tool_name"`
		ToolCallID string                 `json:"tool_call_id"`
		Parameters map[string]interface{} `json:"parameters"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.ToolName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tool_name required"})
		return
	}
	toolCtx, toolCancel := context.WithTimeout(c.Request.Context(), voiceToolTimeout)
	defer toolCancel()

	tc := infraai.ToolCall{ID: req.ToolCallID, Name: req.ToolName, Arguments: req.Parameters}
	result, execErr := h.orchestrator.ExecuteToolPublic(toolCtx, userID, tc)
	if execErr != nil {
		h.logger.Warn("voice tool execution failed",
			zap.String("user_id", userID.String()),
			zap.String("tool", req.ToolName),
			zap.Error(execErr))
		c.JSON(http.StatusOK, gin.H{
			"result":   map[string]interface{}{"error": voiceToolErrorMessage(req.ToolName)},
			"is_error": true,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"result":   result,
		"is_error": false,
	})
}

// HandleSession upgrades to WebSocket and proxies audio between client and AssemblyAI Voice Agent API.
func (h *VoiceHandler) HandleSession(c *gin.Context) {
	userID, err := h.authenticateVoiceSession(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if h.orchestrator.IsUserOverCostCeiling(c.Request.Context(), userID) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "monthly AI limit reached"})
		return
	}

	const maxVoiceSessionsPerHour = 10

	if !globalVoiceRateLimiter.allow(userID.String(), maxVoiceSessionsPerHour) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "voice session rate limit reached. Try again later."})
		return
	}

	upgrader := wsUpgrader
	// Voice WebSocket is authenticated via voice session token (JWT) —
	// origin check is unnecessary and breaks iOS native clients.
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(maxVoiceFrameBytes)
	var clientMu sync.Mutex

	// Connect to ElevenLabs Conversational AI API
	agentConn, err := infraai.DialElevenLabs(h.apiKey, h.agentID, h.logger)
	if err != nil {
		h.logger.Error("elevenlabs voice agent dial failed", zap.Error(err))
		if wErr := clientConn.WriteJSON(map[string]string{"type": "error", "message": "voice service unavailable"}); wErr != nil {
			h.logger.Error("failed to send error to voice client", zap.Error(wErr), zap.String("user_id", userID.String()))
		}
		return
	}
	defer agentConn.Close()

	// Send conversation_initiation_client_data with Miriam's prompt override
	if err := h.initSession(c.Request.Context(), userID, agentConn); err != nil {
		h.logger.Error("session init failed", zap.Error(err))
		clientConn.WriteJSON(map[string]string{"type": "session.error", "code": "configuration_error", "message": "Voice session configuration failed"})
		return
	}

	h.logger.Info("voice session started", zap.String("user_id", userID.String()))
	startTime := time.Now()
	var lastActivity atomic.Value
	lastActivity.Store(time.Now())

	// Create a persisted conversation for this voice session
	var convID uuid.UUID
	if h.convService != nil {
		conv, convErr := h.convService.CreateConversation(c.Request.Context(), userID, "Voice conversation")
		if convErr != nil {
			h.logger.Warn("failed to create voice conversation", zap.Error(convErr))
		} else {
			convID = conv.ID
		}
	}

	// Transcript accumulator for persistence
	var transcriptMu sync.Mutex
	var transcripts []transcriptEntry

	ctx, cancel := context.WithTimeout(c.Request.Context(), maxSessionDuration)
	defer cancel()

	toolSem := make(chan struct{}, 5) // max 5 concurrent tool executions per session

	ready := make(chan struct{})
	var readyOnce sync.Once
	var endOnce sync.Once
	endSession := func() {
		endOnce.Do(func() {
			h.trackUsage(c.Request.Context(), userID, startTime)
			h.persistVoiceTranscripts(userID, convID, &transcriptMu, transcripts)
			h.logger.Info("voice session ended",
				zap.String("user_id", userID.String()),
				zap.Duration("duration", time.Since(startTime)))
		})
	}

	go func() {
		<-ctx.Done()
		agentConn.Close()
		// clientConn closed by defer — don't race with writeClientEvent
		readyOnce.Do(func() { close(ready) })
	}()

	// Client → ElevenLabs: forward audio
	go func() {
		defer cancel()
		windowStart := time.Now()
		windowAudioBytes := 0
		totalAudioBytes := 0
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

			if messageType == websocket.TextMessage {
				var textEvent struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(msg, &textEvent) == nil {
					switch textEvent.Type {
					case "ping":
						h.writeClientControlEvent(clientConn, &clientMu, "pong", nil, cancel)
						continue
					case "input.interrupt":
						h.writeClientControlEvent(clientConn, &clientMu, "rail.playback.flush", map[string]string{"reason": "user_interrupt"}, cancel)
						continue
					}
				}
			}

			event, audioBytes, ok := normalizeClientVoiceEvent(messageType, msg)
			if !ok {
				continue
			}
			if audioBytes > maxVoiceAudioFrameBytes {
				h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.limit_exceeded", map[string]string{"message": "Audio frame too large"}, cancel)
				return
			}
			if audioBytes > 0 {
				now := time.Now()
				if now.Sub(windowStart) >= time.Second {
					windowStart = now
					windowAudioBytes = 0
				}
				windowAudioBytes += audioBytes
				totalAudioBytes += audioBytes
				if windowAudioBytes > maxVoiceAudioBytesPerSecond {
					h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.limit_exceeded", map[string]string{"message": "Audio input is too fast"}, cancel)
					return
				}
				if totalAudioBytes > maxVoiceAudioBytesPerSession {
					h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.limit_exceeded", map[string]string{"message": "Voice session audio limit reached"}, cancel)
					return
				}
			}
			if err := agentConn.Send(event); err != nil {
				return
			}
		}
	}()

	// Idle timeout + ping
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		sessionWarned := false
		idleWarned := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := agentConn.Ping(); err != nil {
					cancel()
					return
				}
				// Send user_activity to prevent EL's server-side idle timeout
				if err := agentConn.Send(infraai.NewELUserActivity()); err != nil {
					h.logger.Warn("failed to send user_activity", zap.Error(err))
				}
				// Warn 1 minute before max session duration
				elapsed := time.Since(startTime)
				if !sessionWarned && elapsed >= maxSessionDuration-1*time.Minute {
					sessionWarned = true
					h.writeClientControlEvent(clientConn, &clientMu, "rail.session.timeout_warning", map[string]string{"message": "Session ending in 1 minute"}, cancel)
				}
				// Warn 1 minute before idle timeout
				if last, ok := lastActivity.Load().(time.Time); ok {
					idle := time.Since(last)
					if !idleWarned && idle >= idleTimeout-1*time.Minute {
						idleWarned = true
						h.writeClientControlEvent(clientConn, &clientMu, "rail.session.timeout_warning", map[string]string{"message": "Disconnecting due to inactivity in 1 minute"}, cancel)
					}
					if idle > idleTimeout {
						cancel()
						return
					}
					// Reset idle warning if activity resumes
					if idle < idleTimeout-1*time.Minute {
						idleWarned = false
					}
				}
			}
		}
	}()

	// Proactive insight checker — sends non-interrupting contextual updates to EL every 60s
	go func() {
		proactiveTicker := time.NewTicker(60 * time.Second)
		defer proactiveTicker.Stop()
		// Send initial insight after a short delay (let greeting play first)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		lastInsight := ""
		for {
			insight := h.orchestrator.GetProactiveVoiceInsight(ctx, userID)
			if insight != "" && insight != lastInsight {
				h.logger.Info("sending proactive context to EL",
					zap.String("insight", insight),
					zap.String("user_id", userID.String()),
				)
				if err := agentConn.Send(infraai.NewELContextualUpdate(insight)); err != nil {
					h.logger.Warn("failed to send proactive context", zap.Error(err))
				}
				lastInsight = insight
			}
			select {
			case <-ctx.Done():
				return
			case <-proactiveTicker.C:
			}
		}
	}()

	// ElevenLabs → Client: translate EL events to the AssemblyAI protocol the client expects
	replyAudioBytes := 0

	for {
		select {
		case <-ctx.Done():
			endSession()
			return
		default:
		}

		raw, err := agentConn.ReadEvent()
		if err != nil {
			endSession()
			return
		}

		var event struct {
			Type string `json:"type"`
		}
		json.Unmarshal(raw, &event)

		switch event.Type {
		case "conversation_initiation_metadata":
			// EL ready → translate to session.ready so the client transitions to listening
			var meta struct {
				ConversationInitiationMetadataEvent struct {
					ConversationID string `json:"conversation_id"`
				} `json:"conversation_initiation_metadata_event"`
			}
			_ = json.Unmarshal(raw, &meta)
			elConvID := meta.ConversationInitiationMetadataEvent.ConversationID
			h.logger.Info("elevenlabs voice session ready",
				zap.String("user_id", userID.String()),
				zap.String("el_conversation_id", elConvID))
			readyOnce.Do(func() { close(ready) })
			// Send session.ready so the client starts listening
			readyMsg, _ := json.Marshal(map[string]string{"type": "session.ready", "session_id": elConvID})
			if !h.writeClientEvent(clientConn, &clientMu, readyMsg, cancel) {
				return
			}
			if convID != uuid.Nil {
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.conversation", map[string]string{"conversation_id": convID.String()}, cancel) {
					return
				}
			}

		case "audio":
			// EL audio → translate to reply.audio with data field
			var audioEvent struct {
				AudioEvent struct {
					AudioBase64 string `json:"audio_base_64"`
					EventID     int64  `json:"event_id"`
				} `json:"audio_event"`
			}
			_ = json.Unmarshal(raw, &audioEvent)
			if audioEvent.AudioEvent.AudioBase64 != "" {
				replyAudioBytes += decodedBase64Len(audioEvent.AudioEvent.AudioBase64)
				translated, _ := json.Marshal(map[string]string{
					"type": "reply.audio",
					"data": audioEvent.AudioEvent.AudioBase64,
				})
				if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
					return
				}
			}

		case "interruption":
			// EL interruption → flush + input.speech.started
			lastActivity.Store(time.Now())
			replyAudioBytes = 0
			if !h.writeClientControlEvent(clientConn, &clientMu, "rail.playback.flush", map[string]string{"reason": "interruption"}, cancel) {
				return
			}
			translated, _ := json.Marshal(map[string]string{"type": "input.speech.started"})
			if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
				return
			}

		case "user_transcript":
			lastActivity.Store(time.Now())
			var ut struct {
				UserTranscriptionEvent struct {
					UserTranscript string `json:"user_transcript"`
				} `json:"user_transcription_event"`
			}
			_ = json.Unmarshal(raw, &ut)
			text := strings.TrimSpace(ut.UserTranscriptionEvent.UserTranscript)
			if text != "" {
				// Send input.speech.stopped so client transitions to 'thinking' during LLM processing
				speechStopped, _ := json.Marshal(map[string]string{"type": "input.speech.stopped"})
				if !h.writeClientEvent(clientConn, &clientMu, speechStopped, cancel) {
					return
				}
				transcriptMu.Lock()
				transcripts = append(transcripts, transcriptEntry{role: "user", text: text})
				transcriptMu.Unlock()
				// Translate to transcript.user
				translated, _ := json.Marshal(map[string]string{"type": "transcript.user", "text": text})
				if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
					return
				}
			}

		case "agent_response":
			// EL agent_response fires when agent starts speaking — translate to reply.started + transcript.agent
			lastActivity.Store(time.Now())
			var ar struct {
				AgentResponseEvent struct {
					AgentResponse string `json:"agent_response"`
				} `json:"agent_response_event"`
			}
			_ = json.Unmarshal(raw, &ar)
			text := strings.TrimSpace(ar.AgentResponseEvent.AgentResponse)

			// Send reply.started so client opens jitter buffer
			replyStarted, _ := json.Marshal(map[string]string{"type": "reply.started"})
			if !h.writeClientEvent(clientConn, &clientMu, replyStarted, cancel) {
				return
			}

			if text != "" {
				transcriptMu.Lock()
				transcripts = append(transcripts, transcriptEntry{role: "assistant", text: text})
				transcriptMu.Unlock()
				// Send transcript.agent
				translated, _ := json.Marshal(map[string]interface{}{"type": "transcript.agent", "text": text, "interrupted": false})
				if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
					return
				}
				// Send sync event with duration estimate
				durationMs := pcm16DurationMS(replyAudioBytes, voiceSampleRateHz)
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.transcript.agent.sync", map[string]string{
					"text":                  text,
					"estimated_duration_ms": durationMsString(durationMs),
				}, cancel) {
					return
				}
				replyAudioBytes = 0
			}

		case "agent_response_correction":
			// Barge-in correction — send interrupted transcript.agent
			var arc struct {
				AgentResponseCorrectionEvent struct {
					CorrectedAgentResponse string `json:"corrected_agent_response"`
				} `json:"agent_response_correction_event"`
			}
			_ = json.Unmarshal(raw, &arc)
			if text := strings.TrimSpace(arc.AgentResponseCorrectionEvent.CorrectedAgentResponse); text != "" {
				translated, _ := json.Marshal(map[string]interface{}{"type": "transcript.agent", "text": text, "interrupted": true})
				if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
					return
				}
			}
			// Send reply.done interrupted so client flushes
			doneFlushed, _ := json.Marshal(map[string]string{"type": "reply.done", "status": "interrupted"})
			if !h.writeClientEvent(clientConn, &clientMu, doneFlushed, cancel) {
				return
			}

		case "ping":
			var pingEvent struct {
				PingEvent struct {
					EventID int64 `json:"event_id"`
				} `json:"ping_event"`
			}
			_ = json.Unmarshal(raw, &pingEvent)
			if err := agentConn.Send(infraai.NewELPong(pingEvent.PingEvent.EventID)); err != nil {
				h.logger.Warn("failed to send pong", zap.Error(err))
			}

		case "client_tool_call":
			lastActivity.Store(time.Now())
			var toolEvent struct {
				ClientToolCall struct {
					ToolName   string                 `json:"tool_name"`
					ToolCallID string                 `json:"tool_call_id"`
					Parameters map[string]interface{} `json:"parameters"`
				} `json:"client_tool_call"`
			}
			_ = json.Unmarshal(raw, &toolEvent)
			tc := toolEvent.ClientToolCall

			select {
			case toolSem <- struct{}{}:
			case <-ctx.Done():
				continue
			}

			go func(toolCallID, name string, args map[string]interface{}, conn *infraai.ElevenLabsClient) {
				defer func() { <-toolSem }()
				toolStart := time.Now()
				toolCtx, toolCancel := context.WithTimeout(ctx, voiceToolTimeout)
				defer toolCancel()

				toolTC := infraai.ToolCall{ID: toolCallID, Name: name, Arguments: args}
				result, execErr := h.orchestrator.ExecuteToolPublic(toolCtx, userID, toolTC)
				isError := false
				if execErr != nil {
					h.logger.Warn("voice tool execution failed",
						zap.String("user_id", userID.String()),
						zap.String("tool", name),
						zap.Duration("duration", time.Since(toolStart)),
						zap.Error(execErr))
					result = map[string]interface{}{"error": voiceToolErrorMessage(name)}
					isError = true
				} else {
					h.logger.Debug("voice tool execution completed",
						zap.String("user_id", userID.String()),
						zap.String("tool", name),
						zap.Duration("duration", time.Since(toolStart)))
				}
				resultJSON, _ := json.Marshal(result)
				if err := conn.Send(infraai.NewELToolResult(toolCallID, string(resultJSON), isError)); err != nil {
					h.logger.Warn("failed to send tool result", zap.Error(err))
				}
			}(tc.ToolCallID, tc.ToolName, tc.Parameters, agentConn)

		case "vad_score":
			// EL VAD confidence score → forward as rail.voice.vad_score for faster barge-in
			var vs struct {
				VADScoreEvent struct {
					Score float64 `json:"vad_score"`
				} `json:"vad_score_event"`
			}
			if json.Unmarshal(raw, &vs) == nil {
				h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.vad_score",
					map[string]string{"score": strconv.FormatFloat(vs.VADScoreEvent.Score, 'f', 4, 64)}, cancel)
			}

		case "agent_response_complete":
			// EL finished agent response → client should settle back to listening
			translated, _ := json.Marshal(map[string]string{"type": "reply.done"})
			if !h.writeClientEvent(clientConn, &clientMu, translated, cancel) {
				return
			}

		case "internal_server_error":
			var errEvent struct {
				Message string `json:"message"`
			}
			json.Unmarshal(raw, &errEvent)
			h.logger.Error("elevenlabs voice agent error",
				zap.String("user_id", userID.String()),
				zap.String("message", errEvent.Message))
			errMsg, _ := json.Marshal(map[string]string{"type": "session.error", "message": errEvent.Message})
			h.writeClientEvent(clientConn, &clientMu, errMsg, cancel)
			h.writeClientControlEvent(clientConn, &clientMu, "rail.session.ended", map[string]string{"reason": "agent_error"}, cancel)
			return

		default:
			// Forward unknown events as-is
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}
		}
	}
}

func (h *VoiceHandler) authenticateVoiceSession(c *gin.Context) (uuid.UUID, error) {
	ticket := strings.TrimSpace(c.Query("voice_session_token"))
	if ticket == "" {
		ticket = strings.TrimSpace(c.Query("ticket"))
	}
	if ticket == "" {
		return uuid.Nil, fmt.Errorf("voice session token required")
	}
	userID, err := auth.ValidateVoiceSessionToken(ticket, h.tokenSecret)
	if err != nil {
		h.logger.Warn("voice session authentication failed", zap.Error(err), zap.String("remote_addr", c.ClientIP()))
		return uuid.Nil, err
	}
	return userID, nil
}

type transcriptEntry struct {
	role string
	text string
}

func (h *VoiceHandler) writeClientEvent(conn *websocket.Conn, mu *sync.Mutex, raw json.RawMessage, cancel context.CancelFunc) bool {
	mu.Lock()
	defer mu.Unlock()
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		h.logger.Debug("voice client write failed", zap.Error(err))
		cancel()
		return false
	}
	return true
}

func (h *VoiceHandler) writeClientControlEvent(conn *websocket.Conn, mu *sync.Mutex, eventType string, fields map[string]string, cancel context.CancelFunc) bool {
	payload := map[string]string{"type": eventType}
	for k, v := range fields {
		payload[k] = v
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		h.logger.Warn("voice control event marshal failed", zap.Error(err))
		return true
	}
	return h.writeClientEvent(conn, mu, raw, cancel)
}

func normalizeClientVoiceEvent(messageType int, msg []byte) (interface{}, int, bool) {
	if messageType == websocket.BinaryMessage {
		return infraai.NewELAudioChunk(base64.StdEncoding.EncodeToString(msg)), len(msg), true
	}

	var event struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(msg, &event); err == nil && event.Type == "input_audio_buffer.append" && event.Audio != "" {
		return infraai.NewELAudioChunk(event.Audio), decodedBase64Len(event.Audio), true
	}
	if err := json.Unmarshal(msg, &event); err == nil && event.Type == "input.audio" && event.Audio != "" {
		return infraai.NewELAudioChunk(event.Audio), decodedBase64Len(event.Audio), true
	}
	switch event.Type {
	case "input_audio_buffer.commit", "input_audio_buffer.clear", "response.create", "ping", "input.interrupt":
		return nil, 0, false
	}

	return json.RawMessage(msg), 0, true
}

func decodedBase64Len(s string) int {
	if s == "" {
		return 0
	}
	padding := 0
	if strings.HasSuffix(s, "==") {
		padding = 2
	} else if strings.HasSuffix(s, "=") {
		padding = 1
	}
	return (len(s)/4)*3 - padding
}

func pcm16DurationMS(byteLen, sampleRate int) int {
	if byteLen <= 0 || sampleRate <= 0 {
		return 0
	}
	return int((float64(byteLen) / 2 / float64(sampleRate)) * 1000)
}

func durationMsString(ms int) string {
	if ms <= 0 {
		return "0"
	}
	return strconv.Itoa(ms)
}

func (h *VoiceHandler) initSession(ctx context.Context, userID uuid.UUID, conn *infraai.ElevenLabsClient) error {
	dynamicVars := h.orchestrator.BuildRealtimeDynamicVars(ctx, userID)

	ttsConfig := h.ttsConfig
	if h.pidginVoiceID != "" {
		locale, _ := dynamicVars["locale_style"].(string)
		if locale == "nigeria" || locale == "west_africa" {
			cfg := *h.ttsConfig // copy
			cfg.VoiceID = h.pidginVoiceID
			ttsConfig = &cfg
		}
	}

	return conn.Send(infraai.NewELConversationInit(dynamicVars, ttsConfig))
}

// voiceToolErrorMessage returns a user-friendly error message specific to the tool that failed.
func voiceToolErrorMessage(toolName string) string {
	switch toolName {
	case aiservice.ToolTransferFunds:
		return "I couldn't move your money right now. Try again in a moment, or use the app to transfer."
	case aiservice.ToolInitiateWithdrawal:
		return "The withdrawal didn't go through. Try again shortly, or withdraw from the app."
	case aiservice.ToolGetAccountSummary:
		return "I couldn't pull up your balance right now. Give it a sec and ask again."
	case aiservice.ToolSetSavingsGoal:
		return "I couldn't set that goal right now. Try again in a moment."
	case aiservice.ToolCreateAutomation:
		return "I couldn't create that automation. Try again, or set it up in the app."
	case aiservice.ToolCreateObligationReminder:
		return "I couldn't save that reminder. Try again in a moment."
	case aiservice.ToolGetMoneyFlow, aiservice.ToolGetWithdrawalHistory, aiservice.ToolGetDepositHistory:
		return "I couldn't fetch your transaction history right now. Try again shortly."
	case aiservice.ToolGetFinancialHealth, aiservice.ToolGetFinancialAudit:
		return "I couldn't run your financial check right now. Try again in a moment."
	default:
		return "That didn't work from voice. Try again in a moment, or do it in the app."
	}
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

// persistVoiceTranscripts saves accumulated voice transcripts as conversation messages.
// Pairs consecutive user→assistant turns into exchanges. Runs in background.
// Deletes the conversation if no meaningful transcripts were captured.
func (h *VoiceHandler) persistVoiceTranscripts(userID uuid.UUID, convID uuid.UUID, mu *sync.Mutex, transcripts []transcriptEntry) {
	if h.convService == nil || convID == uuid.Nil {
		return
	}
	mu.Lock()
	entries := make([]transcriptEntry, len(transcripts))
	copy(entries, transcripts)
	mu.Unlock()

	if len(entries) == 0 {
		// No transcripts — clean up the empty conversation
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.convService.DeleteConversation(ctx, userID, convID)
		}()
		return
	}

	go func() {
		// Scale timeout: 5s base + 1s per exchange pair
		timeout := 5*time.Second + time.Duration(len(entries)/2)*time.Second
		if timeout > 30*time.Second {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		// Pair user/assistant turns into exchanges
		var userBuf strings.Builder
		var firstUserMsg string
		for _, e := range entries {
			if e.role == "user" {
				if userBuf.Len() > 0 {
					userBuf.WriteString(" ")
				}
				userBuf.WriteString(e.text)
				if firstUserMsg == "" {
					firstUserMsg = e.text
				}
			} else if e.role == "assistant" {
				userMsg := strings.TrimSpace(userBuf.String())
				if userMsg == "" {
					userMsg = "(voice input)"
				}
				if err := h.convService.RecordExchange(ctx, convID, userMsg, e.text, 0, decimal.Zero, "voice-elevenlabs", nil); err != nil {
					h.logger.Warn("failed to persist voice transcript", zap.Error(err))
					return
				}
				userBuf.Reset()
			}
		}
		// Trailing user message with no response
		if trailing := strings.TrimSpace(userBuf.String()); trailing != "" {
			_ = h.convService.RecordExchange(ctx, convID, trailing, "", 0, decimal.Zero, "voice-assemblyai", nil)
			if firstUserMsg == "" {
				firstUserMsg = trailing
			}
		}

		// Auto-generate title from first user utterance
		if firstUserMsg != "" {
			title := "🎙 " + generateVoiceTitle(firstUserMsg)
			_ = h.convService.UpdateTitle(ctx, convID, title)
		}

		// Ingest voice session into Supermemory for long-term memory
		var pairs [][2]string
		var userBuf2 strings.Builder
		for _, e := range entries {
			if e.role == "user" {
				if userBuf2.Len() > 0 {
					userBuf2.WriteString(" ")
				}
				userBuf2.WriteString(e.text)
			} else if e.role == "assistant" {
				pairs = append(pairs, [2]string{strings.TrimSpace(userBuf2.String()), e.text})
				userBuf2.Reset()
			}
		}
		if h.orchestrator != nil {
			h.orchestrator.IngestVoiceTranscripts(userID, pairs)
		}
	}()
}

// generateVoiceTitle creates a short title from the first voice utterance.
func generateVoiceTitle(msg string) string {
	title := strings.TrimSpace(msg)
	if title == "" {
		return "Voice conversation"
	}
	if len(title) <= 40 {
		return title
	}
	truncated := title[:40]
	if idx := strings.LastIndexByte(truncated, ' '); idx > 15 {
		truncated = truncated[:idx]
	}
	return truncated + "..."
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
	if origin == "" || origin == "null" {
		return true
	}
	if h.allowAnyOrigin {
		return true
	}
	// Native mobile apps (iOS CFNetwork) — no meaningful browser-style origin to validate.
	// The endpoint is already protected by a voice session token.
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
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
