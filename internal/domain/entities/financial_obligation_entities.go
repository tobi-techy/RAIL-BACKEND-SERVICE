package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	ObligationTypeDebt          = "debt"
	ObligationTypeInvoice       = "invoice"
	ObligationTypePayroll       = "payroll"
	ObligationTypeInsurance     = "insurance"
	ObligationTypeEducation     = "education"
	ObligationTypeRent          = "rent"
	ObligationTypeFamilySupport = "family_support"
	ObligationTypeTax           = "tax"
	ObligationTypeSubscription  = "subscription"
	ObligationTypeVendorBill    = "vendor_bill"
	ObligationTypeOther         = "other"

	ObligationCadenceOneTime   = "one_time"
	ObligationCadenceWeekly    = "weekly"
	ObligationCadenceBiweekly  = "biweekly"
	ObligationCadenceMonthly   = "monthly"
	ObligationCadenceQuarterly = "quarterly"
	ObligationCadenceAnnual    = "annual"

	ObligationPriorityCritical = "critical"
	ObligationPriorityHigh     = "high"
	ObligationPriorityMedium   = "medium"
	ObligationPriorityLow      = "low"

	ObligationStatusActive    = "active"
	ObligationStatusPaused    = "paused"
	ObligationStatusPaid      = "paid"
	ObligationStatusCancelled = "cancelled"
)

// FinancialObligation stores user-entered recurring or dated obligations Miriam
// can use to build operating plans without relying on bank aggregation.
type FinancialObligation struct {
	ID           uuid.UUID       `json:"id" db:"id"`
	UserID       uuid.UUID       `json:"user_id" db:"user_id"`
	Type         string          `json:"type" db:"type"`
	Name         string          `json:"name" db:"name"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	Currency     string          `json:"currency" db:"currency"`
	Cadence      string          `json:"cadence" db:"cadence"`
	DueDate      *time.Time      `json:"due_date,omitempty" db:"due_date"`
	DueDay       *int            `json:"due_day,omitempty" db:"due_day"`
	Priority     string          `json:"priority" db:"priority"`
	Counterparty *string         `json:"counterparty,omitempty" db:"counterparty"`
	InterestRate *decimal.Decimal `json:"interest_rate,omitempty" db:"interest_rate"`
	Status       string          `json:"status" db:"status"`
	Metadata     json.RawMessage `json:"metadata,omitempty" db:"metadata"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
}
