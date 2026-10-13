package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

func sourceTestClient(t *testing.T, bodies *[]map[string]any) *PythonAgentClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not json: %v", err)
		}
		*bodies = append(*bodies, body)
		if _, err := w.Write([]byte(`{"response":"ok"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return NewPythonAgentClient(PythonAgentClientConfig{
		BaseURL:   srv.URL,
		JWTSecret: "test-secret",
		JWTTTL:    time.Minute,
	}, zap.NewNop())
}

func TestChatCarriesTheMessageSourceToPython(t *testing.T) {
	for _, source := range []entities.TextSource{
		entities.TextSourceUser, entities.TextSourceVoice, entities.TextSourceVision, entities.TextSourceSystem,
	} {
		var bodies []map[string]any
		client := sourceTestClient(t, &bodies)
		if _, err := client.Chat(context.Background(), uuid.New(), "a@b.c", "user", "platform:imessage:t", "hello", source); err != nil {
			t.Fatal(err)
		}
		if len(bodies) != 1 || bodies[0]["message_source"] != string(source) {
			t.Fatalf("message_source = %v, want %q", bodies, source)
		}
	}
}

func TestGuestChatCarriesTheMessageSourceToPython(t *testing.T) {
	var bodies []map[string]any
	client := sourceTestClient(t, &bodies)
	id := uuid.New()
	if _, err := client.ChatAsGuest(context.Background(), id, "g", "g@b.c", "guest", "platform:imessage:t", "Got it, Shoprite.", entities.TextSourceVision); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["message_source"] != "vision" {
		t.Fatalf("guest message_source = %v, want vision", bodies[0]["message_source"])
	}
}

func TestStatementScanTextIsMarkedSystem(t *testing.T) {
	var bodies []map[string]any
	client := sourceTestClient(t, &bodies)
	if _, err := client.ChatWithDocument(context.Background(), uuid.New(), "a@b.c", "user", "c", "I sent my bank statement.\n\n[statement scan]\n...", PythonChatDocument{Name: "s.pdf"}); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["message_source"] != "system" {
		t.Fatalf("statement message_source = %v, want system", bodies[0]["message_source"])
	}
}

func TestPollVoteIsMarkedUser(t *testing.T) {
	var bodies []map[string]any
	client := sourceTestClient(t, &bodies)
	if _, err := client.ChatPollVote(context.Background(), uuid.New(), "a@b.c", "user", "c", "Savings", "What for?"); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["message_source"] != "user" {
		t.Fatalf("poll vote message_source = %v, want user", bodies[0]["message_source"])
	}
}

func TestChatRefusesTextWithNoSource(t *testing.T) {
	var bodies []map[string]any
	client := sourceTestClient(t, &bodies)
	if _, err := client.Chat(context.Background(), uuid.New(), "a@b.c", "user", "c", "send 5k to Ada", ""); err == nil {
		t.Fatal("text with no source must not be sent")
	}
	if len(bodies) != 0 {
		t.Fatalf("a request with no source reached python: %v", bodies)
	}
}

func TestConfirmationTapCarriesNoMessage(t *testing.T) {
	var bodies []map[string]any
	client := sourceTestClient(t, &bodies)
	if _, err := client.ChatConfirm(context.Background(), uuid.New(), "a@b.c", "user", "c", "confirm_1", true); err != nil {
		t.Fatal(err)
	}
	if _, has := bodies[0]["message_source"]; has {
		t.Fatalf("a tap has no text, so no source: %v", bodies[0])
	}
}

func TestChatForwardsTheProviderMessageID(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"response":"ok"}`))
	}))
	defer srv.Close()
	client := NewPythonAgentClient(PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "test-secret", JWTTTL: time.Minute}, zap.NewNop())

	ctx := entities.WithInboundMessageID(context.Background(), "whatsapp:wamid.X1")
	if _, err := client.Chat(ctx, uuid.New(), "a@b.c", "user", "c", "hello", entities.TextSourceUser); err != nil {
		t.Fatal(err)
	}
	if bodies[0]["message_id"] != "whatsapp:wamid.X1" {
		t.Fatalf("message_id = %v", bodies[0]["message_id"])
	}

	if _, err := client.Chat(context.Background(), uuid.New(), "a@b.c", "user", "c", "hello", entities.TextSourceUser); err != nil {
		t.Fatal(err)
	}
	if _, has := bodies[1]["message_id"]; has {
		t.Fatalf("no id in context, so none on the wire: %v", bodies[1])
	}
}
