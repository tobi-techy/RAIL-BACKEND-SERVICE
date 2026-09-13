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

// PythonChatPoll mirrors an iMessage poll rendered by the Python agent during
// conversational onboarding. Go surfaces it as a platform poll; votes come back
// as messages with IsPollVote set and are forwarded by message text.
type PythonChatPoll struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

// PythonOnboardingStatus mirrors the agent's onboarding marker so Go can log
// and gate without parsing copy.
type PythonOnboardingStatus struct {
	Stage     string `json:"stage"`
	Completed bool   `json:"completed"`
}

// PythonChatResponse mirrors the Python agent's /api/v1/chat response payload.
type PythonChatResponse struct {
	Response             string                  `json:"response"`
	ConversationID       string                  `json:"conversation_id"`
	RequiresConfirmation bool                    `json:"requires_confirmation"`
	Cards                []PythonChatCard        `json:"cards"`
	Poll                 *PythonChatPoll         `json:"poll,omitempty"`
	Onboarding           *PythonOnboardingStatus `json:"onboarding,omitempty"`
}

// PythonApprovedAction is one card replayed back to Python for execution after
// the user's email OTP step-up succeeds. The Python agent executes the mutation
// through its Go REST client; Go never holds the money path.
type PythonApprovedAction struct {
	Tool      string                 `json:"tool"`
	Arguments map[string]interface{} `json:"arguments"`
}

// PythonChatDocument is a linked statement scan forwarded from Go's durable
// statement-attachment pipeline (name, mime, and the sync scan summary).
type PythonChatDocument struct {
	Name    string `json:"name,omitempty"`
	MIME    string `json:"mime,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// pythonChatRequest is the request body sent to the Python agent.
type pythonChatRequest struct {
	Message         string                `json:"message"`
	ConversationID  string                `json:"conversation_id"`
	ApprovedActions []PythonApprovedAction `json:"approved_actions,omitempty"`
	IsPollVote      bool                  `json:"is_poll_vote,omitempty"`
	PollTitle       string                `json:"poll_title,omitempty"`
	Document        *PythonChatDocument   `json:"document,omitempty"`
}

// PythonProactiveOutcome mirrors the Python agent's /api/v1/proactive/analyze
// response: the analyst's decision about whether there is one genuinely useful
// thing worth telling the user right now, plus the drafted message. Go owns
// quiet hours, the daily cap, and the actual iMessage delivery.
type PythonProactiveOutcome struct {
	ShouldReachOut bool   `json:"should_reach_out"`
	Priority       string `json:"priority"`
	Category       string `json:"category"`
	Message        string `json:"message"`
	Reason         string `json:"reason"`
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
	return c.doChat(ctx, userID, email, role, pythonChatRequest{
		Message:         message,
		ConversationID:  conversationID,
		ApprovedActions: approved,
	})
}

// ChatPollVote forwards a platform poll vote (is_poll_vote + the poll title
// that rendered it) so Python can match the option text while being tolerant of
// bridge truncation of the options.
func (c *PythonAgentClient) ChatPollVote(ctx context.Context, userID uuid.UUID, email, role, conversationID, message, pollTitle string) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
		IsPollVote:     true,
		PollTitle:      pollTitle,
	})
}

// ChatWithDocument forwards a linked statement's scan summary so the Python
// brain can ground the financial plan in it during onboarding.
func (c *PythonAgentClient) ChatWithDocument(ctx context.Context, userID uuid.UUID, email, role, conversationID, message string, doc PythonChatDocument) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, role, pythonChatRequest{
		Message:         message,
		ConversationID:  conversationID,
		Document:        &doc,
	})
}

// doChat mints the per-user JWT and posts one /api/v1/chat payload.
func (c *PythonAgentClient) doChat(ctx context.Context, userID uuid.UUID, email, role string, body pythonChatRequest) (*PythonChatResponse, error) {
	// Mint a short-lived per-user "agent" token (not a normal access token):
	// Python decodes it to recover user_id/email/role for its own RBAC, then
	// reuses this SAME token to call Go's own REST API on the user's behalf
	// (get_balance, send_money, etc). Go's Authentication middleware
	// recognizes the "agent" token type and skips the interactive-session
	// lookup for it, since this token is never the product of a real login.
	token, _, err := auth.GenerateAgentToken(userID, email, role, c.jwtSecret, int(c.jwtTTL.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("mint python agent jwt: %w", err)
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

// AnalyzeProactive asks the Python agent whether there is something worth
// telling the user right now (the proactive reacher worker's per-user tick). It
// uses the same short-lived JWT as Chat so Python resolves the user's plan and
// memory server-side. Fail-open behaviour lives in Python: no data, a disabled
// feature, or an unreachable model all resolve to "stay quiet".
func (c *PythonAgentClient) AnalyzeProactive(ctx context.Context, userID uuid.UUID, email, role string) (*PythonProactiveOutcome, error) {
	token, _, err := auth.GenerateAgentToken(userID, email, role, c.jwtSecret, int(c.jwtTTL.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("mint python agent jwt: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/proactive/analyze", nil)
	if err != nil {
		return nil, fmt.Errorf("create python proactive request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("python agent proactive request: %w", err)
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
		return nil, fmt.Errorf("python agent proactive: unexpected status %d", resp.StatusCode)
	}

	var out PythonProactiveOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode python agent proactive response: %w", err)
	}
	return &out, nil
}