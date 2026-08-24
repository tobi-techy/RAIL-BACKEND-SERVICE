package mono

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

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
// MonoURL is the Mono Connect link the user opens in a webview to select their bank.
type InitiateLinkingResponse struct {
	MonoURL string `json:"mono_url"` // Mono Connect widget URL — open in webview
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
// The Mono API nests institution info under account.institution and
// data availability under meta.data_status.
type Account struct {
	ID            string       `json:"_id"`
	AccountNumber string       `json:"account_number"`
	Name          string       `json:"name"`
	Type          string       `json:"type"`
	Balance       int64        `json:"balance"`     // in kobo/pesewa/cents
	Currency      string       `json:"currency"`   // e.g. NGN
	BVN           string       `json:"bvn,omitempty"`
	AuthMethod    string       `json:"auth_method,omitempty"`
	Institution   *Institution `json:"institution,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Institution holds the bank/institution details from the Mono API.
type Institution struct {
	Name     string `json:"name"`      // e.g. "GTBank"
	BankCode string `json:"bank_code"` // e.g. "058"
	Type     string `json:"type"`      // e.g. "PERSONAL_BANKING"
}

// AccountDetailsResponse wraps the account + meta in the data field.
type AccountDetailsResponse struct {
	Account Account      `json:"account"`
	Meta    *AccountMeta `json:"meta,omitempty"`
}

// AccountMeta holds data availability status after linking.
type AccountMeta struct {
	DataStatus    string   `json:"data_status"`    // available, partial, unavailable, failed
	AuthMethod    string   `json:"auth_method"`
	RetrievedData []string `json:"retrieved_data"`  // e.g. ["identity", "balance", "transactions"]
}

// --- Transactions ---

// Transaction is a single transaction from GET /v2/accounts/{id}/transactions.
// Note: transactions use "id" (not "_id" like accounts).
type Transaction struct {
	ID          string          `json:"id"`
	Amount      int64           `json:"amount"`       // in kobo/pesewa/cents
	Type        string          `json:"type"`          // "credit" or "debit"
	Description string          `json:"narration"`     // transaction narration
	Date        time.Time       `json:"date"`
	Category    string          `json:"category,omitempty"`
	Balance     int64           `json:"balance,omitempty"`
	Reference   string          `json:"reference,omitempty"`
	Meta        *TransactionMeta `json:"meta,omitempty"`
}

// TransactionMeta holds enriched metadata (merchant, category, etc.).
type TransactionMeta struct {
	Type        string `json:"type,omitempty"`
	Category    string `json:"category,omitempty"`
	SubCategory string `json:"sub_category,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// TransactionsResponse is not used — the Mono API returns data as a bare
// array, not wrapped in {"transactions": [...]}. The client unmarshals
// directly into monoResponse[[]Transaction].
// Kept for backward compatibility; do not use.
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
// MonoURL is the checkout link the user opens to authorize the payment.
type InitiatePaymentResponse struct {
	ID          string    `json:"id"`           // Mono payment ID
	MonoURL     string    `json:"mono_url"`     // checkout URL for the user
	Amount      int64     `json:"amount"`
	Reference   string    `json:"reference"`
	Type        string    `json:"type"`
	Method      string    `json:"method"`
	Status      string    `json:"status,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
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

// --- Webhook Event Types ---

// WebhookEvent is the envelope for all Mono webhook deliveries.
type WebhookEvent struct {
	Event     string      `json:"event"`       // e.g. "mono.events.account_connected"
	EventID   string      `json:"event_id"`
	Timestamp time.Time   `json:"timestamp"`
	Data      WebhookData `json:"data"`
}

// WebhookData is the data payload of a webhook event. Different events
// populate different fields — the rest are nil/empty.
//
// Note on the "account" field: Mono uses it differently per event:
//   - mono.events.account_connected: data.id holds the account ID (string)
//   - mono.events.account_updated: data.account holds the full account object
//   - mono.events.account_income: data.account holds the account ID (string)
// We use a json.RawMessage so both shapes can be decoded without conflict.
type WebhookData struct {
	// account_connected: the account ID is in data.id
	ID string `json:"id,omitempty"`

	// account_updated: full account object; account_income: account ID string.
	// Use AccountObject() or AccountIDStr() to extract the right value.
	AccountRaw json.RawMessage `json:"account,omitempty"`

	// Data availability (account_updated, account_connected)
	Meta *AccountMeta `json:"meta,omitempty"`

	// Income analysis result (mono.events.account_income)
	IncomeSummary *IncomeSummary `json:"income_summary,omitempty"`
	IncomeStreams []IncomeStream `json:"income_streams,omitempty"`
	AccountName   string         `json:"account_name,omitempty"`
	AnnualIncome  int64          `json:"annual_income,omitempty"`
	MonthlyIncome int64          `json:"monthly_income,omitempty"`

	// Payment events (direct_debit.payment_*)
	Reference     string `json:"reference,omitempty"`
	PaymentStatus string `json:"status,omitempty"`
}

// AccountObject attempts to decode the raw account field as a WebhookAccount
// struct (used by mono.events.account_updated). Returns nil if the field is
// empty or contains a string instead of an object.
func (d *WebhookData) AccountObject() *WebhookAccount {
	if len(d.AccountRaw) == 0 || d.AccountRaw[0] != '{' {
		return nil
	}
	var acct WebhookAccount
	if err := json.Unmarshal(d.AccountRaw, &acct); err != nil {
		return nil
	}
	return &acct
}

// AccountIDStr attempts to extract the account field as a string ID (used by
// mono.events.account_income and mono.events.account_unlinked when the
// account is just an ID string). Returns "" if the field is an object or empty.
func (d *WebhookData) AccountIDStr() string {
	if len(d.AccountRaw) == 0 || d.AccountRaw[0] == '{' {
		return ""
	}
	var s string
	if err := json.Unmarshal(d.AccountRaw, &s); err != nil {
		return ""
	}
	return s
}

// WebhookAccount is the account object in webhook payloads (uses camelCase
// for accountNumber and authMethod, unlike the REST API which uses snake_case).
type WebhookAccount struct {
	ID            string              `json:"_id"`
	Name          string              `json:"name"`
	AccountNumber string              `json:"accountNumber"`
	Currency      string              `json:"currency"`
	Balance       int64               `json:"balance"`
	Type          string              `json:"type"`
	BVN           string              `json:"bvn"`
	AuthMethod    string              `json:"authMethod"`
	Institution   *WebhookInstitution `json:"institution"`
}

// WebhookInstitution is the institution object in webhook payloads.
// Mono webhooks use camelCase bankCode; the REST Institution type keeps bank_code.
type WebhookInstitution struct {
	Name     string `json:"name"`
	BankCode string `json:"bankCode"`
	Type     string `json:"type"`
}

// IncomeSummary is the summary in the income webhook.
type IncomeSummary struct {
	TotalIncome int64  `json:"total_income"`
	Employer    string `json:"employer"`
}

// IncomeStream is a single income source from the income analysis.
type IncomeStream struct {
	IncomeType            string  `json:"income_type"`            // SALARY, WAGES
	Frequency             string  `json:"frequency"`              // MONTHLY, VARIABLE
	MonthlyAverage        int64           `json:"monthly_average"`
	AverageIncomeAmount   decimal.Decimal `json:"average_income_amount"`
	Stability             float64 `json:"stability"`              // 0-1
	FirstIncomeDate       string  `json:"first_income_date"`
	LastIncomeDate        string  `json:"last_income_date"`
	LastIncomeAmount      int64   `json:"last_income_amount"`
	LastIncomeDescription string  `json:"last_income_description"`
	PeriodsWithIncome     int     `json:"periods_with_income"`
	NumberOfIncomes       int     `json:"number_of_incomes"`
	NumberOfMonths        int     `json:"number_of_months"`
}
