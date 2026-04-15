package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// PendingAction represents an action awaiting user confirmation.
type PendingAction struct {
	ID             string                 `json:"id"`
	ConversationID uuid.UUID              `json:"conversation_id"`
	UserID         uuid.UUID              `json:"user_id"`
	Action         string                 `json:"action"`          // "transfer_funds", "set_savings_goal"
	Description    string                 `json:"description"`     // Human-readable summary
	Params         map[string]interface{} `json:"params"`          // Action-specific parameters
	ExpiresAt      time.Time              `json:"expires_at"`
	CreatedAt      time.Time              `json:"created_at"`
}

// IsExpired returns true if the action has timed out.
func (p *PendingAction) IsExpired() bool {
	return time.Now().After(p.ExpiresAt)
}

// TransferParams are the typed parameters for a transfer_funds action.
type TransferParams struct {
	From   string          `json:"from"`   // "spend" or "stash"
	To     string          `json:"to"`     // "spend" or "stash"
	Amount decimal.Decimal `json:"amount"`
}

// SavingsGoalParams are the typed parameters for a set_savings_goal action.
type SavingsGoalParams struct {
	Name       string          `json:"name"`
	Target     decimal.Decimal `json:"target"`
	DeadlineAt *time.Time      `json:"deadline_at,omitempty"`
}

// ActionAuditEntry records an executed action for audit trail.
type ActionAuditEntry struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	UserID         uuid.UUID              `json:"user_id" db:"user_id"`
	ConversationID uuid.UUID              `json:"conversation_id" db:"conversation_id"`
	Action         string                 `json:"action" db:"action"`
	Params         map[string]interface{} `json:"params" db:"params"`
	Status         string                 `json:"status" db:"status"` // "executed", "cancelled", "expired"
	ErrorMessage   string                 `json:"error_message,omitempty" db:"error_message"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
}
