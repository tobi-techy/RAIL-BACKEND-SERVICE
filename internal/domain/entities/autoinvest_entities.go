package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// AutoInvestStatus represents the status of an auto-invest event
type AutoInvestStatus string

const (
	AutoInvestStatusPending   AutoInvestStatus = "pending"
	AutoInvestStatusCompleted AutoInvestStatus = "completed"
	AutoInvestStatusFailed    AutoInvestStatus = "failed"
)

// AutoInvestSettings represents user's auto-investment configuration
type AutoInvestSettings struct {
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	Enabled   bool            `json:"enabled" db:"enabled"`
	BasketID  uuid.UUID       `json:"basket_id" db:"basket_id"`
	Threshold decimal.Decimal `json:"threshold" db:"threshold"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

// AutoInvestEvent represents an auto-investment execution record
type AutoInvestEvent struct {
	ID        uuid.UUID        `json:"id" db:"id"`
	UserID    uuid.UUID        `json:"user_id" db:"user_id"`
	BasketID  uuid.UUID        `json:"basket_id" db:"basket_id"`
	Amount    decimal.Decimal  `json:"amount" db:"amount"`
	OrderID   uuid.UUID        `json:"order_id" db:"order_id"`
	Status    AutoInvestStatus `json:"status" db:"status"`
	Error     *string          `json:"error,omitempty" db:"error"`
	CreatedAt time.Time        `json:"created_at" db:"created_at"`
}
