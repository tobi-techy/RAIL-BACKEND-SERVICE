package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ReceiptItem represents a single line item on a receipt.
type ReceiptItem struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity,omitempty"`
	Price    string `json:"price,omitempty"`
}

// ReceiptScan represents a scanned and parsed receipt.
type ReceiptScan struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	Merchant    string          `json:"merchant" db:"merchant"`
	Amount      decimal.Decimal `json:"amount" db:"amount"`
	Currency    string          `json:"currency" db:"currency"`
	ReceiptDate *time.Time      `json:"receipt_date,omitempty" db:"receipt_date"`
	Category    string          `json:"category" db:"category"`
	Items       json.RawMessage `json:"items" db:"items"`
	RawText     *string         `json:"raw_text,omitempty" db:"raw_text"`
	ImageHash   *string         `json:"image_hash,omitempty" db:"image_hash"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
}

// ParsedItems returns the items as a typed slice.
func (r *ReceiptScan) ParsedItems() []ReceiptItem {
	var items []ReceiptItem
	if len(r.Items) > 0 {
		json.Unmarshal(r.Items, &items)
	}
	return items
}
