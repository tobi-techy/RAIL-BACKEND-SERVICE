package confirmation

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
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// MiriamConfirmPayloadKey is the payload key Miriam sets when it mints a card
// for one of its own challenges. Its presence routes execution through the
// MiriamSettleExecutor (any action); its absence keeps the direct executors
// for app-originated cards.
const MiriamConfirmPayloadKey = "miriam_confirm_id"

// MiriamConfirmID returns the Miriam challenge id a card was minted for, or ""
// when the card did not originate from a Miriam challenge.
func MiriamConfirmID(p map[string]any) string {
	if p == nil {
		return ""
	}
	if v, ok := p[MiriamConfirmPayloadKey]; ok && v != nil {
		if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
			return s
		}
	}
	return ""
}

// RouteByMiriamConfirm wraps a direct executor so Miriam-originated cards
// (payload carries miriam_confirm_id) settle through Miriam, while
// app-originated cards keep their direct executor.
//
// The direct path bypasses the X-Miriam-Confirm-Id header gate legitimately:
// those cards carry an app session, not a challenge, and the single-use card
// token plus Face ID is their authorization. Miriam-originated cards must go
// through the settle call so the challenge binding is re-checked and the
// confirm_id header is attached exactly once.
func RouteByMiriamConfirm(direct, settle Executor) Executor {
	return func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (string, error) {
		if MiriamConfirmID(c.Payload) != "" {
			if settle == nil {
				return "", fmt.Errorf("miriam settle not wired (fail-closed)")
			}
			return settle(ctx, userID, c)
		}
		if direct == nil {
			return "", fmt.Errorf("no executor registered (fail-closed)")
		}
		return direct(ctx, userID, c)
	}
}

// MintAgentToken issues a short-lived per-user JWT for Go->Miriam calls.
// Wiring provides it (pkg/auth); the primitive never imports auth itself.
type MintAgentToken func(ctx context.Context, userID uuid.UUID) (string, error)

// MiriamSettleConfig drives the settle executor and the terminal notifier.
type MiriamSettleConfig struct {
	// BaseURL is the Miriam agent HTTP base, e.g. http://localhost:8000.
	BaseURL string
	// RailServiceKey is the shared Go<->Miriam secret (X-Rail-Service-Key).
	// It must match Miriam's settings.RAIL_SERVICE_KEY.
	RailServiceKey string
	// MintToken issues the per-user bearer token for the settle call.
	MintToken MintAgentToken
	// HTTPClient is optional; a 15s-timeout client is used when nil.
	HTTPClient *http.Client
}

func (c MiriamSettleConfig) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c MiriamSettleConfig) base() string {
	return strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
}

// settleRequest is what Go POSTs to Miriam POST /api/v1/chat/settle.
// Assurance tells Miriam how strongly the approve is bound to the device
// owner: token_only | enrolled | secure_enclave. Miriam records it on the
// receipt audit; policy may treat token_only as a weaker factor.
type settleRequest struct {
	ConfirmID    string `json:"confirm_id"`
	Biometric    string `json:"biometric"`
	CardActionID string `json:"card_action_id"`
	Assurance    string `json:"assurance,omitempty"`
}

// settleResponse mirrors Miriam's settle reply. Status completed means this
// call executed the challenge; already_settled means the challenge was already
// terminal (chat tap won the race) and is a no-op success; anything else is a
// failure the card must show as failed.
type settleResponse struct {
	Status        string `json:"status"`
	State         string `json:"state"`
	ResultSummary string `json:"result_summary"`
	ReceiptID     string `json:"receipt_id"`
}

// MiriamSettleExecutor settles a Miriam-originated card by calling Miriam
// POST /api/v1/chat/settle, which runs the existing _handle_confirm path
// (full binding re-checks, no second JEV) and executes through GoRail with
// the confirm_id header. Amount and counterparty always come from the Miriam
// challenge binding at settle time, never from this card's payload.
func MiriamSettleExecutor(cfg MiriamSettleConfig) Executor {
	return func(ctx context.Context, userID uuid.UUID, c *entities.Confirmation) (summary string, err error) {
		if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.RailServiceKey) == "" {
			return "", fmt.Errorf("miriam settle not configured (fail-closed)")
		}
		if cfg.MintToken == nil {
			return "", fmt.Errorf("miriam settle token minter not wired (fail-closed)")
		}
		confirmID := MiriamConfirmID(c.Payload)
		if confirmID == "" {
			return "", fmt.Errorf("miriam settle needs payload.miriam_confirm_id")
		}
		req, err := buildSettleRequest(ctx, cfg, userID, c, confirmID)
		if err != nil {
			return "", err
		}
		resp, err := cfg.client().Do(req)
		if err != nil {
			return "", fmt.Errorf("miriam settle request: %w", err)
		}
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil && err == nil {
				err = fmt.Errorf("close miriam settle response: %w", cerr)
			}
		}()
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read miriam settle response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("miriam settle: unexpected status %d: %s", resp.StatusCode, truncateErrorBody(respBody))
		}
		return interpretSettleResult(respBody)
	}
}

