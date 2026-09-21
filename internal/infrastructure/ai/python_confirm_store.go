package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// confirmKeyPrefix scopes Redis keys for Python-issued confirmation challenges.
const confirmKeyPrefix = "platform_confirm:"

// pendingPythonConfirm is the record staged when the Python agent asks the user
// to confirm a money movement.
//
// It holds one thing: the confirm_id Hands issued. Go does not hold the amount,
// the recipient, or the policy verdict, because Go is not the authority on any of
// them — the ledger is. This record only remembers which challenge the
// Confirm/Cancel poll in this thread is asking about, so a tap can name it.
type pendingPythonConfirm struct {
	ConfirmID string    `json:"confirm_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ConfirmStore remembers the open confirm_id for a conversation.
//
// The platform layer resolves a Confirm/Cancel tap by thread, not by id
// (ConfirmPlatformAction takes a thread, and ConfirmRequest carries only the
// question), so the thread→id mapping has to live somewhere. That is all this is.
//
// Fail-closed: an unreadable entry returns false, so a tap whose challenge cannot
// be read settles nothing rather than guessing at a movement.
type ConfirmStore struct {
	redis cache.RedisClient
	ttl   time.Duration
	log   *zap.Logger
}

// NewConfirmStore builds a Redis-backed confirm store. A non-positive TTL falls
// back to ten minutes, matching the horizon the Python ledger gives a challenge.
func NewConfirmStore(redis cache.RedisClient, ttl time.Duration, logger *zap.Logger) *ConfirmStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &ConfirmStore{redis: redis, ttl: ttl, log: logger}
}

func confirmKey(convID uuid.UUID) string {
	return confirmKeyPrefix + convID.String()
}

// Put remembers the open confirm_id for a conversation, replacing any previous
// one: a second challenge supersedes the first.
func (s *ConfirmStore) Put(ctx context.Context, convID uuid.UUID, confirmID string) error {
	if s.redis == nil || confirmID == "" {
		return nil
	}
	entry := pendingPythonConfirm{ConfirmID: confirmID, ExpiresAt: time.Now().Add(s.ttl)}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.redis.Set(ctx, confirmKey(convID), string(payload), s.ttl)
}

// Peek returns the open confirm_id for a conversation, if one is staged and has
// not expired.
func (s *ConfirmStore) Peek(ctx context.Context, convID uuid.UUID) (string, bool) {
	if s.redis == nil {
		return "", false
	}
	var raw string
	if err := s.redis.Get(ctx, confirmKey(convID), &raw); err != nil {
		s.log.Debug("confirm lookup miss",
			zap.String("conversation", convID.String()),
			zap.Error(err))
		return "", false
	}
	if raw == "" {
		return "", false
	}
	var entry pendingPythonConfirm
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		s.log.Warn("malformed confirm entry",
			zap.String("conversation", convID.String()),
			zap.Error(err))
		return "", false
	}
	if entry.ConfirmID == "" || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.ConfirmID, true
}

// Delete clears the open confirm for a conversation, so a settled or declined
// challenge cannot be tapped a second time.
func (s *ConfirmStore) Delete(ctx context.Context, convID uuid.UUID) {
	if s.redis == nil {
		return
	}
	if err := s.redis.Del(ctx, confirmKey(convID)); err != nil {
		// The id is dropped locally either way; a failed delete means the entry
		// outlives its usefulness by its TTL, which is what the TTL is for.
		s.log.Warn("could not clear a staged confirm",
			zap.String("conversation", convID.String()),
			zap.Error(err))
	}
}
