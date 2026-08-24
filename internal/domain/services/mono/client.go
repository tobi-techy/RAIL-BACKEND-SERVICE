package mono

import (
	"context"
	"time"
)

// Client is the domain-facing contract for the Mono Financial Data + DirectPay
// HTTP API. It is implemented by the infrastructure adapter
// (internal/infrastructure/adapters/mono) via the wrapper wired in
// internal/infrastructure/di/mono_adapters.go, so this package never imports
// infrastructure directly.
type Client interface {
	// InitiateLinking starts the Mono Connect widget flow and returns the
	// redirect URL the frontend should open in a webview.
	InitiateLinking(ctx context.Context, req *LinkingRequest) (redirectURL string, err error)

	// ExchangeCode swaps the public code (returned by the widget after linking)
	// for a persistent Mono account ID plus brief account details.
	ExchangeCode(ctx context.Context, code string) (*AccountInfo, error)

	// GetAccount retrieves full details for a linked account.
	GetAccount(ctx context.Context, monoAccountID string) (*AccountInfo, error)

	// GetTransactions retrieves transactions for a linked account between two
	// dates (inclusive).
	GetTransactions(ctx context.Context, monoAccountID string, query *TransactionQuery) ([]Transaction, error)

	// InitiatePayment starts a one-time debit from a linked bank account and
	// returns an approval URL the user must visit to authorise it.
	InitiatePayment(ctx context.Context, req *PaymentRequest) (*PaymentInitiationResult, error)

	// VerifyPayment checks the status of a DirectPay transaction by reference.
	VerifyPayment(ctx context.Context, reference string) (*PaymentVerification, error)

	// InitiateIncomeAnalysis triggers the async income analysis for a linked
	// account. Results arrive via the mono.events.account_income webhook.
	// periodMonths limits analysis to N months (0 = all history).
	InitiateIncomeAnalysis(ctx context.Context, monoAccountID string, periodMonths int) error

	// UnlinkAccount disconnects a linked account.
	UnlinkAccount(ctx context.Context, monoAccountID string) error
}

// LinkingRequest starts a Mono Connect session for a customer.
type LinkingRequest struct {
	CustomerName  string
	CustomerEmail string
	MetaRef       string // free-form reference tag for the linking session
	RedirectURL   string // where the Mono widget redirects after linking
}

// AccountInfo describes a Mono account — either the brief payload from code
// exchange or the full account details endpoint.
type AccountInfo struct {
	ID            string // persistent Mono account ID used for all subsequent calls
	Name          string // account holder name
	BankName      string
	AccountNumber string
	Type          string
	Currency      string
	Balance       int64 // kobo/pesewa/cents
}

// TransactionQuery bounds a transaction fetch by date.
type TransactionQuery struct {
	Start time.Time
	End   time.Time
}

// Transaction is a single transaction from a linked Mono account. Category and
// SubCategory are already resolved from Mono's enriched metadata when present,
// falling back to the raw category.
type Transaction struct {
	ID          string
	Amount      int64 // kobo/pesewa/cents
	Type        string
	Description string
	Category    string
	SubCategory string
	Date        time.Time
	Reference   string
}

// PaymentRequest initiates a DirectPay one-time debit.
type PaymentRequest struct {
	AmountKobo    int64
	AccountID     string // linked Mono account ID
	Description   string
	Reference     string // merchant-unique reference
	RedirectURL   string
	CustomerEmail string
	CustomerName  string
}

// PaymentInitiationResult carries the outcome of a payment initiation.
type PaymentInitiationResult struct {
	Status      string
	PaymentID   string // Mono's internal reference
	ApprovalURL string // URL the user visits to authorise
}

// PaymentVerification is the result of verifying a payment with Mono.
type PaymentVerification struct {
	Status  string
	MonoRef string
}
