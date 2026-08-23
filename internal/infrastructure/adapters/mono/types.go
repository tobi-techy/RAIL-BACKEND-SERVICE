package mono

import "time"

// --- API response wrapper ---

// monoResponse wraps the standard Mono response envelope:
//   { "status": "successful", "data": {...}, "meta": {...} }
type monoResponse[T any] struct {
	Status string `json:"status"`
	Data   T      `json:"data"`
	Meta   *Meta  `json:"meta,omitempty"`
}

// Meta holds pagination metadata.
type Meta struct {
	Next      string `json:"next,omitempty"`
	Previous  string `json:"previous,omitempty"`
	Total     int    `json:"total,omitempty"`
	PageCount int    `json:"page_count,omitempty"`
}

// --- Account Linking (Authorisation) ---

// InitiateLinkingRequest is the body for POST /v2/accounts/initiate.
type InitiateLinkingRequest struct {
	Customer    Customer `json:"customer"`
	Meta        *MetaRef `json:"meta,omitempty"`
	Scope       string   `json:"scope"`        // "auth"
	RedirectURL string   `json:"redirect_url"` // where Mono widget redirects after linking
}

// Customer identifies the user on the Mono Connect widget.
type Customer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// MetaRef is a free-form reference tag for the linking session.
type MetaRef struct {
	Ref string `json:"ref,omitempty"`
}

// InitiateLinkingResponse is returned in the data field of the initiate response.
type InitiateLinkingResponse struct {
	RedirectURL string `json:"redirect_url"` // Mono Connect widget URL — open in webview
}

// ExchangeTokenRequest is the body for POST /v2/accounts/auth.
type ExchangeTokenRequest struct {
	Code string `json:"code"`
}

// ExchangeTokenResponse is returned after exchanging the public code for an account ID.
type ExchangeTokenResponse struct {
	ID     string `json:"id"`     // Mono account ID (persistent, used for all subsequent calls)
	Account AccountBrief `json:"account"`
}

// AccountBrief is the lightweight account info returned by the exchange endpoint.
type AccountBrief struct {
	Name          string `json:"name"`
	BankName      string `json:"bank_name,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	Type          string `json:"type,omitempty"`
}

// --- Account Details ---

// Account holds the full account details from GET /v2/accounts/{id}.
type Account struct {
	ID             string    `json:"_id"`
	AccountNumber  string    `json:"account_number"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Balance        int64     `json:"balance"` // in kobo/pesewa/cents
	BankName       string    `json:"meta.bank_name,omitempty"` // nested in meta on some responses
	Currency       string    `json:"currency,omitempty"`       // e.g. NGN
	BVN            string    `json:"bvn,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AccountDetailsResponse wraps the account object in the data field.
type AccountDetailsResponse struct {
	Account Account `json:"account"`
}

// --- Transactions ---

// Transaction is a single transaction from GET /v2/accounts/{id}/transactions.
type Transaction struct {
	ID          string    `json:"_id"`
	Amount      int64     `json:"amount"`       // in kobo/pesewa/cents
	Type        string    `json:"type"`          // "credit" or "debit"
	Description string    `json:"narration"`     // transaction narration
	Date        time.Time `json:"date"`
	Category    string    `json:"category,omitempty"`
	Balance     int64     `json:"balance,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	Meta        *TransactionMeta `json:"meta,omitempty"`
}

