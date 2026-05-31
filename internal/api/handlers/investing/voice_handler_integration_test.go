package investing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/pkg/auth"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ---------- Mock ElevenLabs WebSocket server ----------

type mockELServer struct {
	t        *testing.T
	server   *httptest.Server
	url      string
	conn     *websocket.Conn
	received []json.RawMessage
	mu       sync.Mutex
	sendCh   chan []byte
	done     chan struct{}
}

func newMockELServer(t *testing.T) *mockELServer {
	m := &mockELServer{
		t:      t,
		sendCh: make(chan []byte, 100),
		done:   make(chan struct{}),
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		m.conn = conn

		go func() {
			defer close(m.done)
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				m.mu.Lock()
				m.received = append(m.received, msg)
				m.mu.Unlock()
			}
		}()

		for msg := range m.sendCh {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}))

	m.url = strings.Replace(m.server.URL, "http://", "ws://", 1)
	return m
}

func (m *mockELServer) send(event map[string]interface{}) {
	data, err := json.Marshal(event)
	require.NoError(m.t, err)
	m.sendCh <- data
}

func (m *mockELServer) receivedEvents() []json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]json.RawMessage, len(m.received))
	copy(result, m.received)
	return result
}

func (m *mockELServer) waitForReceived(count int) {
	deadline := time.After(3 * time.Second)
	for {
		m.mu.Lock()
		if len(m.received) >= count {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		select {
		case <-deadline:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (m *mockELServer) close() {
	close(m.sendCh)
	m.server.Close()
}

// ---------- Mock types for interface dependencies ----------

type mockVoiceUsageTracker struct {
	trackFn func(ctx context.Context, userID uuid.UUID, seconds int) error
}

func (m *mockVoiceUsageTracker) TrackVoice(ctx context.Context, userID uuid.UUID, seconds int) error {
	return m.trackFn(ctx, userID, seconds)
}

type mockConvService struct {
	createFn  func(ctx context.Context, userID uuid.UUID, title string) (*entities.AIConversation, error)
	recordFn  func(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error
	updateFn  func(ctx context.Context, convID uuid.UUID, title string) error
	deleteFn  func(ctx context.Context, userID, convID uuid.UUID) error
}

func (m *mockConvService) CreateConversation(ctx context.Context, userID uuid.UUID, title string) (*entities.AIConversation, error) {
	if m.createFn != nil {
		return m.createFn(ctx, userID, title)
	}
	return &entities.AIConversation{ID: uuid.New(), UserID: userID}, nil
}

func (m *mockConvService) RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
	if m.recordFn != nil {
		return m.recordFn(ctx, convID, userMsg, assistantMsg, tokens, cost, model, cards)
	}
	return nil
}

func (m *mockConvService) UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, convID, title)
	}
	return nil
}

func (m *mockConvService) DeleteConversation(ctx context.Context, userID, convID uuid.UUID) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, userID, convID)
	}
	return nil
}

// ---------- Normalize client event tests (edge cases beyond existing tests) ----------

func TestVoiceIntegration_NormalizeClientEvent_Additional(t *testing.T) {
	tests := []struct {
		name        string
		messageType int
		data        []byte
		expectOK    bool
	}{
		{
			name:        "drops input_audio_buffer.commit",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"input_audio_buffer.commit"}`),
			expectOK:    false,
		},
		{
			name:        "drops input_audio_buffer.clear",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"input_audio_buffer.clear"}`),
			expectOK:    false,
		},
		{
			name:        "drops input.interrupt",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"input.interrupt"}`),
			expectOK:    false,
		},
		{
			name:        "drops ping",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"ping"}`),
			expectOK:    false,
		},
		{
			name:        "drops response.create",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"response.create"}`),
			expectOK:    false,
		},
		{
			name:        "forwards unknown text event as raw",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"custom.event","foo":"bar"}`),
			expectOK:    true,
		},
		{
			name:        "forwards plain JSON without type",
			messageType: websocket.TextMessage,
			data:        []byte(`{"hello":"world"}`),
			expectOK:    true,
		},
		{
			name:        "input.audio with empty audio drops",
			messageType: websocket.TextMessage,
			data:        []byte(`{"type":"input.audio","":""}`),
			expectOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, _, ok := normalizeClientVoiceEvent(tt.messageType, tt.data)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				require.NotNil(t, event)
			} else {
				assert.Nil(t, event)
			}
		})
	}
}

