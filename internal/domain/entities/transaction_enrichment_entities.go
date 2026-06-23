package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Classification layer that resolved the enrichment.
const (
	ClassificationLayerRule    = "rule"
	ClassificationLayerML      = "ml"
	ClassificationLayerLLM     = "llm"
)

// EnrichedTransaction is the structured representation of a raw transaction
// after passing through the enrichment pipeline.
type EnrichedTransaction struct {
	ID              uuid.UUID       `json:"id" db:"id"`
	TransactionID   uuid.UUID       `json:"transaction_id" db:"transaction_id"`
	UserID          uuid.UUID       `json:"user_id" db:"user_id"`
	RawDescription  string          `json:"raw_description" db:"raw_description"`
	Amount          decimal.Decimal `json:"amount" db:"amount"`
	Currency        string          `json:"currency" db:"currency"`
	TransactionDate time.Time       `json:"transaction_date" db:"transaction_date"`
	Direction       string          `json:"direction" db:"direction"` // inflow, outflow

	// Enriched fields
	Counterparty    string `json:"counterparty" db:"counterparty"`
	CategoryL1      string `json:"category_l1" db:"category_l1"`
	CategoryL2      string `json:"category_l2" db:"category_l2"`
	IsEssential     bool   `json:"is_essential" db:"is_essential"`
	IsRecurring     bool   `json:"is_recurring" db:"is_recurring"`

	// Classification metadata
	ClassificationLayer string          `json:"classification_layer" db:"classification_layer"`
	Confidence          decimal.Decimal `json:"confidence" db:"confidence"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
