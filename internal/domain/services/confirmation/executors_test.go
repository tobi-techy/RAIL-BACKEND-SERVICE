package confirmation

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type fakeP2P struct {
	calls int
	last  *entities.P2PSendRequest
	err   error
}

func (f *fakeP2P) Send(ctx context.Context, senderID uuid.UUID, req *entities.P2PSendRequest) (*entities.P2PTransferResponse, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &entities.P2PTransferResponse{Transfer: &entities.P2PTransfer{ID: uuid.New()}}, nil
}

type fakeBuyer struct {
	calls int
	last  *entities.InvestmentOrderRequest
	err   error
}

func (f *fakeBuyer) PlaceOrder(ctx context.Context, userID uuid.UUID, req *entities.InvestmentOrderRequest, actor entities.InvestmentActor) (*entities.InvestmentOrderResponse, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &entities.InvestmentOrderResponse{}, nil
}

type fakeRuleUpdater struct {
	calls int
	last  *UpdateAutomationFields
	err   error
}

func (f *fakeRuleUpdater) Update(ctx context.Context, userID, id uuid.UUID, req *UpdateAutomationFields) (*struct{}, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &struct{}{}, nil
}

func TestThreeActionsSamePath(t *testing.T) {
	s := testService()
	p2p := &fakeP2P{}
	buyer := &fakeBuyer{}
	rules := &fakeRuleUpdater{}
	s.RegisterExecutor(entities.ConfirmationActionTransferSend, TransferSendExecutor(p2p))
	s.RegisterExecutor(entities.ConfirmationActionInvestBuy, InvestBuyExecutor(buyer))
	s.RegisterExecutor(entities.ConfirmationActionSaveSweep, SaveSweepExecutor(rules))

	ctx := context.Background()
	uid := uuid.New()
	ruleID := uuid.New()

	cases := []CreateInput{
		{UserID: uid, Action: entities.ConfirmationActionInvestBuy, Payload: map[string]any{"symbol": "GOOGL", "amount_usd": "100"}},
		{UserID: uid, Action: entities.ConfirmationActionTransferSend, Payload: map[string]any{"to": "@tobi", "amount": "20000"}},
		{UserID: uid, Action: entities.ConfirmationActionSaveSweep, Payload: map[string]any{"automation_id": ruleID.String(), "percentage": "15%"}},
	}
	for _, in := range cases {
		c, url, err := s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if c.Title == "" {
			t.Fatalf("%s: empty title", in.Action)
		}
		tok := url[strings.Index(url, "?t=")+3:]
		out, err := s.Approve(ctx, uid, c.ID, tok, "pass")
		if err != nil {
			t.Fatalf("%s: approve: %v", in.Action, err)
		}
		if out.State != entities.ConfirmationCompleted {
			t.Fatalf("%s: state=%s summary=%q", in.Action, out.State, out.ResultSummary)
		}
	}
	if p2p.calls != 1 || p2p.last.IdempotencyKey == "" {
		t.Fatalf("p2p executor: calls=%d key=%q", p2p.calls, p2p.last.IdempotencyKey)
	}
	if buyer.calls != 1 || buyer.last.Side != "buy" || buyer.last.Symbol != "GOOGL" {
		t.Fatalf("invest executor misbuilt: %+v", buyer.last)
	}
	if !buyer.last.AmountUSD.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("invest amount: %s", buyer.last.AmountUSD.String())
	}
	if rules.calls != 1 || rules.last.ActionConfig["percentage"] != "15%" {
		t.Fatalf("save executor misbuilt: %+v", rules.last)
	}
}

func TestExecutorsFailClosed(t *testing.T) {
	ctx := context.Background()
	uid := uuid.New()
	c := &entities.Confirmation{ID: uuid.New(), UserID: uid, ExecuteKey: uuid.New().String()}
	if _, err := TransferSendExecutor(nil)(ctx, uid, c); err == nil {
		t.Fatal("nil p2p sender should fail closed")
	}
	if _, err := InvestBuyExecutor(nil)(ctx, uid, c); err == nil {
		t.Fatal("nil buyer should fail closed")
	}
	if _, err := SaveSweepExecutor(nil)(ctx, uid, c); err == nil {
		t.Fatal("nil updater should fail closed")
	}
	c.Payload = map[string]any{}
	if _, err := TransferSendExecutor(&fakeP2P{})(ctx, uid, c); err == nil {
		t.Fatal("missing identifier should fail")
	}
}
