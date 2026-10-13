package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// inflowServer stands in for the Python agent's /api/v1/money/inflow.
func inflowServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func clientFor(t *testing.T, srv *httptest.Server) *PythonAgentClient {
	t.Helper()
	return NewPythonAgentClient(PythonAgentClientConfig{
		BaseURL:   srv.URL,
		JWTSecret: "test-secret",
	}, zap.NewNop())
}

func TestNotifyInflowPostsTheCreditID(t *testing.T) {
	var got PythonInflow
	var path string
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read inflow body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode inflow: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	c := clientFor(t, srv)

	userID := uuid.New()
	if err := c.NotifyInflow(context.Background(), userID, "graph-tx-1", "420000", "NGN", "ACME PAYROLL", entities.InflowIdentity{}); err != nil {
		t.Fatalf("NotifyInflow: %v", err)
	}

	if path != "/api/v1/money/inflow" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	// The credit's own idempotency key travels as payment_id, which is what makes
	// a re-delivered webhook split once.
	if got.PaymentID != "graph-tx-1" {
		t.Fatalf("want the credit id, got %q", got.PaymentID)
	}
	if got.Amount != "420000" {
		t.Fatalf("want the amount as a string, got %q", got.Amount)
	}
	if got.SourceRaw != "ACME PAYROLL" {
		t.Fatalf("want the raw source, got %q", got.SourceRaw)
	}
}

func TestNotifyInflowSkipsNonNGN(t *testing.T) {
	// Miriam's ledger is single-currency, so a USDC credit must not be posted
	// into it: it would misstate the balance she spends from.
	var hits atomic.Int32
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	c := clientFor(t, srv)

	for _, currency := range []string{"USD", "USDC", "usd", "GBP"} {
		if err := c.NotifyInflow(context.Background(), uuid.New(), "key-1", "25", currency, "chain deposit", entities.InflowIdentity{}); err != nil {
			t.Fatalf("a skipped credit must not error (%s): %v", currency, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a non-NGN credit must not reach Python, got %d calls", hits.Load())
	}
}

func TestNotifyInflowRetriesA503(t *testing.T) {
	// 503 is Python saying "my ledger store is down", which is usually momentary.
	var hits atomic.Int32
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	c := clientFor(t, srv)

	if err := c.NotifyInflow(context.Background(), uuid.New(), "key-1", "420000", "NGN", "payroll", entities.InflowIdentity{}); err != nil {
		t.Fatalf("a retried 503 should end in success: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("want three attempts, got %d", hits.Load())
	}
}

func TestNotifyInflowGivesUpOnAPersistent503(t *testing.T) {
	// When it stays down the error goes back to the caller, whose own retry
	// (the provider's webhook retry) is the outer loop.
	var hits atomic.Int32
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c := clientFor(t, srv)

	err := c.NotifyInflow(context.Background(), uuid.New(), "key-1", "420000", "NGN", "payroll", entities.InflowIdentity{})
	if err == nil {
		t.Fatal("a persistent 503 must surface as an error so the caller retries")
	}
	if hits.Load() != inflowRetries {
		t.Fatalf("want %d attempts, got %d", inflowRetries, hits.Load())
	}
}

func TestNotifyInflowDoesNotRetryA4xx(t *testing.T) {
	// A bad request stays bad; retrying it just makes the outage longer.
	var hits atomic.Int32
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	})
	c := clientFor(t, srv)

	if err := c.NotifyInflow(context.Background(), uuid.New(), "key-1", "420000", "NGN", "payroll", entities.InflowIdentity{}); err == nil {
		t.Fatal("a 4xx must surface as an error")
	}
	if hits.Load() != 1 {
		t.Fatalf("a 4xx must not be retried, got %d attempts", hits.Load())
	}
}

func TestNotifyInflowNeedsAnIDAndAnAmount(t *testing.T) {
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a malformed notification must not be posted")
		w.WriteHeader(http.StatusOK)
	})
	c := clientFor(t, srv)

	if err := c.NotifyInflow(context.Background(), uuid.New(), "", "420000", "NGN", "", entities.InflowIdentity{}); err == nil {
		t.Fatal("no payment id means no idempotency key")
	}
	if err := c.NotifyInflow(context.Background(), uuid.New(), "key-1", "", "NGN", "", entities.InflowIdentity{}); err == nil {
		t.Fatal("no amount means nothing to record")
	}
}

func captureInflowBody(t *testing.T, identity entities.InflowIdentity) []byte {
	t.Helper()
	var raw []byte
	srv := inflowServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read inflow body: %v", err)
		}
		raw = body
		w.WriteHeader(http.StatusOK)
	})
	c := clientFor(t, srv)
	if err := c.NotifyInflow(context.Background(), uuid.New(), "idem-key-1", "420000", "NGN", "fiat deposit dep_123", identity); err != nil {
		t.Fatalf("NotifyInflow: %v", err)
	}
	return raw
}

func TestNotifyInflowSendsTheProvidersReference(t *testing.T) {
	raw := captureInflowBody(t, entities.InflowIdentity{
		Channel:   entities.InflowChannelBridge,
		Sender:    "ACME PAYROLL LTD",
		Reference: "dep_123",
	})

	want := `{"payment_id":"idem-key-1","amount":"420000","source_raw":"fiat deposit dep_123","channel":"bridge","sender":"ACME PAYROLL LTD","reference":"dep_123"}`
	if string(raw) != want {
		t.Fatalf("body mismatch\n got: %s\nwant: %s", raw, want)
	}
}

func TestNotifyInflowOmitsFieldsTheProviderDidNotSupply(t *testing.T) {
	raw := captureInflowBody(t, entities.InflowIdentity{
		Channel:   entities.InflowChannelGraph,
		Reference: "tx_777",
	})

	want := `{"payment_id":"idem-key-1","amount":"420000","source_raw":"fiat deposit dep_123","channel":"graph","reference":"tx_777"}`
	if string(raw) != want {
		t.Fatalf("body mismatch\n got: %s\nwant: %s", raw, want)
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := fields["sender"]; ok {
		t.Fatalf("sender must be absent when the provider gave none, got %v", fields["sender"])
	}
}

func TestNotifyInflowWithoutAnIdentityIsByteIdenticalToTheOldBody(t *testing.T) {
	raw := captureInflowBody(t, entities.InflowIdentity{})

	want := `{"payment_id":"idem-key-1","amount":"420000","source_raw":"fiat deposit dep_123"}`
	if string(raw) != want {
		t.Fatalf("body mismatch\n got: %s\nwant: %s", raw, want)
	}
}

func TestNotifyInflowNeverCopiesThePaymentIDIntoTheReference(t *testing.T) {
	raw := captureInflowBody(t, entities.InflowIdentity{Channel: entities.InflowChannelP2P})

	var got PythonInflow
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PaymentID != "idem-key-1" {
		t.Fatalf("payment_id changed: %q", got.PaymentID)
	}
	if got.Reference != "" {
		t.Fatalf("reference must stay empty when the provider gave none, got %q", got.Reference)
	}
}
