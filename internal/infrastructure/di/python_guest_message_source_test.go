package di

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
)

func guestSourceAdapter(t *testing.T, seen *[]string) *pythonGuestCompleterAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body struct {
			MessageSource string `json:"message_source"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body is not json: %v", err)
		}
		*seen = append(*seen, body.MessageSource)
		if _, err := w.Write([]byte(`{"response":"hi"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return &pythonGuestCompleterAdapter{
		python: ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger: zap.NewNop(),
	}
}

func TestPythonGuestCompleter_ForwardsTheTurnSource(t *testing.T) {
	var seen []string
	adapter := guestSourceAdapter(t, &seen)
	ctx := platform.ContextWithGuestSender(context.Background(), platform.GuestSender{
		Platform: entities.PlatformWhatsApp, SenderID: "234800", ThreadID: "t", Source: entities.TextSourceVision,
	})
	if _, err := adapter.CompleteGuest(ctx, "sys", []platform.GuestMessage{{Role: "user", Content: "Got it, Shoprite."}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "vision" {
		t.Fatalf("python saw message_source %v, want [vision]", seen)
	}
}

func TestPythonGuestCompleter_NoSourceNoCall(t *testing.T) {
	var seen []string
	adapter := guestSourceAdapter(t, &seen)
	ctx := platform.ContextWithGuestSender(context.Background(), platform.GuestSender{
		Platform: entities.PlatformWhatsApp, SenderID: "234800", ThreadID: "t",
	})
	if _, err := adapter.CompleteGuest(ctx, "sys", []platform.GuestMessage{{Role: "user", Content: "hello"}}, nil); err == nil {
		t.Fatal("a turn with no source must fail closed")
	}
	if len(seen) != 0 {
		t.Fatalf("python was called without a source: %v", seen)
	}
}
