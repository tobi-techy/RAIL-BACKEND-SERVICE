package tools

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// fakeBankTransferProvider implements core.BankTransferProvider for testing.
type fakeBankTransferProvider struct {
	banks          []map[string]interface{}
	resolveResult  map[string]interface{}
	resolveErr     error
	offrampResult  map[string]interface{}
	offrampErr     error
	lastOfframpArgs struct {
		bankCode, accountNumber, bankName, amount, currency, accountName string
	}
}

func (f *fakeBankTransferProvider) ListBanks(ctx context.Context) ([]map[string]interface{}, error) {
	return f.banks, nil
}

func (f *fakeBankTransferProvider) ResolveBankAccount(ctx context.Context, bankCode, accountNumber, bankName string) (map[string]interface{}, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.resolveResult, nil
}

func (f *fakeBankTransferProvider) CreateOfframp(ctx context.Context, userID uuid.UUID, bankCode, accountNumber, bankName, amount, currency, accountName string) (map[string]interface{}, error) {
	f.lastOfframpArgs.bankCode = bankCode
	f.lastOfframpArgs.accountNumber = accountNumber
	f.lastOfframpArgs.bankName = bankName
	f.lastOfframpArgs.amount = amount
	f.lastOfframpArgs.currency = currency
	f.lastOfframpArgs.accountName = accountName
	if f.offrampErr != nil {
		return nil, f.offrampErr
	}
	return f.offrampResult, nil
}

func TestListBanks_Registered(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)

	tool := r.Get("list_banks")
	if tool == nil {
		t.Fatal("list_banks not registered")
	}
	if tool.Category != core.CategoryAction {
		t.Fatalf("list_banks category = %s, want CategoryAction", tool.Category)
	}
}

func TestListBanks_ReturnsBanks(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	deps := &core.Dependencies{
		BankTransfer: &fakeBankTransferProvider{
			banks: []map[string]interface{}{
				{"bank_code": "058", "bank_name": "GTBank"},
				{"bank_code": "044", "bank_name": "Access Bank"},
			},
		},
	}

	res, err := r.Execute(context.Background(), uuid.New(), "list_banks", nil, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	banks, _ := res.Data["banks"].([]map[string]interface{})
	if len(banks) != 2 {
		t.Fatalf("expected 2 banks, got %d", len(banks))
	}
	count, _ := res.Data["count"].(int)
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
}

func TestListBanks_NilProvider(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	deps := &core.Dependencies{}

	res, err := r.Execute(context.Background(), uuid.New(), "list_banks", nil, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for nil provider")
	}
}

func TestResolveBankAccount_Registered(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)

	tool := r.Get("resolve_bank_account")
	if tool == nil {
		t.Fatal("resolve_bank_account not registered")
	}
	if tool.Category != core.CategoryAction {
		t.Fatalf("resolve_bank_account category = %s, want CategoryAction", tool.Category)
	}
}

