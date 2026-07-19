package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// --- fakes ---

type fakeMerchantBlocker struct {
	blocked   []string
	unblocked []string
}

func (f *fakeMerchantBlocker) BlockMerchant(_ context.Context, _ uuid.UUID, merchant string) (map[string]interface{}, error) {
	f.blocked = append(f.blocked, merchant)
	return map[string]interface{}{"status": "blocked", "merchant": merchant}, nil
}
func (f *fakeMerchantBlocker) UnblockMerchant(_ context.Context, _ uuid.UUID, merchant string) (map[string]interface{}, error) {
	f.unblocked = append(f.unblocked, merchant)
	return map[string]interface{}{"status": "unblocked"}, nil
}
func (f *fakeMerchantBlocker) ListBlockedMerchants(_ context.Context, _ uuid.UUID) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(f.blocked))
	for _, m := range f.blocked {
		out = append(out, map[string]interface{}{"merchant": m})
	}
	return out, nil
}

type fakeInvestmentExec struct {
	orders int
}

func (f *fakeInvestmentExec) ListInvestmentOptions(_ context.Context, _ uuid.UUID) ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"id": "b1", "name": "Core"}}, nil
}
func (f *fakeInvestmentExec) ExecuteInvestment(_ context.Context, _ uuid.UUID, basketID, side, amount string) (map[string]interface{}, error) {
	f.orders++
	return map[string]interface{}{"status": "submitted", "basket": basketID, "side": side, "amount": amount}, nil
}

func execRegistry(t *testing.T) (*Registry, *core.Dependencies, *fakeMerchantBlocker, *fakeInvestmentExec) {
	t.Helper()
	r := NewRegistry()
	RegisterExecutionTools(r)
	blocker := &fakeMerchantBlocker{}
	invest := &fakeInvestmentExec{}
	deps := &core.Dependencies{MerchantBlock: blocker, InvestmentExec: invest}
	return r, deps, blocker, invest
}

// --- tests ---

func TestExecutionTools_Registered(t *testing.T) {
	r, _, _, _ := execRegistry(t)
	names := []string{
		"get_upcoming_bills", "setup_bill_autopay",
		"audit_subscriptions", "cancel_subscription",
		"get_investment_options", "execute_investment",
		"get_yield_status", "optimize_yield",
		"block_merchant", "unblock_merchant", "list_blocked_merchants",
		"list_trade_conductors", "get_copy_trading_status", "copy_trader",
		"pause_trade_copying", "resume_trade_copying", "stop_trade_copying",
	}
	for _, name := range names {
		if r.Get(name) == nil {
			t.Errorf("tool %s not registered", name)
		}
	}
}

func TestExecutionTools_RequireConfirmation(t *testing.T) {
	r, deps, blocker, invest := execRegistry(t)
	userID := uuid.New()

	res, err := r.Execute(context.Background(), userID, "block_merchant",
		map[string]interface{}{"merchant": "DraftKings"}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["action_required"] != "confirmation" {
		t.Fatalf("expected confirmation gate, got %v", res.Data)
	}
	if len(blocker.blocked) != 0 {
		t.Fatal("merchant must not be blocked before confirmation")
	}

	res, err = r.Execute(context.Background(), userID, "execute_investment",
		map[string]interface{}{"basket_id": "b1", "side": "buy", "amount": "50.00"}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["action_required"] != "confirmation" {
		t.Fatalf("expected confirmation gate, got %v", res.Data)
	}
	if invest.orders != 0 {
		t.Fatal("order must not be placed before confirmation")
	}
}

func TestExecutionTools_ExecuteWithConfirm(t *testing.T) {
	r, deps, blocker, invest := execRegistry(t)
	userID := uuid.New()

	res, err := r.Execute(context.Background(), userID, "block_merchant",
		map[string]interface{}{"merchant": "DraftKings", "confirm": true}, deps)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if len(blocker.blocked) != 1 || blocker.blocked[0] != "DraftKings" {
		t.Fatalf("expected DraftKings blocked, got %v", blocker.blocked)
	}

	res, err = r.Execute(context.Background(), userID, "execute_investment",
		map[string]interface{}{"basket_id": "b1", "side": "buy", "amount": "50.00", "confirm": true}, deps)
	if err != nil || res.Error != "" {
		t.Fatalf("execute: err=%v toolErr=%s", err, res.Error)
	}
	if invest.orders != 1 {
		t.Fatalf("expected 1 order, got %d", invest.orders)
	}
}

func TestExecutionTools_ValidateArgs(t *testing.T) {
	r, deps, _, invest := execRegistry(t)
	userID := uuid.New()

	res, err := r.Execute(context.Background(), userID, "block_merchant",
		map[string]interface{}{"confirm": true}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for missing merchant")
	}

	res, err = r.Execute(context.Background(), userID, "execute_investment",
		map[string]interface{}{"basket_id": "b1", "side": "hold", "amount": "50", "confirm": true}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for invalid side")
	}
	if invest.orders != 0 {
		t.Fatal("invalid side must not place an order")
	}
}

func TestExecutionTools_UnavailableProvider(t *testing.T) {
	r := NewRegistry()
	RegisterExecutionTools(r)
	deps := &core.Dependencies{} // nothing wired

	res, err := r.Execute(context.Background(), uuid.New(), "copy_trader",
		map[string]interface{}{"conductor_id": "c1", "amount": "100", "confirm": true}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected 'not available' error when provider is nil")
	}
}
