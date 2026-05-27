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

const elevenLabsEndpoint = "wss://api.elevenlabs.io/v1/convai/conversation"

// ElevenLabsClient manages a WebSocket connection to the ElevenLabs Conversational AI API.
type ElevenLabsClient struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	logger *zap.Logger
	closed bool
}

// DialElevenLabs opens a WebSocket to the ElevenLabs Conversational AI API.
// Uses a signed URL for private agents (recommended) or direct agent_id for public agents.
func DialElevenLabs(apiKey, agentID string, logger *zap.Logger) (*ElevenLabsClient, error) {
	// Get a signed URL so the API key never leaves the server
	signedURL, err := fetchElevenLabsSignedURL(apiKey, agentID)
	if err != nil {
		// Fall back to direct agent_id connection (public agents)
		logger.Warn("elevenlabs signed URL fetch failed, falling back to direct connection", zap.Error(err))
		signedURL = elevenLabsEndpoint + "?agent_id=" + agentID
	}

	conn, resp, err := websocket.DefaultDialer.Dial(signedURL, nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("elevenlabs voice agent connection failed (status %d): %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("elevenlabs voice agent connection failed: %w", err)
	}

	return &ElevenLabsClient{conn: conn, logger: logger}, nil
}

// fetchElevenLabsSignedURL obtains a signed WebSocket URL from ElevenLabs.
func fetchElevenLabsSignedURL(apiKey, agentID string) (string, error) {
	req, err := http.NewRequest("GET", "https://api.elevenlabs.io/v1/convai/conversation/get-signed-url?agent_id="+agentID, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("xi-api-key", apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("signed URL request failed with status %d", resp.StatusCode)
	}

	var result struct {
		SignedURL string `json:"signed_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.SignedURL == "" {
		return "", fmt.Errorf("empty signed URL in response")
	}
	return result.SignedURL, nil
}

// Send sends a JSON event to the ElevenLabs Conversational AI API.
func (c *ElevenLabsClient) Send(event interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteJSON(event)
}

// ReadEvent reads the next event from the ElevenLabs API. Blocks until a message arrives.
func (c *ElevenLabsClient) ReadEvent() (json.RawMessage, error) {
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return json.RawMessage(msg), nil
}

// Ping sends a WebSocket ping frame to keep the connection alive.
func (c *ElevenLabsClient) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
}

// Close closes the WebSocket connection.
func (c *ElevenLabsClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.conn.Close()
	}
}

// --- Outbound event types (client → ElevenLabs) ---

// ELConversationInit is sent on connection open to initialize the conversation.
type ELConversationInit struct {
	Type                        string                 `json:"type"`
	ConversationConfigOverride  *ELConversationConfig  `json:"conversation_config_override,omitempty"`
	CustomLLMExtraBody          map[string]interface{} `json:"custom_llm_extra_body,omitempty"`
	DynamicVariables            map[string]interface{} `json:"dynamic_variables,omitempty"`
}

// ELConversationConfig overrides agent configuration for this session.
type ELConversationConfig struct {
	Agent *ELAgentConfig `json:"agent,omitempty"`
	TTS   *ELTTSConfig   `json:"tts,omitempty"`
}

// ELAgentConfig overrides the agent's prompt and first message.
type ELAgentConfig struct {
	Prompt       *ELPromptConfig `json:"prompt,omitempty"`
	FirstMessage string          `json:"first_message,omitempty"`
}

// ELPromptConfig overrides the system prompt.
type ELPromptConfig struct {
	Prompt string `json:"prompt"`
}

// ELTTSConfig overrides the voice for this session.
type ELTTSConfig struct {
	VoiceID string `json:"voice_id,omitempty"`
}

// ELAudioChunk sends a base64-encoded PCM16 audio chunk to the agent.
type ELAudioChunk struct {
	UserAudioChunk string `json:"user_audio_chunk"` // base64-encoded PCM16
}

// ELClientToolResult sends a tool execution result back to the agent.
type ELClientToolResult struct {
	Type       string `json:"type"`
	ToolCallID string `json:"tool_call_id"`
	Result     string `json:"result"`
	IsError    bool   `json:"is_error"`
}

// ELContextualUpdate sends non-interrupting context to the agent.
type ELContextualUpdate struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ELPong responds to a server ping.
type ELPong struct {
	Type    string `json:"type"`
	EventID int64  `json:"event_id"`
}

// NewELConversationInit creates the initialization event.
// Only dynamic variables are sent — the system prompt and first message
// are configured in the ElevenLabs agent dashboard and must not be overridden
// here, as that would conflict with the agent's {{variable}} substitution.
func NewELConversationInit(dynamicVars map[string]interface{}) ELConversationInit {
	return ELConversationInit{
		Type:             "conversation_initiation_client_data",
		DynamicVariables: dynamicVars,
	}
}

// NewELAudioChunk creates an audio input event.
func NewELAudioChunk(base64Audio string) ELAudioChunk {
	return ELAudioChunk{UserAudioChunk: base64Audio}
}

// NewELToolResult creates a client_tool_result event.
func NewELToolResult(toolCallID, resultJSON string, isError bool) ELClientToolResult {
	return ELClientToolResult{
		Type:       "client_tool_result",
		ToolCallID: toolCallID,
		Result:     resultJSON,
		IsError:    isError,
	}
}

// NewELContextualUpdate creates a contextual_update event.
func NewELContextualUpdate(text string) ELContextualUpdate {
	return ELContextualUpdate{Type: "contextual_update", Text: text}
}

// NewELPong creates a pong response for a server ping.
func NewELPong(eventID int64) ELPong {
	return ELPong{Type: "pong", EventID: eventID}
}
