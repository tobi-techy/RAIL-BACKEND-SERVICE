package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

const (
	confirmTokenPrefix = "confirm_token:"
	confirmTokenTTL    = 10 * time.Minute
)

// ConfirmTokenPayload is the data stored for an email confirmation link.
type ConfirmTokenPayload struct {
	UserID    uuid.UUID              `json:"user_id"`
	ConvID    uuid.UUID              `json:"conv_id"`
	ActionID  string                 `json:"action_id"`
	Action    string                 `json:"action"`
	Params    map[string]interface{} `json:"params"`
	CreatedAt time.Time              `json:"created_at"`
}

// ConfirmEmailSender sends the email that carries a one-time confirmation link.
type ConfirmEmailSender interface {
	SendTransactionConfirmation(ctx context.Context, toEmail, description, confirmURL string) error
}

// UserEmailResolver looks up the email address for a user.
type UserEmailResolver interface {
	GetEmailByUserID(ctx context.Context, userID uuid.UUID) (string, error)
}

// ConfirmTokenStore issues and validates one-time confirmation tokens for
// fund-moving actions. The token itself is a 32-byte random hex string; only
// the SHA-256 hash is stored in Redis so a Redis leak does not expose a usable
// token.
type ConfirmTokenStore struct {
	redis  cache.RedisClient
	logger *zap.Logger
}

func NewConfirmTokenStore(redis cache.RedisClient, logger *zap.Logger) *ConfirmTokenStore {
	return &ConfirmTokenStore{redis: redis, logger: logger}
}

func (s *ConfirmTokenStore) key(tokenHash string) string {
	return confirmTokenPrefix + tokenHash
}

// Create generates a new confirmation token, stores its hash in Redis, and
// returns the plaintext token to embed in an email link.
func (s *ConfirmTokenStore) Create(ctx context.Context, payload *ConfirmTokenPayload) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	hash := sha256Hex(token)

	payload.CreatedAt = time.Now().UTC()
	if err := s.redis.Set(ctx, s.key(hash), payload, confirmTokenTTL); err != nil {
		return "", err
	}
	return token, nil
}

// Validate looks up the token hash, returns the payload, and deletes the token
// so it cannot be reused.
func (s *ConfirmTokenStore) Validate(ctx context.Context, token string) (*ConfirmTokenPayload, error) {
	hash := sha256Hex(token)
	var payload ConfirmTokenPayload
	if err := s.redis.Get(ctx, s.key(hash), &payload); err != nil {
		return nil, err
	}
	_ = s.redis.Del(ctx, s.key(hash))
	return &payload, nil
}
