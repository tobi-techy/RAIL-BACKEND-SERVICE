package entities

import (
	"time"

	"github.com/shopspring/decimal"
)

// DepositMonthlyTotal is a monthly completed-deposit aggregate for a user.
type DepositMonthlyTotal struct {
	Month         time.Time       `json:"month" db:"month"`
	Total         decimal.Decimal `json:"total" db:"total"`
	Count         int             `json:"count" db:"count"`
	LastDepositAt *time.Time      `json:"last_deposit_at,omitempty" db:"last_deposit_at"`
}
