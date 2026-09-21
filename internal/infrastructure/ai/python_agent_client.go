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

// PythonChatPoll mirrors an iMessage poll rendered by the Python agent during
// conversational onboarding. Go surfaces it as a platform poll; votes come back
// as messages with IsPollVote set and are forwarded by message text.
type PythonChatPoll struct {
	Title   string   `json:"title"`
	Options []string `json:"options"`
}

// PythonChatShare is a rich link Miriam wants to hand over (e.g. a chart or the
// plan page). Go validates the URL against an allowlist before sending; the
// bridge renders it as a native rich-link preview where supported.
type PythonChatShare struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// PythonOnboardingStatus mirrors the agent's onboarding marker so Go can log
// and gate without parsing copy. Automated distinguishes the plan-consent
// outcome: consented to standing rules (false fires when the user declined and
// only the draft was saved), so Go only hands completed-onboarded guests into
// the account funnel when they actually said "set it up".
type PythonOnboardingStatus struct {
	Stage     string `json:"stage"`
	Completed bool   `json:"completed"`
	Automated bool   `json:"automated,omitempty"`
}

// PythonChatResponse mirrors the Python agent's /api/v1/chat response payload.
//
// There is no requires_confirmation and no cards. A money turn instead carries a
// confirm_id that Hands issued, and that id is the only thing that can settle
// the movement: Go renders it as a Confirm/Cancel poll and sends the id back
// with the user's answer. The earlier approval-card protocol — stage a card,
// email a 6-digit code, replay the card — is deleted, because it put the
// confirmation authority in Go's approval store rather than in the ledger.
type PythonChatResponse struct {
	Response       string                  `json:"response"`
	ConversationID string                  `json:"conversation_id"`
	ConfirmID      string                  `json:"confirm_id"`
	Decision       map[string]interface{}  `json:"decision"`
	Receipt        map[string]interface{}  `json:"receipt"`
	Poll           *PythonChatPoll         `json:"poll,omitempty"`
	Onboarding     *PythonOnboardingStatus `json:"onboarding,omitempty"`
	Name           string                  `json:"name,omitempty"`
	Messages       []string                `json:"messages,omitempty"`
	Reaction       string                  `json:"reaction,omitempty"`
	Share          *PythonChatShare        `json:"share,omitempty"`
}

// PythonChatDocument is a linked statement scan forwarded from Go's durable
// statement-attachment pipeline (name, mime, and the sync scan summary).
type PythonChatDocument struct {
	Name    string `json:"name,omitempty"`
	MIME    string `json:"mime,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// pythonChatRequest is the request body sent to the Python agent.
//
// A confirmation tap carries confirm_id plus yes; yes=false is a decline and
// closes the challenge without moving anything. Nothing else in the body can
// settle an action, and a bare "yes" sent as a message does not.
type pythonChatRequest struct {
	Message        string              `json:"message"`
	ConversationID string              `json:"conversation_id"`
	ConfirmID      string              `json:"confirm_id,omitempty"`
	Yes            *bool               `json:"yes,omitempty"`
	IsPollVote     bool                `json:"is_poll_vote,omitempty"`
	PollTitle      string              `json:"poll_title,omitempty"`
	Document       *PythonChatDocument `json:"document,omitempty"`
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
	return c.doChat(ctx, userID, email, "", role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
	})
}

// ChatAsGuest advances a pre-signup (guest) conversation on the Python brain.
// Guests carry a stable synthetic identity plus a unique per-sender username and
// email (Python's users table enforces uniqueness on both), so the agent can
// persist their conversation and interview state without ever colliding with a
// real user row.
func (c *PythonAgentClient) ChatAsGuest(ctx context.Context, userID uuid.UUID, username, email, role, conversationID, message string) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, username, role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
	})
}

// ChatAsGuestPollVote is the guest equivalent of ChatPollVote: the tapped option
// title plus the question it answered, so the onboarding interview can resolve a
// deliberate selection instead of reading a bare option fragment as a fresh
// topic (which made it re-ask the question with yet another poll).
func (c *PythonAgentClient) ChatAsGuestPollVote(ctx context.Context, userID uuid.UUID, username, email, role, conversationID, message, pollTitle string) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, username, role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
		IsPollVote:     true,
		PollTitle:      pollTitle,
	})
}

// ChatConfirm settles a challenge the user tapped, by the confirm_id Hands
// issued. `yes=false` declines it and closes the challenge without moving
// anything. This is the only way a movement is authorised: the id comes from the
// response that asked the question, and the ledger holds the challenge it names.
func (c *PythonAgentClient) ChatConfirm(ctx context.Context, userID uuid.UUID, email, role, conversationID, confirmID string, yes bool) (*PythonChatResponse, error) {
	answer := yes
	return c.doChat(ctx, userID, email, "", role, pythonChatRequest{
		ConversationID: conversationID,
		ConfirmID:      confirmID,
		Yes:            &answer,
	})
}

// ChatPollVote forwards a platform poll vote (is_poll_vote + the poll title
// that rendered it) so Python can match the option text while being tolerant of
// bridge truncation of the options.
func (c *PythonAgentClient) ChatPollVote(ctx context.Context, userID uuid.UUID, email, role, conversationID, message, pollTitle string) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, "", role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
		IsPollVote:     true,
		PollTitle:      pollTitle,
	})
}

// ChatWithDocument forwards a linked statement's scan summary so the Python
// brain can ground the financial plan in it during onboarding.
func (c *PythonAgentClient) ChatWithDocument(ctx context.Context, userID uuid.UUID, email, role, conversationID, message string, doc PythonChatDocument) (*PythonChatResponse, error) {
	return c.doChat(ctx, userID, email, "", role, pythonChatRequest{
		Message:        message,
		ConversationID: conversationID,
		Document:       &doc,
	})
}

// doChat mints the per-user JWT and posts one /api/v1/chat payload.
func (c *PythonAgentClient) doChat(ctx context.Context, userID uuid.UUID, email, username, role string, body pythonChatRequest) (*PythonChatResponse, error) {
	// Mint a short-lived per-user "agent" token (not a normal access token):
	// Python decodes it to recover user_id/email/role for its own RBAC, then
	// reuses this SAME token to call Go's own REST API on the user's behalf
	// (get_balance, send_money, etc). Go's Authentication middleware
	// recognizes the "agent" token type and skips the interactive-session
	// lookup for it, since this token is never the product of a real login.
	token, _, err := auth.GenerateAgentToken(userID, email, username, role, c.jwtSecret, int(c.jwtTTL.Seconds()))
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
	token, _, err := auth.GenerateAgentToken(userID, email, "", role, c.jwtSecret, int(c.jwtTTL.Seconds()))
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
