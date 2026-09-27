package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestMergeUsersPostsTargetAndRailKey(t *testing.T) {
	target := uuid.New()
	from := uuid.New()
	var gotKey, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/merge" {
			t.Errorf("path %s", r.URL.Path)
		}
		gotKey = r.Header.Get("X-Rail-Service-Key")
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"merged_from":"a","merged_into":"b"}`))
	}))
	defer srv.Close()

	client := NewPythonAgentClient(PythonAgentClientConfig{
		BaseURL:        srv.URL,
		JWTSecret:      "test-secret",
		JWTTTL:         time.Minute,
		RailServiceKey: "shared-rail-key",
	}, zap.NewNop())
	if err := client.MergeUsers(context.Background(), target, "ada@example.com", from); err != nil {
		t.Fatal(err)
	}
	if gotKey != "shared-rail-key" {
		t.Fatalf("rail key %q", gotKey)
	}
	if gotAuth == "" {
		t.Fatal("expected a bearer token")
	}
	if !strings.Contains(gotBody, from.String()) {
		t.Fatalf("body %s does not name %s", gotBody, from)
	}
}

func TestMergeUsersRequiresRailKey(t *testing.T) {
	client := NewPythonAgentClient(PythonAgentClientConfig{
		BaseURL:   "http://127.0.0.1:1",
		JWTSecret: "test-secret",
	}, zap.NewNop())
	err := client.MergeUsers(context.Background(), uuid.New(), "a@b.c", uuid.New())
	if err == nil {
		t.Fatal("expected an error without the rail service key")
	}
}
