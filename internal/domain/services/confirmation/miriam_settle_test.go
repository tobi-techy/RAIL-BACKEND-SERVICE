package confirmation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

func TestMiriamConfirmID(t *testing.T) {
	if got := MiriamConfirmID(nil); got != "" {
		t.Fatalf("nil payload = %q", got)
	}
	if got := MiriamConfirmID(map[string]any{"amount": "20"}); got != "" {
		t.Fatalf("no key = %q", got)
	}
	if got := MiriamConfirmID(map[string]any{MiriamConfirmPayloadKey: "confirm_abc123"}); got != "confirm_abc123" {
		t.Fatalf("key = %q", got)
	}
}

func TestRouteByMiriamConfirm(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()
	direct := Executor(func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "direct", nil
	})
	settle := Executor(func(ctx context.Context, u uuid.UUID, c *entities.Confirmation) (string, error) {
		return "settled", nil
	})

	// Without the key the direct executor runs (app-originated card).
	out, err := RouteByMiriamConfirm(direct, settle)(ctx, uid, &entities.Confirmation{Payload: map[string]any{}})
	if err != nil || out != "direct" {
		t.Fatalf("direct = %q, %v", out, err)
	}
	// With the key the settle executor runs for any action.
	out, err = RouteByMiriamConfirm(direct, settle)(ctx, uid, &entities.Confirmation{
		Payload: map[string]any{MiriamConfirmPayloadKey: "confirm_x"},
	})
	if err != nil || out != "settled" {
		t.Fatalf("settled = %q, %v", out, err)
	}
	// Miriam card with no settle executor fails closed (never direct).
	if _, err := RouteByMiriamConfirm(direct, nil)(ctx, uid, &entities.Confirmation{
		Payload: map[string]any{MiriamConfirmPayloadKey: "confirm_x"},
	}); err == nil {
		t.Fatal("nil settle must fail closed")
	}
	// App card with no direct executor fails closed.
	if _, err := RouteByMiriamConfirm(nil, settle)(ctx, uid, &entities.Confirmation{Payload: map[string]any{}}); err == nil {
		t.Fatal("nil direct must fail closed")
	}
}

// settleStub is an httptest Miriam: it records the request and replies canned.
type settleStub struct {
	t            *testing.T
	wantKey      string
	wantConfirm  string
	wantCard     string
	status       int
	reply        settleResponse
	gotAuth      string
	gotKey       string
	gotBiometric string
	calls        int
}

func (s *settleStub) handler(w http.ResponseWriter, r *http.Request) {
	s.calls++
	s.gotAuth = r.Header.Get("Authorization")
	s.gotKey = r.Header.Get("X-Rail-Service-Key")
	var req settleRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.gotBiometric = req.Biometric
	if req.ConfirmID != s.wantConfirm || req.CardActionID != s.wantCard {
		http.Error(w, "bad join", http.StatusBadRequest)
		return
	}
	reply, err := json.Marshal(s.reply)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	if _, err := w.Write(reply); err != nil {
		s.t.Logf("settle stub write failed: %v", err)
	}
}

func settleCard() *entities.Confirmation {
	return &entities.Confirmation{
		ID:      uuid.New(),
		Payload: map[string]any{MiriamConfirmPayloadKey: "confirm_abc"},
	}
}

func settleCfg(t *testing.T, base, key string) MiriamSettleConfig {
	t.Helper()
	return MiriamSettleConfig{
		BaseURL:        base,
		RailServiceKey: key,
		MintToken: func(ctx context.Context, u uuid.UUID) (string, error) {
			return "agent-jwt-for-" + u.String(), nil
		},
	}
}

