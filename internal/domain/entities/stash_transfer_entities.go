package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StashTransferStatus represents the status of a stash transfer
type StashTransferStatus string

const (
	StashTransferStatusPending   StashTransferStatus = "pending"
	StashTransferStatusCompleted StashTransferStatus = "completed"
	StashTransferStatusFailed    StashTransferStatus = "failed"
)

// ValidStashTransferStatuses contains all valid stash transfer statuses
var ValidStashTransferStatuses = map[StashTransferStatus]bool{
	StashTransferStatusPending:   true,
	StashTransferStatusCompleted: true,
	StashTransferStatusFailed:    true,
}

// StashTransfer represents a transfer from stash to spending balance
type StashTransfer struct {
	ID          uuid.UUID           `json:"id" db:"id"`
	UserID      uuid.UUID           `json:"user_id" db:"user_id"`
	Amount      decimal.Decimal     `json:"amount" db:"amount"`
	Status      StashTransferStatus `json:"status" db:"status"`
	CreatedAt   time.Time           `json:"created_at" db:"created_at"`
	CompletedAt *time.Time          `json:"completed_at,omitempty" db:"completed_at"`
}

// Validate validates the stash transfer entity
func (s *StashTransfer) Validate() error {
	if s.ID == uuid.Nil {
		return fmt.Errorf("stash transfer ID is required")
	}
	if s.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if s.Amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	if !ValidStashTransferStatuses[s.Status] {
		return fmt.Errorf("invalid status: %s", s.Status)
	}
	return nil
}

// IsTerminal returns true if the transfer is in a terminal state
func (s *StashTransfer) IsTerminal() bool {
	return s.Status == StashTransferStatusCompleted || s.Status == StashTransferStatusFailed
}

// StashTransferRequest represents a request to transfer from stash to spending
type StashTransferRequest struct {
	UserID uuid.UUID       `json:"user_id"`
	Amount decimal.Decimal `json:"amount"`
}

// Validate validates the stash transfer request
func (r *StashTransferRequest) Validate() error {
	if r.UserID == uuid.Nil {
		return fmt.Errorf("user ID is required")
	}
	if r.Amount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("amount must be positive")
	}
	if r.Amount.LessThan(MinWithdrawalAmount) {
		return fmt.Errorf("amount must be at least %s", MinWithdrawalAmount.String())
	}
	return nil
}

// StashTransferResponse represents the response to a stash transfer request
type StashTransferResponse struct {
	StashTransfer StashTransfer `json:"stash_transfer"`
	Message       string        `json:"message"`
}

// StashBalance represents the current stash and spending balances
type StashBalance struct {
	UserID          uuid.UUID       `json:"user_id"`
	StashBalance    decimal.Decimal `json:"stash_balance"`
	SpendingBalance decimal.Decimal `json:"spending_balance"`
	TotalBalance    decimal.Decimal `json:"total_balance"`
}
