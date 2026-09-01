package platform

import (
	"strings"
	"testing"
)

func TestParseBankDetailsFromText_FullDetails(t *testing.T) {
	text := "GTBank\nAccount Name: John Doe\nAccount Number: 0916473844\nAmount: ₦2,500"
	ext := parseBankDetailsFromText(text)
	if ext == nil {
		t.Fatal("expected extraction, got nil")
	}
	if ext.AccountNumber != "0916473844" {
		t.Errorf("account number = %q, want 0916473844", ext.AccountNumber)
	}
	if ext.BankName != "GTBank" {
		t.Errorf("bank name = %q, want GTBank", ext.BankName)
	}
	if ext.AccountName != "John Doe" {
		t.Errorf("account name = %q, want John Doe", ext.AccountName)
	}
	if ext.Amount != "2500" {
		t.Errorf("amount = %q, want 2500", ext.Amount)
	}
	if ext.Currency != "NGN" {
		t.Errorf("currency = %q, want NGN", ext.Currency)
	}
}

func TestParseBankDetailsFromText_BareAccountNumber(t *testing.T) {
	text := "Please send to 0916473844 at Access Bank"
	ext := parseBankDetailsFromText(text)
	if ext == nil {
		t.Fatal("expected extraction, got nil")
	}
	if ext.AccountNumber != "0916473844" {
		t.Errorf("account number = %q, want 0916473844", ext.AccountNumber)
	}
	if ext.BankName != "Access Bank" {
		t.Errorf("bank name = %q, want Access Bank", ext.BankName)
	}
}

func TestParseBankDetailsFromText_NoAccountNumber(t *testing.T) {
	text := "This is just a receipt from Starbucks, total 18.90"
	ext := parseBankDetailsFromText(text)
	if ext != nil {
		t.Fatal("expected nil for non-bank-detail text")
	}
}

func TestParseBankDetailsFromText_EmptyText(t *testing.T) {
	ext := parseBankDetailsFromText("")
	if ext != nil {
		t.Fatal("expected nil for empty text")
	}
}

func TestParseBankDetailsFromText_WithLabeledAccountNumber(t *testing.T) {
	text := "Account No: 0123456789\nBank: Zenith Bank"
	ext := parseBankDetailsFromText(text)
	if ext == nil {
		t.Fatal("expected extraction, got nil")
	}
	if ext.AccountNumber != "0123456789" {
		t.Errorf("account number = %q, want 0123456789", ext.AccountNumber)
	}
	if ext.BankName != "Zenith Bank" {
		t.Errorf("bank name = %q, want Zenith Bank", ext.BankName)
	}
}

func TestParseBankDetailsFromText_USDAmount(t *testing.T) {
	text := "Account Number: 0916473844\nAmount: $50.00"
	ext := parseBankDetailsFromText(text)
	if ext == nil {
		t.Fatal("expected extraction, got nil")
	}
	if ext.Amount != "50.00" {
		t.Errorf("amount = %q, want 50.00", ext.Amount)
	}
	if ext.Currency != "USD" {
		t.Errorf("currency = %q, want USD", ext.Currency)
	}
}

func TestFormatBankDetailSummary_FullDetails(t *testing.T) {
	ext := &BankDetailExtraction{
		BankName:      "GTBank",
		AccountNumber: "0916473844",
		AccountName:   "John Doe",
		Amount:        "2500",
		Currency:      "NGN",
	}
	summary := FormatBankDetailSummary(ext)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	// Summary should contain key details
	for _, want := range []string{"GTBank", "0916473844", "John Doe", "2500"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q: %s", want, summary)
		}
	}
}

func TestFormatBankDetailSummary_PartialDetails(t *testing.T) {
	ext := &BankDetailExtraction{
		AccountNumber: "0916473844",
	}
	summary := FormatBankDetailSummary(ext)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "0916473844") {
		t.Errorf("summary missing account number: %s", summary)
	}
}