func TestVoiceIntegration_NormalizeClientEvent_InputAudioWithAudio(t *testing.T) {
	event, audioBytes, ok := normalizeClientVoiceEvent(websocket.TextMessage, []byte(`{"type":"input.audio","audio":"AQIDBAUG"}`))
	require.True(t, ok)
	chunk, ok := event.(infraai.ELAudioChunk)
	require.True(t, ok)
	assert.Equal(t, "AQIDBAUG", chunk.UserAudioChunk)
	assert.Equal(t, 6, audioBytes)
}

// ---------- Voice tool error message tests ----------

func TestVoiceIntegration_ToolErrorMessages(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected string
	}{
		{
			name:     "transfer funds",
			toolName: aiservice.ToolTransferFunds,
			expected: "I couldn't move your money right now. Try again in a moment, or use the app to transfer.",
		},
		{
			name:     "initiate withdrawal",
			toolName: aiservice.ToolInitiateWithdrawal,
			expected: "The withdrawal didn't go through. Try again shortly, or withdraw from the app.",
		},
		{
			name:     "account summary",
			toolName: aiservice.ToolGetAccountSummary,
			expected: "I couldn't pull up your balance right now. Give it a sec and ask again.",
		},
		{
			name:     "set savings goal",
			toolName: aiservice.ToolSetSavingsGoal,
			expected: "I couldn't set that goal right now. Try again in a moment.",
		},
		{
			name:     "create automation",
			toolName: aiservice.ToolCreateAutomation,
			expected: "I couldn't create that automation. Try again, or set it up in the app.",
		},
		{
			name:     "create obligation reminder",
			toolName: aiservice.ToolCreateObligationReminder,
			expected: "I couldn't save that reminder. Try again in a moment.",
		},
		{
			name:     "money flow",
			toolName: aiservice.ToolGetMoneyFlow,
			expected: "I couldn't fetch your transaction history right now. Try again shortly.",
		},
		{
			name:     "withdrawal history",
			toolName: aiservice.ToolGetWithdrawalHistory,
			expected: "I couldn't fetch your transaction history right now. Try again shortly.",
		},
		{
			name:     "deposit history",
			toolName: aiservice.ToolGetDepositHistory,
			expected: "I couldn't fetch your transaction history right now. Try again shortly.",
		},
		{
			name:     "financial health",
			toolName: aiservice.ToolGetFinancialHealth,
			expected: "I couldn't run your financial check right now. Try again in a moment.",
		},
		{
			name:     "financial audit",
			toolName: aiservice.ToolGetFinancialAudit,
			expected: "I couldn't run your financial check right now. Try again in a moment.",
		},
		{
			name:     "unknown tool falls back to default",
			toolName: "unknown_tool",
			expected: "That didn't work from voice. Try again in a moment, or do it in the app.",
		},
		{
			name:     "empty string falls back to default",
			toolName: "",
			expected: "That didn't work from voice. Try again in a moment, or do it in the app.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := voiceToolErrorMessage(tt.toolName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------- Conversation title generation tests ----------

func TestVoiceIntegration_GenerateVoiceTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty input",
			input:    "",
			expected: "Voice conversation",
		},
		{
			name:     "short message",
			input:    "Hello Miriam",
			expected: "Hello Miriam",
		},
		{
			name:     "exactly 40 characters",
			input:    "1234567890123456789012345678901234567890",
			expected: "1234567890123456789012345678901234567890",
		},
		{
			name:     "long message truncates at space",
			input:    "This is a very long voice message that should be truncated nicely",
			expected: "This is a very long voice message that...",
		},
		{
			name:     "long message no space in first 40",
			input:    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAmoretext",
			expected: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA...",
		},
		{
			name:     "long message space too soon falls through",
			input:    "short then long text that goes well beyond forty characters total",
			expected: "short then long text that goes well...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateVoiceTitle(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------- Helper function edge cases ----------

func TestVoiceIntegration_DurationMsString(t *testing.T) {
	assert.Equal(t, "0", durationMsString(0))
	assert.Equal(t, "0", durationMsString(-5))
	assert.Equal(t, "1500", durationMsString(1500))
	assert.Equal(t, "30000", durationMsString(30000))
}

func TestVoiceIntegration_PCM16DurationMS_EdgeCases(t *testing.T) {
	assert.Equal(t, 0, pcm16DurationMS(0, 24000))
	assert.Equal(t, 0, pcm16DurationMS(48000, 0))
	assert.Equal(t, 0, pcm16DurationMS(0, 0))
	assert.Equal(t, 1000, pcm16DurationMS(48000, 24000))
	assert.Equal(t, 500, pcm16DurationMS(24000, 24000))
	assert.Equal(t, 0, pcm16DurationMS(-100, 24000))
}

func TestVoiceIntegration_DecodedBase64Len_EdgeCases(t *testing.T) {
	assert.Equal(t, 0, decodedBase64Len(""))
	assert.Equal(t, 3, decodedBase64Len("AQID"))
	assert.Equal(t, 2, decodedBase64Len("AQI="))
	assert.Equal(t, 1, decodedBase64Len("AQ=="))
	assert.Equal(t, -2, decodedBase64Len("=="))
}

// ---------- Session constants verification ----------

func TestVoiceIntegration_SessionConstants(t *testing.T) {
	assert.Equal(t, 15*time.Minute, maxSessionDuration)
	assert.Equal(t, 5*time.Minute, idleTimeout)
	assert.Equal(t, 12*time.Second, voiceToolTimeout)
	assert.Equal(t, 24000, voiceSampleRateHz)
	assert.Equal(t, 60*time.Second, voiceSessionTicketTTL)
	assert.Equal(t, 256*1024, maxVoiceFrameBytes)
	assert.Equal(t, 128*1024, maxVoiceAudioFrameBytes)
	assert.Equal(t, 128*1024, maxVoiceAudioBytesPerSecond)
	assert.Equal(t, 25*1024*1024, maxVoiceAudioBytesPerSession)
}

// ---------- Origin configuration and checking ----------

func TestVoiceIntegration_OriginConfiguration(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	t.Run("allows wildcard origin", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"*"})
		assert.True(t, h.allowAnyOrigin)
	})

	t.Run("allows exact origin match", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"https://example.com"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://example.com")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("rejects unknown origin", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"https://example.com"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://evil.com")
		assert.False(t, h.isAllowedOrigin(req))
	})

	t.Run("accepts non-http mobile origin", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "railapp://session")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("accepts empty origin", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		req := httptest.NewRequest("GET", "/", nil)
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("accepts null origin", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "null")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("handles wildcard host suffix match", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"*.rail.app"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://app.rail.app")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("rejects non-matching wildcard suffix", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"*.rail.app"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://evil.com")
		assert.False(t, h.isAllowedOrigin(req))
	})

	t.Run("host match works across ports", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"https://rail.app"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://rail.app:8443")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("allows subdomain under exact host", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"https://rail.app"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://sub.rail.app")
		assert.False(t, h.isAllowedOrigin(req))
	})
}

