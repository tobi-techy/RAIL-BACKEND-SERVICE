package document

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func dec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func TestValidateReceipt(t *testing.T) {
	v := NewRuleValidator()

	// Correct: 15.00 + 3.90 = 18.90
	good := &Fields{Type: DocReceipt, Subtotal: dec("15.00"), Tax: dec("3.90"), Total: dec("18.90")}
	if res := v.Validate(DocReceipt, good); !res.Passed {
		t.Errorf("expected valid receipt, got errors: %v", res.Errors)
	}

	// OCR misread total as 1880 instead of 18.80 — subtotal+tax mismatch.
	bad := &Fields{Type: DocReceipt, Subtotal: dec("15.00"), Tax: dec("3.80"), Total: dec("1880")}
	if res := v.Validate(DocReceipt, bad); res.Passed {
		t.Error("expected invalid receipt for mismatched total")
	}

	// Missing total.
	if res := v.Validate(DocReceipt, &Fields{Type: DocReceipt}); res.Passed {
		t.Error("expected invalid receipt for missing total")
	}
}

func TestValidateStatement(t *testing.T) {
	v := NewRuleValidator()
	now := time.Now()

	// opening 1000 + credit 200 - debit 50 = 1150
	good := &Fields{
		Type:           DocBankStatement,
		OpeningBalance: dec("1000"),
		ClosingBalance: dec("1150"),
		Transactions: []Txn{
			{Date: now, Amount: decimal.RequireFromString("200"), Type: "credit"},
			{Date: now, Amount: decimal.RequireFromString("50"), Type: "debit"},
		},
	}
	if res := v.Validate(DocBankStatement, good); !res.Passed {
		t.Errorf("expected valid statement, got errors: %v", res.Errors)
	}

	// Broken balance math.
	bad := &Fields{
		Type:           DocBankStatement,
		OpeningBalance: dec("1000"),
		ClosingBalance: dec("9999"),
		Transactions: []Txn{
			{Date: now, Amount: decimal.RequireFromString("200"), Type: "credit"},
		},
	}
	if res := v.Validate(DocBankStatement, bad); res.Passed {
		t.Error("expected invalid statement for broken balance math")
	}

	// No transactions.
	empty := &Fields{Type: DocBankStatement, OpeningBalance: dec("1000"), ClosingBalance: dec("1000")}
	if res := v.Validate(DocBankStatement, empty); res.Passed {
		t.Error("expected invalid statement with no transactions")
	}
}
