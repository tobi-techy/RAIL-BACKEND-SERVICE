package entities

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DefaultSpendingCommitmentIncreaseFee is the flat fee (USD) charged whenever a
// user raises their self-imposed daily spending commitment. Overridable via the
// SPENDING_COMMITMENT_INCREASE_FEE env var at wiring time.
var DefaultSpendingCommitmentIncreaseFee = decimal.NewFromFloat(1.00)

// SpendingCommitment is a user's self-imposed daily cap on total outflows.
// Lowering it is free; raising it costs a flat fee.
type SpendingCommitment struct {
	UserID          uuid.UUID  `json:"user_id" db:"user_id"`
	DailyLimitCents int64      `json:"daily_limit_cents" db:"daily_limit_cents"`
	Currency        string     `json:"currency" db:"currency"`
	IsActive        bool       `json:"is_active" db:"is_active"`
	IncreaseCount   int        `json:"increase_count" db:"increase_count"`
	LastIncreasedAt *time.Time `json:"last_increased_at,omitempty" db:"last_increased_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

// SpendingCommitmentUsage tracks USD-cent outflow used against the daily cap.
type SpendingCommitmentUsage struct {
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	UsedCents int64     `json:"used_cents" db:"used_cents"`
	ResetAt   time.Time `json:"reset_at" db:"reset_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// SetCommitmentRequest is the API payload to set or update the daily cap.
type SetCommitmentRequest struct {
	DailyLimitCents int64  `json:"daily_limit_cents" binding:"required"`
	Currency        string `json:"currency,omitempty"`
	ConfirmFee      bool   `json:"confirm_fee,omitempty"`
}

// CommitmentStatusResponse is the API view of a user's commitment and today's usage.
type CommitmentStatusResponse struct {
	Active           bool   `json:"active"`
	DailyLimitCents  int64  `json:"daily_limit_cents"`
	UsedCents        int64  `json:"used_cents"`
	RemainingCents   int64  `json:"remaining_cents"`
	Currency         string `json:"currency"`
	ResetsAt         string `json:"resets_at,omitempty"`
	IncreaseFeeCents int64  `json:"increase_fee_cents"`
	IncreaseCount    int    `json:"increase_count"`
}

// Spending commitment errors.
var (
	ErrCommitmentExceeded     = errors.New("daily spending commitment reached")
	ErrIncreaseFeeUnconfirmed = errors.New("raising the daily limit requires fee confirmation")
	ErrInsufficientForFee     = errors.New("insufficient spend balance to cover the increase fee")
	ErrInvalidCommitmentLimit = errors.New("daily limit must be a positive amount")
)