func TestVoiceIntegration_ConfigureOrigins(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	t.Run("empty origins list", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{})
		assert.False(t, h.allowAnyOrigin)
		assert.Empty(t, h.allowedOriginSet)
		assert.Empty(t, h.allowedHostSet)
	})

	t.Run("multiple origin patterns", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{
			"https://app.rail.app",
			"https://admin.rail.app",
			"*.rail.app",
		})

		req1 := httptest.NewRequest("GET", "/", nil)
		req1.Header.Set("Origin", "https://app.rail.app")
		assert.True(t, h.isAllowedOrigin(req1))

		req2 := httptest.NewRequest("GET", "/", nil)
		req2.Header.Set("Origin", "https://admin.rail.app")
		assert.True(t, h.isAllowedOrigin(req2))

		req3 := httptest.NewRequest("GET", "/", nil)
		req3.Header.Set("Origin", "https://anything.rail.app")
		assert.True(t, h.isAllowedOrigin(req3))

		req4 := httptest.NewRequest("GET", "/", nil)
		req4.Header.Set("Origin", "https://evil.com")
		assert.False(t, h.isAllowedOrigin(req4))
	})

	t.Run("ignores empty and whitespace strings", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"", "  ", "https://example.com"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://example.com")
		assert.True(t, h.isAllowedOrigin(req))
	})

	t.Run("origin with path ignores path component", func(t *testing.T) {
		h := &VoiceHandler{
			allowedOriginSet: make(map[string]struct{}),
			allowedHostSet:   make(map[string]struct{}),
			logger:           logger,
		}
		h.configureAllowedOrigins([]string{"https://rail.app/app"})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://rail.app")
		assert.True(t, h.isAllowedOrigin(req))
	})
}

