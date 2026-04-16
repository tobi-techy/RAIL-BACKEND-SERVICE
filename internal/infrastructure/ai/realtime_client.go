package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const realtimeURL = "wss://api.openai.com/v1/realtime?model=%s"

// RealtimeEvent is a message sent to/from the OpenAI Realtime API.
type RealtimeEvent struct {
	Type string `json:"type"`
	// Fields vary by event type — we keep the raw JSON and decode as needed.
	raw json.RawMessage
}

func (e *RealtimeEvent) UnmarshalJSON(data []byte) error {
	var obj struct{ Type string `json:"type"` }
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	e.Type = obj.Type
	e.raw = data
	return nil
}

func (e RealtimeEvent) Raw() json.RawMessage { return e.raw }

// RealtimeClient manages a WebSocket connection to the OpenAI Realtime API.
type RealtimeClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	logger *zap.Logger
	closed bool
}

// DialRealtime opens a WebSocket to the OpenAI Realtime API.
func DialRealtime(apiKey, model string, logger *zap.Logger) (*RealtimeClient, error) {
	url := fmt.Sprintf(realtimeURL, model)
	header := http.Header{
		"Authorization": []string{"Bearer " + apiKey},
		"OpenAI-Beta":   []string{"realtime=v1"},
	}

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("realtime dial failed (status %d): %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("realtime dial failed: %w", err)
	}

	return &RealtimeClient{conn: conn, logger: logger}, nil
}

// Send sends a JSON event to the Realtime API.
func (c *RealtimeClient) Send(event interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteJSON(event)
}

// ReadEvent reads the next event from the Realtime API. Blocks until a message arrives.
func (c *RealtimeClient) ReadEvent() (json.RawMessage, error) {
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return json.RawMessage(msg), nil
}

// Close closes the WebSocket connection.
func (c *RealtimeClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.conn.Close()
	}
}

// --- Outbound event helpers (client → OpenAI) ---

// SessionUpdate configures the session (system prompt, tools, voice, etc.)
type SessionUpdate struct {
	Type    string        `json:"type"`
	Session SessionConfig `json:"session"`
}

type SessionConfig struct {
	Modalities       []string          `json:"modalities"`
	Instructions     string            `json:"instructions"`
	Voice            string            `json:"voice"`
	InputAudioFormat string            `json:"input_audio_format"`
	OutputAudioFormat string           `json:"output_audio_format"`
	Tools            []SessionTool     `json:"tools,omitempty"`
	TurnDetection    *TurnDetection    `json:"turn_detection,omitempty"`
	Temperature      float64           `json:"temperature,omitempty"`
	MaxResponseOutputTokens interface{} `json:"max_response_output_tokens,omitempty"`
}

type SessionTool struct {
	Type        string                 `json:"type"` // "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type TurnDetection struct {
	Type            string  `json:"type"` // "server_vad"
	Threshold       float64 `json:"threshold,omitempty"`
	PrefixPaddingMs int     `json:"prefix_padding_ms,omitempty"`
	SilenceDurationMs int   `json:"silence_duration_ms,omitempty"`
}

// InputAudioBufferAppend sends audio data to the Realtime API.
type InputAudioBufferAppend struct {
	Type  string `json:"type"`
	Audio string `json:"audio"` // base64-encoded PCM16 audio
}

// ConversationItemCreate sends a tool result back to the Realtime API.
type ConversationItemCreate struct {
	Type string           `json:"type"`
	Item ConversationItem `json:"item"`
}

type ConversationItem struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"` // JSON string of tool result
}

// ResponseCreate triggers the model to generate a response.
type ResponseCreate struct {
	Type string `json:"type"`
}

// NewSessionUpdate creates a session.update event with Miriam's config.
func NewSessionUpdate(instructions string, tools []SessionTool) SessionUpdate {
	return SessionUpdate{
		Type: "session.update",
		Session: SessionConfig{
			Modalities:        []string{"text", "audio"},
			Instructions:      instructions,
			Voice:             "nova",
			InputAudioFormat:  "pcm16",
			OutputAudioFormat: "pcm16",
			Tools:             tools,
			TurnDetection: &TurnDetection{
				Type:              "server_vad",
				Threshold:         0.5,
				PrefixPaddingMs:   300,
				SilenceDurationMs: 500,
			},
			Temperature:             0.7,
			MaxResponseOutputTokens: "inf",
		},
	}
}

func NewAudioAppend(base64Audio string) InputAudioBufferAppend {
	return InputAudioBufferAppend{Type: "input_audio_buffer.append", Audio: base64Audio}
}

func NewToolResult(callID, resultJSON string) ConversationItemCreate {
	return ConversationItemCreate{
		Type: "conversation.item.create",
		Item: ConversationItem{
			Type:   "function_call_output",
			CallID: callID,
			Output: resultJSON,
		},
	}
}

func NewResponseCreate() ResponseCreate {
	return ResponseCreate{Type: "response.create"}
}
