package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	infraplatform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// blipRepo fails every lookup with a driver-level error, which the resolver
// classifies as transient (retryable).
type blipRepo struct{}

func (blipRepo) GetByPlatformUser(context.Context, entities.Platform, string) (*entities.PlatformIdentity, error) {
	return nil, errors.New("connection reset by peer")
}
func (blipRepo) GetByID(context.Context, uuid.UUID) (*entities.PlatformIdentity, error) {
	return nil, errors.New("unused")
}
func (blipRepo) GetByUserAndPlatform(context.Context, uuid.UUID, entities.Platform) (*entities.PlatformIdentity, error) {
	return nil, errors.New("unused")
}
func (blipRepo) GetByHandshakeTokenHash(context.Context, string) (*entities.PlatformIdentity, error) {
	return nil, errors.New("unused")
}
func (blipRepo) ListByUser(context.Context, uuid.UUID) ([]*entities.PlatformIdentity, error) {
	return nil, errors.New("unused")
}
func (blipRepo) ListLinkedByPlatform(context.Context, entities.Platform) ([]*entities.PlatformIdentity, error) {
	return nil, errors.New("unused")
}
func (blipRepo) Create(context.Context, *entities.PlatformIdentity) error {
	return errors.New("unused")
}
func (blipRepo) SetHandshake(context.Context, uuid.UUID, string, time.Time) error {
	return errors.New("unused")
}
func (blipRepo) CompleteHandshake(context.Context, uuid.UUID, string) error {
	return errors.New("unused")
}
func (blipRepo) TouchLastUsed(context.Context, uuid.UUID) error { return nil }
func (blipRepo) Delete(context.Context, uuid.UUID) error        { return errors.New("unused") }

func serveInbound(t *testing.T, handler gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/platform/inbound", strings.NewReader(body))
	handler(c)
	return rec
}

func observedFields(t *testing.T, body []byte) map[string]any {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	zap.New(core).Error("boom", inboundErrorFields(body)...)
	if logs.Len() != 1 {
		t.Fatalf("expected exactly one log entry, got %d", logs.Len())
	}
	return logs.All()[0].ContextMap()
}

func TestInboundErrorFields_ReportAttemptBudget(t *testing.T) {
	fields := observedFields(t, []byte(
		`{"platform":"imessage","user_id":"+15551234567","thread_id":"t1","text":"hey","attempt":2,"max_attempts":3}`,
	))

	// ContextMap renders zap.Int as int64.
	for key, want := range map[string]any{
		"platform":     "imessage",
		"user_id":      "+15551234567",
		"thread_id":    "t1",
		"attempt":      int64(2),
		"max_attempts": int64(3),
	} {
		if got := fields[key]; got != want {
			t.Errorf("log field %q = %v, want %v", key, got, want)
		}
	}
}

func TestInboundErrorFields_AttemptsAlwaysPresent(t *testing.T) {
	// A bridge that sends no counters still has to produce a log line that
	// names the attempt, otherwise "zero" and "not reported" read the same.
	fields := observedFields(t, []byte(`{"text":"hey"}`))
	if got, ok := fields["attempt"]; !ok || got != int64(0) {
		t.Errorf("attempt = %v (present=%v), want 0", got, ok)
	}
	if got, ok := fields["max_attempts"]; !ok || got != int64(0) {
		t.Errorf("max_attempts = %v (present=%v), want 0", got, ok)
	}
	if _, ok := fields["user_id"]; ok {
		t.Error("empty user_id should be omitted rather than logged as \"\"")
	}
}

func TestInboundErrorFields_UnparseableBodyYieldsNothing(t *testing.T) {
	if fields := inboundErrorFields([]byte("not json")); fields != nil {
		t.Fatalf("expected no fields for an unparseable body, got %v", fields)
	}
}

func TestHandleInbound_TransientFailureIsFiveHundredWithDiagnostics(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewPlatformHandlerWithLogger(nil, "", zap.New(core))
	proc := infraplatform.NewProcessor(
		infraplatform.NewUserResolver(blipRepo{}),
		nil, nil, nil, nil,
		func(context.Context, *infraplatform.OutboundMessage) error { return nil },
	)

	body := `{"platform":"imessage","user_id":"+15551234567","thread_id":"t1","text":"hey","attempt":1,"max_attempts":3}`
	rec := serveInbound(t, h.HandleInbound(proc), body)

	// The bridge retries 5xx, so a transient failure must stay a 500 — that
	// contract is what makes the redelivery possible at all.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "processing failed") {
		t.Fatalf("body = %q, want a processing failed error", rec.Body.String())
	}

	var entry *observer.LoggedEntry
	for i, e := range logs.All() {
		if e.Message == "platform inbound processing failed" {
			entry = &logs.All()[i]
			break
		}
	}
	if entry == nil {
		t.Fatalf("no failure was logged; entries: %v", logs.All())
	}
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("level = %v, want error — a 500 that the bridge retries must be findable in logs", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["retryable"] != true {
		t.Errorf("retryable = %v, want true (the bridge still has redeliveries)", fields["retryable"])
	}
	if fields["attempt"] != int64(1) || fields["max_attempts"] != int64(3) {
		t.Errorf("attempt budget = %v/%v, want 1/3", fields["attempt"], fields["max_attempts"])
	}
	if fields["thread_id"] != "t1" || fields["user_id"] != "+15551234567" {
		t.Errorf("missing identity in log: %v", fields)
	}
	if fields["error"] == nil {
		t.Error("the wrapped cause must be logged, not just the status")
	}
}

func TestHandleInbound_SuccessLogsNoFailure(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewPlatformHandlerWithLogger(nil, "", zap.New(core))
	proc := infraplatform.NewProcessor(
		infraplatform.NewUserResolver(blipRepo{}),
		nil, nil, nil, nil,
		func(context.Context, *infraplatform.OutboundMessage) error { return nil },
	)

	// A read receipt is acked before any identity lookup, so this succeeds even
	// with the resolver failing — pinning that a delivery signal never 500s.
	rec := serveInbound(t, h.HandleInbound(proc), `{"platform":"imessage","user_id":"+1555","thread_id":"t1","is_read_receipt":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, e := range logs.All() {
		if e.Message == "platform inbound processing failed" {
			t.Fatalf("an acked read receipt must not log a failure: %v", e.ContextMap())
		}
	}
}

func TestHandleInbound_NilProcessorIsServiceUnavailable(t *testing.T) {
	h := NewPlatformHandlerWithLogger(nil, "", zap.NewNop())
	rec := serveInbound(t, h.HandleInbound(nil), `{}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