// ---------- WebSocket write and read integration with mock EL server ----------

func TestVoiceIntegration_WriteClientEvent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	h := &VoiceHandler{logger: logger}

	el := newMockELServer(t)
	defer el.close()

	conn, _, err := websocket.DefaultDialer.Dial(el.url, nil)
	require.NoError(t, err)
	defer conn.Close()

	var mu sync.Mutex
	cancel := func() {}

	t.Run("writeClientEvent sends JSON to client", func(t *testing.T) {
		msg := json.RawMessage(`{"type":"test.event","key":"value"}`)
		ok := h.writeClientEvent(conn, &mu, msg, cancel)
		require.True(t, ok)

		el.waitForReceived(1)
		received := el.receivedEvents()
		require.GreaterOrEqual(t, len(received), 1)

		var parsed map[string]interface{}
		err := json.Unmarshal(received[0], &parsed)
		require.NoError(t, err)
		assert.Equal(t, "test.event", parsed["type"])
		assert.Equal(t, "value", parsed["key"])
	})

	t.Run("writeClientControlEvent sends type+fields", func(t *testing.T) {
		ok := h.writeClientControlEvent(conn, &mu, "test.control", map[string]string{"reason": "test"}, cancel)
		require.True(t, ok)

		el.waitForReceived(2)
		received := el.receivedEvents()
		require.GreaterOrEqual(t, len(received), 2)

		var parsed map[string]interface{}
		err := json.Unmarshal(received[1], &parsed)
		require.NoError(t, err)
		assert.Equal(t, "test.control", parsed["type"])
		assert.Equal(t, "test", parsed["reason"])
	})
}

// ---------- EL wire protocol integration ----------

