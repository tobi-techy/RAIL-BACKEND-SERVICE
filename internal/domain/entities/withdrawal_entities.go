package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// WithdrawalStatus represents the status of a withdrawal
type WithdrawalStatus string

const (
	WithdrawalStatusInitiated       WithdrawalStatus = "initiated"        // Request created internally
	WithdrawalStatusPending         WithdrawalStatus = "pending"          // Sent to processor
	WithdrawalStatusAlpacaDebited   WithdrawalStatus = "alpaca_debited"   // Funds debited from Alpaca
	WithdrawalStatusDueProcessing   WithdrawalStatus = "due_processing"   // Due/Bridge processing
	WithdrawalStatusOnChainTransfer WithdrawalStatus = "onchain_transfer" // On-chain transfer in progress
	WithdrawalStatusTimeout         WithdrawalStatus = "timeout"          // No response within SLA
	WithdrawalStatusCompleted       WithdrawalStatus = "completed"        // Terminal: success
	WithdrawalStatusFailed          WithdrawalStatus = "failed"           // Terminal: failed
	WithdrawalStatusReversed        WithdrawalStatus = "reversed"         // Terminal: reversed/refunded
)

// ValidWithdrawalStatuses contains all valid withdrawal statuses
var ValidWithdrawalStatuses = map[WithdrawalStatus]bool{
	WithdrawalStatusInitiated:       true,
	WithdrawalStatusPending:         true,
	WithdrawalStatusAlpacaDebited:   true,
	WithdrawalStatusDueProcessing:   true,
	WithdrawalStatusOnChainTransfer: true,
	WithdrawalStatusTimeout:         true,
	WithdrawalStatusCompleted:       true,
	WithdrawalStatusFailed:          true,
	WithdrawalStatusReversed:        true,
}

// ValidWithdrawalTransitions defines allowed status transitions
var ValidWithdrawalTransitions = map[WithdrawalStatus][]WithdrawalStatus{
	WithdrawalStatusInitiated:       {WithdrawalStatusPending, WithdrawalStatusFailed},
	WithdrawalStatusPending:         {WithdrawalStatusAlpacaDebited, WithdrawalStatusFailed, WithdrawalStatusTimeout},
	WithdrawalStatusAlpacaDebited:   {WithdrawalStatusDueProcessing, WithdrawalStatusFailed, WithdrawalStatusReversed},
	WithdrawalStatusDueProcessing:   {WithdrawalStatusOnChainTransfer, WithdrawalStatusFailed, WithdrawalStatusTimeout, WithdrawalStatusReversed},
	WithdrawalStatusOnChainTransfer: {WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusTimeout},
	WithdrawalStatusTimeout:         {WithdrawalStatusCompleted, WithdrawalStatusFailed, WithdrawalStatusReversed}, // Can still resolve
	WithdrawalStatusCompleted:       {},                                                                            // Terminal
	WithdrawalStatusFailed:          {WithdrawalStatusReversed},                                                    // Can be reversed
	WithdrawalStatusReversed:        {},                                                                            // Terminal
}

// IsValid checks if the status is valid
func (s WithdrawalStatus) IsValid() bool {
	return ValidWithdrawalStatuses[s]
}

// CanTransitionTo checks if transition to new status is allowed
func (s WithdrawalStatus) CanTransitionTo(newStatus WithdrawalStatus) bool {
	allowed, exists := ValidWithdrawalTransitions[s]
	if !exists {
		return false
	}
	for _, status := range allowed {
		if status == newStatus {
			return true
		}
	}
	return false
}

// IsTerminal returns true if this is a terminal state
func (s WithdrawalStatus) IsTerminal() bool {
	return s == WithdrawalStatusCompleted || s == WithdrawalStatusFailed || s == WithdrawalStatusReversed
}

// IsPending returns true if withdrawal is still in progress
func (s WithdrawalStatus) IsPending() bool {
	return !s.IsTerminal() && s != WithdrawalStatusTimeout
}

// ValidateTransition validates and returns error if transition is invalid
func (s WithdrawalStatus) ValidateTransition(newStatus WithdrawalStatus) error {
	if !newStatus.IsValid() {
		return fmt.Errorf("invalid withdrawal status: %s", newStatus)
	}
	if !s.CanTransitionTo(newStatus) {
		return fmt.Errorf("invalid status transition from %s to %s", s, newStatus)
	}
	return nil
}

// Withdrawal represents a USD to USDC withdrawal
type Withdrawal struct {
	ID                 uuid.UUID        `json:"id" db:"id"`
	UserID             uuid.UUID        `json:"user_id" db:"user_id"`
	AlpacaAccountID    string           `json:"alpaca_account_id" db:"alpaca_account_id"`
	Amount             decimal.Decimal  `json:"amount" db:"amount"`
	DestinationChain   string           `json:"destination_chain" db:"destination_chain"`
	DestinationAddress string           `json:"destination_address" db:"destination_address"`
	Status             WithdrawalStatus `json:"status" db:"status"`
	AlpacaJournalID    *string          `json:"alpaca_journal_id,omitempty" db:"alpaca_journal_id"`
	DueTransferID      *string          `json:"due_transfer_id,omitempty" db:"due_transfer_id"`
	DueRecipientID     *string          `json:"due_recipient_id,omitempty" db:"due_recipient_id"`
	TxHash             *string          `json:"tx_hash,omitempty" db:"tx_hash"`
	ErrorMessage       *string          `json:"error_message,omitempty" db:"error_message"`
	CreatedAt          time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at" db:"updated_at"`
	CompletedAt        *time.Time       `json:"completed_at,omitempty" db:"completed_at"`
}

// InitiateWithdrawalRequest represents a withdrawal request
type InitiateWithdrawalRequest struct {
	UserID             uuid.UUID       `json:"user_id"`
	AlpacaAccountID    string          `json:"alpaca_account_id"`
	Amount             decimal.Decimal `json:"amount"`
	DestinationChain   string          `json:"destination_chain"`
	DestinationAddress string          `json:"destination_address"`
}

// InitiateWithdrawalResponse represents the response to a withdrawal request
type InitiateWithdrawalResponse struct {
	WithdrawalID uuid.UUID        `json:"withdrawal_id"`
	Status       WithdrawalStatus `json:"status"`
	Message      string           `json:"message"`
}
