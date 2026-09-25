package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
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

type fakeCategoryRules struct {
	calls int
	rule  *entities.StatementCategoryRule
}

func (f *fakeCategoryRules) SaveCategoryRule(_ context.Context, rule *entities.StatementCategoryRule, _ bool) (int, error) {
	f.calls++
	f.rule = rule
	return 3, nil
}

func TestCorrectStatementCategoryRequiresConfirm(t *testing.T) {
	reg := NewRegistry()
	RegisterBankStatementTools(reg)
	tool := reg.Get("correct_statement_category")
	rules := &fakeCategoryRules{}
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{
		"pattern": "Shoprite",
		"bucket":  "groceries",
	}, &core.Dependencies{StatementCategoryRules: rules})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["status"] != "needs_confirmation" {
		t.Fatalf("status = %#v", res.Data["status"])
	}
	if rules.calls != 0 {
		t.Fatal("unc confirmed correction was saved")
	}
}

func TestCorrectStatementCategorySavesOnConfirm(t *testing.T) {
	reg := NewRegistry()
	RegisterBankStatementTools(reg)
	tool := reg.Get("correct_statement_category")
	rules := &fakeCategoryRules{}
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{
		"confirm":    true,
		"pattern":    "Shoprite",
		"bucket":     "grocery",
		"match_type": "contains",
	}, &core.Dependencies{StatementCategoryRules: rules})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("error: %s", res.Error)
	}
	if rules.calls != 1 || rules.rule.Bucket != "groceries" || rules.rule.Pattern != "Shoprite" {
		t.Fatalf("saved %#v", rules.rule)
	}
	if res.Data["lines_updated"] != 3 {
		t.Fatalf("lines_updated = %#v", res.Data["lines_updated"])
	}
}

func TestCorrectStatementCategoryRejectsShortPattern(t *testing.T) {
	reg := NewRegistry()
	RegisterBankStatementTools(reg)
	tool := reg.Get("correct_statement_category")
	rules := &fakeCategoryRules{}
	res, err := tool.Execute(context.Background(), uuid.New(), map[string]interface{}{
		"confirm": true,
		"pattern": "a",
		"bucket":  "groceries",
	}, &core.Dependencies{StatementCategoryRules: rules})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected a pattern error")
	}
	if rules.calls != 0 {
		t.Fatal("broad correction was saved")
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
