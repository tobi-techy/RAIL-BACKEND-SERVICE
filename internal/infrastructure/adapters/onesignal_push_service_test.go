package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestOneSignalPushService_SendToUser_Success(t *testing.T) {
	var gotBody oneSignalNotificationRequest
	var gotAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/notifications" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"notif-123","recipients":1}`))
	}))
	defer ts.Close()

	svc := NewOneSignalPushService("app-1", "key-1", zap.NewNop())
	svc.baseURL = ts.URL + "/api/v1/notifications"

	userID := uuid.New()
	err := svc.SendToUser(context.Background(), userID, "Hello", "World", map[string]interface{}{"type": "test"})
	if err != nil {
		t.Fatalf("SendToUser returned error: %v", err)
	}

	if gotAuth != "Basic key-1" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Basic key-1")
	}
	if gotBody.AppID != "app-1" {
		t.Errorf("app_id = %q, want app-1", gotBody.AppID)
	}
	if len(gotBody.IncludeExternalUserIDs) != 1 || gotBody.IncludeExternalUserIDs[0] != userID.String() {
		t.Errorf("include_external_user_ids = %v, want [%s]", gotBody.IncludeExternalUserIDs, userID.String())
	}
	if gotBody.Headings["en"] != "Hello" || gotBody.Contents["en"] != "World" {
		t.Errorf("headings/contents mismatch: %+v %+v", gotBody.Headings, gotBody.Contents)
	}
	if gotBody.Data["type"] != "test" {
		t.Errorf("data = %+v, want type=test", gotBody.Data)
	}
	if gotBody.Priority != 10 {
		t.Errorf("priority = %d, want 10", gotBody.Priority)
	}
	if gotBody.TTL != oneSignalTTL {
		t.Errorf("ttl = %d, want %d", gotBody.TTL, oneSignalTTL)
	}
}

func TestOneSignalPushService_SendToUser_MissingConfig(t *testing.T) {
	svc := NewOneSignalPushService("", "", zap.NewNop())
	err := svc.SendToUser(context.Background(), uuid.New(), "t", "b", nil)
	if err == nil {
		t.Fatal("expected error when credentials missing")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOneSignalPushService_SendToUser_PerRecipientErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"notif-1","recipients":0,"errors":["No Subscriptions found for external_id foo"]}`))
	}))
	defer ts.Close()

	svc := NewOneSignalPushService("app-1", "key-1", zap.NewNop())
	svc.baseURL = ts.URL

	// Per-recipient errors inside a 200 response must NOT fail the call
	// (e.g. user never logged in on a device / unsubscribed).
	err := svc.SendToUser(context.Background(), uuid.New(), "t", "b", nil)
	if err != nil {
		t.Fatalf("SendToUser should tolerate per-recipient errors, got: %v", err)
	}
}

func TestOneSignalPushService_SendToUser_RetriesOn429(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"notif-2","recipients":1}`))
	}))
	defer ts.Close()

	svc := NewOneSignalPushService("app-1", "key-1", zap.NewNop())
	svc.baseURL = ts.URL

	err := svc.SendToUser(context.Background(), uuid.New(), "t", "b", nil)
	if err != nil {
		t.Fatalf("SendToUser should retry 429, got: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestOneSignalPushService_SendToUser_NonRetryableStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errors":["Bad request"]}`))
	}))
	defer ts.Close()

	svc := NewOneSignalPushService("app-1", "key-1", zap.NewNop())
	svc.baseURL = ts.URL

	err := svc.SendToUser(context.Background(), uuid.New(), "t", "b", nil)
	if err == nil {
		t.Fatal("expected error for 400 status")
	}
	if !strings.Contains(err.Error(), "non-retryable") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOneSignalPushService_SendToUser_RetryExhausted(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	svc := NewOneSignalPushService("app-1", "key-1", zap.NewNop())
	svc.baseURL = ts.URL

	err := svc.SendToUser(context.Background(), uuid.New(), "t", "b", nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts != oneSignalMaxRetries+1 {
		t.Errorf("attempts = %d, want %d", attempts, oneSignalMaxRetries+1)
	}
}

func TestRetryAfterDuration(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "seconds", header: "5", want: 5 * time.Second},
		{name: "empty", header: "", want: oneSignalRetryBaseDelay},
		{name: "garbage", header: "abc", want: oneSignalRetryBaseDelay},
		{name: "http-date", header: time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat), want: 2 * time.Second},
		{name: "past-date", header: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), want: oneSignalRetryBaseDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryAfterDuration(tt.header)
			if tt.name == "http-date" {
				// http.TimeFormat has second granularity; the parsed time can be
				// up to ~1s shy of the 2s target. Assert a sane range instead.
				if got <= time.Second || got > 2*time.Second {
					t.Errorf("retryAfterDuration(%q) = %v, want ~2s", tt.header, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("retryAfterDuration(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
