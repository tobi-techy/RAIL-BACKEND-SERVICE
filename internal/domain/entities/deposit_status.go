package entities

import "fmt"

// DepositStatus represents the status of a deposit
type DepositStatus string

const (
	DepositStatusInitiated        DepositStatus = "initiated"         // Request created internally
	DepositStatusPending          DepositStatus = "pending"           // Sent to processor
	DepositStatusConfirmed        DepositStatus = "confirmed"         // Processor confirmed
	DepositStatusFailed           DepositStatus = "failed"            // Terminal: failed
	DepositStatusExpired          DepositStatus = "expired"           // Terminal: timed out before confirmation
	DepositStatusTimeout          DepositStatus = "timeout"           // No response within SLA (can still resolve)
	DepositStatusOffRampInitiated DepositStatus = "off_ramp_initiated"
	DepositStatusOffRampCompleted DepositStatus = "off_ramp_completed"
	DepositStatusBrokerFunded     DepositStatus = "broker_funded"     // Terminal: success
)

// ValidDepositStatuses contains all valid deposit statuses
var ValidDepositStatuses = map[DepositStatus]bool{
	DepositStatusInitiated:        true,
	DepositStatusPending:          true,
	DepositStatusConfirmed:        true,
	DepositStatusFailed:           true,
	DepositStatusExpired:          true,
	DepositStatusTimeout:          true,
	DepositStatusOffRampInitiated: true,
	DepositStatusOffRampCompleted: true,
	DepositStatusBrokerFunded:     true,
}

// ValidTransitions defines allowed status transitions
var ValidDepositTransitions = map[DepositStatus][]DepositStatus{
	DepositStatusInitiated:        {DepositStatusPending, DepositStatusFailed},
	DepositStatusPending:          {DepositStatusConfirmed, DepositStatusFailed, DepositStatusExpired, DepositStatusTimeout},
	DepositStatusTimeout:          {DepositStatusConfirmed, DepositStatusFailed, DepositStatusExpired}, // Can still resolve
	DepositStatusConfirmed:        {DepositStatusOffRampInitiated, DepositStatusFailed},
	DepositStatusOffRampInitiated: {DepositStatusOffRampCompleted, DepositStatusFailed},
	DepositStatusOffRampCompleted: {DepositStatusBrokerFunded, DepositStatusFailed},
	DepositStatusBrokerFunded:     {}, // Terminal state
	DepositStatusFailed:           {}, // Terminal state
	DepositStatusExpired:          {}, // Terminal state
}

// IsValid checks if the status is a valid deposit status
func (s DepositStatus) IsValid() bool {
	return ValidDepositStatuses[s]
}

// CanTransitionTo checks if transition to new status is allowed
func (s DepositStatus) CanTransitionTo(newStatus DepositStatus) bool {
	allowed, exists := ValidDepositTransitions[s]
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
func (s DepositStatus) IsTerminal() bool {
	return s == DepositStatusFailed || s == DepositStatusExpired || s == DepositStatusBrokerFunded
}

// IsPending returns true if deposit is still pending
func (s DepositStatus) IsPending() bool {
	return s == DepositStatusPending || s == DepositStatusInitiated || s == DepositStatusTimeout
}

// IsAwaitingResponse returns true if waiting for external response
func (s DepositStatus) IsAwaitingResponse() bool {
	return s == DepositStatusPending || s == DepositStatusTimeout
}

// ValidateTransition validates and returns error if transition is invalid
func (s DepositStatus) ValidateTransition(newStatus DepositStatus) error {
	if !newStatus.IsValid() {
		return fmt.Errorf("invalid deposit status: %s", newStatus)
	}
	if !s.CanTransitionTo(newStatus) {
		return fmt.Errorf("invalid status transition from %s to %s", s, newStatus)
	}
	return nil
}

// Deposit configuration constants
// Note: For deposit/withdrawal limits based on KYC tier, see limits_entities.go
// All monetary values in minor units (cents): 100 = $1.00
const (
	MinDepositAmountMinorUnits int64 = 100  // Minimum deposit: $1.00 in cents
	DepositTimeoutHours        int   = 24   // Hours before pending deposit expires
	MaxDepositsPerDay          int   = 1000 // Maximum deposit addresses per user per day
)

// MinDepositAmountUSDC is deprecated, use MinDepositAmountMinorUnits
// Kept for backward compatibility
const MinDepositAmountUSDC = 1.0
