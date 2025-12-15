package entities

import "fmt"

// DepositStatus represents the status of a deposit
type DepositStatus string

const (
	DepositStatusPending          DepositStatus = "pending"
	DepositStatusConfirmed        DepositStatus = "confirmed"
	DepositStatusFailed           DepositStatus = "failed"
	DepositStatusExpired          DepositStatus = "expired"
	DepositStatusOffRampInitiated DepositStatus = "off_ramp_initiated"
	DepositStatusOffRampCompleted DepositStatus = "off_ramp_completed"
	DepositStatusBrokerFunded     DepositStatus = "broker_funded"
)

// ValidDepositStatuses contains all valid deposit statuses
var ValidDepositStatuses = map[DepositStatus]bool{
	DepositStatusPending:          true,
	DepositStatusConfirmed:        true,
	DepositStatusFailed:           true,
	DepositStatusExpired:          true,
	DepositStatusOffRampInitiated: true,
	DepositStatusOffRampCompleted: true,
	DepositStatusBrokerFunded:     true,
}

// ValidTransitions defines allowed status transitions
var ValidDepositTransitions = map[DepositStatus][]DepositStatus{
	DepositStatusPending:          {DepositStatusConfirmed, DepositStatusFailed, DepositStatusExpired},
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
	return s == DepositStatusPending
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
const (
	MinDepositAmountUSDC = 1.0   // Minimum deposit amount in USDC (matches MinDepositAmount in limits_entities.go)
	DepositTimeoutHours  = 24    // Hours before pending deposit expires
	MaxDepositsPerDay    = 1000  // Maximum deposit addresses per user per day
)
