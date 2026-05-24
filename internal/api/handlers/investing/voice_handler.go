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
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/auth"
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
	voiceToolDrainWait = 100 * time.Millisecond
	voiceSampleRateHz  = 24000
	maxSilentRetries   = 1

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

// VoiceHandler handles real-time voice sessions via AssemblyAI Voice Agent API.
type VoiceHandler struct {
	apiKey              string
	tokenSecret         string
	voice               string
	orchestrator        *aiservice.Orchestrator
	usage               VoiceUsageTracker
	allowAnyOrigin      bool
	allowedOriginSet    map[string]struct{}
	allowedHostSet      map[string]struct{}
	allowedHostSuffixes []string
	logger              *zap.Logger
}

func NewVoiceHandler(apiKey, tokenSecret, voice string, orchestrator *aiservice.Orchestrator, usage VoiceUsageTracker, allowedOrigins []string, logger *zap.Logger) *VoiceHandler {
	h := &VoiceHandler{
		apiKey:           apiKey,
		tokenSecret:      tokenSecret,
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

	upgrader := wsUpgrader
	upgrader.CheckOrigin = func(r *http.Request) bool { return true }
	clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", zap.Error(err))
		return
	}
	defer clientConn.Close()
	clientConn.SetReadLimit(maxVoiceFrameBytes)
	var clientMu sync.Mutex

	// Connect to AssemblyAI Voice Agent API
	agentConn, err := infraai.DialRealtime(h.apiKey, h.logger)
	if err != nil {
		h.logger.Error("assemblyai voice agent dial failed", zap.Error(err))
		if wErr := clientConn.WriteJSON(map[string]string{"type": "error", "message": "voice service unavailable"}); wErr != nil {
			h.logger.Error("failed to send error to voice client", zap.Error(wErr), zap.String("user_id", userID.String()))
		}
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

	// AssemblyAI → Client: handle events
	var pendingTools []pendingTool
	var pendingMu sync.Mutex
	lastAgentEvent := ""
	waitingForAnswer := false
	replyAudioChunks := 0
	replyAudioBytes := 0
	replyID := ""
	silentReplyRetries := 0
	silentReplyText := ""

	setLastAgentEvent := func(eventType string) {
		pendingMu.Lock()
		lastAgentEvent = eventType
		pendingMu.Unlock()
	}

	dropPendingTools := func() {
		pendingMu.Lock()
		pendingTools = nil
		pendingMu.Unlock()
	}

	var flushReadyTools func()
	flushReadyTools = func() {
		var readyTools []completedTool
		pendingMu.Lock()
		if lastAgentEvent == "reply.done" {
			remaining := pendingTools[:0]
			for _, pt := range pendingTools {
				select {
				case result := <-pt.result:
					readyTools = append(readyTools, completedTool{callID: pt.callID, result: result})
				default:
					remaining = append(remaining, pt)
				}
			}
			pendingTools = remaining
		}
		pendingMu.Unlock()

		for _, tool := range readyTools {
			if err := agentConn.Send(infraai.NewToolResult(tool.callID, tool.result)); err != nil {
				h.logger.Warn("failed to send tool result", zap.Error(err))
			}
		}
	}

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
			ReplyID   string                 `json:"reply_id"`
			Data      string                 `json:"data"`
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
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		case "reply.started":
			setLastAgentEvent(event.Type)
			replyID = event.ReplyID
			replyAudioChunks = 0
			replyAudioBytes = 0
			silentReplyText = ""
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		case "reply.audio":
			if event.Data != "" {
				replyAudioChunks++
				replyAudioBytes += decodedBase64Len(event.Data)
			}
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		case "input.speech.started":
			setLastAgentEvent(event.Type)
			lastActivity.Store(time.Now())
			dropPendingTools()
			if !h.writeClientControlEvent(clientConn, &clientMu, "rail.playback.flush", map[string]string{"reason": "input_speech_started"}, cancel) {
				return
			}
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		case "transcript.user":
			lastActivity.Store(time.Now())
			if waitingForAnswer {
				waitingForAnswer = false
				if err := agentConn.Send(infraai.NewBaselineTurnDetectionUpdate()); err != nil {
					h.logger.Warn("failed to restore voice turn detection", zap.Error(err))
				}
			}
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		case "transcript.agent":
			var transcriptEvent struct {
				Text        string `json:"text"`
				Interrupted bool   `json:"interrupted"`
			}
			_ = json.Unmarshal(raw, &transcriptEvent)
			if transcriptEvent.Interrupted {
				dropPendingTools()
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.playback.flush", map[string]string{"reason": "agent_interrupted"}, cancel) {
					return
				}
			} else if strings.HasSuffix(strings.TrimSpace(transcriptEvent.Text), "?") && !waitingForAnswer {
				waitingForAnswer = true
				if err := agentConn.Send(infraai.NewQuestionTurnDetectionUpdate()); err != nil {
					h.logger.Warn("failed to extend voice turn detection", zap.Error(err))
				}
			}
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}
			if !transcriptEvent.Interrupted && strings.TrimSpace(transcriptEvent.Text) != "" {
				durationMs := pcm16DurationMS(replyAudioBytes, voiceSampleRateHz)
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.transcript.agent.sync", map[string]string{
					"reply_id":              firstNonEmpty(replyID, event.ReplyID),
					"text":                  transcriptEvent.Text,
					"estimated_duration_ms": durationMsString(durationMs),
				}, cancel) {
					return
				}
				if replyAudioChunks == 0 {
					silentReplyText = transcriptEvent.Text
				}
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
					result = map[string]interface{}{"error": voiceToolErrorMessage(name)}
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
					case <-time.After(voiceToolDrainWait):
					}
				}
				flushReadyTools()
			}(event.CallID, event.Name, event.Arguments)

		case "reply.done":
			lastActivity.Store(time.Now())
			if event.Status == "interrupted" {
				// User barged in — discard pending tool results
				setLastAgentEvent("reply.interrupted")
				dropPendingTools()
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.playback.flush", map[string]string{"reason": "reply_interrupted"}, cancel) {
					return
				}
			} else {
				setLastAgentEvent(event.Type)
			}

			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}
			if event.Status != "interrupted" && silentReplyText != "" && silentReplyRetries < maxSilentRetries {
				repairText := silentReplyText
				silentReplyText = ""
				silentReplyRetries++
				if !h.writeClientControlEvent(clientConn, &clientMu, "rail.voice.audio_missing", map[string]string{
					"reply_id": firstNonEmpty(replyID, event.ReplyID),
				}, cancel) {
					return
				}
				if err := agentConn.Send(infraai.NewReplyCreate("Repeat this exact response out loud. The previous audio stream was silent: " + truncateForVoiceRepair(repairText))); err != nil {
					h.logger.Warn("failed to request silent voice repair", zap.Error(err))
					silentReplyRetries = 0
				} else {
					replyAudioChunks = 0
					replyAudioBytes = 0
				}
			}
			if event.Status != "interrupted" {
				flushReadyTools()
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
			if !h.writeClientEvent(clientConn, &clientMu, raw, cancel) {
				return
			}

		default:
			// Forward everything else to client (reply.audio, transcript.*, session.ready, etc.)
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

type pendingTool struct {
	callID string
	result <-chan string
}

type completedTool struct {
	callID string
	result string
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
		return infraai.NewAudioInput(base64.StdEncoding.EncodeToString(msg)), len(msg), true
	}

	var event struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
	}
	if err := json.Unmarshal(msg, &event); err == nil && event.Type == "input_audio_buffer.append" && event.Audio != "" {
		return infraai.NewAudioInput(event.Audio), decodedBase64Len(event.Audio), true
	}
	if err := json.Unmarshal(msg, &event); err == nil && event.Type == "input.audio" && event.Audio != "" {
		return json.RawMessage(msg), decodedBase64Len(event.Audio), true
	}
	switch event.Type {
	case "input_audio_buffer.commit", "input_audio_buffer.clear", "response.create":
		// OpenAI Realtime clients send these control events. AssemblyAI handles
		// turn commits and response creation server-side, so forwarding them
		// would produce invalid_format session errors.
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateForVoiceRepair(text string) string {
	text = strings.TrimSpace(text)
	const maxRunes = 240
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "."
}

func (h *VoiceHandler) configureSession(ctx context.Context, userID uuid.UUID, conn *infraai.RealtimeClient) error {
	tools := append(h.orchestrator.GetTools(), aiservice.VoiceTools()...)
	// Voice gets two router tools for chat parity plus a small set of direct
	// high-frequency actions for better parameter extraction.
	voiceTools := voiceToolDescriptions()
	var filteredTools []infraai.SessionTool
	for _, t := range tools {
		description, ok := voiceTools[t.Name]
		if !ok {
			continue
		}
		filteredTools = append(filteredTools, infraai.SessionTool{
			Type:           "function",
			Name:           t.Name,
			Description:    description,
			Parameters:     t.Parameters,
			ExecutionMode:  "interactive",
			TimeoutSeconds: 15,
		})
	}
	sessionTools := filteredTools

	instructions := h.orchestrator.BuildRealtimeInstructions(ctx, userID)
	greeting := h.orchestrator.BuildRealtimeGreeting(ctx, userID)
	return conn.Send(infraai.NewSessionUpdate(instructions, h.voice, greeting, sessionTools))
}

func voiceToolDescriptions() map[string]string {
	return map[string]string{
		aiservice.ToolVoiceMoneyLookup:          "Use for any read-only question Miriam can answer in chat: balances, budgets, spending, transactions, deposits, withdrawals, income, yield, taxes, goals, profile, obligations, automations, subscriptions, runway, receipts, audits, financial health, advice, timeline, investing, market/news, knowledge, or memory. Set the tool field to the underlying chat tool name.",
		aiservice.ToolVoiceMoneyAction:          "Use for less-common chat actions in voice. Set action to the underlying action tool name and params to that action's arguments. Use after clear user intent or confirmation.",
		aiservice.ToolGetAccountSummary:         "Call when the user asks for balance, overview, safe spend, or how their money looks. Fast account snapshot.",
		aiservice.ToolGetBudget:                 "Call when the user asks about their budget, monthly limit, remaining budget, daily allowance, budget status, or how much they can still spend.",
		aiservice.ToolSetBudget:                 "Call when the user wants to set or change their monthly spending budget. Never say the budget changed unless this succeeds.",
		aiservice.ToolTransferFunds:             "Call when the user asks to move money between Spend and Stash. Never say money moved unless this succeeds.",
		aiservice.ToolInitiateWithdrawal:        "Call when the user asks to withdraw or cash out to a linked bank. Ask for missing amount or currency first.",
		aiservice.ToolGetLinkedBanks:            "Call before a withdrawal if the user asks which bank is linked or does not specify a destination.",
		aiservice.ToolSetSavingsGoal:            "Call when the user wants to save for a named target, like rent, travel, emergency fund, or school fees.",
		aiservice.ToolCreateAutomation:          "Call when the user wants an automatic rule. Never claim it is active until the tool succeeds; if authorization is required, say that clearly.",
		aiservice.ToolCreateObligationReminder:  "Call when the user wants Miriam to remember a bill, debt, rent, invoice, subscription, tax, or family support obligation.",
		aiservice.ToolGetMiriamBrief:            "Call when the user asks what changed, what matters, or what they should do next. Fast canonical Miriam brief.",
		aiservice.ToolGetMoneyFlow:              "Call when the user asks where money went, how much they spent, or wants spending versus deposits.",
		aiservice.ToolGetWithdrawalHistory:      "Call when the user asks about recent withdrawals, cash-outs, or money leaving Rail.",
		aiservice.ToolGetFinancialHealth:        "Call when the user asks how they are doing financially, their financial health, score, or progress. Supports multi-month analysis.",
		aiservice.ToolGetFinancialAudit:         "Call when the user says audit me, hard mode, roast my finances, reality check, or wants accountability. Provides detailed financial audit with scores and recommendations.",
		aiservice.ToolGetMiriamMoneyState:       "Call when the user asks what Miriam sees, whether money is safe to move, safe-to-spend, runway, income cadence, upcoming obligations, recurring spend, anomalies, or confidence.",
		aiservice.ToolListMiriamMandates:        "Call when the user asks what Miriam is allowed to do automatically, which autopilot rules are active, or whether quiet money moves are enabled.",
		aiservice.ToolGetMiriamDecisionReceipts: "Call when the user asks what Miriam did quietly, why money moved, recent autopilot actions, skipped actions, or decision receipts.",
	}
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
