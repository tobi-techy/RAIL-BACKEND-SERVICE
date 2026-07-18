package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Classification layer that resolved the enrichment.
const (
	ClassificationLayerRule = "rule"
	ClassificationLayerML   = "ml"
	ClassificationLayerLLM  = "llm"
)

// BehaviorTag represents a detected behavioral pattern on a transaction.
type BehaviorTag struct {
	Tag        string          `json:"tag"`
	Confidence float64         `json:"confidence"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// TransactionFact represents a durable financial fact extracted from a transaction.
type TransactionFact struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
}

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
	Counterparty string `json:"counterparty" db:"counterparty"`
	CategoryL1   string `json:"category_l1" db:"category_l1"`
	CategoryL2   string `json:"category_l2" db:"category_l2"`
	IsEssential  bool   `json:"is_essential" db:"is_essential"`
	IsRecurring  bool   `json:"is_recurring" db:"is_recurring"`

	// Understanding fields — plain-English descriptions for Miriam to read transactions
	PlainDescription string `json:"plain_description" db:"plain_description"` // "Groceries at Shoprite"
	MerchantContext  string `json:"merchant_context" db:"merchant_context"`   // "Supermarket chain, you shop here weekly"

	// Bank & transaction type from ML sidecar
	Bank   string `json:"bank" db:"bank"`     // "Chase", "GTBank", etc.
	TxType string `json:"tx_type" db:"tx_type"` // "card_payment", "p2p", "atm_withdrawal", etc.

	// Pipeline output fields — behavior tags, financial facts, embeddings
	BehaviorTags json.RawMessage `json:"behavior_tags" db:"behavior_tags"` // JSON array of BehaviorTag
	Facts        json.RawMessage `json:"facts" db:"facts"`                 // JSON array of TransactionFact
	Embedding    []float32       `json:"embedding" db:"embedding"`         // vector for semantic search

	// Classification metadata
	ClassificationLayer string          `json:"classification_layer" db:"classification_layer"`
	Confidence          decimal.Decimal `json:"confidence" db:"confidence"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
