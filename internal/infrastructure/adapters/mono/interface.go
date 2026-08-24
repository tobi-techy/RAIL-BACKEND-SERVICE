package mono

import "context"

// Client defines the Mono Financial Data + DirectPay API surface.
type Client interface {
	// --- Account Linking (Authorisation) ---

	// InitiateLinking starts the Mono Connect widget flow. Returns the redirect URL
	// the frontend should open in a webview for the user to select their bank.
	InitiateLinking(ctx context.Context, req *InitiateLinkingRequest) (*InitiateLinkingResponse, error)

	// ExchangeCode swaps the public code (returned by the widget after linking)
	// for a persistent Mono account ID.
	ExchangeCode(ctx context.Context, code string) (*ExchangeTokenResponse, error)

	// --- Account Data ---

	// GetAccount retrieves details for a linked account (balance, number, type, etc.).
	GetAccount(ctx context.Context, accountID string) (*Account, error)

	// GetTransactions retrieves transactions for a linked account with optional filters.
	GetTransactions(ctx context.Context, accountID string, query *TransactionListQuery) ([]Transaction, error)

	// GetIncome retrieves income analysis for a linked account.
	// Note: The first call triggers a background analysis; results arrive
	// via the mono.events.account_income webhook.
	GetIncome(ctx context.Context, accountID string) (*IncomeAnalysis, error)

	// InitiateIncomeAnalysis triggers the async income analysis for a linked
	// account. Results come via the mono.events.account_income webhook.
	// periodMonths limits analysis to N months (0 = all available history).
	InitiateIncomeAnalysis(ctx context.Context, accountID string, periodMonths int) error

	// GetIdentity retrieves identity verification data for a linked account.
	GetIdentity(ctx context.Context, accountID string) (*Identity, error)

	// UnlinkAccount disconnects a linked account. Subsequent calls with that
	// account ID will fail.
	UnlinkAccount(ctx context.Context, accountID string) error

	// --- DirectPay (one-time payments) ---

	// InitiatePayment starts a one-time debit from a linked bank account.
	// Returns an approval URL the user must visit to authorise the payment.
	InitiatePayment(ctx context.Context, req *InitiatePaymentRequest) (*InitiatePaymentResponse, error)

	// VerifyPayment checks the status of a DirectPay transaction by reference.
	VerifyPayment(ctx context.Context, reference string) (*PaymentVerifyResponse, error)
}
