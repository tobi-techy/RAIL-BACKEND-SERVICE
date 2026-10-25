package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

// sampleTurnResponse is the contract's own payment-claim example.
const sampleTurnResponse = `{
  "schema_version": "turns.v1",
  "turn_id": "turn_ed014cc40e394cfe2114d730",
  "trace_id": "tr_example",
  "message_id": "wamid.HBgN001",
  "label": "payment_claim",
  "confidence": 0.9,
  "needs_clarification": false,
  "reply": {"text": "Thank you. I go record say you don send N25,000.", "language": "pcm"},
  "intents": [
    {
      "intent_id": "int_f079100483cf299be4f36633",
      "status": "proposed",
      "source": "text",
      "confidence": 0.9,
      "evidence": "25k",
      "source_read_id": null,
      "evidence_media_ids": [],
      "kind": "record_payment_claim",
      "amount_kobo": 2500000,
      "currency": "NGN",
      "reference": null
    },
    {
      "intent_id": "int_stock_1",
      "status": "proposed",
      "source": "text",
      "confidence": 0.8,
      "evidence": "indomie 5 carton",
      "source_read_id": null,
      "evidence_media_ids": [],
      "kind": "record_stock_count",
      "item": "Indomie",
      "quantity": "5",
      "unit": "carton"
    }
  ],
  "drafts": [
    {
      "draft_id": "drf_664d4df6815c213c8bb745f8",
      "kind": "order_request",
      "audience": "owner",
      "text": "Order request from Mama Bisi: Indomie 10 cartons.",
      "lines": [{"item": "Indomie", "quantity": "10", "unit": "carton"}],
      "needs_approval": true
    }
  ],
  "flags": ["parser:rules"],
  "replayed": false
}`

func sampleTurnRequest() *TurnRequest {
	return &TurnRequest{
		TenantID:        "dist_ade",
		ShopID:          "shop_mama_bisi",
		Sender:          TurnSender{ID: "person_bisi", Handle: "+2348030000000", DisplayName: "Mama Bisi", Role: "shop_owner"},
		MessageID:       "wamid.HBgN001",
		Text:            "I don pay 25k abeg check",
		SentAt:          "2026-10-25T08:00:00+01:00",
		LocaleHints:     []string{"pcm", "en"},
		DistributorName: "Ade Distributors",
	}
}

type capturedTurn struct {
	path    string
	headers http.Header
	body    []byte
}

// turnsServer answers each call with the next handler in steps (the last one
// repeats) and records what Go sent.
func turnsServer(t *testing.T, steps ...func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]capturedTurn) {
	t.Helper()
	var mu sync.Mutex
	var calls []capturedTurn
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		calls = append(calls, capturedTurn{path: r.URL.Path, headers: r.Header.Clone(), body: body})
		mu.Unlock()
		i := int(n.Add(1)) - 1
		if i >= len(steps) {
			i = len(steps) - 1
		}
		steps[i](w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// writeTestBody writes a canned body; a failed write only shows up as a
// failed decode on the client side, which the test then reports.
func writeTestBody(w io.Writer, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}

func okTurn(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	writeTestBody(w, sampleTurnResponse)
}

func statusTurn(code int) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		writeTestBody(w, `{"detail":"nope","trace_id":"tr_err"}`)
	}
}

func newTestTurnsClient(url string) *MimiTurnsClient {
	c := NewMimiTurnsClient(MimiTurnsClientConfig{
		BaseURL:        url,
		RailServiceKey: "svc-key",
		TextTimeout:    2 * time.Second,
		MediaTimeout:   3 * time.Second,
	}, zap.NewNop())
	c.backoff = time.Millisecond
	return c
}

func TestTurnPostsTheContractRequest(t *testing.T) {
	srv, calls := turnsServer(t, okTurn)
	c := newTestTurnsClient(srv.URL)

	resp, err := c.Turn(context.Background(), sampleTurnRequest(), "tr_go_1")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("want one call, got %d", len(*calls))
	}
	assertTurnHeaders(t, (*calls)[0])
	assertTurnBodyFields(t, (*calls)[0].body)
	assertDecodedSample(t, resp)
}

func assertTurnHeaders(t *testing.T, got capturedTurn) {
	t.Helper()
	if got.path != "/v1/turns" {
		t.Fatalf("path = %q, want /v1/turns", got.path)
	}
	if got.headers.Get("X-Rail-Service-Key") != "svc-key" {
		t.Fatalf("missing service key header")
	}
	if got.headers.Get("X-Miriam-Trace-Id") != "tr_go_1" {
		t.Fatalf("trace id header = %q", got.headers.Get("X-Miriam-Trace-Id"))
	}
	if got.headers.Get("Authorization") != "" {
		t.Fatalf("/v1/turns is service-only; no user JWT may be sent")
	}
	if ct := got.headers.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
}

