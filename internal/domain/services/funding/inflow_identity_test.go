package funding

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	"github.com/shopspring/decimal"
)

type inflowCall struct {
	paymentID string
	amount    string
	currency  string
	sourceRaw string
	identity  entities.InflowIdentity
}

type recordingNotifier struct {
	mu    sync.Mutex
	calls []inflowCall
}

func (r *recordingNotifier) NotifyInflow(_ context.Context, _ uuid.UUID, paymentID, amount, currency, sourceRaw string, identity entities.InflowIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, inflowCall{paymentID, amount, currency, sourceRaw, identity})
	return nil
}

func TestProcessNGNDeposit_ReportsTheGraphTransactionAsTheReference(t *testing.T) {
	va := newFakeVARepo()
	seedNGNAccount(va)
	gc := &fakeGraphClient{conversion: &graph.Conversion{TargetAmount: "100", Status: "completed"}}
	svc := newTestService(gc, va, newFakeDepositRepo(), &fakeLedger{}, &fakeAllocation{})
	notifier := &recordingNotifier{}
	svc.SetInflowNotifier(notifier)

	event := &GraphNGNDepositEvent{
		GraphAccountID: "acct_1",
		TransactionID:  "tx_ref_42",
		AmountNGN:      "160000",
		Reference:      "rice and beans",
		Direction:      "credit",
	}
	if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// A webhook replay lands on the already-processed path and re-reports from the
	// stored deposit, so it must carry the same identity as the first delivery.
	if err := svc.ProcessNGNDeposit(context.Background(), event); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(notifier.calls) != 2 {
		t.Fatalf("want a report for the delivery and for the replay, got %d", len(notifier.calls))
	}
	want := entities.InflowIdentity{Channel: entities.InflowChannelGraph, Reference: "tx_ref_42"}
	for i, call := range notifier.calls {
		if call.identity != want {
			t.Errorf("call %d identity = %+v, want %+v", i, call.identity, want)
		}
		if call.identity.Reference == call.paymentID {
			t.Errorf("call %d: the idempotency key must not be sent as the reference", i)
		}
	}
	if notifier.calls[0].paymentID != notifier.calls[1].paymentID {
		t.Errorf("delivery and replay must share a payment_id: %q vs %q", notifier.calls[0].paymentID, notifier.calls[1].paymentID)
	}
}

func TestNotifyFiatInflow_ReportsTheBridgeDepositAsTheReference(t *testing.T) {
	notifier := &recordingNotifier{}

	if err := notifyFiatInflow(context.Background(), notifier, uuid.New(), "idem-key", decimal.NewFromInt(5000), "NGN", "fiat deposit dep_9", "dep_9"); err != nil {
		t.Fatalf("notifyFiatInflow: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("want one report, got %d", len(notifier.calls))
	}
	want := entities.InflowIdentity{Channel: entities.InflowChannelBridge, Reference: "dep_9"}
	if got := notifier.calls[0].identity; got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
	if got := notifier.calls[0].identity.Sender; got != "" {
		t.Fatalf("Bridge reports no sender, so none may be sent; got %q", got)
	}
}

func TestNotifyFiatInflow_SkipsNonNGN(t *testing.T) {
	notifier := &recordingNotifier{}

	if err := notifyFiatInflow(context.Background(), notifier, uuid.New(), "idem-key", decimal.NewFromInt(50), "USD", "fiat deposit dep_9", "dep_9"); err != nil {
		t.Fatalf("notifyFiatInflow: %v", err)
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("a USD credit must not be reported, got %d calls", len(notifier.calls))
	}
}
