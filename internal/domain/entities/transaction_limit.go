package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type LimitType string
type LimitPeriod string

const (
	LimitTypeDeposit    LimitType = "deposit"
	LimitTypeWithdrawal LimitType = "withdrawal"
	LimitTypeTrade      LimitType = "trade"
	LimitTypeTransfer   LimitType = "transfer"

	LimitPeriodDaily   LimitPeriod = "daily"
	LimitPeriodWeekly  LimitPeriod = "weekly"
	LimitPeriodMonthly LimitPeriod = "monthly"
)

type TransactionLimit struct {
	ID          uuid.UUID       `json:"id" db:"id"`
	UserID      uuid.UUID       `json:"user_id" db:"user_id"`
	LimitType   LimitType       `json:"limit_type" db:"limit_type"`
	Period      LimitPeriod     `json:"period" db:"period"`
	MaxAmount   decimal.Decimal `json:"max_amount" db:"max_amount"`
	UsedAmount  decimal.Decimal `json:"used_amount" db:"used_amount"`
	ResetAt     time.Time       `json:"reset_at" db:"reset_at"`
	CreatedAt   time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at" db:"updated_at"`
}

type FraudAlert struct {
	ID          uuid.UUID              `json:"id" db:"id"`
	UserID      uuid.UUID              `json:"user_id" db:"user_id"`
	TxID        uuid.UUID              `json:"tx_id" db:"tx_id"`
	RiskScore   decimal.Decimal        `json:"risk_score" db:"risk_score"`
	RiskFactors map[string]interface{} `json:"risk_factors" db:"risk_factors"`
	Status      string                 `json:"status" db:"status"`
	ReviewedBy  *uuid.UUID             `json:"reviewed_by,omitempty" db:"reviewed_by"`
	ReviewedAt  *time.Time             `json:"reviewed_at,omitempty" db:"reviewed_at"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
}