func TestVoiceIntegration_MockELServerEventExchange(t *testing.T) {
	el := newMockELServer(t)
	defer el.close()

	conn, _, err := websocket.DefaultDialer.Dial(el.url, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Client sends init to server
	initEvent := map[string]interface{}{
		"type": "conversation_initiation_client_data",
		"dynamic_variables": map[string]interface{}{
			"user_name": "test_user",
			"currency":  "₦",
		},
	}
	err = conn.WriteJSON(initEvent)
	require.NoError(t, err)

	el.waitForReceived(1)
	received := el.receivedEvents()
	require.Len(t, received, 1)

	var parsedInit map[string]interface{}
	err = json.Unmarshal(received[0], &parsedInit)
	require.NoError(t, err)
	assert.Equal(t, "conversation_initiation_client_data", parsedInit["type"])
	dynVars := parsedInit["dynamic_variables"].(map[string]interface{})
	assert.Equal(t, "test_user", dynVars["user_name"])
	assert.Equal(t, "₦", dynVars["currency"])

	// Server sends conversation_initiation_metadata to client
	el.send(map[string]interface{}{
		"type": "conversation_initiation_metadata",
		"conversation_initiation_metadata_event": map[string]interface{}{
			"conversation_id": "el-conv-abc-123",
		},
	})

	var serverEvent map[string]interface{}
	err = conn.ReadJSON(&serverEvent)
	require.NoError(t, err)
	assert.Equal(t, "conversation_initiation_metadata", serverEvent["type"])
	meta := serverEvent["conversation_initiation_metadata_event"].(map[string]interface{})
	assert.Equal(t, "el-conv-abc-123", meta["conversation_id"])

	// Server sends user_transcript to client
	el.send(map[string]interface{}{
		"type": "user_transcript",
		"user_transcription_event": map[string]interface{}{
			"user_transcript": "What is my balance?",
		},
	})

	err = conn.ReadJSON(&serverEvent)
	require.NoError(t, err)
	assert.Equal(t, "user_transcript", serverEvent["type"])
	ut := serverEvent["user_transcription_event"].(map[string]interface{})
	assert.Equal(t, "What is my balance?", ut["user_transcript"])

	// Server sends audio event to client
	el.send(map[string]interface{}{
		"type": "audio",
		"audio_event": map[string]interface{}{
			"audio_base_64": "dGVzdCBhdWRpbw==",
			"event_id":      42,
		},
	})

	err = conn.ReadJSON(&serverEvent)
	require.NoError(t, err)
	assert.Equal(t, "audio", serverEvent["type"])
	ae := serverEvent["audio_event"].(map[string]interface{})
	assert.Equal(t, "dGVzdCBhdWRpbw==", ae["audio_base_64"])
	assert.Equal(t, float64(42), ae["event_id"])
}

// ---------- ElevenLabs client event constructors ----------

func TestVoiceIntegration_ELEventConstructors(t *testing.T) {
	t.Run("NewELConversationInit", func(t *testing.T) {
		init := infraai.NewELConversationInit(map[string]interface{}{"user_name": "test"}, nil)
		assert.Equal(t, "conversation_initiation_client_data", init.Type)
		assert.Equal(t, "test", init.DynamicVariables["user_name"])
	})

	t.Run("NewELAudioChunk", func(t *testing.T) {
		chunk := infraai.NewELAudioChunk("dGVzdA==")
		assert.Equal(t, "dGVzdA==", chunk.UserAudioChunk)
	})

	t.Run("NewELToolResult success", func(t *testing.T) {
		result := infraai.NewELToolResult("call-123", `{"ok":true}`, false)
		assert.Equal(t, "client_tool_result", result.Type)
		assert.Equal(t, "call-123", result.ToolCallID)
		assert.Equal(t, `{"ok":true}`, result.Result)
		assert.False(t, result.IsError)
	})

	t.Run("NewELToolResult error", func(t *testing.T) {
		result := infraai.NewELToolResult("call-456", `{"error":"fail"}`, true)
		assert.True(t, result.IsError)
	})

	t.Run("NewELContextualUpdate", func(t *testing.T) {
		update := infraai.NewELContextualUpdate("User is active")
		assert.Equal(t, "contextual_update", update.Type)
		assert.Equal(t, "User is active", update.Text)
	})

	t.Run("NewELUserActivity", func(t *testing.T) {
		activity := infraai.NewELUserActivity()
		assert.Equal(t, "user_activity", activity.Type)
	})

	t.Run("NewELPong", func(t *testing.T) {
		pong := infraai.NewELPong(42)
		assert.Equal(t, "pong", pong.Type)
		assert.Equal(t, int64(42), pong.EventID)
	})
}

// ---------- Voice session authentication ----------

func TestVoiceIntegration_AuthenticateSession(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"
	logger, _ := zap.NewDevelopment()

	h := &VoiceHandler{
		tokenSecret: secret,
		logger:      logger,
	}

	token, expiresAt, err := auth.GenerateVoiceSessionToken(userID, secret, 60*time.Second)
	require.NoError(t, err)
	require.False(t, expiresAt.IsZero())

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/voice/session?voice_session_token="+token, nil)

	got, err := h.authenticateVoiceSession(c)
	require.NoError(t, err)
	assert.Equal(t, userID, got)
}

func TestVoiceIntegration_AuthenticateSession_FallbackTicketParam(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"
	logger, _ := zap.NewDevelopment()

	h := &VoiceHandler{
		tokenSecret: secret,
		logger:      logger,
	}

	token, _, err := auth.GenerateVoiceSessionToken(userID, secret, 60*time.Second)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/voice/session?ticket="+token, nil)

	got, err := h.authenticateVoiceSession(c)
	require.NoError(t, err)
	assert.Equal(t, userID, got)
}

func TestVoiceIntegration_AuthenticateSession_MissingToken(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	h := &VoiceHandler{
		tokenSecret: "test-secret",
		logger:      logger,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/voice/session", nil)

	_, err := h.authenticateVoiceSession(c)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "voice session token required")
}

func TestVoiceIntegration_AuthenticateSession_InvalidToken(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	h := &VoiceHandler{
		tokenSecret: "test-secret",
		logger:      logger,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/voice/session?voice_session_token=invalid-token", nil)

	_, err := h.authenticateVoiceSession(c)
	assert.Error(t, err)
}

// ---------- Usage tracking ----------

func TestVoiceIntegration_TrackUsage(t *testing.T) {
	t.Run("calls TrackVoice with correct seconds", func(t *testing.T) {
		var trackedUserID uuid.UUID
		var trackedSeconds int
		usage := &mockVoiceUsageTracker{
			trackFn: func(ctx context.Context, userID uuid.UUID, seconds int) error {
				trackedUserID = userID
				trackedSeconds = seconds
				return nil
			},
		}

		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			usage:  usage,
			logger: logger,
		}

		uid := uuid.New()
		startTime := time.Now().Add(-30 * time.Second)
		h.trackUsage(context.Background(), uid, startTime)
		assert.Equal(t, uid, trackedUserID)
		assert.GreaterOrEqual(t, trackedSeconds, 29)
	})

	t.Run("skips tracking when seconds is zero", func(t *testing.T) {
		called := false
		usage := &mockVoiceUsageTracker{
			trackFn: func(ctx context.Context, userID uuid.UUID, seconds int) error {
				called = true
				return nil
			},
		}

		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			usage:  usage,
			logger: logger,
		}

		h.trackUsage(context.Background(), uuid.New(), time.Now())
		assert.False(t, called)
	})

	t.Run("handles nil usage tracker", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			logger: logger,
		}
		// Should not panic
		h.trackUsage(context.Background(), uuid.New(), time.Now().Add(-10*time.Second))
	})
}

