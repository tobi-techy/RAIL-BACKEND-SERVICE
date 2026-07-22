package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/shopspring/decimal"
)

type fakeFundsTransferer struct {
	calls  int
	from   string
	to     string
	amount decimal.Decimal
}

func (f *fakeFundsTransferer) Transfer(_ context.Context, _ uuid.UUID, from, to string, amount decimal.Decimal) error {
	f.calls++
	f.from = from
	f.to = to
	f.amount = amount
	return nil
}
func (f *fakeFundsTransferer) Withdraw(context.Context, uuid.UUID, decimal.Decimal, string) error {
	return nil
}
func (f *fakeFundsTransferer) SetSavingsGoal(context.Context, uuid.UUID, string, decimal.Decimal) error {
	return nil
}

// TestTransferFundsTool_Registered ensures the tool exists and is categorized
// as an action so the chat loop stages it for confirmation.
func TestTransferFundsTool_Registered(t *testing.T) {
	r := NewRegistry()
	RegisterWithdrawalTools(r)

	tool := r.Get("transfer_funds")
	if tool == nil {
		t.Fatal("transfer_funds not registered")
	}
	if tool.Category != core.CategoryAction {
		t.Fatalf("transfer_funds category = %s, want CategoryAction", tool.Category)
	}
}

// TestTransferFundsTool_ExecutesThroughProvider verifies args are parsed and
// forwarded to the FundsTransfer dependency. (In the live chat loop this tool
// is staged as a pending action, so this executes only after confirmation.)
func TestTransferFundsTool_ExecutesThroughProvider(t *testing.T) {
	r := NewRegistry()
	RegisterWithdrawalTools(r)
	ft := &fakeFundsTransferer{}
	deps := &core.Dependencies{FundsTransfer: ft}

	res, err := r.Execute(context.Background(), uuid.New(), "transfer_funds",
		map[string]interface{}{"to": "stash", "amount": "200"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || res.Error != "" {
		t.Fatalf("unexpected tool error: %+v", res)
	}
	if ft.calls != 1 {
		t.Fatalf("expected 1 transfer call, got %d", ft.calls)
	}
	if ft.from != "spend" || ft.to != "stash" {
		t.Fatalf("unexpected from/to: %s -> %s", ft.from, ft.to)
	}
	if !ft.amount.Equal(decimal.NewFromInt(200)) {
		t.Fatalf("unexpected amount: %s", ft.amount)
	}
}

// TestTransferFundsTool_RequiresArgs rejects malformed calls before touching money.
func TestTransferFundsTool_RequiresArgs(t *testing.T) {
	r := NewRegistry()
	RegisterWithdrawalTools(r)
	ft := &fakeFundsTransferer{}
	deps := &core.Dependencies{FundsTransfer: ft}

	res, err := r.Execute(context.Background(), uuid.New(), "transfer_funds",
		map[string]interface{}{"to": "stash"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected an error for missing amount")
	}
	if ft.calls != 0 {
		t.Fatal("no transfer should run when amount is missing")
	}
}