func assertTurnBodyFields(t *testing.T, raw []byte) {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["schema_version"] != "turns.v1" || body["channel"] != "whatsapp" {
		t.Fatalf("schema_version/channel defaults not applied: %v", body)
	}
	allowed := map[string]bool{
		"schema_version": true, "tenant_id": true, "shop_id": true, "sender": true, "channel": true,
		"message_id": true, "text": true, "media": true, "sent_at": true, "locale_hints": true, "distributor_name": true,
	}
	for k := range body {
		if !allowed[k] {
			t.Fatalf("field %q is not in turns.v1 (Python refuses unknown fields)", k)
		}
	}
	if _, ok := body["media"]; ok {
		t.Fatalf("empty media must be omitted")
	}
}

func assertDecodedSample(t *testing.T, resp *TurnResponse) {
	t.Helper()
	if resp.TurnID != "turn_ed014cc40e394cfe2114d730" || resp.Label != "payment_claim" || resp.Replayed {
		t.Fatalf("decoded response wrong: %+v", resp)
	}
	if resp.Reply == nil || resp.Reply.Language != "pcm" {
		t.Fatalf("reply not decoded: %+v", resp.Reply)
	}
	if len(resp.Intents) != 2 || len(resp.Drafts) != 1 {
		t.Fatalf("want 2 intents and 1 draft, got %d and %d", len(resp.Intents), len(resp.Drafts))
	}
	assertSampleIntents(t, resp)
}

func assertSampleIntents(t *testing.T, resp *TurnResponse) {
	t.Helper()
	pay := resp.Intents[0]
	if pay.Kind != TurnIntentRecordPaymentClaim || pay.AmountKobo != 2500000 || pay.Currency != "NGN" || !pay.TouchesMoney() {
		t.Fatalf("payment intent wrong: %+v", pay)
	}
	if !strings.Contains(string(pay.Raw), `"amount_kobo": 2500000`) {
		t.Fatalf("raw intent bytes must be kept verbatim, got %s", pay.Raw)
	}
	stock := resp.Intents[1]
	if stock.Kind != TurnIntentRecordStockCount || stock.Quantity != "5" || stock.Unit != "carton" || stock.TouchesMoney() {
		t.Fatalf("stock intent wrong: %+v", stock)
	}
	d := resp.Drafts[0]
	if !d.NeedsApproval || d.Audience != "owner" || len(d.Lines) != 1 || d.Lines[0].Quantity == nil || *d.Lines[0].Quantity != "10" {
		t.Fatalf("draft wrong: %+v", d)
	}
}

func TestTurnRetriesWithTheSameBodyOn5xx(t *testing.T) {
	srv, calls := turnsServer(t, statusTurn(http.StatusInternalServerError), okTurn)
	c := newTestTurnsClient(srv.URL)

	if _, err := c.Turn(context.Background(), sampleTurnRequest(), ""); err != nil {
		t.Fatalf("turn: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("want a retry after the 500, got %d calls", len(*calls))
	}
	if string((*calls)[0].body) != string((*calls)[1].body) {
		t.Fatalf("a retry must send the identical body (idempotency on message_id):\n%s\n%s", (*calls)[0].body, (*calls)[1].body)
	}
}

func TestTurnRetriesWithTheSameBodyAfterATimeout(t *testing.T) {
	slow := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}
	srv, calls := turnsServer(t, slow, okTurn)
	c := newTestTurnsClient(srv.URL)
	c.textTimeout = 100 * time.Millisecond

	resp, err := c.Turn(context.Background(), sampleTurnRequest(), "")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if resp.MessageID != "wamid.HBgN001" {
		t.Fatalf("message id = %q", resp.MessageID)
	}
	if len(*calls) != 2 || string((*calls)[0].body) != string((*calls)[1].body) {
		t.Fatalf("want two calls with one body, got %d", len(*calls))
	}
}

