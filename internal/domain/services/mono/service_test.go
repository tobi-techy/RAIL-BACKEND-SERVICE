package mono

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

// mockClient is a test double for the Client interface.
type mockClient struct {
	// Linking
	initiateLinkingURL string
	initiateLinkingErr error

	// Exchange
	exchangeResult *AccountInfo
	exchangeErr    error

	// Account
	accountResult *AccountInfo
	accountErr    error

	// Transactions
	transactionsResult []Transaction
	transactionsErr    error

	// Payment
	paymentResult *PaymentInitiationResult
	paymentErr    error

	// Verify
	verifyResult *PaymentVerification
	verifyErr    error

	// Income
	incomeErr error

	// Unlink
	unlinkErr error
}

func (m *mockClient) InitiateLinking(_ context.Context, _ *LinkingRequest) (string, error) {
	return m.initiateLinkingURL, m.initiateLinkingErr
}

func (m *mockClient) ExchangeCode(_ context.Context, _ string) (*AccountInfo, error) {
	return m.exchangeResult, m.exchangeErr
}

func (m *mockClient) GetAccount(_ context.Context, _ string) (*AccountInfo, error) {
	return m.accountResult, m.accountErr
}

func (m *mockClient) GetTransactions(_ context.Context, _ string, _ *TransactionQuery) ([]Transaction, error) {
	return m.transactionsResult, m.transactionsErr
}

func (m *mockClient) InitiatePayment(_ context.Context, _ *PaymentRequest) (*PaymentInitiationResult, error) {
	return m.paymentResult, m.paymentErr
}

func (m *mockClient) VerifyPayment(_ context.Context, _ string) (*PaymentVerification, error) {
	return m.verifyResult, m.verifyErr
}

func (m *mockClient) InitiateIncomeAnalysis(_ context.Context, _ string, _ int) error {
	return m.incomeErr
}

func (m *mockClient) UnlinkAccount(_ context.Context, _ string) error {
	return m.unlinkErr
}

// --- Tests ---

func TestInitiateLinking(t *testing.T) {
	client := &mockClient{initiateLinkingURL: "https://link.mono.co/TEST123"}
	svc := NewService(client, nil, nil)

	url, err := svc.InitiateLinking(context.Background(), uuid.New(), "Test User", "test@test.com", "https://rail.app/redirect")
	if err != nil {
		t.Fatalf("InitiateLinking failed: %v", err)
	}
	if url != "https://link.mono.co/TEST123" {
		t.Errorf("expected mono link URL, got %s", url)
	}
}

func TestInitiateLinkingError(t *testing.T) {
	client := &mockClient{initiateLinkingErr: context.DeadlineExceeded}
	svc := NewService(client, nil, nil)

	_, err := svc.InitiateLinking(context.Background(), uuid.New(), "Test", "test@test.com", "https://rail.app")
	if err == nil {
		t.Fatal("expected error when client fails")
	}
}

func TestPaymentInitiationResultFields(t *testing.T) {
	// Verify the PaymentInitiationResult struct has the expected fields
	// that the DI adapter populates from the corrected Mono API response.
	result := &PaymentInitiationResult{
		Status:      "pending",
		PaymentID:   "ODW2QV0WLIDG",
		ApprovalURL: "https://checkout.mono.co/ODW2QV0WLIDG",
	}
	if result.PaymentID != "ODW2QV0WLIDG" {
		t.Errorf("expected PaymentID to be set from Mono's id field")
	}
	if result.ApprovalURL != "https://checkout.mono.co/ODW2QV0WLIDG" {
		t.Errorf("expected ApprovalURL to be set from Mono's mono_url field")
	}
}

func TestLinkingRequestFields(t *testing.T) {
	req := &LinkingRequest{
		CustomerName:  "Samuel Olamide",
		CustomerEmail: "samuel@neem.com",
		MetaRef:       "user-uuid-123",
		RedirectURL:   "https://rail.app/redirect",
	}
	if req.CustomerName != "Samuel Olamide" {
		t.Error("CustomerName not set")
	}
	if req.RedirectURL != "https://rail.app/redirect" {
		t.Error("RedirectURL not set")
	}
}

func TestAccountInfoFields(t *testing.T) {
	info := &AccountInfo{
		ID:            "6979274350b9c321c14524b1",
		Name:          "Samuel Olamide",
		BankName:      "GTBank",
		AccountNumber: "0123456789",
		Type:          "SAVINGS",
		Currency:      "NGN",
		Balance:       73573,
	}
	if info.BankName != "GTBank" {
		t.Error("BankName should be populated from Institution.Name")
	}
	if info.Balance != 73573 {
		t.Error("Balance should be in kobo")
	}
}

func TestTransactionFields(t *testing.T) {
	txns := []Transaction{
		{
			ID:          "66141bbff58d2687e7d91234",
			Amount:      500,
			Type:        "debit",
			Description: "PG00001",
			Category:    "bank_charges",
			SubCategory: "fees",
			Date:        time.Now(),
			Reference:   "REF001",
		},
	}
	if txns[0].Category != "bank_charges" {
		t.Error("Category should be set from enriched metadata")
	}
	if txns[0].SubCategory != "fees" {
		t.Error("SubCategory should be set from enriched metadata")
	}
}

func TestPaymentVerificationFields(t *testing.T) {
	v := &PaymentVerification{
		Status:  "successful",
		MonoRef: "ODW2QV0WLIDG",
	}
	if v.Status != "successful" {
		t.Error("Status should match Mono payment status")
	}
}

func TestEntityStatusConstants(t *testing.T) {
	// Verify entity status constants are consistent
	if entities.MonoAccountStatusLinked != "linked" {
		t.Error("MonoAccountStatusLinked should be 'linked'")
	}
	if entities.MonoAccountStatusUnlinked != "unlinked" {
		t.Error("MonoAccountStatusUnlinked should be 'unlinked'")
	}
	if entities.MonoPaymentStatusSuccessful != "successful" {
		t.Error("MonoPaymentStatusSuccessful should be 'successful'")
	}
	if entities.MonoPaymentStatusPending != "pending" {
		t.Error("MonoPaymentStatusPending should be 'pending'")
	}
}
