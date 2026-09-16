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

// servePythonChat fakes the Python agent's /api/v1/chat. When respond is nil it
// returns 500 so the adapter's error path is exercised.
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
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
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
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
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
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
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

func TestPythonGuestCompleter_PythonErrorSurfacesNoFallback(t *testing.T) {
	srv, hit := servePythonChat(t, nil) // 500
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t", Timeout: 2 * time.Second}, zap.NewNop()),
		logger: zap.NewNop(),
	}

	_, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected an error when the python agent fails, not a Go fallback answer")
	}
	if hit.Load() != 1 {
		t.Fatalf("expected one python hit, got %d", hit.Load())
	}
}

func TestPythonGuestCompleter_NoSenderErrors(t *testing.T) {
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://127.0.0.1:1", JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}
	if _, err := adapter.CompleteGuest(context.Background(), "sys",
		[]platform.GuestMessage{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Fatal("expected an error when no guest sender is present, not a Go fallback answer")
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

func TestPythonGuestCompleter_EmptyTextErrors(t *testing.T) {
	adapter := &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: "http://127.0.0.1:1", JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}
	if _, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-A", ThreadID: "t1",
	}), "sys", []platform.GuestMessage{{Role: "assistant", Content: "welcome"}}, nil); err == nil {
		t.Fatal("expected an error when there is no user text, not a Go fallback answer")
	}
}

// pythonChatBody is the subset of the /api/v1/chat request the guest completer's
// tests assert on.
type pythonChatBody struct {
	Message    string `json:"message"`
	IsPollVote bool   `json:"is_poll_vote"`
	PollTitle  string `json:"poll_title"`
}

// servePythonChatCapturing records the request bodies the adapter posts.
func servePythonChatCapturing(t *testing.T, respond *ai.PythonChatResponse) (*httptest.Server, *[]pythonChatBody) {
	t.Helper()
	var bodies []pythonChatBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body pythonChatBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode python request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(respond); err != nil {
			t.Errorf("serve python response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func newGuestCompleterAdapter(srv *httptest.Server) *pythonGuestCompleterAdapter {
	return &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}
}

// A tap on an interactive poll carries only the option title, so the vote flag
// and the question it answered must reach the Python interview — otherwise a
// deliberate selection is answered by re-asking the same question with a fresh
// poll, which is what a dead tap looks like to the person who tapped.
func TestPythonGuestCompleter_PollVoteForwardsVoteFlag(t *testing.T) {
	srv, bodies := servePythonChatCapturing(t, &ai.PythonChatResponse{
		Response: "Savings it is. Where would you like that money to live?",
	})
	adapter := newGuestCompleterAdapter(srv)

	const pollTitle = "What's been bothering you about money lately?"
	ctx := platform.ContextWithGuestVote(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), platform.GuestVote{IsPollVote: true, PollTitle: pollTitle})

	res, err := adapter.CompleteGuest(ctx, "sys",
		[]platform.GuestMessage{{Role: "user", Content: "Savings or investments"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if res.Text != "Savings it is. Where would you like that money to live?" {
		t.Fatalf("unexpected text %q", res.Text)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected one python call, got %d", len(*bodies))
	}
	body := (*bodies)[0]
	if !body.IsPollVote {
		t.Fatalf("expected is_poll_vote on the python request, got %+v", body)
	}
	if body.PollTitle != pollTitle {
		t.Fatalf("expected poll title %q, got %q", pollTitle, body.PollTitle)
	}
	if body.Message != "Savings or investments" {
		t.Fatalf("expected the option title as the message, got %q", body.Message)
	}
}

func TestPythonGuestCompleter_PlainTextIsNotAVote(t *testing.T) {
	srv, bodies := servePythonChatCapturing(t, &ai.PythonChatResponse{Response: "Tell me more."})
	adapter := newGuestCompleterAdapter(srv)

	_, err := adapter.CompleteGuest(guestTurn(context.Background(), platform.GuestSender{
		Platform: entities.PlatformIMessage, SenderID: "user-7A", ThreadID: "thread-9",
	}), "sys", []platform.GuestMessage{{Role: "user", Content: "I want to save more"}}, nil)
	if err != nil {
		t.Fatalf("CompleteGuest failed: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected one python call, got %d", len(*bodies))
	}
	if body := (*bodies)[0]; body.IsPollVote || body.PollTitle != "" {
		t.Fatalf("plain text must not be sent as a vote, got %+v", body)
	}
}
