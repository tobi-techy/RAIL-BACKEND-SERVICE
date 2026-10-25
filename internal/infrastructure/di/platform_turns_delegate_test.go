package di

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// A fake Python brain that serves both the legacy /api/v1/chat and the new
// /v1/turns, so a test can see which one Go called.
// ---------------------------------------------------------------------------

type brainCall struct {
	path    string
	headers http.Header
	body    []byte
}

type fakeBrain struct {
	mu    sync.Mutex
	calls []brainCall
	turns string // body for /v1/turns
}

func (b *fakeBrain) paths() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.calls))
	for _, c := range b.calls {
		out = append(out, c.path)
	}
	return out
}

func (b *fakeBrain) last() brainCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[len(b.calls)-1]
}

func serveBrain(t *testing.T, turnsBody string) (*httptest.Server, *fakeBrain) {
	t.Helper()
	b := &fakeBrain{turns: turnsBody}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		b.mu.Lock()
		b.calls = append(b.calls, brainCall{path: r.URL.Path, headers: r.Header.Clone(), body: body})
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/turns":
			writeBrainBody(w, b.turns)
		case "/api/v1/chat":
			writeBrainBody(w, `{"response":"legacy brain here","conversation_id":"c1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, b
}

func writeBrainBody(w io.Writer, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}

func turnsBodyFor(messageID string) string {
	return `{
	  "schema_version": "turns.v1", "turn_id": "turn_1", "trace_id": "tr_1",
	  "message_id": "` + messageID + `", "label": "payment_claim", "confidence": 0.9,
	  "needs_clarification": false,
	  "reply": {"text": "Thank you. I go record am as reported.", "language": "pcm"},
	  "intents": [{"intent_id": "int_pay_1", "status": "proposed", "source": "text",
	    "confidence": 0.9, "evidence": "25k", "source_read_id": null, "evidence_media_ids": [],
	    "kind": "record_payment_claim", "amount_kobo": 2500000, "currency": "NGN", "reference": null}],
	  "drafts": [], "flags": ["parser:rules"], "replayed": false
	}`
}

// ---------------------------------------------------------------------------
// A tiny database/sql driver so the legacy path's conversation lookup returns
// a row without a database: every query yields one id column.
// ---------------------------------------------------------------------------

type rowDriver struct{}
type rowConn struct{}
type rowStmt struct{}
type oneRow struct{ done bool }

func (rowDriver) Open(string) (driver.Conn, error)         { return rowConn{}, nil }
func (rowConn) Prepare(string) (driver.Stmt, error)        { return rowStmt{}, nil }
func (rowConn) Close() error                               { return nil }
func (rowConn) Begin() (driver.Tx, error)                  { return nil, driver.ErrSkip }
func (rowStmt) Close() error                               { return nil }
func (rowStmt) NumInput() int                              { return -1 }
func (rowStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(1), nil }
func (rowStmt) Query([]driver.Value) (driver.Rows, error)  { return &oneRow{}, nil }
func (*oneRow) Columns() []string                          { return []string{"id"} }
func (*oneRow) Close() error                               { return nil }
func (r *oneRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = "7d0b3c3e-6a37-4f3e-9d34-111111111111"
	return nil
}

var registerRowDriver sync.Once

func fakeConvRepo(t *testing.T) *repositories.ConversationRepository {
	t.Helper()
	registerRowDriver.Do(func() { sql.Register("turns_test_rows", rowDriver{}) })
	db, err := sql.Open("turns_test_rows", "")
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("close fake db: %v", err)
		}
	})
	return repositories.NewConversationRepository(db, zap.NewNop())
}

func newRoutingAdapter(t *testing.T, srvURL string, turnsOn bool, store ai.ProposalStore) *orchestratorAdapter {
	t.Helper()
	return &orchestratorAdapter{
		convRepo:     fakeConvRepo(t),
		logger:       zap.NewNop(),
		python:       ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srvURL, JWTSecret: "t"}, zap.NewNop()),
		confirmStore: ai.NewConfirmStore(newFakeConfirmRedis(), time.Minute, zap.NewNop()),
		turns: &turnsRouting{
			enabled:         turnsOn,
			tenantID:        "dist_pilot",
			distributorName: "Ade Distributors",
			proposals:       store,
			client: ai.NewMimiTurnsClient(ai.MimiTurnsClientConfig{
				BaseURL: srvURL, RailServiceKey: "svc-key", TextTimeout: 2 * time.Second,
			}, zap.NewNop()),
		},
	}
}

func inboundCtx(msgID, text string) context.Context {
	return platform.ContextWithInboundTurn(context.Background(), platform.InboundTurn{
		Platform:   entities.PlatformWhatsApp,
		MsgID:      msgID,
		ThreadID:   "thread-1",
		SenderID:   "+2348030000000",
		Text:       text,
		ReceivedAt: time.Date(2026, 10, 25, 7, 0, 0, 0, time.UTC),
	})
}

func TestFlagOffKeepsTheLegacyChatPath(t *testing.T) {
	srv, brain := serveBrain(t, turnsBodyFor("wamid.1"))
	a := newRoutingAdapter(t, srv.URL, false, ai.NewMemoryProposalStore())
	uid := uuid.New()

	reply, err := a.HandlePlatformMessage(inboundCtx("wamid.1", "I don pay 25k"), uid.String(), uuid.NewString(), "I don pay 25k", "thread-1", entities.PlatformWhatsApp)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := brain.paths(); len(got) != 1 || got[0] != "/api/v1/chat" {
		t.Fatalf("flag off must call only /api/v1/chat, got %v", got)
	}
	if reply == nil || reply.Text != "legacy brain here" {
		t.Fatalf("reply = %+v", reply)
	}
	if !a.WantsStatementScan(context.Background(), uid) {
		t.Fatal("flag off keeps the statement scan fast path")
	}
}

func TestFlagOnRoutesToTurnsAndStoresProposals(t *testing.T) {
	srv, brain := serveBrain(t, turnsBodyFor("wamid.HBgN001"))
	store := ai.NewMemoryProposalStore()
	a := newRoutingAdapter(t, srv.URL, true, store)
	uid := uuid.New()

	// The message arg is what the legacy path would see (a quoted reply folded
	// in); the turns path must send the shop's own words from the inbound turn.
	reply, err := a.HandlePlatformMessage(inboundCtx("wamid.HBgN001", "I don pay 25k"), uid.String(), uuid.NewString(),
		"[replying to your earlier message: \"Send N5,000?\"]\nI don pay 25k", "thread-1", entities.PlatformWhatsApp)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := brain.paths(); len(got) != 1 || got[0] != "/v1/turns" {
		t.Fatalf("flag on must call only /v1/turns, got %v", got)
	}
	assertTurnCall(t, brain.last(), uid)
	if reply == nil || !strings.Contains(reply.Text, "reported") || reply.Confirm != nil || reply.Poll != nil {
		t.Fatalf("reply must be the brain's text only, got %+v", reply)
	}
	all := store.All()
	if len(all) != 1 || all[0].ProposalID != "int_pay_1" || all[0].Status != ai.ProposalStatusHeldMoney {
		t.Fatalf("the payment claim must be stored as a held proposal, got %+v", all)
	}

	// The same message again (bridge retry): one more call with the same
	// message id, and no new proposal.
	if _, err := a.HandlePlatformMessage(inboundCtx("wamid.HBgN001", "I don pay 25k"), uid.String(), uuid.NewString(), "I don pay 25k", "thread-1", entities.PlatformWhatsApp); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(store.All()) != 1 {
		t.Fatalf("a replayed intent_id must not be stored twice, got %d", len(store.All()))
	}
}

func assertTurnCall(t *testing.T, call brainCall, uid uuid.UUID) {
	t.Helper()
	if call.headers.Get("X-Rail-Service-Key") != "svc-key" || call.headers.Get("Authorization") != "" {
		t.Fatalf("turns is service-auth only: %v", call.headers)
	}
	var sent ai.TurnRequest
	if err := json.Unmarshal(call.body, &sent); err != nil {
		t.Fatalf("body: %v", err)
	}
	if sent.MessageID != "wamid.HBgN001" {
		t.Fatalf("the channel message id is the idempotency key, got %q", sent.MessageID)
	}
	if sent.Text != "I don pay 25k" {
		t.Fatalf("turns must send the shop's own words, got %q", sent.Text)
	}
	if sent.TenantID != "dist_pilot" || sent.ShopID != "shop_"+uid.String() || sent.Sender.ID != uid.String() {
		t.Fatalf("identity must come from Go's records: %+v", sent)
	}
	if sent.Sender.Handle != "+2348030000000" || sent.DistributorName != "Ade Distributors" || sent.SentAt == "" {
		t.Fatalf("sender/handle/distributor/sent_at not filled: %+v", sent)
	}
}

func TestFlagOnTurnsOffTheLegacyOnlyPaths(t *testing.T) {
	srv, brain := serveBrain(t, turnsBodyFor("x"))
	a := newRoutingAdapter(t, srv.URL, true, ai.NewMemoryProposalStore())
	uid := uuid.New()
	ctx := context.Background()

	if _, handled, err := a.HandlePlatformPollVote(ctx, uid.String(), uuid.NewString(), "thread-1", entities.PlatformWhatsApp, "Yes", "Set it up?"); handled || err != nil {
		t.Fatalf("poll votes are not forwarded with turns on (handled=%v err=%v)", handled, err)
	}
	if _, handled, err := a.HandlePlatformDocument(ctx, uid.String(), uuid.NewString(), "thread-1", entities.PlatformWhatsApp, platform.StatementScan{Summary: "x"}); handled || err != nil {
		t.Fatalf("statement scans are not forwarded with turns on (handled=%v err=%v)", handled, err)
	}
	if a.WantsStatementScan(ctx, uid) {
		t.Fatal("no blocking statement scan with turns on")
	}
	cid := uuid.New()
	if err := a.confirmStore.Put(ctx, cid, "confirm_old"); err != nil {
		t.Fatalf("put: %v", err)
	}
	reply, err := a.settlePythonConfirm(ctx, uid, cid, "thread-1", entities.PlatformWhatsApp, "confirm_old", true)
	if err != nil || reply == nil || reply.Text != turnsExpiredConfirmReply {
		t.Fatalf("a legacy confirm after the flip is closed, not sent: %+v %v", reply, err)
	}
	if _, ok := a.pendingPythonConfirm(ctx, cid); ok {
		t.Fatal("the stale confirm must be dropped")
	}
	if got := brain.paths(); len(got) != 0 {
		t.Fatalf("no Python call expected, got %v", got)
	}
}

func TestFlagOnWithoutATenantDoesNotGuessAShop(t *testing.T) {
	srv, brain := serveBrain(t, turnsBodyFor("wamid.1"))
	a := newRoutingAdapter(t, srv.URL, true, ai.NewMemoryProposalStore())
	a.turns.tenantID = ""

	reply, err := a.HandlePlatformMessage(inboundCtx("wamid.1", "hi"), uuid.NewString(), uuid.NewString(), "hi", "thread-1", entities.PlatformWhatsApp)
	if err != nil || reply == nil || reply.Text != turnsNotSetUpReply {
		t.Fatalf("want the not-set-up reply, got %+v %v", reply, err)
	}
	if len(brain.paths()) != 0 {
		t.Fatalf("the brain must not be called with a guessed shop, got %v", brain.paths())
	}
}

func TestFlagOnBrainDownGivesAnApologyAndStoresNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	store := ai.NewMemoryProposalStore()
	a := newRoutingAdapter(t, srv.URL, true, store)

	reply, err := a.HandlePlatformMessage(inboundCtx("wamid.1", "hi"), uuid.NewString(), uuid.NewString(), "hi", "thread-1", entities.PlatformWhatsApp)
	if err != nil || reply == nil || reply.Text != turnsUnavailableReply {
		t.Fatalf("want the unavailable reply, got %+v %v", reply, err)
	}
	if len(store.All()) != 0 {
		t.Fatal("nothing to store")
	}
}

func TestTurnRequestCarriesAPhotoInline(t *testing.T) {
	in := platform.InboundTurn{MsgID: "wamid.P", Text: "stock book today", ImageB64: "aGVsbG8=", ImageMime: "image/png", SenderID: "+234"}
	req := buildTurnRequest(in, true, "OCR SUMMARY: total 5,000", "t", "s", "u", "", entities.PlatformWhatsApp, "th", zap.NewNop())
	if len(req.Media) != 1 || req.Media[0].DataB64 != "aGVsbG8=" || req.Media[0].MIME != "image/png" || req.Media[0].Kind != "image" {
		t.Fatalf("photo must be inline: %+v", req.Media)
	}
	if req.Text != "stock book today" || req.Media[0].Caption != "stock book today" {
		t.Fatalf("the legacy OCR summary must not be sent as the shop's words: %q", req.Text)
	}
	if !req.HasMedia() {
		t.Fatal("photo turns use the media budget")
	}
}

func TestTurnRequestSendsAVoiceNoteAsATranscript(t *testing.T) {
	in := platform.InboundTurn{MsgID: "wamid.V", Text: "I don pay 25k", IsVoice: true}
	req := buildTurnRequest(in, true, "I don pay 25k", "t", "s", "u", "", entities.PlatformWhatsApp, "th", zap.NewNop())
	if req.Text != "" || len(req.Media) != 1 || req.Media[0].Kind != "audio" || req.Media[0].Transcript != "I don pay 25k" {
		t.Fatalf("voice: %+v", req)
	}
	if err := ai.ValidateTurnRequest(req); err != nil {
		t.Fatalf("a transcript-only turn is valid: %v", err)
	}
}

func TestTurnsMessageIDFallbacks(t *testing.T) {
	a := turnsMessageID(platform.InboundTurn{TurnID: "batch-1", SenderID: "+234"}, entities.PlatformWhatsApp, "th", "hi", zap.NewNop())
	b := turnsMessageID(platform.InboundTurn{TurnID: "batch-1", SenderID: "+234"}, entities.PlatformWhatsApp, "th", "hi", zap.NewNop())
	if a != b || !strings.HasPrefix(a, "go_") {
		t.Fatalf("a bridge turn id must give a stable message id: %q %q", a, b)
	}
	if c := turnsMessageID(platform.InboundTurn{MsgID: "wamid.X"}, entities.PlatformWhatsApp, "th", "hi", nil); c != "wamid.X" {
		t.Fatalf("msg id wins, got %q", c)
	}
}

func TestGuestTurnsOnAnswersWithoutCallingTheBrain(t *testing.T) {
	srv, brain := serveBrain(t, turnsBodyFor("x"))
	g := &pythonGuestCompleterAdapter{
		python:       ai.NewPythonAgentClient(ai.PythonAgentClientConfig{BaseURL: srv.URL, JWTSecret: "t"}, zap.NewNop()),
		logger:       zap.NewNop(),
		turnsEnabled: true,
	}
	ctx := platform.ContextWithGuestSender(context.Background(), platform.GuestSender{Platform: entities.PlatformWhatsApp, SenderID: "+234", ThreadID: "th"})
	res, err := g.CompleteGuest(ctx, "", []platform.GuestMessage{{Role: "user", Content: "hello"}}, nil)
	if err != nil || res == nil || res.Text != turnsUnlinkedGuestReply || len(res.ToolCalls) != 0 {
		t.Fatalf("guest with turns on: %+v %v", res, err)
	}
	if len(brain.paths()) != 0 {
		t.Fatalf("an unlinked sender is not tied to a shop; no brain call expected, got %v", brain.paths())
	}
}
