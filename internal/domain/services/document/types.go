// Package document implements the document-intelligence pipeline: OCR ->
// classify -> extract -> validate -> enrich. Every stage is an interface so
// engines/parsers/LLMs can be swapped without touching the orchestration.
package document

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Line is a single OCR line with its bounding box and confidence.
type Line struct {
	Text       string      `json:"text"`
	BBox       [][]float64 `json:"bbox"`
	Confidence float64     `json:"confidence"`
	Page       int         `json:"page"`
}

// OCRResult is the normalized output of any OCR engine.
type OCRResult struct {
	Text           string  `json:"text"`
	PageCount      int     `json:"page_count"`
	MeanConfidence float64 `json:"mean_confidence"`
	Lines          []Line  `json:"lines"`
	Engine         string  `json:"engine"`
}

// DocType is the classified document type.
type DocType string

const (
	DocUnknown       DocType = "unknown"
	DocReceipt       DocType = "receipt"
	DocBankStatement DocType = "bank_statement"
	DocInvoice       DocType = "invoice"
)

// LineItem is a single item on a receipt/invoice.
type LineItem struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity,omitempty"`
	Price    string `json:"price,omitempty"`
}

// Txn is a single parsed bank-statement transaction (deterministic layer).
type Txn struct {
	Date         time.Time        `json:"date"`
	Description  string           `json:"description"`
	Amount       decimal.Decimal  `json:"amount"`
	Type         string           `json:"type"` // "credit" | "debit"
	BalanceAfter *decimal.Decimal `json:"balance_after,omitempty"`
	Category     string           `json:"category,omitempty"`
}

// Fields is the structured data extracted from a document. Which subset is
// populated depends on DocType.
type Fields struct {
	Type DocType `json:"type"`

	// Receipt / invoice
	Merchant string           `json:"merchant,omitempty"`
	Total    *decimal.Decimal `json:"total,omitempty"`
	Subtotal *decimal.Decimal `json:"subtotal,omitempty"`
	Tax      *decimal.Decimal `json:"tax,omitempty"`
	Currency string           `json:"currency,omitempty"`
	Date     *time.Time       `json:"date,omitempty"`
	Items    []LineItem       `json:"items,omitempty"`

	// Bank statement
	AccountName    string           `json:"account_name,omitempty"`
	OpeningBalance *decimal.Decimal `json:"opening_balance,omitempty"`
	ClosingBalance *decimal.Decimal `json:"closing_balance,omitempty"`
	PeriodStart    *time.Time       `json:"period_start,omitempty"`
	PeriodEnd      *time.Time       `json:"period_end,omitempty"`
	Transactions   []Txn            `json:"transactions,omitempty"`
	BankName       string           `json:"bank_name,omitempty"`
}

// Validation is the outcome of the deterministic validation stage.
type Validation struct {
	Passed     bool     `json:"passed"`
	Errors     []string `json:"errors"`
	Confidence float64  `json:"confidence"`
}

// Enrichment is the LLM-produced categorization/normalization overlay. It never
// sees the raw image — only the already-extracted structured fields.
type Enrichment struct {
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Merchant    string `json:"merchant,omitempty"` // normalized merchant name
}

// OCREngine turns document bytes into structured text.
type OCREngine interface {
	Recognize(ctx context.Context, data []byte, mimeType string) (*OCRResult, error)
}

// Classifier determines the document type from OCR output using deterministic rules.
type Classifier interface {
	Classify(text string, lines []Line) DocType
}

// FieldExtractor pulls structured fields from OCR text for a known doc type.
type FieldExtractor interface {
	Extract(doc DocType, text string, lines []Line) (*Fields, error)
}

// Validator applies deterministic consistency checks to extracted fields.
type Validator interface {
	Validate(doc DocType, f *Fields) *Validation
}

// Enricher normalizes/categorizes extracted fields via an LLM (optional).
type Enricher interface {
	Categorize(ctx context.Context, f *Fields) (*Enrichment, error)
}
