package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/pkg/auth"
	"go.uber.org/zap"
)

// PythonChatCard mirrors the Python agent's action_confirmation card. Go never
// executes these mutations itself — it stages an email OTP, then replays the
// card back to Python as an approved_action once the code is verified.
type PythonChatCard struct {
	Type      string                 `json:"type"`
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
	Summary   string                 `json:"summary"`
}

// PythonChatResponse mirrors the Python agent's /api/v1/chat response payload.
type PythonChatResponse struct {
	Response             string           `json:"response"`
	ConversationID       string           `json:"conversation_id"`
	RequiresConfirmation bool             `json:"requires_confirmation"`
	Cards                []PythonChatCard `json:"cards"`
}

// PythonApprovedAction is one card replayed back to Python for execution after
// the user's email OTP step-up succeeds. The Python agent executes the mutation
// through its Go REST client; Go never holds the money path.
type PythonApprovedAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

// pythonChatRequest is the request body sent to the Python agent.
type pythonChatRequest struct {
	Message         string                `json:"message"`
	ConversationID  string                `json:"conversation_id"`
	ApprovedActions []PythonApprovedAction `json:"approved_actions,omitempty"`
}

// PythonAgentClient talks to the Python MIRIAM agent (the LLM brain for
// messaging channels). Go mints a short-lived per-user JWT (same JWT_SECRET),
// forwards the user's message, and maps the agent's response back into Go types.
// All money logic stays in Go: Python executes mutations through Go's REST API.
type PythonAgentClient struct {
	baseURL    string
	jwtSecret  string
	jwtTTL     time.Duration
	httpClient *http.Client
	logger     *zap.Logger
}

// PythonAgentClientConfig holds the client's wiring.
type PythonAgentClientConfig struct {
	BaseURL   string
	JWTSecret string
	JWTTTL    time.Duration
	Timeout   time.Duration
}

// NewPythonAgentClient builds a client for the Python agent.
func NewPythonAgentClient(cfg PythonAgentClientConfig, logger *zap.Logger) *PythonAgentClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &PythonAgentClient{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		jwtSecret:  cfg.JWTSecret,
		jwtTTL:     cfg.JWTTTL,
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger,
	}
}

// Chat sends a message to the Python agent on behalf of the given user. `email`
// and `role` are embedded in the minted JWT so the agent can apply RBAC; role
// should be "verified" for KYC-approved users (money tools) or "user" otherwise.
// `conversationID` must be namespaced per platform+thread so agent memory never
// collides across channels (e.g. "platform:imessage:thread-abc").
func (c *PythonAgentClient) Chat(ctx context.Context, userID uuid.UUID, email, role, conversationID, message string) (*PythonChatResponse, error) {
	return c.ChatWithApprovedActions(ctx, userID, email, role, conversationID, message, nil)
}

// ChatWithApprovedActions is Chat plus the confirmation replay: after the user
// proves their email OTP, Go sends the staged cards back as approved_actions so
// the agent executes them (via Go's REST) and returns the final response.
func (c *PythonAgentClient) ChatWithApprovedActions(ctx context.Context, userID uuid.UUID, email, role, conversationID, message string, approved []PythonApprovedAction) (*PythonChatResponse, error) {
	// Mint a short-lived per-user token. Python validates it with the same
	// JWT_SECRET and derives the user's id, email, and role from the claims.
	token, _, err := auth.GenerateAccessToken(userID, email, role, c.jwtSecret, int(c.jwtTTL.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("mint python agent jwt: %w", err)
	}

	body := pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
	}
	if len(approved) > 0 {
		body.ApprovedActions = approved
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal python chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create python chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python agent chat request: %w", err)
	}
	defer func() {
		if _, drainErr := io.Copy(io.Discard, resp.Body); drainErr != nil {
			c.logger.Warn("failed to drain python agent response body", zap.Error(drainErr))
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.logger.Warn("failed to close python agent response body", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.logger.Warn("python agent returned non-200",
			zap.Int("status", resp.StatusCode),
			zap.String("body", string(bodyBytes)),
			zap.String("user_id", userID.String()),
		)
		return nil, fmt.Errorf("python agent chat: unexpected status %d", resp.StatusCode)
	}

	var out PythonChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode python agent chat response: %w", err)
	}
	return &out, nil
}