func TestMiriamSettleExecutorCompleted(t *testing.T) {
	card := settleCard()
	stub := &settleStub{
		t:           t,
		wantKey:     "shared-rail-key-32-chars-minimum-xxxx",
		wantConfirm: "confirm_abc",
		wantCard:    card.ID.String(),
		status:      http.StatusOK,
		reply:       settleResponse{Status: "completed", State: "completed", ResultSummary: "Sent 20000 (ref ab12cd34)", ReceiptID: "rcpt_1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer srv.Close()

	summary, err := MiriamSettleExecutor(settleCfg(t, srv.URL, stub.wantKey))(context.Background(), uuid.New(), card)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "Sent 20000 (ref ab12cd34)" {
		t.Fatalf("summary = %q", summary)
	}
	if stub.gotKey != stub.wantKey {
		t.Fatalf("rail key header = %q", stub.gotKey)
	}
	if !strings.HasPrefix(stub.gotAuth, "Bearer ") {
		t.Fatalf("auth header = %q", stub.gotAuth)
	}
	if stub.gotBiometric != "pass" {
		t.Fatalf("biometric = %q", stub.gotBiometric)
	}
}

func TestMiriamSettleExecutorReplayIsNoOpSuccess(t *testing.T) {
	card := settleCard()
	stub := &settleStub{
		wantKey: "k", wantConfirm: "confirm_abc", wantCard: card.ID.String(),
		status: http.StatusOK,
		reply:  settleResponse{Status: "already_settled", State: "completed", ResultSummary: "already settled via chat"},
	}
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	defer srv.Close()

	summary, err := MiriamSettleExecutor(settleCfg(t, srv.URL, "k"))(context.Background(), uuid.New(), card)
	if err != nil {
		t.Fatalf("replay must not error: %v", err)
	}
	if summary == "" {
		t.Fatal("replay must return a terminal note")
	}
}

func TestMiriamSettleExecutorFailures(t *testing.T) {
	card := settleCard()
	cases := []struct {
		name   string
		status int
		reply  settleResponse
	}{
		{"rejected", http.StatusOK, settleResponse{Status: "rejected", State: "rejected", ResultSummary: "that confirmation does not match anything open"}},
		{"failed", http.StatusOK, settleResponse{Status: "failed", State: "failed", ResultSummary: "rail down"}},
		{"server error", http.StatusInternalServerError, settleResponse{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &settleStub{
				wantKey: "k", wantConfirm: "confirm_abc", wantCard: card.ID.String(),
				status: tc.status, reply: tc.reply,
			}
			srv := httptest.NewServer(http.HandlerFunc(stub.handler))
			defer srv.Close()
			if _, err := MiriamSettleExecutor(settleCfg(t, srv.URL, "k"))(context.Background(), uuid.New(), card); err == nil {
				t.Fatal("expected error (card must show failed)")
			}
		})
	}
	// Unconfigured executor fails closed without any HTTP call.
	if _, err := MiriamSettleExecutor(MiriamSettleConfig{})(context.Background(), uuid.New(), card); err == nil {
		t.Fatal("empty config must fail closed")
	}
}

func TestMarkExternalWalksLegalPath(t *testing.T) {
	s := testService()
	edits := 0
	s.SetCardEditor(func(ctx context.Context, c *entities.Confirmation) error { edits++; return nil })
	ctx := context.Background()
	uid := uuid.New()

	c, _, err := s.Create(ctx, CreateInput{
		UserID: uid, Action: entities.ConfirmationActionTransferSend,
		Payload: map[string]any{"to": "Funsho", "amount": "20000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Chat settles first: pending -> completed walks authenticating/approved.
	out, err := s.MarkExternal(ctx, c.ID, entities.ConfirmationCompleted, "Sent 20000 (ref ab12cd34)")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationCompleted {
		t.Fatalf("state = %s", out.State)
	}
	if out.ResultSummary != "Sent 20000 (ref ab12cd34)" {
		t.Fatalf("summary = %q", out.ResultSummary)
	}
	if edits == 0 {
		t.Fatal("mark must trigger the card edit")
	}
	// Terminal replay: 200 no-op, same state, no extra edits.
	before := edits
	out2, err := s.MarkExternal(ctx, c.ID, entities.ConfirmationCompleted, "again")
	if err != nil {
		t.Fatal(err)
	}
	if out2.State != entities.ConfirmationCompleted || edits != before {
		t.Fatalf("replay must no-op: state=%s edits=%d->%d", out2.State, before, edits)
	}
	// Unknown id errors (handler maps to 404).
	if _, err := s.MarkExternal(ctx, uuid.New(), entities.ConfirmationCompleted, "x"); err == nil {
		t.Fatal("unknown id must error")
	}
}

func TestMarkExternalRejectAndExpire(t *testing.T) {
	s := testService()
	ctx := context.Background()
	uid := uuid.New()
	c, _, err := s.Create(ctx, CreateInput{
		UserID: uid, Action: entities.ConfirmationActionSaveSweep,
		Payload: map[string]any{"percentage": "15", "miriam_confirm_id": "confirm_z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.MarkExternal(ctx, c.ID, entities.ConfirmationRejected, "")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != entities.ConfirmationRejected {
		t.Fatalf("state = %s", out.State)
	}

	c2, _, err := s.Create(ctx, CreateInput{
		UserID: uid, Action: entities.ConfirmationActionSaveSweep,
		Payload: map[string]any{"percentage": "15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out2, err := s.MarkExternal(ctx, c2.ID, entities.ConfirmationExpired, "")
	if err != nil {
		t.Fatal(err)
	}
	if out2.State != entities.ConfirmationExpired {
		t.Fatalf("state = %s", out2.State)
	}
}

func TestStepToward(t *testing.T) {
	if got := stepToward(entities.ConfirmationPending, entities.ConfirmationCompleted); got != entities.ConfirmationAuthenticating {
		t.Fatalf("pending->completed first step = %s", got)
	}
	if got := stepToward(entities.ConfirmationAuthenticating, entities.ConfirmationFailed); got != entities.ConfirmationApproved {
		t.Fatalf("authenticating->failed step = %s", got)
	}
	if got := stepToward(entities.ConfirmationApproved, entities.ConfirmationCompleted); got != entities.ConfirmationCompleted {
		t.Fatalf("approved->completed = %s", got)
	}
	// Approved already passed biometrics: a user cancel can never follow.
	if got := stepToward(entities.ConfirmationApproved, entities.ConfirmationRejected); got != "" {
		t.Fatalf("approved->rejected must have no legal edge, got %s", got)
	}
	if got := stepToward(entities.ConfirmationApproved, entities.ConfirmationExpired); got != entities.ConfirmationExpired {
		t.Fatalf("approved->expired = %s", got)
	}
	if got := stepToward(entities.ConfirmationCompleted, entities.ConfirmationCompleted); got != "" {
		t.Fatalf("terminal must have no step, got %s", got)
	}
}

func TestTerminalNotifyFiresOnReject(t *testing.T) {
	var gotConfirm, gotState string
	done := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Rail-Service-Key") != "shared-key" {
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		var req cardTerminalRequest
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		gotConfirm, gotState = req.ConfirmID, req.State
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
			t.Logf("terminal stub write failed: %v", err)
		}
		done <- struct{}{}
	}))
	defer srv.Close()

	s := testService()
	s.SetTerminalNotifier(func(ctx context.Context, mid, state string) {
		n := &TerminalNotifier{BaseURL: srv.URL, RailServiceKey: "shared-key"}
		if err := n.Notify(ctx, mid, state); err != nil {
			t.Errorf("notify: %v", err)
		}
	})
	ctx := context.Background()
	c, _, err := s.Create(ctx, CreateInput{
		UserID: uuid.New(), Action: entities.ConfirmationActionTransferSend,
		Payload: map[string]any{"to": "Funsho", "amount": "20000", MiriamConfirmPayloadKey: "confirm_face1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fetch a valid token for the reject call.
	url, err := s.ConfirmURL(c)
	if err != nil {
		t.Fatal(err)
	}
	tok := url[strings.Index(url, "?t=")+3:]
	if _, err := s.Reject(ctx, c.UserID, c.ID, tok, "cancel"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal callback to miriam never fired")
	}
	if gotConfirm != "confirm_face1" || gotState != "rejected" {
		t.Fatalf("callback = %q %q", gotConfirm, gotState)
	}
}

func TestTerminalNotifierRetriesOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := &TerminalNotifier{BaseURL: srv.URL, RailServiceKey: "k"}
	if err := n.Notify(context.Background(), "confirm_x", "expired"); err == nil {
		t.Fatal("persistent 500 must error")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("must try once + one retry, got %d", got)
	}
	if err := (&TerminalNotifier{}).Notify(context.Background(), "confirm_x", "expired"); err == nil {
		t.Fatal("unconfigured notifier must error")
	}
}

func TestMiriamSettleExecutorMintError(t *testing.T) {
	card := settleCard()
	ex := MiriamSettleExecutor(MiriamSettleConfig{
		BaseURL:        "http://localhost:1",
		RailServiceKey: "k",
		MintToken: func(ctx context.Context, u uuid.UUID) (string, error) {
			return "", errors.New("kms down")
		},
	})
	if _, err := ex(context.Background(), uuid.New(), card); err == nil {
		t.Fatal("mint failure must fail the card")
	}
	ex2 := MiriamSettleExecutor(settleCfg(t, "http://localhost:1", "k"))
	bare := &entities.Confirmation{ID: uuid.New(), Payload: map[string]any{}}
	if _, err := ex2(context.Background(), uuid.New(), bare); err == nil {
		t.Fatal("missing miriam_confirm_id must fail")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncateErrorBody([]byte("  hello  ")); got != "hello" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncateErrorBody([]byte(strings.Repeat("x", 400))); len(got) != 300+len("…") {
		t.Fatalf("long truncate len = %d", len(got))
	}
}
