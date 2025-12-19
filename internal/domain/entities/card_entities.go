package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CardStatus represents the status of a card
type CardStatus string

const (
	CardStatusPending   CardStatus = "pending"
	CardStatusActive    CardStatus = "active"
	CardStatusFrozen    CardStatus = "frozen"
	CardStatusCancelled CardStatus = "cancelled"
)

// CardTransactionStatus represents the status of a card transaction
type CardTransactionStatus string

const (
	CardTxStatusPending   CardTransactionStatus = "pending"   // Authorization requested
	CardTxStatusCompleted CardTransactionStatus = "completed" // Terminal: captured/settled
	CardTxStatusDeclined  CardTransactionStatus = "declined"  // Terminal: declined
	CardTxStatusReversed  CardTransactionStatus = "reversed"  // Terminal: refunded/reversed
	CardTxStatusTimeout   CardTransactionStatus = "timeout"   // No response within SLA
)

// ValidCardTxStatuses contains all valid card transaction statuses
var ValidCardTxStatuses = map[CardTransactionStatus]bool{
	CardTxStatusPending:   true,
	CardTxStatusCompleted: true,
	CardTxStatusDeclined:  true,
	CardTxStatusReversed:  true,
	CardTxStatusTimeout:   true,
}

// ValidCardTxTransitions defines allowed status transitions
var ValidCardTxTransitions = map[CardTransactionStatus][]CardTransactionStatus{
	CardTxStatusPending:   {CardTxStatusCompleted, CardTxStatusDeclined, CardTxStatusTimeout},
	CardTxStatusTimeout:   {CardTxStatusCompleted, CardTxStatusDeclined, CardTxStatusReversed},
	CardTxStatusCompleted: {CardTxStatusReversed}, // Can be refunded
	CardTxStatusDeclined:  {},                     // Terminal
	CardTxStatusReversed:  {},                     // Terminal
}

// IsValid checks if the status is valid
func (s CardTransactionStatus) IsValid() bool {
	return ValidCardTxStatuses[s]
}

// CanTransitionTo checks if transition to new status is allowed
func (s CardTransactionStatus) CanTransitionTo(newStatus CardTransactionStatus) bool {
	allowed, exists := ValidCardTxTransitions[s]
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
func (s CardTransactionStatus) IsTerminal() bool {
	return s == CardTxStatusDeclined || s == CardTxStatusReversed
}

// ValidateTransition validates and returns error if transition is invalid
func (s CardTransactionStatus) ValidateTransition(newStatus CardTransactionStatus) error {
	if !newStatus.IsValid() {
		return fmt.Errorf("invalid card transaction status: %s", newStatus)
	}
	if !s.CanTransitionTo(newStatus) {
		return fmt.Errorf("invalid status transition from %s to %s", s, newStatus)
	}
	return nil
}

// CardType represents the type of card
type CardType string

const (
	CardTypeVirtual  CardType = "virtual"
	CardTypePhysical CardType = "physical"
)

// BridgeCard represents a user's debit card linked to their Spend Balance via Bridge
type BridgeCard struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	UserID           uuid.UUID  `json:"user_id" db:"user_id"`
	BridgeCardID     string     `json:"bridge_card_id" db:"bridge_card_id"`
	BridgeCustomerID string     `json:"bridge_customer_id" db:"bridge_customer_id"`
	Type             CardType   `json:"type" db:"type"`
	Status           CardStatus `json:"status" db:"status"`
	Last4            string     `json:"last_4" db:"last_4"`
	Expiry           string     `json:"expiry" db:"expiry"`
	CardImageURL     *string    `json:"card_image_url,omitempty" db:"card_image_url"`
	Currency         string     `json:"currency" db:"currency"`
	Chain            string     `json:"chain" db:"chain"`
	WalletAddress    string     `json:"wallet_address" db:"wallet_address"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

// BridgeCardTransaction represents a card transaction via Bridge
type BridgeCardTransaction struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	CardID           uuid.UUID       `json:"card_id" db:"card_id"`
	UserID           uuid.UUID       `json:"user_id" db:"user_id"`
	BridgeTransID    string          `json:"bridge_trans_id" db:"bridge_trans_id"`
	Type             string          `json:"type" db:"type"` // authorization, capture, refund
	Amount           decimal.Decimal `json:"amount" db:"amount"`
	Currency         string          `json:"currency" db:"currency"`
	MerchantName     *string         `json:"merchant_name,omitempty" db:"merchant_name"`
	MerchantCategory *string         `json:"merchant_category,omitempty" db:"merchant_category"`
	Status           string          `json:"status" db:"status"` // pending, completed, declined, reversed
	DeclineReason    *string         `json:"decline_reason,omitempty" db:"decline_reason"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// CreateCardRequest represents a request to create a card
type CreateCardRequest struct {
	Type CardType `json:"type" binding:"required,oneof=virtual physical"`
}

// CreateCardResponse represents the response after creating a card
type CreateCardResponse struct {
	Card    *BridgeCard `json:"card"`
	Message string      `json:"message,omitempty"`
}

// CardDetailsResponse represents card details for display
type CardDetailsResponse struct {
	ID           uuid.UUID  `json:"id"`
	Type         CardType   `json:"type"`
	Status       CardStatus `json:"status"`
	Last4        string     `json:"last_4"`
	Expiry       string     `json:"expiry"`
	CardImageURL *string    `json:"card_image_url,omitempty"`
	Currency     string     `json:"currency"`
	CreatedAt    time.Time  `json:"created_at"`
}

// CardListResponse represents a list of user cards
type CardListResponse struct {
	Cards []CardDetailsResponse `json:"cards"`
	Total int                   `json:"total"`
}

// FreezeCardRequest represents a request to freeze a card
type FreezeCardRequest struct {
	Reason string `json:"reason,omitempty"`
}

// OrderPhysicalCardRequest represents a request to order a physical card
type OrderPhysicalCardRequest struct {
	ShippingAddress *ShippingAddress `json:"shipping_address" binding:"required"`
}

// ShippingAddress represents a shipping address for physical cards
type ShippingAddress struct {
	StreetLine1 string `json:"street_line_1" binding:"required"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city" binding:"required"`
	State       string `json:"state,omitempty"`
	PostalCode  string `json:"postal_code" binding:"required"`
	Country     string `json:"country" binding:"required"`
}

// CardTransactionListResponse represents a list of card transactions
type CardTransactionListResponse struct {
	Transactions []BridgeCardTransaction `json:"transactions"`
	Total        int                     `json:"total"`
	HasMore      bool                    `json:"has_more"`
}
