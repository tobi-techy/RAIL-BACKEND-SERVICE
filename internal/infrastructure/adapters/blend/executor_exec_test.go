package blend

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"go.uber.org/zap"
)

type fakeContractExecutor struct {
	lastReq *circlepkg.CreateContractExecutionRequest
	calls   int
}

func (f *fakeContractExecutor) ExecuteContract(_ context.Context, req *circlepkg.CreateContractExecutionRequest) (*circlepkg.Transaction, error) {
	f.calls++
	f.lastReq = req
	return &circlepkg.Transaction{ID: "tx-1", TxHash: "0xhash", State: circlepkg.TransactionStateComplete}, nil
}

func (f *fakeContractExecutor) GetTransaction(_ context.Context, _ string) (*circlepkg.Transaction, error) {
	return &circlepkg.Transaction{ID: "tx-1", TxHash: "0xhash"}, nil
}

func (f *fakeContractExecutor) ListTransactions(_ context.Context, _ string, _ string, _ string) ([]circlepkg.Transaction, error) {
	return []circlepkg.Transaction{{ID: "tx-1", TxHash: "0xhash"}}, nil
}

type fakeVerifier struct{ called bool }

func (f *fakeVerifier) VerifySafe(_ context.Context, _ int64, _, _ string) error {
	f.called = true
	return nil
}

func TestExecute_MultisendRoutesToSafe(t *testing.T) {
	fe := &fakeContractExecutor{}
	exec := NewPlanExecutor(fe, NewAllowlist(nil), zap.NewNop())
	exec.SetSafeVerifier(&fakeVerifier{})

	safe := "0x000000000000000000000000000000000000dead"
	owner := "0x1111111111111111111111111111111111111111"
	plan := &ActionPlan{
		DeployType: deployMultisend,
		Steps: []ActionStep{
			{To: "0x0000000000000000000000000000000000000aaa", Data: "0x01", Value: "0"},
			{To: "0x0000000000000000000000000000000000000bbb", Data: "0x02", Value: "0", IsDelegateCall: true},
		},
	}

	resolveWallet := func(_ context.Context, _ int64) (string, string, error) {
		return "wallet-1", "0x1111111111111111111111111111111111111111", nil
	}

	out, err := exec.Execute(context.Background(), resolveWallet, plan, "redeem-1",
		&TrustedSafe{Address: safe, OwnerEOA: owner, ChainID: 8453})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Exactly one Circle call (the batched Safe execTransaction), not one per step.
	if fe.calls != 1 {
		t.Fatalf("circle calls = %d, want 1", fe.calls)
	}
	if len(out) != 1 || out[0].TransactionID != "tx-1" {
		t.Fatalf("unexpected result: %+v", out)
	}
	// The single call targets the Safe, with execTransaction calldata.
	if !strings.EqualFold(fe.lastReq.ContractAddress, safe) {
		t.Fatalf("call target = %s, want Safe %s", fe.lastReq.ContractAddress, safe)
	}
	cd := strings.TrimPrefix(strings.ToLower(fe.lastReq.CallData), "0x")
	if !strings.HasPrefix(cd, selectorExecTransaction) {
		t.Fatalf("calldata does not start with execTransaction selector: %s", cd[:8])
	}
	if _, err := hex.DecodeString(cd); err != nil {
		t.Fatalf("calldata not valid hex: %v", err)
	}
}

func TestExecute_MultisendRequiresVerifier(t *testing.T) {
	exec := NewPlanExecutor(&fakeContractExecutor{}, NewAllowlist(nil), zap.NewNop())
	// No verifier set → must refuse to trust the Safe.
	plan := &ActionPlan{DeployType: deployMultisend, Steps: []ActionStep{{To: "0x0000000000000000000000000000000000000aaa", Data: "0x01"}}}
	resolveWallet := func(_ context.Context, _ int64) (string, string, error) {
		return "wallet-1", "", nil
	}
	_, err := exec.Execute(context.Background(), resolveWallet, plan, "redeem-1",
		&TrustedSafe{Address: "0x000000000000000000000000000000000000dead", OwnerEOA: "0x1111111111111111111111111111111111111111", ChainID: 8453})
	if err == nil {
		t.Fatal("expected refusal without a configured Safe verifier")
	}
}