// buildSettleRequest mints the per-user agent JWT and builds the settle call.
func buildSettleRequest(ctx context.Context, cfg MiriamSettleConfig, userID uuid.UUID, c *entities.Confirmation, confirmID string) (*http.Request, error) {
	token, err := cfg.MintToken(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("mint miriam settle token: %w", err)
	}
	body, err := json.Marshal(settleRequest{
		ConfirmID:    confirmID,
		Biometric:    "pass",
		CardActionID: c.ID.String(),
		Assurance:    c.Assurance,
	})
	if err != nil {
		return nil, fmt.Errorf("encode miriam settle request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.base()+"/api/v1/chat/settle", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build miriam settle request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Rail-Service-Key", cfg.RailServiceKey)
	return req, nil
}

// interpretSettleResult maps Miriam's settle reply onto the card outcome:
// completed returns the receipt summary; already_settled (the chat tap won
// the race) is a terminal no-op success so the card lands completed instead
// of failed; anything else is an error the card shows as failed.
func interpretSettleResult(body []byte) (string, error) {
	var out settleResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode miriam settle response: %w", err)
	}
	switch out.Status {
	case "completed":
		if out.ResultSummary == "" {
			return "settled via Miriam", nil
		}
		return out.ResultSummary, nil
	case "already_settled":
		if out.ResultSummary == "" {
			return "already settled via chat", nil
		}
		return out.ResultSummary, nil
	default:
		if out.ResultSummary != "" {
			return "", fmt.Errorf("miriam settle %s: %s", out.Status, out.ResultSummary)
		}
		return "", fmt.Errorf("miriam settle %s", out.Status)
	}
}

// cardTerminalRequest is what Go POSTs to Miriam POST /api/v1/chat/card-terminal.
type cardTerminalRequest struct {
	ConfirmID string `json:"confirm_id"`
	State     string `json:"state"` // rejected|expired
}

// TerminalNotifier tells Miriam when a Miriam-originated card reaches a
// terminal state on the Go side first (user cancelled Face ID, TTL hit), so
// Miriam can decline/expire the joined challenge. Fire-and-forget with one
// retry; card state is already persisted before this runs. Approve/completed
// never notify: that path returns through the settle call itself.
type TerminalNotifier struct {
	BaseURL        string
	RailServiceKey string
	HTTPClient     *http.Client
}

func (n *TerminalNotifier) client() *http.Client {
	if n.HTTPClient != nil {
		return n.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Notify posts one terminal state; it retries once and then gives up (the
// card state already stands, and Miriam treats the endpoint idempotently, so
// a later mark from the chat side converges anyway).
func (n *TerminalNotifier) Notify(ctx context.Context, miriamConfirmID, state string) error {
	if n == nil || strings.TrimSpace(n.BaseURL) == "" || strings.TrimSpace(n.RailServiceKey) == "" {
		return fmt.Errorf("terminal notifier not configured")
	}
	if miriamConfirmID == "" {
		return fmt.Errorf("terminal notify needs a miriam confirm id")
	}
	body, err := json.Marshal(cardTerminalRequest{ConfirmID: miriamConfirmID, State: state})
	if err != nil {
		return err
	}
	url := strings.TrimRight(strings.TrimSpace(n.BaseURL), "/") + "/api/v1/chat/card-terminal"
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		lastErr = n.postOnce(ctx, url, body)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (n *TerminalNotifier) postOnce(ctx context.Context, url string, body []byte) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rail-Service-Key", n.RailServiceKey)
	resp, err := n.client().Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close card-terminal response: %w", cerr)
		}
	}()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("card-terminal: unexpected status %d: %s", resp.StatusCode, truncateErrorBody(respBody))
	}
	return nil
}

// truncateErrorBody trims an upstream error body for log/error lines.
func truncateErrorBody(b []byte) string {
	const limit = 300
	s := strings.TrimSpace(string(b))
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
