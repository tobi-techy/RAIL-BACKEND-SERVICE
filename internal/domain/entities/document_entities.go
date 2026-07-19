package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Document status values.
const (
	DocumentStatusProcessing = "processing"
	DocumentStatusCompleted  = "completed"
	DocumentStatusFailed     = "failed"
)

// Document types produced by the classifier.
const (
	DocTypeUnknown       = "unknown"
	DocTypeReceipt       = "receipt"
	DocTypeBankStatement = "bank_statement"
	DocTypeInvoice       = "invoice"
)

// Document is a top-level uploaded file processed by the intelligence pipeline.
// The original bytes live in object storage (R2/S3); only the key is stored here.
type Document struct {
	ID            uuid.UUID `json:"id" db:"id"`
	UserID        uuid.UUID `json:"user_id" db:"user_id"`
	Status        string    `json:"status" db:"status"`
	Type          string    `json:"type" db:"type"`
	FileKey       string    `json:"file_key" db:"file_key"`
	MimeType      string    `json:"mime_type" db:"mime_type"`
	FileSizeBytes int       `json:"file_size_bytes" db:"file_size_bytes"`
	FileHash      string    `json:"file_hash" db:"file_hash"`
	ErrorMessage  *string   `json:"error_message,omitempty" db:"error_message"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// OCRResultRecord is the raw OCR output persisted for a document.
type OCRResultRecord struct {
	ID             uuid.UUID       `json:"id" db:"id"`
	DocumentID     uuid.UUID       `json:"document_id" db:"document_id"`
	RawText        string          `json:"raw_text" db:"raw_text"`
	MeanConfidence float64         `json:"mean_confidence" db:"mean_confidence"`
	Engine         string          `json:"engine" db:"engine"`
	PageCount      int             `json:"page_count" db:"page_count"`
	Lines          json.RawMessage `json:"lines" db:"lines"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// ExtractedFieldsRecord holds deterministically extracted fields for a document.
type ExtractedFieldsRecord struct {
	ID             uuid.UUID        `json:"id" db:"id"`
	DocumentID     uuid.UUID        `json:"document_id" db:"document_id"`
	Merchant       *string          `json:"merchant,omitempty" db:"merchant"`
	Amount         *decimal.Decimal `json:"amount,omitempty" db:"amount"`
	Currency       *string          `json:"currency,omitempty" db:"currency"`
	DocDate        *time.Time       `json:"doc_date,omitempty" db:"doc_date"`
	AccountName    *string          `json:"account_name,omitempty" db:"account_name"`
	OpeningBalance *decimal.Decimal `json:"opening_balance,omitempty" db:"opening_balance"`
	ClosingBalance *decimal.Decimal `json:"closing_balance,omitempty" db:"closing_balance"`
	JSON           json.RawMessage  `json:"json" db:"json"`
	CreatedAt      time.Time        `json:"created_at" db:"created_at"`
}

// DocumentValidationRecord holds the validation outcome for a document.
type DocumentValidationRecord struct {
	ID         uuid.UUID       `json:"id" db:"id"`
	DocumentID uuid.UUID       `json:"document_id" db:"document_id"`
	Passed     bool            `json:"passed" db:"passed"`
	Errors     json.RawMessage `json:"errors" db:"errors"`
	Confidence float64         `json:"confidence" db:"confidence"`
	CreatedAt  time.Time       `json:"created_at" db:"created_at"`
}
