package ai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const assemblyAIEndpoint = "wss://agents.assemblyai.com/v1/ws"

// RealtimeClient manages a WebSocket connection to the AssemblyAI Voice Agent API.
type RealtimeClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	logger *zap.Logger
	closed bool
}

// DialRealtime opens a WebSocket to the AssemblyAI Voice Agent API.
func DialRealtime(apiKey string, logger *zap.Logger) (*RealtimeClient, error) {
	header := http.Header{
		"Authorization": []string{"Bearer " + apiKey},
	}

	conn, resp, err := websocket.DefaultDialer.Dial(assemblyAIEndpoint, header)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("voice agent connection failed (status %d): %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("voice agent connection failed: %w", err)
	}

	return &RealtimeClient{conn: conn, logger: logger}, nil
}

// Send sends a JSON event to the Voice Agent API.
func (c *RealtimeClient) Send(event interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteJSON(event)
}

// ReadEvent reads the next event from the Voice Agent API. Blocks until a message arrives.
func (c *RealtimeClient) ReadEvent() (json.RawMessage, error) {
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return json.RawMessage(msg), nil
}

// Ping sends a WebSocket ping frame to keep the connection alive.
func (c *RealtimeClient) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
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

// --- Outbound event types (client → AssemblyAI) ---

// SessionUpdate configures the voice agent session.
type SessionUpdate struct {
	Type    string        `json:"type"`
	Session SessionConfig `json:"session"`
}

type SessionConfig struct {
	SystemPrompt string        `json:"system_prompt,omitempty"`
	Greeting     string        `json:"greeting,omitempty"`
	Tools        []SessionTool `json:"tools,omitempty"`
	Input        *InputConfig  `json:"input,omitempty"`
	Output       *OutputConfig `json:"output,omitempty"`
}

type InputConfig struct {
	Format        *AudioFormat   `json:"format,omitempty"`
	Keyterms      []string       `json:"keyterms,omitempty"`
	TurnDetection *TurnDetection `json:"turn_detection,omitempty"`
}

type OutputConfig struct {
	Voice  string       `json:"voice,omitempty"`
	Format *AudioFormat `json:"format,omitempty"`
}

type AudioFormat struct {
	Encoding string `json:"encoding"` // "audio/pcm"
}

type SessionTool struct {
	Type        string                 `json:"type"` // "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type TurnDetection struct {
	VadThreshold      float64 `json:"vad_threshold,omitempty"`
	MinSilence        int     `json:"min_silence,omitempty"`
	MaxSilence        int     `json:"max_silence,omitempty"`
	InterruptResponse *bool   `json:"interrupt_response,omitempty"`
}

// InputAudio sends audio data to the Voice Agent API.
type InputAudio struct {
	Type  string `json:"type"`
	Audio string `json:"audio"` // base64-encoded PCM16 24kHz mono
}

// ToolResult sends a tool execution result back to the Voice Agent API.
type ToolResult struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Result string `json:"result"` // JSON string
}

// NewSessionUpdate creates a session.update event for Miriam.
func NewSessionUpdate(instructions string, voice string, greeting string, tools []SessionTool) SessionUpdate {
	return SessionUpdate{
		Type: "session.update",
		Session: SessionConfig{
			SystemPrompt: instructions,
			Greeting:     greeting,
			Tools:        tools,
			Input: &InputConfig{
				Format: &AudioFormat{Encoding: "audio/pcm"},
				Keyterms: []string{
					"Rail", "stash", "Miriam", "USDC", "naira",
					"spend wallet", "stash wallet", "automation",
					"obligation", "round-up", "yield", "deposit",
					"Rail tag", "operating plan",
				},
				TurnDetection: &TurnDetection{
					VadThreshold: 0.5,
					MinSilence:   800,
					MaxSilence:   2500,
				},
			},
			Output: &OutputConfig{
				Voice:  voice,
				Format: &AudioFormat{Encoding: "audio/pcm"},
			},
		},
	}
}

func NewAudioInput(base64Audio string) InputAudio {
	return InputAudio{Type: "input.audio", Audio: base64Audio}
}

func NewToolResult(callID, resultJSON string) ToolResult {
	return ToolResult{
		Type:   "tool.result",
		CallID: callID,
		Result: resultJSON,
	}
}
