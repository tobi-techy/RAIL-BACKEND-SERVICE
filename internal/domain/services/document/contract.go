// Package document — contract.go addition: versioned result envelope (below).
package document

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"
)

// SchemaVersion is the current contract version served by GetResult.
const SchemaVersion = "1.0"

// SupportedMajor is the only major version this Go backend serves.
const SupportedMajor = "1"

// Statuses mirror the documents.status CHECK constraint. Future lifecycle
// states (e.g. needs_review) must be added here and in the DB constraint
// together, keeping this file the single source of truth for the API shape.
const (
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Document types mirror the documents.type CHECK constraint. New types
// (payslip, utility_bill, ...) extend this list without changing the shape.
const (
	TypeUnknown       = "unknown"
	TypeReceipt       = "receipt"
	TypeBankStatement = "bank_statement"
	TypeInvoice       = "invoice"
)

// Result is the versioned document-intelligence result envelope.
type APIResult struct {
	SchemaVersion string        `json:"schema_version"`
	DocumentID    string        `json:"document_id"`
	DocumentType  string        `json:"document_type"`
	Status        string        `json:"status"`
	Confidence    float64       `json:"confidence"`
	Data          APIData       `json:"data"`
	Validation    APIValidation `json:"validation"`
	Evidence      []APIEvidence `json:"evidence"`
}

// Data holds structured extracted information. Only fields the pipeline
// actually produced are populated; missing fields stay nil/empty explicitly.
type APIData struct {
	Merchant       *string          `json:"merchant,omitempty"`
	Amount         *decimal.Decimal `json:"amount,omitempty"`
	Currency       *string          `json:"currency,omitempty"`
	DocumentDate   *string          `json:"document_date,omitempty"`
	AccountName    *string          `json:"account_name,omitempty"`
	OpeningBalance *decimal.Decimal `json:"opening_balance,omitempty"`
	ClosingBalance *decimal.Decimal `json:"closing_balance,omitempty"`
	Raw            json.RawMessage  `json:"raw,omitempty"`
}

// Validation carries deterministic validation/reconciliation info.
// Money uses decimal strings (shopspring/decimal) — never float64.
type APIValidation struct {
	Status     string     `json:"status"`
	Reconciled bool       `json:"reconciled"`
	Difference string     `json:"difference"`
	Checks     []APICheck `json:"checks"`
	Errors     []string   `json:"errors,omitempty"`
}

// Check is a single named deterministic check outcome.
type APICheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// Evidence references source pages/fields behind an extracted value.
type APIEvidence struct {
	Field      string    `json:"field"`
	Page       *int      `json:"page,omitempty"`
	Region     []float64 `json:"region,omitempty"`
	Engine     string    `json:"engine,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
}

// NonCompleted returns a minimal envelope for processing/failed documents:
// identity + status only, no invented financial data.
func NonCompleted(documentID, docType, status string) APIResult {
	return APIResult{
		SchemaVersion: SchemaVersion,
		DocumentID:    documentID,
		DocumentType:  docType,
		Status:        status,
		Data:          APIData{},
		Validation:    APIValidation{Status: status, Difference: "0.00", Checks: []APICheck{}},
		Evidence:      []APIEvidence{},
	}
}

// Validate checks the envelope is well-formed before serving.
func (r APIResult) Validate() error {
	if r.SchemaVersion == "" {
		return fmt.Errorf("schema_version is required")
	}
	if major(r.SchemaVersion) != SupportedMajor {
		return fmt.Errorf("unsupported contract major version %q", r.SchemaVersion)
	}
	if r.DocumentID == "" {
		return fmt.Errorf("document_id is required")
	}
	if r.DocumentType == "" {
		return fmt.Errorf("document_type is required")
	}
	switch r.Status {
	case StatusProcessing, StatusCompleted, StatusFailed:
	default:
		return fmt.Errorf("unknown status %q", r.Status)
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return fmt.Errorf("confidence %v out of range [0,1]", r.Confidence)
	}
	return nil
}

func major(v string) string {
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			return v[:i]
		}
	}
	return v
}
