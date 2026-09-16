package document

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestContractValidateAcceptsV1(t *testing.T) {
	r := APIResult{
		SchemaVersion: "1.0",
		DocumentID:    "doc-1",
		DocumentType:  TypeReceipt,
		Status:        StatusCompleted,
		Confidence:    0.9,
		Evidence:      []APIEvidence{},
		Validation:    APIValidation{Status: "valid", Difference: "0.00", Checks: []APICheck{}},
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("expected valid contract, got %v", err)
	}
}

func TestContractValidateRejectsUnknownMajor(t *testing.T) {
	r := APIResult{SchemaVersion: "2.0", DocumentID: "d", DocumentType: TypeReceipt, Status: StatusCompleted}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for major version 2")
	}
}

func TestContractValidateRejectsBadStatusAndConfidence(t *testing.T) {
	r := APIResult{SchemaVersion: "1.0", DocumentID: "d", DocumentType: TypeReceipt, Status: "bogus"}
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for unknown status")
	}
	r.Status = StatusCompleted
	r.Confidence = 1.5
	if err := r.Validate(); err == nil {
		t.Fatal("expected error for out-of-range confidence")
	}
}

func TestContractMoneySerializesAsDecimalString(t *testing.T) {
	amt := decimal.NewFromFloat(1500.50)
	r := APIResult{
		SchemaVersion: "1.0",
		DocumentID:    "d",
		DocumentType:  TypeReceipt,
		Status:        StatusCompleted,
		Data:          APIData{Amount: &amt, Currency: strPtr("NGN")},
		Validation:    APIValidation{Status: "valid", Difference: "0.00", Checks: []APICheck{}},
		Evidence:      []APIEvidence{},
	}
	raw, err := json.Marshal(r.Data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["amount"] != "1500.5" {
		t.Fatalf("expected decimal string amount, got %v", decoded["amount"])
	}
}

func strPtr(s string) *string { return &s }
