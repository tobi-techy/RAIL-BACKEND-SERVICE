package entities

import (
	"time"

	"github.com/google/uuid"
)

// --- Linked Account Status ---

const (
	MonoAccountStatusLinked    = "linked"     // account is connected and data is available
	MonoAccountStatusReauth    = "reauth"     // re-authorisation required (Mono sent webhook)
	MonoAccountStatusUnlinked  = "unlinked"   // user or system disconnected the account
)

// MonoLinkedAccount represents a user's bank account linked through Mono Connect.
// The MonoAccountID is the persistent identifier returned by POST /v2/accounts/auth
// and is used for all subsequent Financial Data API calls.
type MonoLinkedAccount struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	MonoAccountID string     `json:"mono_account_id" db:"mono_account_id"` // Mono's persistent account ID
	Institution   string     `json:"institution" db:"institution"`         // bank name
	AccountName   string     `json:"account_name" db:"account_name"`       // account holder name
	AccountNumber string     `json:"account_number" db:"account_number"`   // last 4 digits for display
	AccountType   string     `json:"account_type" db:"account_type"`       // savings, current, etc.
	Currency      string     `json:"currency" db:"currency"`               // NGN, GHS, KES, ZAR
	Balance       int64      `json:"balance" db:"balance"`                 // in kobo/pesewa/cents
	Status        string     `json:"status" db:"status"`
	LastSyncedAt  *time.Time `json:"last_synced_at,omitempty" db:"last_synced_at"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// MonoImportedTransaction is a transaction imported from a Mono-linked account.
// Amounts are stored in kobo/pesewa/cents (the raw Mono unit) and divided by 100 for display.
type MonoImportedTransaction struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	AccountID     uuid.UUID  `json:"account_id" db:"account_id"`     // FK to mono_linked_accounts.id
	MonoTxnID     string     `json:"mono_txn_id" db:"mono_txn_id"`   // Mono's transaction _id
	Amount        int64      `json:"amount" db:"amount"`             // kobo/pesewa/cents
	Type          string     `json:"type" db:"type"`                 // "credit" or "debit"
	Description   string     `json:"description" db:"description"`   // narration
	Category      string     `json:"category" db:"category"`         // Mono-enriched category
	SubCategory   string     `json:"sub_category" db:"sub_category"` // Mono-enriched sub-category
	TransactionDate time.Time `json:"transaction_date" db:"transaction_date"`
	Reference     string     `json:"reference" db:"reference"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
}

// MonoPayment represents a DirectPay one-time payment initiated through Mono.
type MonoPayment struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	UserID        uuid.UUID  `json:"user_id" db:"user_id"`
	AccountID     uuid.UUID  `json:"account_id" db:"account_id"` // FK to mono_linked_accounts.id
	Amount        int64      `json:"amount" db:"amount"`         // kobo
	Reference     string     `json:"reference" db:"reference"`   // merchant-unique reference
	Status        string     `json:"status" db:"status"`         // pending, successful, failed, reversed
	MonoRef       string     `json:"mono_ref" db:"mono_ref"`     // Mono's internal reference
	ApprovalURL   string     `json:"approval_url" db:"approval_url"` // URL the user visits to authorise
	Description   string     `json:"description" db:"description"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty" db:"verified_at"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

// --- Payment Status Constants ---

const (
	MonoPaymentStatusPending    = "pending"
	MonoPaymentStatusSuccessful = "successful"
	MonoPaymentStatusFailed     = "failed"
	MonoPaymentStatusReversed   = "reversed"
)

// --- Spending Analysis (computed from imported transactions) ---

// MonoSpendingAnalysis is the aggregated spending breakdown derived from
// Mono-imported transactions. Used by Miriam's coaching context and the
// bank statement analysis tool.
type MonoSpendingAnalysis struct {
	TotalCredits     int64                      `json:"total_credits"`      // kobo
	TotalDebits      int64                      `json:"total_debits"`       // kobo
	NetCashFlow      int64                      `json:"net_cash_flow"`      // total_credits - total_debits
	SavingsRate      float64                    `json:"savings_rate"`       // 0-1
	ByCategory       []MonoCategoryBreakdown    `json:"by_category"`
	Period           MonoAnalysisPeriod         `json:"period"`
	TransactionCount int                        `json:"transaction_count"`
}

// MonoCategoryBreakdown is the spending total for a single category.
type MonoCategoryBreakdown struct {
	Category string  `json:"category"`
	Amount   int64   `json:"amount"`  // kobo
	Count    int     `json:"count"`
	Percent  float64 `json:"percent"` // share of total debits (0-1)
}

// MonoAnalysisPeriod defines the date range for an analysis.
type MonoAnalysisPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Days  int       `json:"days"`
}