func TestResolveBankAccount_ReturnsName(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	deps := &core.Dependencies{
		BankTransfer: &fakeBankTransferProvider{
			resolveResult: map[string]interface{}{
				"account_name":   "John Doe",
				"account_number": "0916473844",
				"bank_code":      "058",
			},
		},
	}

	res, err := r.Execute(context.Background(), uuid.New(), "resolve_bank_account",
		map[string]interface{}{"bank_code": "058", "account_number": "0916473844"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	name, _ := res.Data["account_name"].(string)
	if name != "John Doe" {
		t.Fatalf("expected account_name 'John Doe', got %q", name)
	}
}

func TestResolveBankAccount_RequiresArgs(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	deps := &core.Dependencies{
		BankTransfer: &fakeBankTransferProvider{},
	}

	res, err := r.Execute(context.Background(), uuid.New(), "resolve_bank_account",
		map[string]interface{}{"bank_code": "058"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for missing account_number")
	}
}

func TestSendToBank_Registered(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)

	tool := r.Get("send_to_bank")
	if tool == nil {
		t.Fatal("send_to_bank not registered")
	}
	if tool.Category != core.CategoryAction {
		t.Fatalf("send_to_bank category = %s, want CategoryAction", tool.Category)
	}
}

func TestSendToBank_ExecutesThroughProvider(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	ft := &fakeBankTransferProvider{
		offrampResult: map[string]interface{}{
			"status":         "pending",
			"transaction_id":  "tx-123",
			"fiat_amount":     2500.0,
		},
	}
	deps := &core.Dependencies{BankTransfer: ft}

	res, err := r.Execute(context.Background(), uuid.New(), "send_to_bank",
		map[string]interface{}{
			"bank_code":      "058",
			"account_number": "0916473844",
			"bank_name":      "GTBank",
			"account_name":   "John Doe",
			"amount":         "2500",
		}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if ft.lastOfframpArgs.bankCode != "058" {
		t.Fatalf("expected bank_code '058', got %q", ft.lastOfframpArgs.bankCode)
	}
	if ft.lastOfframpArgs.accountName != "John Doe" {
		t.Fatalf("expected account_name 'John Doe', got %q", ft.lastOfframpArgs.accountName)
	}
	if ft.lastOfframpArgs.amount != "2500" {
		t.Fatalf("expected amount '2500', got %q", ft.lastOfframpArgs.amount)
	}
}

func TestSendToBank_RequiresArgs(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	ft := &fakeBankTransferProvider{}
	deps := &core.Dependencies{BankTransfer: ft}

	res, err := r.Execute(context.Background(), uuid.New(), "send_to_bank",
		map[string]interface{}{"bank_code": "058"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for missing account_number and amount")
	}
}

func TestSendCrypto_Registered(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)

	tool := r.Get("send_crypto")
	if tool == nil {
		t.Fatal("send_crypto not registered")
	}
	if tool.Category != core.CategoryAction {
		t.Fatalf("send_crypto category = %s, want CategoryAction", tool.Category)
	}
}

// fakeCryptoSendProvider implements core.CryptoSendProvider for testing.
type fakeCryptoSendProvider struct {
	result map[string]interface{}
	err    error
	last   struct {
		address, amount, chain string
	}
}

func (f *fakeCryptoSendProvider) SendCrypto(ctx context.Context, userID uuid.UUID, destinationAddress, amount, chain string) (map[string]interface{}, error) {
	f.last.address = destinationAddress
	f.last.amount = amount
	f.last.chain = chain
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestSendCrypto_ExecutesThroughProvider(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	fc := &fakeCryptoSendProvider{
		result: map[string]interface{}{
			"status":        "pending",
			"withdrawal_id": "w-456",
		},
	}
	deps := &core.Dependencies{CryptoSend: fc}

	res, err := r.Execute(context.Background(), uuid.New(), "send_crypto",
		map[string]interface{}{
			"destination_address": "0x1234567890abcdef",
			"amount":              "50.00",
			"chain":               "evm",
		}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if fc.last.address != "0x1234567890abcdef" {
		t.Fatalf("expected address, got %q", fc.last.address)
	}
	if fc.last.amount != "50.00" {
		t.Fatalf("expected amount '50.00', got %q", fc.last.amount)
	}
}

func TestSendCrypto_RequiresArgs(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	deps := &core.Dependencies{CryptoSend: &fakeCryptoSendProvider{}}

	res, err := r.Execute(context.Background(), uuid.New(), "send_crypto",
		map[string]interface{}{"amount": "50"}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected error for missing destination_address")
	}
}

func TestTruncateAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0x1234567890abcdef1234567890abcdef12345678", "0x1234...5678"},
		{"0x123", "0x123"},
		{"", ""},
	}
	for _, tc := range tests {
		got := truncateAddress(tc.input)
		if got != tc.want {
			t.Fatalf("truncateAddress(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCurrencySymbol(t *testing.T) {
	if got := currencySymbol("NGN"); got != "₦" {
		t.Fatalf("expected ₦ for NGN, got %q", got)
	}
	if got := currencySymbol("USD"); got != "$" {
		t.Fatalf("expected $ for USD, got %q", got)
	}
	if got := currencySymbol(""); got != "" {
		t.Fatalf("expected empty for empty currency, got %q", got)
	}
}

func TestNormalizeAmountArg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2k", "2000"},
		{"2.5k", "2500"},
		{"5k", "5000"},
		{"0.5k", "500"},
		{"1m", "1000000"},
		{"2.5m", "2500000"},
		{"500", "500"},
		{"2500.00", "2500.00"},
		{"", ""},
		{"  2k  ", "2000"},
		{"2K", "2000"},
		{"1M", "1000000"},
	}
	for _, tc := range tests {
		got := normalizeAmountArg(tc.input)
		if got != tc.want {
			t.Errorf("normalizeAmountArg(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSendToBank_NormalizesAmountShorthand(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	ft := &fakeBankTransferProvider{
		offrampResult: map[string]interface{}{"status": "pending"},
	}
	deps := &core.Dependencies{BankTransfer: ft}

	_, err := r.Execute(context.Background(), uuid.New(), "send_to_bank",
		map[string]interface{}{
			"bank_code":      "058",
			"account_number": "0916473844",
			"amount":         "2k",
		}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if ft.lastOfframpArgs.amount != "2000" {
		t.Fatalf("expected normalized amount '2000', got %q", ft.lastOfframpArgs.amount)
	}
}

func TestSendCrypto_NormalizesAmountShorthand(t *testing.T) {
	r := NewRegistry()
	RegisterBankTransferTools(r)
	fc := &fakeCryptoSendProvider{
		result: map[string]interface{}{"status": "pending"},
	}
	deps := &core.Dependencies{CryptoSend: fc}

	_, err := r.Execute(context.Background(), uuid.New(), "send_crypto",
		map[string]interface{}{
			"destination_address": "0x1234567890abcdef",
			"amount":              "2.5k",
			"chain":               "evm",
		}, deps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fc.last.amount != "2500" {
		t.Fatalf("expected normalized amount '2500', got %q", fc.last.amount)
	}
}
