package entities

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// P2PIdentifierType represents how the recipient was identified
type P2PIdentifierType string

const (
	P2PIdentifierRailTag P2PIdentifierType = "railtag"
	P2PIdentifierEmail   P2PIdentifierType = "email"
	P2PIdentifierPhone   P2PIdentifierType = "phone"
)

// P2PTransferStatus represents the status of a P2P transfer
type P2PTransferStatus string

const (
	P2PStatusPending   P2PTransferStatus = "pending"   // Awaiting claim (non-user)
	P2PStatusCompleted P2PTransferStatus = "completed" // Instant transfer to existing user
	P2PStatusClaimed   P2PTransferStatus = "claimed"   // New user signed up and claimed
	P2PStatusExpired   P2PTransferStatus = "expired"   // 14 days passed, refunded
	P2PStatusCancelled P2PTransferStatus = "cancelled" // Sender cancelled
)

// P2PTransferExpiryDays is the number of days before unclaimed transfers expire
const P2PTransferExpiryDays = 14

// P2PTransfer represents a peer-to-peer money transfer
type P2PTransfer struct {
	ID                  uuid.UUID         `json:"id" db:"id"`
	SenderID            uuid.UUID         `json:"senderId" db:"sender_id"`
	RecipientID         *uuid.UUID        `json:"recipientId,omitempty" db:"recipient_id"`
	RecipientIdentifier string            `json:"recipientIdentifier" db:"recipient_identifier"`
	IdentifierType      P2PIdentifierType `json:"identifierType" db:"identifier_type"`
	Amount              decimal.Decimal   `json:"amount" db:"amount"`
	Currency            string            `json:"currency" db:"currency"`
	Note                *string           `json:"note,omitempty" db:"note"`
	Status              P2PTransferStatus `json:"status" db:"status"`
	ClaimToken          *string           `json:"-" db:"claim_token"`
	ClaimLinkSentAt     *time.Time        `json:"claimLinkSentAt,omitempty" db:"claim_link_sent_at"`
	ReminderSentAt      *time.Time        `json:"reminderSentAt,omitempty" db:"reminder_sent_at"`
	CompletedAt         *time.Time        `json:"completedAt,omitempty" db:"completed_at"`
	CancelledAt         *time.Time        `json:"cancelledAt,omitempty" db:"cancelled_at"`
	ExpiresAt           time.Time         `json:"expiresAt" db:"expires_at"`
	CreatedAt           time.Time         `json:"createdAt" db:"created_at"`
	UpdatedAt           time.Time         `json:"updatedAt" db:"updated_at"`
}

// P2PRecentRecipient represents a recent transfer recipient for quick access
type P2PRecentRecipient struct {
	UserID      uuid.UUID `json:"userId" db:"user_id"`
	RecipientID uuid.UUID `json:"recipientId" db:"recipient_id"`
	LastSentAt  time.Time `json:"lastSentAt" db:"last_sent_at"`
	SendCount   int       `json:"sendCount" db:"send_count"`
}

// P2PSendRequest represents a request to send money
type P2PSendRequest struct {
	Identifier string `json:"identifier" validate:"required"` // RailTag, email, or phone
	Amount     string `json:"amount" validate:"required"`
	Note       string `json:"note,omitempty" validate:"max=255"`
}

// P2PLookupResponse represents the result of looking up a recipient
type P2PLookupResponse struct {
	Found          bool              `json:"found"`
	IdentifierType P2PIdentifierType `json:"identifierType"`
	User           *P2PUserInfo      `json:"user,omitempty"`   // If found
	CanSend        bool              `json:"canSend"`          // True if valid identifier
	Message        string            `json:"message,omitempty"` // "Will be invited to Rail"
}

// P2PUserInfo represents basic user info for P2P display
type P2PUserInfo struct {
	ID        uuid.UUID `json:"id"`
	RailTag   *string   `json:"railTag,omitempty"`
	FirstName *string   `json:"firstName,omitempty"`
	LastName  *string   `json:"lastName,omitempty"`
	Email     string    `json:"-"` // Hidden for privacy
}

// P2PTransferResponse represents the response after sending money
type P2PTransferResponse struct {
	Transfer *P2PTransfer `json:"transfer"`
	Message  string       `json:"message"`
}

// P2PRecentRecipientWithUser includes user details for display
type P2PRecentRecipientWithUser struct {
	RecipientID uuid.UUID `json:"recipientId"`
	RailTag     *string   `json:"railTag,omitempty"`
	FirstName   *string   `json:"firstName,omitempty"`
	LastName    *string   `json:"lastName,omitempty"`
	LastSentAt  time.Time `json:"lastSentAt"`
	SendCount   int       `json:"sendCount"`
}

// GenerateClaimToken generates a secure random token for claim links
func GenerateClaimToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// NewP2PTransfer creates a new P2P transfer with defaults
func NewP2PTransfer(senderID uuid.UUID, identifier string, identifierType P2PIdentifierType, amount decimal.Decimal, note *string) *P2PTransfer {
	now := time.Now()
	return &P2PTransfer{
		ID:                  uuid.New(),
		SenderID:            senderID,
		RecipientIdentifier: identifier,
		IdentifierType:      identifierType,
		Amount:              amount,
		Currency:            "USD",
		Note:                note,
		Status:              P2PStatusPending,
		ExpiresAt:           now.AddDate(0, 0, P2PTransferExpiryDays),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

// IsPending returns true if transfer is awaiting claim
func (t *P2PTransfer) IsPending() bool {
	return t.Status == P2PStatusPending
}

// CanCancel returns true if sender can cancel this transfer
func (t *P2PTransfer) CanCancel() bool {
	return t.Status == P2PStatusPending
}

// IsExpired returns true if transfer has passed expiry date
func (t *P2PTransfer) IsExpired() bool {
	return t.Status == P2PStatusPending && time.Now().After(t.ExpiresAt)
}
