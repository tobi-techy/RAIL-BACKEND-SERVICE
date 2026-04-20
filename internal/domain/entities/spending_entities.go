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

// MoneyFlowSummary holds pre-computed money-in and money-out totals for a period.
type MoneyFlowSummary struct {
	TotalDeposits    decimal.Decimal `json:"total_deposits"`
	DepositCount     int             `json:"deposit_count"`
	TotalWithdrawals decimal.Decimal `json:"total_withdrawals"`
	WithdrawalCount  int             `json:"withdrawal_count"`
	TotalCardSpend   decimal.Decimal `json:"total_card_spend"`
	CardSpendCount   int             `json:"card_spend_count"`
	TotalP2P         decimal.Decimal `json:"total_p2p"`
	P2PCount         int             `json:"p2p_count"`
	TotalReceipts    decimal.Decimal `json:"total_receipts"`
	ReceiptCount     int             `json:"receipt_count"`
}
