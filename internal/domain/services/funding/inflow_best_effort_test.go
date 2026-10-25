package funding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// pythonDown stands in for the Python brain after MIRIAM PR #51: the inflow
// endpoint answers with the given status, or hangs when status is 0.
func pythonDown(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status == 0 {
			select {
			case <-r.Context().Done():
			case <-time.After(1500 * time.Millisecond):
			}
			return
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(`{"detail":"Not Found"}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func realInflowClient(url string) *ai.PythonAgentClient {
	return ai.NewPythonAgentClient(ai.PythonAgentClientConfig{
		BaseURL:   url,
		JWTSecret: "test-secret",
		JWTTTL:    time.Minute,
		Timeout:   30 * time.Second,
	}, zap.NewNop())
}

func shortBudget(t *testing.T, d time.Duration) {
	t.Helper()
	old := inflowNotifyBudget
	inflowNotifyBudget = d
	t.Cleanup(func() { inflowNotifyBudget = old })
}

func TestNGNDepositSucceedsWhenPythonInflowFails(t *testing.T) {
	shortBudget(t, 300*time.Millisecond)
	for name, status := range map[string]int{"404": http.StatusNotFound, "500": http.StatusInternalServerError, "503": http.StatusServiceUnavailable, "timeout": 0} {
		t.Run(name, func(t *testing.T) {
			srv, calls := pythonDown(t, status)
			va := newFakeVARepo()
			seedNGNAccount(va)
			dr := newFakeDepositRepo()
			ledger := &fakeLedger{}
			gc := &fakeGraphClient{conversion: &graph.Conversion{TargetAmount: "100", Status: "completed"}}
			svc := newTestService(gc, va, dr, ledger, &fakeAllocation{})
			svc.SetInflowNotifier(realInflowClient(srv.URL))

			event := &GraphNGNDepositEvent{GraphAccountID: "acct_1", TransactionID: "tx_" + name, AmountNGN: "160000", Direction: "credit"}
			start := time.Now()
			if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
				t.Fatalf("a failed python report must not fail the deposit: %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("the report must be bounded by the budget, took %v", elapsed)
			}
			if calls.Load() == 0 {
				t.Fatal("the report should still be attempted when the notifier is wired")
			}
			if ledger.calls != 1 {
				t.Fatalf("the credit must be booked exactly once, got %d", ledger.calls)
			}
			// A provider replay of the same webhook is also a success.
			if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
				t.Fatalf("replay must not fail on the report either: %v", err)
			}
			if ledger.calls != 1 {
				t.Fatalf("a replay must not book again, got %d", ledger.calls)
			}
		})
	}
}

func TestFiatInflowReportErrorIsOnlyForLogging(t *testing.T) {
	shortBudget(t, 300*time.Millisecond)
	srv, _ := pythonDown(t, 0)
	start := time.Now()
	err := notifyFiatInflow(context.Background(), realInflowClient(srv.URL), uuid.New(), "idem", decimal.NewFromInt(5000), "NGN", "fiat deposit d1", "d1")
	if err == nil {
		t.Fatal("the helper reports the failure so the caller can log it")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("budget not applied: %v", time.Since(start))
	}
}
