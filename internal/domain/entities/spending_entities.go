package entities

import "github.com/shopspring/decimal"

// SpendingByCategory represents spending grouped by merchant category.
type SpendingByCategory struct {
	Category string          `db:"merchant_category" json:"category"`
	Total    decimal.Decimal `db:"total" json:"total"`
	Count    int             `db:"count" json:"count"`
}

// SpendingByMerchant represents spending grouped by merchant.
type SpendingByMerchant struct {
	Merchant string          `db:"merchant_name" json:"merchant"`
	Total    decimal.Decimal `db:"total" json:"total"`
	Count    int             `db:"count" json:"count"`
}

// SpendingByPeriod represents spending for a time bucket.
type SpendingByPeriod struct {
	Period string          `db:"period" json:"period"`
	Total  decimal.Decimal `db:"total" json:"total"`
	Count  int             `db:"count" json:"count"`
}

// SpendingTransaction represents a single outflow transaction (card, withdrawal, or P2P).
type SpendingTransaction struct {
	Date     string          `db:"date" json:"date"`
	Amount   decimal.Decimal `db:"amount" json:"amount"`
	Category string          `db:"category" json:"category"`
	Source   string          `db:"source" json:"source"`
}
