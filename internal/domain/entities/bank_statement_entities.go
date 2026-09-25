package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	StatementStatusPending    = "pending"
	StatementStatusProcessing = "processing"
	StatementStatusCompleted  = "completed"
	StatementStatusFailed     = "failed"

	StatementTxnTypeCredit = "credit"
	StatementTxnTypeDebit  = "debit"

	StatementRecurrenceOneOff       = "one_off"
	StatementRecurrenceBill         = "bill"
	StatementRecurrenceSubscription = "subscription"

	StatementRuleExact    = "exact"
	StatementRuleContains = "contains"

	FactCategoryExternalSpending = "external_spending"
	FactCategoryExternalIncome   = "external_income"
	FactSourceBankStatement      = "bank_statement"
)

type BankStatementUpload struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	UserID           uuid.UUID  `json:"user_id" db:"user_id"`
	BankName         string     `json:"bank_name" db:"bank_name"`
	FileHash         string     `json:"file_hash" db:"file_hash"`
	FileSizeBytes    int        `json:"file_size_bytes" db:"file_size_bytes"`
	FileData         []byte     `json:"-" db:"file_data"`
	PageCount        *int       `json:"page_count,omitempty" db:"page_count"`
	Status           string     `json:"status" db:"status"`
	ErrorMessage     *string    `json:"error_message,omitempty" db:"error_message"`
	Summary          *string    `json:"summary,omitempty" db:"summary"`
	PeriodStart      *time.Time `json:"period_start,omitempty" db:"period_start"`
	PeriodEnd        *time.Time `json:"period_end,omitempty" db:"period_end"`
	TransactionCount int        `json:"transaction_count" db:"transaction_count"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

type BankStatementTransaction struct {
	ID                 uuid.UUID        `json:"id" db:"id"`
	UploadID           uuid.UUID        `json:"upload_id" db:"upload_id"`
	UserID             uuid.UUID        `json:"user_id" db:"user_id"`
	TransactionDate    time.Time        `json:"transaction_date" db:"transaction_date"`
	Description        string           `json:"description" db:"description"`
	Amount             decimal.Decimal  `json:"amount" db:"amount"`
	Currency           string           `json:"currency" db:"currency"`
	Type               string           `json:"type" db:"type"`
	Category           string           `json:"category" db:"category"`
	Counterparty       string           `json:"counterparty" db:"counterparty"`
	IsEssential        bool             `json:"is_essential" db:"is_essential"`
	CategoryConfidence float64          `json:"category_confidence" db:"category_confidence"`
	Recurrence         string           `json:"recurrence" db:"recurrence"`
	BalanceAfter       *decimal.Decimal `json:"balance_after,omitempty" db:"balance_after"`
	RawLine            *string          `json:"raw_line,omitempty" db:"raw_line"`
	CreatedAt          time.Time        `json:"created_at" db:"created_at"`
}

// StatementCategoryRule is a user correction. It overrides the categorizer
// for future statement lines and for lines already stored for that user.
type StatementCategoryRule struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	MatchType string    `json:"match_type" db:"match_type"`
	Pattern   string    `json:"pattern" db:"pattern"`
	Bucket    string    `json:"bucket" db:"bucket"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
