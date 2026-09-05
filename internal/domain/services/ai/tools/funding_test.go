package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

type fakeFunding struct {
	accounts   []*entities.VirtualAccount
	accountErr error
	address    *entities.DepositAddressResponse
	addressErr error
	gotChain   entities.Chain
	addrCalls  int
}

func (f *fakeFunding) GetVirtualAccounts(_ context.Context, _ uuid.UUID) ([]*entities.VirtualAccount, error) {
	return f.accounts, f.accountErr
}

func (f *fakeFunding) CreateDepositAddress(_ context.Context, _ uuid.UUID, chain entities.Chain, _ entities.Stablecoin) (*entities.DepositAddressResponse, error) {
	f.addrCalls++
	f.gotChain = chain
	return f.address, f.addressErr
}

func fundingDeps(f *fakeFunding) *core.Dependencies {
	return &core.Dependencies{FundingInstructions: f}
}

func runFundingTool(t *testing.T, deps *core.Dependencies, args map[string]interface{}) *core.ToolResult {
	t.Helper()
	reg := NewRegistry()
	RegisterFundingTools(reg)
	tool := reg.Get("get_funding_instructions")
	if tool == nil {
		t.Fatal("get_funding_instructions not registered")
	}
	res, err := tool.Execute(context.Background(), uuid.New(), args, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

func TestFundingTool_RegisteredInProductionSet(t *testing.T) {
	reg := NewRegistry()
	RegisterAllRemainingTools(reg)
	if reg.Get("get_funding_instructions") == nil {
		t.Fatal("get_funding_instructions missing from RegisterAllRemainingTools")
	}
}

func TestFundingTool_UnavailableWithoutDeps(t *testing.T) {
	res := runFundingTool(t, &core.Dependencies{}, map[string]interface{}{})
	data := res.Data
	if avail, _ := data["available"].(bool); avail {
		t.Fatalf("expected available=false without deps, got %v", data)
	}
}

func TestFundingTool_ReturnsActiveAccountsAndAddress(t *testing.T) {
	f := &fakeFunding{
		accounts: []*entities.VirtualAccount{
			{BankName: "Lead Bank", AccountNumber: "1234567890", RoutingNumber: "084009519", Currency: "USD", BeneficiaryName: "Tobi Omotade", Status: entities.VirtualAccountStatusActive, PaymentRails: pq.StringArray{"ach"}},
			{BankName: "Pending Bank", AccountNumber: "999", Currency: "USD", Status: entities.VirtualAccountStatusPending},
			nil,
		},
		address: &entities.DepositAddressResponse{Chain: entities.ChainBase, Address: "0xabc", Currency: entities.StablecoinUSDC},
	}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{})
	data := res.Data

	vas, _ := data["virtual_accounts"].([]map[string]interface{})
	if len(vas) != 1 {
		t.Fatalf("expected only the active account, got %d", len(vas))
	}
	if vas[0]["account_number"] != "1234567890" || vas[0]["bank_name"] != "Lead Bank" {
		t.Fatalf("unexpected account payload: %v", vas[0])
	}
	crypto, _ := data["crypto"].(map[string]interface{})
	if crypto["address"] != "0xabc" || crypto["chain"] != "BASE" {
		t.Fatalf("unexpected crypto payload: %v", crypto)
	}
	if f.gotChain != entities.ChainBase {
		t.Fatalf("default chain should be Base, got %q", f.gotChain)
	}
	if _, ok := data["split"]; !ok {
		t.Fatal("expected the 70/30 split note")
	}
}

func TestFundingTool_ChainParsing(t *testing.T) {
	f := &fakeFunding{address: &entities.DepositAddressResponse{Chain: entities.ChainSOL, Address: "soladdr", Currency: entities.StablecoinUSDC}}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{"method": "crypto", "chain": "solana"})
	data := res.Data
	if f.gotChain != entities.ChainSOL {
		t.Fatalf("expected SOL, got %q", f.gotChain)
	}
	crypto, _ := data["crypto"].(map[string]interface{})
	if crypto["address"] != "soladdr" {
		t.Fatalf("unexpected crypto payload: %v", crypto)
	}
}

func TestFundingTool_BadChainRejected(t *testing.T) {
	f := &fakeFunding{}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{"method": "crypto", "chain": "dogechain"})
	if res.Error == "" {
		t.Fatal("expected an error for an unsupported chain")
	}
	if f.addrCalls != 0 {
		t.Fatal("no address call should fire for an unsupported chain")
	}
}

func TestFundingTool_SplitPreview(t *testing.T) {
	f := &fakeFunding{address: &entities.DepositAddressResponse{Chain: entities.ChainBase, Address: "0xabc", Currency: entities.StablecoinUSDC}}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{"method": "crypto", "amount": "100"})
	data := res.Data
	preview, _ := data["split_preview"].(map[string]interface{})
	if preview["to_spend"] != "$70.00" || preview["to_stash"] != "$30.00" {
		t.Fatalf("bad split preview: %v", preview)
	}
}

func TestFundingTool_BadAmountIsSoftError(t *testing.T) {
	f := &fakeFunding{address: &entities.DepositAddressResponse{Chain: entities.ChainBase, Address: "0xabc", Currency: entities.StablecoinUSDC}}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{"method": "crypto", "amount": "lots"})
	data := res.Data
	if data["split_preview_error"] == nil {
		t.Fatal("expected a split preview error for a bad amount")
	}
	if data["crypto"] == nil {
		t.Fatal("address should still come back when the amount is bad")
	}
}

func TestFundingTool_PartialFailureStillDelivers(t *testing.T) {
	f := &fakeFunding{
		accountErr: fmt.Errorf("virtual account service not configured"),
		address:    &entities.DepositAddressResponse{Chain: entities.ChainBase, Address: "0xabc", Currency: entities.StablecoinUSDC},
	}
	res := runFundingTool(t, fundingDeps(f), map[string]interface{}{})
	data := res.Data
	if data["virtual_accounts_error"] == nil {
		t.Fatal("expected the VA error surfaced")
	}
	crypto, _ := data["crypto"].(map[string]interface{})
	if crypto["address"] != "0xabc" {
		t.Fatalf("crypto rail must still deliver when VAs fail, got %v", data)
	}
}
