package document

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseAmount(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"1,234.56", "1234.56", true},
		{"18.90", "18.90", true},
		{"₦2,000.00", "2000.00", true},
		{"$1880", "1880", true},
		{"no digits", "0", false},
	}
	for _, tt := range tests {
		got, ok := parseAmount(tt.in)
		if ok != tt.wantOK {
			t.Errorf("parseAmount(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			continue
		}
		if ok && !got.Equal(decimal.RequireFromString(tt.want)) {
			t.Errorf("parseAmount(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestDetectCurrency(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Total ₦2,000", "NGN"},
		{"Total $18.90", "USD"},
		{"Amount £45", "GBP"},
		{"Price €10", "EUR"},
		{"USD 100", "USD"},
		{"no currency here", ""},
	}
	for _, tt := range tests {
		if got := detectCurrency(tt.in); got != tt.want {
			t.Errorf("detectCurrency(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractReceipt(t *testing.T) {
	e := NewRuleFieldExtractor()
	text := "STARBUCKS COFFEE\n2026-01-15\nLatte 5.00\nMuffin 10.00\nSubtotal 15.00\nVAT 3.90\nTotal 18.90"
	f, err := e.Extract(DocReceipt, text, nil)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if f.Merchant != "STARBUCKS COFFEE" {
		t.Errorf("merchant = %q, want STARBUCKS COFFEE", f.Merchant)
	}
	if f.Total == nil || !f.Total.Equal(decimal.RequireFromString("18.90")) {
		t.Errorf("total = %v, want 18.90", f.Total)
	}
	if f.Subtotal == nil || !f.Subtotal.Equal(decimal.RequireFromString("15.00")) {
		t.Errorf("subtotal = %v, want 15.00", f.Subtotal)
	}
	if f.Tax == nil || !f.Tax.Equal(decimal.RequireFromString("3.90")) {
		t.Errorf("tax = %v, want 3.90", f.Tax)
	}
	if f.Date == nil {
		t.Error("expected a parsed date")
	}
}

func TestExtractStatement(t *testing.T) {
	e := NewRuleFieldExtractor()
	text := "Account Name: John Doe\nStatement 2026-01-01 to 2026-01-31\nOpening Balance 1,000.00\nClosing Balance 1,150.00"
	f, err := e.Extract(DocBankStatement, text, nil)
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if f.OpeningBalance == nil || !f.OpeningBalance.Equal(decimal.RequireFromString("1000.00")) {
		t.Errorf("opening = %v, want 1000.00", f.OpeningBalance)
	}
	if f.ClosingBalance == nil || !f.ClosingBalance.Equal(decimal.RequireFromString("1150.00")) {
		t.Errorf("closing = %v, want 1150.00", f.ClosingBalance)
	}
	if f.AccountName != "John Doe" {
		t.Errorf("account name = %q, want John Doe", f.AccountName)
	}
	if f.PeriodStart == nil || f.PeriodEnd == nil {
		t.Error("expected period start/end")
	}
}
