package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

type fakeBankLinker struct {
	url string
	err error
	n   int
}

func (f *fakeBankLinker) InitiateLinking(_ context.Context, _ uuid.UUID, _, _, _ string) (string, error) {
	f.n++
	return f.url, f.err
}

func TestConnectBank_ReturnsURL(t *testing.T) {
	reg := NewRegistry()
	RegisterBankStatementTools(reg)
	tool := reg.Get("connect_bank")
	if tool == nil {
		t.Fatal("connect_bank not registered")
	}
	linker := &fakeBankLinker{url: "https://link.mono.co/TEST"}
	deps := &core.Dependencies{BankLinker: linker}
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{}, deps)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["url"] != "https://link.mono.co/TEST" {
		t.Fatalf("url = %#v", res.Data["url"])
	}
	if linker.n != 1 {
		t.Fatalf("expected 1 initiate call, got %d", linker.n)
	}
}

func TestConnectBank_Unavailable(t *testing.T) {
	reg := NewRegistry()
	RegisterBankStatementTools(reg)
	tool := reg.Get("connect_bank")
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{}, &core.Dependencies{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["available"] != false {
		t.Fatalf("expected unavailable, got %#v", res.Data)
	}
}