// ---------- Transcript persistence ----------

func TestVoiceIntegration_PersistTranscripts(t *testing.T) {
	t.Run("pairs user and assistant transcripts into exchanges", func(t *testing.T) {
		var exchanges []struct {
			userMsg      string
			assistantMsg string
		}
		var mu sync.Mutex

		convID := uuid.New()
		userID := uuid.New()

		svc := &mockConvService{
			recordFn: func(ctx context.Context, cID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
				mu.Lock()
				exchanges = append(exchanges, struct {
					userMsg      string
					assistantMsg string
				}{userMsg, assistantMsg})
				mu.Unlock()
				return nil
			},
			updateFn: func(ctx context.Context, cID uuid.UUID, title string) error {
				assert.Contains(t, title, "What")
				return nil
			},
		}

		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			convService: svc,
			logger:      logger,
		}

		var transcriptMu sync.Mutex
		transcripts := []transcriptEntry{
			{role: "user", text: "What is my balance?"},
			{role: "assistant", text: "Your balance is ₦50,000."},
			{role: "user", text: "Transfer ₦10,000 to savings."},
			{role: "assistant", text: "Done! I moved ₦10,000 to your savings goal."},
		}

		h.persistVoiceTranscripts(userID, convID, &transcriptMu, transcripts)

		// Wait for goroutine to complete
		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		require.Len(t, exchanges, 2)
		assert.Equal(t, "What is my balance?", exchanges[0].userMsg)
		assert.Equal(t, "Your balance is ₦50,000.", exchanges[0].assistantMsg)
		assert.Equal(t, "Transfer ₦10,000 to savings.", exchanges[1].userMsg)
		assert.Equal(t, "Done! I moved ₦10,000 to your savings goal.", exchanges[1].assistantMsg)
		mu.Unlock()
	})

	t.Run("trailing user message recorded as unpaired exchange", func(t *testing.T) {
		var exchanges []struct {
			userMsg      string
			assistantMsg string
		}
		var mu sync.Mutex

		convID := uuid.New()
		userID := uuid.New()

		svc := &mockConvService{
			recordFn: func(ctx context.Context, cID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
				mu.Lock()
				exchanges = append(exchanges, struct {
					userMsg      string
					assistantMsg string
				}{userMsg, assistantMsg})
				mu.Unlock()
				return nil
			},
			updateFn: func(ctx context.Context, cID uuid.UUID, title string) error {
				return nil
			},
		}

		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			convService: svc,
			logger:      logger,
		}

		var transcriptMu sync.Mutex
		transcripts := []transcriptEntry{
			{role: "user", text: "Hello"},
			{role: "assistant", text: "Hi there!"},
			{role: "user", text: "Trailing message"},
		}

		h.persistVoiceTranscripts(userID, convID, &transcriptMu, transcripts)

		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		require.Len(t, exchanges, 2)
		assert.Equal(t, "Hello", exchanges[0].userMsg)
		assert.Equal(t, "Trailing message", exchanges[1].userMsg)
		assert.Equal(t, "", exchanges[1].assistantMsg)
		mu.Unlock()
	})

	t.Run("empty transcripts deletes conversation", func(t *testing.T) {
		var deleted bool
		var delMu sync.Mutex
		convID := uuid.New()
		userID := uuid.New()

		svc := &mockConvService{
			deleteFn: func(ctx context.Context, uID, cID uuid.UUID) error {
				delMu.Lock()
				deleted = true
				delMu.Unlock()
				assert.Equal(t, userID, uID)
				assert.Equal(t, convID, cID)
				return nil
			},
		}

		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			convService: svc,
			logger:      logger,
		}

		var mu sync.Mutex
		h.persistVoiceTranscripts(userID, convID, &mu, nil)

		time.Sleep(200 * time.Millisecond)
		delMu.Lock()
		assert.True(t, deleted)
		delMu.Unlock()
	})

	t.Run("skips persistence when convService is nil", func(t *testing.T) {
		logger, _ := zap.NewDevelopment()
		h := &VoiceHandler{
			logger: logger,
		}

		var mu sync.Mutex
		// Should not panic
		h.persistVoiceTranscripts(uuid.New(), uuid.New(), &mu, []transcriptEntry{
			{role: "user", text: "test"},
		})
	})
}

