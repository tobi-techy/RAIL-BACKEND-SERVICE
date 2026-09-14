package di

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	authpkg "github.com/rail-service/rail_service/pkg/auth"
	"go.uber.org/zap"
)

type stubGuestCompleter struct {
	result *platform.GuestResult
	err    error
	calls  atomic.Int32
}

func (s *stubGuestCompleter) CompleteGuest(_ context.Context, _ string, _ []platform.GuestMessage, _ []platform.GuestToolDef) (*platform.GuestResult, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// servePythonChat fakes the Python agent's /api/v1/chat. When respond is nil it
// returns 500 so the adapter's fallback path is exercised.
func servePythonChat(t *testing.T, respond *ai.PythonChatResponse) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hit atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Add(1)
		if respond == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(respond); err != nil {
			t.Errorf("serve python response: %v", err)
			return
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hit
}

func guestTurn(ctx context.Context, sender platform.GuestSender) context.Context {
	return platform.ContextWithGuestSender(ctx, sender)
}

func TestPythonGuestCompleter_PollMapping(t *testing.T) {
	srv, hit := servePythonChat(t, &ai.PythonChatResponse{
		Response: "How do you feel about your spending?",
		Poll:     &ai.PythonChatPoll{Title: "Pick your vibe", Options: []string{"Avoider", "Optimizer", "Worrier"}},
	})
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		fallback: &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback"}},
		logger:   zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if hit.Load() != 1 {
		t.Fatalf("expected one python call, got %d", hit.Load())
	}
	if res.Text != "How do you feel about your spending?" {
		t.Fatalf("unexpected text %q", res.Text)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "send_poll" {
		t.Fatalf("expected send_poll, got %+v", res.ToolCalls)
	}
	q, ok := res.ToolCalls[0].Arguments["question"].(string)
	if !ok {
		t.Fatalf("send_poll arguments missing question: %+v", res.ToolCalls[0].Arguments)
	}
	if q != "Pick your vibe" {
		t.Fatalf("unexpected poll question %q", q)
	}
}

func TestPythonGuestCompleter_NameMapsToNoteDetail(t *testing.T) {
	srv, hit := servePythonChat(t, &ai.PythonChatResponse{
		Response: "Nice to meet you, Tola! How is money feeling?",
		Name:     "Tola",
	})
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		fallback: &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback"}},
		logger:   zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "my name is tola"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if hit.Load() != 1 {
		t.Fatalf("expected one python call, got %d", hit.Load())
	}
	if res.Text != "Nice to meet you, Tola! How is money feeling?" {
		t.Fatalf("unexpected text %q", res.Text)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "note_detail" {
		t.Fatalf("expected note_detail tool call, got %+v", res.ToolCalls)
	}
	field, _ := res.ToolCalls[0].Arguments["field"].(string)
	value, _ := res.ToolCalls[0].Arguments["value"].(string)
	if field != "first_name" || value != "Tola" {
		t.Fatalf("unexpected note_detail args: field=%q value=%q", field, value)
	}
}

func TestPythonGuestCompleter_PollAddsTextFallback(t *testing.T) {
	srv, hit := servePythonChat(t, &ai.PythonChatResponse{
		Poll: &ai.PythonChatPoll{Title: "When money gets tight, what's behind it?", Options: []string{"Timing", "Spending", "Debt", "Not sure"}},
	})
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		fallback: &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback"}},
		logger:   zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if hit.Load() != 1 {
		t.Fatalf("expected one python call, got %d", hit.Load())
	}
	// A poll must never be the whole reply: text always rides along.
	if res.Text != "When money gets tight, what's behind it?" {
		t.Fatalf("expected poll title as text fallback, got %q", res.Text)
	}
	found := false
	for _, tc := range res.ToolCalls {
		if tc.Name == "send_poll" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected send_poll tool call, got %+v", res.ToolCalls)
	}
}

func TestPythonGuestCompleter_ConsentToSetupMapsToStartSignup(t *testing.T) {
	srv, _ := servePythonChat(t, &ai.PythonChatResponse{
		Response:   "I've got your full picture.",
		Onboarding: &ai.PythonOnboardingStatus{Stage: "complete", Completed: true, Automated: true},
		Poll:       &ai.PythonChatPoll{Title: "Should this keep?", Options: []string{"Yes", "No"}},
	})
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "yes, set it up"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "start_signup" {
		t.Fatalf("expected only start_signup (poll must drop on completed setup), got %+v", res.ToolCalls)
	}
}

func TestPythonGuestCompleter_DeclinedSetupStaysConversational(t *testing.T) {
	srv, _ := servePythonChat(t, &ai.PythonChatResponse{
		Response:   "No problem at all. I've saved the plan - ask me to put it into action anytime.",
		Onboarding: &ai.PythonOnboardingStatus{Stage: "complete", Completed: true, Automated: false},
	})
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "not now"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if res.Text != "No problem at all. I've saved the plan - ask me to put it into action anytime." {
		t.Fatalf("unexpected text %q", res.Text)
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("declined setup must not trigger signup, got %+v", res.ToolCalls)
	}
}

func TestPythonGuestCompleter_CardsDropped(t *testing.T) {
	srv, _ := servePythonChat(t, &ai.PythonChatResponse{
		Response:             "parts",
		RequiresConfirmation: true,
		Cards:                []ai.PythonChatCard{{Tool: "transfer_funds"}},
	})
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "send money"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if res.Text != "parts" {
		t.Fatalf("unexpected text %q", res.Text)
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("cards must never surface as guest effects, got %+v", res.ToolCalls)
	}
}

func TestPythonGuestCompleter_PythonErrorFallsBack(t *testing.T) {
	srv, hit := servePythonChat(t, nil) // 500
	fallback := &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback answer"}}
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t", Timeout: 2 * time.Second}, zap.NewNop()),
		fallback: fallback,
		logger:   zap.NewNop(),
	}

	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if hit.Load() != 1 || fallback.calls.Load() != 1 {
		t.Fatalf("expected python hit and one fallback, got hit=%d fallback=%d", hit.Load(), fallback.calls.Load())
	}
	if res.Text != "fallback answer" {
		t.Fatalf("unexpected text %q", res.Text)
	}
}

func TestPythonGuestCompleter_NoSenderUsesFallback(t *testing.T) {
	fallback := &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback answer"}}
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://127.0.0.1:1", JWTSecret: "t"}, zap.NewNop()),
		fallback: fallback,
		logger:   zap.NewNop(),
	}
	res, err := adapter.CompleteGuest(context.Background(), "sys",
		[]platform.GuestMessage{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if res.Text != "fallback answer" {
		t.Fatalf("unexpected text %q", res.Text)
	}
}

func TestPythonGuestCompleter_TokenClaimsArePerSenderUnique(t *testing.T) {
	secret := "test-secret"
	senders := []platform.GuestSender{
		{Platform: entities.PlatformIMessage, SenderID: "user-A", ThreadID: "t1"},
		{Platform: entities.PlatformIMessage, SenderID: "user-B", ThreadID: "t1"},
	}
	seenUser, seenEmail, seenUsername := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range senders {
		id := guestSyntheticID(s)
		if seenUser[id.String()] {
			t.Fatalf("duplicate synthetic user id for %+v", s)
		}
		seenUser[id.String()] = true

		token, _, err := authpkg.GenerateAgentToken(id, guestEmail(id), guestUsername(id), "guest", secret, 60)
		if err != nil {
			t.Fatalf("GenerateAgentToken failed: %v", err)
		}
		parsed, _, err := new(jwt.Parser).ParseUnverified(token, &authpkg.Claims{})
		if err != nil {
			t.Fatalf("parse token: %v", err)
		}
		claims, ok := parsed.Claims.(*authpkg.Claims)
		if !ok {
			t.Fatalf("claims type mismatch for %+v", s)
		}
		if seenEmail[claims.Email] || seenUsername[claims.Username] {
			t.Fatalf("non-unique claim for %+v: email=%q username=%q", s, claims.Email, claims.Username)
		}
		seenEmail[claims.Email], seenUsername[claims.Username] = true, true
		if !seenUsername[claims.Username] || claims.Role != "guest" {
			t.Fatalf("unexpected claims: %+v", claims)
		}
	}
}

func TestPythonGuestCompleter_EmptyTextUsesFallback(t *testing.T) {
	fallback := &stubGuestCompleter{result: &platform.GuestResult{Text: "fallback answer"}}
	adapter := &pythonGuestCompleterAdapter{
		python:   ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://127.0.0.1:1", JWTSecret: "t"}, zap.NewNop()),
		fallback: fallback,
		logger:   zap.NewNop(),
	}
	res, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-A", ThreadID: "t1",
	}), "sys", []platform.GuestMessage{{Role: "assistant", Content: "welcome"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if res.Text != "fallback answer" {
		t.Fatalf("unexpected text %q", res.Text)
	}
}
