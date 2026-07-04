package ramp

import (
	"testing"

	"github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
)

func TestNormalizeBankNameForMatch(t *testing.T) {
	cases := map[string]string{
		"Moniepoint MFB":                       "moniepoint",
		"Moniepoint Microfinance Bank":         "moniepoint",
		"Opay":                                 "opay",
		"OPay Digital Services Limited (OPay)": "opay",
		"Access Bank":                          "access",
		"Access Money":                         "accessmoney",
		"Gti Microfinance Bank":                "gti",
		"Kuda Bank":                            "kuda",
		"Guaranty Trust Bank":                  "guarantytrust",
	}
	for in, want := range cases {
		if got := normalizeBankNameForMatch(in); got != want {
			t.Errorf("normalizeBankNameForMatch(%q) = %q, want %q", in, got, want)
		}
	}
}

// useBreadBanks mirrors the real live provider-bank-list for UseBread/Flipeet/
// Pajcash (names + codes captured July 2026). Note Moniepoint's code here (090405)
// differs from Paycrest's (50515), and the name differs too.
var useBreadBanks = []ramphub.Bank{
	{BankCode: "044", BankName: "Access Bank"},
	{BankCode: "100013", BankName: "Access Money"},
	{BankCode: "100014", BankName: "Firstmonie Wallet"},
	{BankCode: "090385", BankName: "Gti Microfinance Bank"},
	{BankCode: "50211", BankName: "Kuda Bank"},
	{BankCode: "090405", BankName: "Moniepoint Microfinance Bank"},
}

func TestMatchBankByName_TranslatesAcrossProviders(t *testing.T) {
	// User picked "Moniepoint MFB" from the Paycrest-sourced list; the sell order
	// routes to UseBread, whose code+name for the same bank differ.
	code, name, ok := matchBankByName(useBreadBanks, "Moniepoint MFB")
	if !ok {
		t.Fatal("expected Moniepoint MFB to match UseBread's Moniepoint Microfinance Bank")
	}
	if code != "090405" || name != "Moniepoint Microfinance Bank" {
		t.Fatalf("got code=%q name=%q, want 090405 / Moniepoint Microfinance Bank", code, name)
	}
}

func TestMatchBankByName_DoesNotConfuseSimilarBanks(t *testing.T) {
	// "Firstmonie Wallet" must not be mistaken for Moniepoint (both contain "monie").
	if _, _, ok := matchBankByName(useBreadBanks, "Firstmonie Wallet"); !ok {
		t.Fatal("Firstmonie Wallet should match itself")
	}
	code, _, ok := matchBankByName(useBreadBanks, "Moniepoint MFB")
	if !ok || code == "100014" {
		t.Fatalf("Moniepoint must not resolve to Firstmonie Wallet (100014); got code=%q ok=%v", code, ok)
	}
}

func TestMatchBankByName_FailsClosedOnNoMatch(t *testing.T) {
	if _, _, ok := matchBankByName(useBreadBanks, "Some Bank That Does Not Exist"); ok {
		t.Fatal("expected no match to fail closed")
	}
	if _, _, ok := matchBankByName(useBreadBanks, ""); ok {
		t.Fatal("expected empty name to fail closed")
	}
}

func TestMatchBankByName_FailsClosedOnAmbiguousMatch(t *testing.T) {
	// Two entries normalizing to the same key -> ambiguous -> must refuse.
	ambiguous := []ramphub.Bank{
		{BankCode: "111", BankName: "Opay"},
		{BankCode: "222", BankName: "OPay Digital Services Limited (OPay)"},
	}
	if _, _, ok := matchBankByName(ambiguous, "Opay"); ok {
		t.Fatal("expected ambiguous match to fail closed")
	}
}

func TestNormalizeProviderName(t *testing.T) {
	cases := map[string]string{
		"UseBread Sandbox": "usebread",
		"UseBread":         "usebread",
		"Paycrest Sandbox": "paycrest",
		" Flipeet ":        "flipeet",
	}
	for in, want := range cases {
		if got := normalizeProviderName(in); got != want {
			t.Errorf("normalizeProviderName(%q) = %q, want %q", in, got, want)
		}
	}
}