// ---------- Transcript accumulator edge cases ----------

func TestVoiceIntegration_TranscriptPersistence_ConsecutiveUserMessages(t *testing.T) {
	var recordCallCount int32
	convID := uuid.New()
	userID := uuid.New()

	svc := &mockConvService{
		recordFn: func(ctx context.Context, cID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error {
			atomic.AddInt32(&recordCallCount, 1)
			return nil
		},
		updateFn: func(ctx context.Context, cID uuid.UUID, title string) error {
			return nil
		},
	}

	logger, _ := zap.NewDevelopment()
	h := &VoiceHandler{
		convService: svc,
		logger:      logger,
	}

	var mu sync.Mutex
	// Two consecutive user messages before an assistant reply
	transcripts := []transcriptEntry{
		{role: "user", text: "First message"},
		{role: "user", text: "Second message"},
		{role: "assistant", text: "Combined reply"},
	}

	h.persistVoiceTranscripts(userID, convID, &mu, transcripts)

	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&recordCallCount))
}

// ---------- WebSocket upgrade checks ----------

func TestVoiceIntegration_WsUpgraderConfig(t *testing.T) {
	assert.Equal(t, 16384, wsUpgrader.ReadBufferSize)
	assert.Equal(t, 16384, wsUpgrader.WriteBufferSize)
}

// ---------- IssueSessionToken endpoint ----------

func TestVoiceIntegration_IssueSessionToken(t *testing.T) {
	userID := uuid.New()
	secret := "test-secret"
	logger, _ := zap.NewDevelopment()

	h := &VoiceHandler{
		tokenSecret: secret,
		logger:      logger,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", userID)
	c.Request = httptest.NewRequest("GET", "/voice/token", nil)

	h.IssueSessionToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
	assert.NotEmpty(t, resp.ExpiresAt)
}

func TestVoiceIntegration_IssueSessionToken_Unauthorized(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	h := &VoiceHandler{
		tokenSecret: "test-secret",
		logger:      logger,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// No userID in context
	c.Request = httptest.NewRequest("GET", "/voice/token", nil)

	h.IssueSessionToken(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