func TestTurnGivesUpAfterTheAttemptsWithUnavailable(t *testing.T) {
	srv, calls := turnsServer(t, statusTurn(http.StatusBadGateway))
	c := newTestTurnsClient(srv.URL)

	_, err := c.Turn(context.Background(), sampleTurnRequest(), "")
	if !errors.Is(err, ErrTurnUnavailable) {
		t.Fatalf("want ErrTurnUnavailable, got %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("text turns get 2 attempts, got %d", len(*calls))
	}
}

func TestTurnConflictIsNotRetried(t *testing.T) {
	srv, calls := turnsServer(t, statusTurn(http.StatusConflict))
	c := newTestTurnsClient(srv.URL)

	_, err := c.Turn(context.Background(), sampleTurnRequest(), "")
	if !errors.Is(err, ErrTurnConflict) {
		t.Fatalf("want ErrTurnConflict, got %v", err)
	}
	var herr *TurnHTTPError
	if !errors.As(err, &herr) || herr.TraceID != "tr_err" || herr.Detail != "nope" {
		t.Fatalf("error body not carried: %#v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("a 409 must not be retried, got %d calls", len(*calls))
	}
}

func TestTurn4xxIsNotRetried(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusUnprocessableEntity, http.StatusNotFound} {
		srv, calls := turnsServer(t, statusTurn(code))
		c := newTestTurnsClient(srv.URL)
		_, err := c.Turn(context.Background(), sampleTurnRequest(), "")
		if !errors.Is(err, ErrTurnRejected) {
			t.Fatalf("%d: want ErrTurnRejected, got %v", code, err)
		}
		if len(*calls) != 1 {
			t.Fatalf("%d: want one call, got %d", code, len(*calls))
		}
	}
}

func TestTurnRejectsAResponseForAnotherMessage(t *testing.T) {
	other := func(w http.ResponseWriter, _ *http.Request) {
		writeTestBody(w, strings.Replace(sampleTurnResponse, "wamid.HBgN001", "wamid.OTHER", 1))
	}
	srv, _ := turnsServer(t, other)
	c := newTestTurnsClient(srv.URL)
	if _, err := c.Turn(context.Background(), sampleTurnRequest(), ""); !errors.Is(err, ErrTurnRejected) {
		t.Fatalf("want ErrTurnRejected for a mismatched message_id, got %v", err)
	}
}

func TestTurnValidatesBeforeSending(t *testing.T) {
	srv, calls := turnsServer(t, okTurn)
	c := newTestTurnsClient(srv.URL)
	cases := map[string]func(r *TurnRequest){
		"no message id": func(r *TurnRequest) { r.MessageID = "" },
		"no tenant":     func(r *TurnRequest) { r.TenantID = "" },
		"no shop":       func(r *TurnRequest) { r.ShopID = " " },
		"no sender":     func(r *TurnRequest) { r.Sender.ID = "" },
		"empty turn":    func(r *TurnRequest) { r.Text = "  " },
		"bad version":   func(r *TurnRequest) { r.SchemaVersion = "turns.v2" },
		"bad channel":   func(r *TurnRequest) { r.Channel = "imessage" },
		"long text":     func(r *TurnRequest) { r.Text = strings.Repeat("a", 4001) },
		"too much media": func(r *TurnRequest) {
			for i := 0; i < 6; i++ {
				r.Media = append(r.Media, TurnMedia{ID: "m", Kind: "image"})
			}
		},
	}
	for name, mutate := range cases {
		req := sampleTurnRequest()
		mutate(req)
		if _, err := c.Turn(context.Background(), req, ""); err == nil {
			t.Fatalf("%s: want a validation error", name)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid requests must not reach the brain, got %d calls", len(*calls))
	}
}

func TestTurnNeedsAServiceKey(t *testing.T) {
	c := NewMimiTurnsClient(MimiTurnsClientConfig{BaseURL: "http://localhost:1"}, zap.NewNop())
	if c.Configured() {
		t.Fatal("a client without RAIL_SERVICE_KEY must not report configured")
	}
	if _, err := c.Turn(context.Background(), sampleTurnRequest(), ""); err == nil {
		t.Fatal("want an error without a service key")
	}
}

func TestTurnMediaUsesTheLongerBudgetAndOneAttempt(t *testing.T) {
	c := NewMimiTurnsClient(MimiTurnsClientConfig{BaseURL: "http://x", RailServiceKey: "k"}, zap.NewNop())
	text := sampleTurnRequest()
	photo := sampleTurnRequest()
	photo.Media = []TurnMedia{{ID: "m1", Kind: "image", MIME: "image/jpeg", DataB64: "aGk="}}
	if c.TimeoutFor(text) != DefaultTurnsTextTimeout {
		t.Fatalf("text budget = %v", c.TimeoutFor(text))
	}
	if c.TimeoutFor(photo) != DefaultTurnsMediaTimeout || DefaultTurnsMediaTimeout <= 140*time.Second {
		t.Fatalf("media budget must exceed the brain's 140s reader wall clock, got %v", c.TimeoutFor(photo))
	}
	if c.attemptsFor(photo) != 1 || c.attemptsFor(text) != 2 {
		t.Fatalf("attempts: photo %d text %d", c.attemptsFor(photo), c.attemptsFor(text))
	}

	// A media turn that 5xxs is not re-read inside the same request.
	srv, calls := turnsServer(t, statusTurn(http.StatusInternalServerError))
	c2 := newTestTurnsClient(srv.URL)
	if _, err := c2.Turn(context.Background(), photo, ""); !errors.Is(err, ErrTurnUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("media turns get one attempt per request, got %d", len(*calls))
	}
	var sent TurnRequest
	if err := json.Unmarshal((*calls)[0].body, &sent); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(sent.Media) != 1 || sent.Media[0].DataB64 != "aGk=" {
		t.Fatalf("media must be sent inline as data_b64, got %+v", sent.Media)
	}
}

func TestTurnReplayIsDecoded(t *testing.T) {
	replay := func(w http.ResponseWriter, _ *http.Request) {
		writeTestBody(w, strings.Replace(sampleTurnResponse, `"replayed": false`, `"replayed": true`, 1))
	}
	srv, _ := turnsServer(t, replay)
	c := newTestTurnsClient(srv.URL)
	resp, err := c.Turn(context.Background(), sampleTurnRequest(), "")
	if err != nil || !resp.Replayed {
		t.Fatalf("want a decoded replay, got %+v %v", resp, err)
	}
}