// TransactionMeta holds enriched metadata (merchant, category, etc.).
type TransactionMeta struct {
	Type        string `json:"type,omitempty"`
	Category    string `json:"category,omitempty"`
	SubCategory string `json:"sub_category,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// TransactionsResponse wraps a paginated list of transactions.
type TransactionsResponse struct {
	Transactions []Transaction `json:"transactions"`
}

// TransactionListQuery holds filter parameters for the transactions endpoint.
type TransactionListQuery struct {
	Start     string // ISO date, e.g. 2024-01-01
	End       string // ISO date
	Type      string // "credit" or "debit"
	Paginate  bool   // false = return all in one request
	Limit     int    // page size (default 50)
	RealTime  bool   // set x-real-time header
}

// --- Income Analysis ---

// IncomeAnalysis from GET /v2/accounts/{id}/income.
type IncomeAnalysis struct {
	TotalIncome        int64             `json:"total_income"`
	NumberOfIncome     int               `json:"number_of_income"`
	AverageIncomeValue int64             `json:"average_income_value"`
	IncomeSlots        []IncomeSlot      `json:"income_slots"`
	Type               string            `json:"type,omitempty"`
}

// IncomeSlot is a single income period summary.
type IncomeSlot struct {
	StartDate  string `json:"start_date"`
	EndDate    string `json:"end_date"`
	Amount     int64  `json:"amount"`
	Count      int    `json:"count"`
}

// IncomeResponse wraps the income analysis in the data field.
type IncomeResponse struct {
	Income IncomeAnalysis `json:"income"`
}

// --- Identity ---

// Identity from GET /v2/accounts/{id}/identity.
type Identity struct {
	Name          string `json:"name"`
	Email         string `json:"email,omitempty"`
	Phone         string `json:"phone,omitempty"`
	BVN           string `json:"bvn,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
}

// IdentityResponse wraps the identity object.
type IdentityResponse struct {
	Identity Identity `json:"identity"`
}

// --- DirectPay (one-time payments) ---

// InitiatePaymentRequest is the body for POST /v2/payments/initiate.
type InitiatePaymentRequest struct {
	Amount      int64             `json:"amount"`        // in kobo
	Type        string            `json:"type"`          // "onetime-debit"
	Method      string            `json:"method"`        // "account", "transfer", "whatsapp"
	Account     string            `json:"account"`       // linked Mono account ID
	Description string            `json:"description"`
	Reference   string            `json:"reference"`     // merchant-unique reference
	RedirectURL string            `json:"redirect_url"`
	Customer    *PaymentCustomer  `json:"customer,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
}

// PaymentCustomer is the customer object for DirectPay.
type PaymentCustomer struct {
	Email    string            `json:"email,omitempty"`
	Phone    string            `json:"phone,omitempty"`
	Name     string            `json:"name,omitempty"`
	Address  string            `json:"address,omitempty"`
	Identity *PaymentIdentity  `json:"identity,omitempty"`
}

// PaymentIdentity is BVN/NIN verification for DirectPay.
type PaymentIdentity struct {
	Type   string `json:"type,omitempty"`   // "bvn", "nin"
	Number string `json:"number,omitempty"`
}

// InitiatePaymentResponse is returned in the data field.
type InitiatePaymentResponse struct {
	Amount       int64  `json:"amount"`
	Reference    string `json:"reference"`
	Type         string `json:"type"`
	Status       string `json:"status"`        // "pending" or "successful"
	ApprovalURL  string `json:"approval_url"`  // payment link for the user
	PaymentID    string `json:"payment_id,omitempty"`
}

// PaymentVerifyResponse is returned by GET /v2/payments/verify.
type PaymentVerifyResponse struct {
	Amount         int64  `json:"amount"`
	Reference      string `json:"reference"`
	Status         string `json:"status"` // "successful", "failed", "pending", "reversed"
	Type           string `json:"type"`
	AccountID      string `json:"account_id,omitempty"`
	MonoRef        string `json:"mono_ref,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// PaymentStatus is a type alias for payment states.
type PaymentStatus string

const (
	PaymentStatusSuccessful PaymentStatus = "successful"
	PaymentStatusFailed     PaymentStatus = "failed"
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusReversed   PaymentStatus = "reversed"
)

// --- Unlink ---

// UnlinkResponse is returned by POST /v2/accounts/{id}/unlink.
type UnlinkResponse struct {
	Message string `json:"message,omitempty"`
}
