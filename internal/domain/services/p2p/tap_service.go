package p2p

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

const tapIntentTTL = 5 * time.Minute

var (
	ErrTapIntentNotFound = errors.New("tap intent not found or expired")
	ErrTapIntentMismatch = errors.New("tap intent does not match confirmed parameters")
)

// TapIntentStore persists short-lived tap intents in Redis.
type TapIntentStore interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string, dest interface{}) error
	Del(ctx context.Context, key string) error
}

// TapIntent is the server-issued record stored in Redis.
type TapIntent struct {
	Nonce       string          `json:"nonce"`
	SenderID    uuid.UUID       `json:"sender_id"`
	RecipientID uuid.UUID       `json:"recipient_id"`
	Amount      decimal.Decimal `json:"amount"`
	ExpiresAt   time.Time       `json:"expires_at"`
}

// TapIntentResponse is returned to the sender after POST /p2p/tap/intent.
type TapIntentResponse struct {
	Nonce       string    `json:"nonce"`
	RecipientID uuid.UUID `json:"recipient_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// SetTapIntentStore wires the Redis store into the service.
func (s *Service) SetTapIntentStore(store TapIntentStore) {
	s.tapIntentStore = store
}

// CreateTapIntent validates the recipient, stores a server-issued intent in Redis,
// and returns the nonce to the sender. The nonce is included in the MC payload
// so the recipient can echo it back in transfer_accepted.
func (s *Service) CreateTapIntent(ctx context.Context, senderID uuid.UUID, recipientRailtag, amountStr string) (*TapIntentResponse, error) {
	if s.tapIntentStore == nil {
		return nil, fmt.Errorf("tap intent store not configured")
	}

	amount, err := decimal.NewFromString(amountStr)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, entities.ErrP2PInvalidAmount
	}
	if amount.LessThan(decimal.NewFromFloat(P2PMinTransferAmount)) {
		return nil, entities.ErrP2PAmountTooLow
	}
	if amount.GreaterThan(decimal.NewFromFloat(P2PMaxTransferAmount)) {
		return nil, entities.ErrP2PAmountTooHigh
	}

	// Server-side recipient lookup — rejects attacker-supplied railtags that don't exist.
	recipient, err := s.userLookup.GetByRailTag(ctx, recipientRailtag)
	if err != nil || recipient == nil {
		return nil, fmt.Errorf("recipient not found")
	}
	if recipient.ID == senderID {
		return nil, fmt.Errorf("cannot send to yourself")
	}

	nonce, err := generateNonce()
	if err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	intent := TapIntent{
		Nonce:       nonce,
		SenderID:    senderID,
		RecipientID: recipient.ID,
		Amount:      amount,
		ExpiresAt:   time.Now().Add(tapIntentTTL),
	}

	key := tapIntentKey(senderID, nonce)
	if err := s.tapIntentStore.Set(ctx, key, intent, tapIntentTTL); err != nil {
		return nil, fmt.Errorf("failed to store intent: %w", err)
	}

	return &TapIntentResponse{
		Nonce:       nonce,
		RecipientID: recipient.ID,
		ExpiresAt:   intent.ExpiresAt,
	}, nil
}

// ConfirmTapIntent validates the nonce, executes the transfer, and deletes the intent.
// Must be called on a route protected by RequirePasscodeSession.
func (s *Service) ConfirmTapIntent(ctx context.Context, senderID uuid.UUID, nonce, idempotencyKey string) (*entities.P2PTransferResponse, error) {
	if s.tapIntentStore == nil {
		return nil, fmt.Errorf("tap intent store not configured")
	}

	key := tapIntentKey(senderID, nonce)
	var intent TapIntent
	if err := s.tapIntentStore.Get(ctx, key, &intent); err != nil {
		return nil, ErrTapIntentNotFound
	}
	if time.Now().After(intent.ExpiresAt) {
		_ = s.tapIntentStore.Del(ctx, key)
		return nil, ErrTapIntentNotFound
	}
	if intent.SenderID != senderID {
		return nil, ErrTapIntentMismatch
	}

	// Consume the intent immediately to prevent replay.
	_ = s.tapIntentStore.Del(ctx, key)

	// Require a client-provided idempotency key for tap-to-pay.
	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency key required")
	}

	recipient, err := s.userLookup.GetByID(ctx, intent.RecipientID)
	if err != nil || recipient == nil {
		return nil, fmt.Errorf("recipient not found")
	}

	recipientIdentifier := intent.RecipientID.String()
	if recipient.RailTag != nil && *recipient.RailTag != "" {
		recipientIdentifier = *recipient.RailTag
	}

	req := &entities.P2PSendRequest{
		Identifier:     recipientIdentifier,
		Amount:         intent.Amount.String(),
		Note:           "Tap to Pay",
		IdempotencyKey: idempotencyKey,
	}

	return s.Send(ctx, senderID, req)
}

// tapIntentKey returns the Redis key for a tap intent.
func tapIntentKey(senderID uuid.UUID, nonce string) string {
	return fmt.Sprintf("tap_intent:%s:%s", senderID.String(), nonce)
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
