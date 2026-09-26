package platform

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// TurnTracker records the newest inbound "turn" per conversation so a reply
// generated for an older turn can be suppressed once the user has moved on.
// See docs/miriam-inbound-supersession.md.
//
// It is Redis-backed and deliberately fail-open: every lookup error treats the
// turn as current, so a Redis blip degrades to today's behavior (deliver) rather
// than silently swallowing a real reply.
type TurnTracker interface {
	// MarkTurn records turnID as the conversation's current turn.
	MarkTurn(ctx context.Context, key, turnID string)
	// IsCurrent reports whether turnID is still the conversation's current turn.
	// Unknown/missing keys and lookup failures report true.
	IsCurrent(ctx context.Context, key, turnID string) bool
}

const (
	// turnKeyTTL bounds how long a "current turn" marker lives. It must outlast
	// the slowest turn (a statement scan can hold the request ~150s) so an
	// in-flight reply is never treated as unknown-and-thus-superseded; it must
	// be short enough that a stale marker from a long-idle thread doesn't pin a
	// much later reply as current. On expiry the lookup fails open anyway.
	turnKeyTTL    = 5 * time.Minute
	turnKeyPrefix = "miriam:turn:"
)

type redisTurnTracker struct {
	redis cache.RedisClient
	log   *zap.Logger
}

// NewTurnTracker builds a Redis-backed tracker. When redis is nil the returned
// tracker is a no-op that treats every turn as current.
func NewTurnTracker(redis cache.RedisClient, log *zap.Logger) TurnTracker {
	return &redisTurnTracker{redis: redis, log: log}
}

// turnConversationKey scopes a turn to one conversation. threadID is the
// bridge's thread (space) id, which is stable per conversation.
func turnConversationKey(platform entities.Platform, threadID string) string {
	if threadID == "" {
		return ""
	}
	return turnKeyPrefix + platform.String() + ":" + threadID
}

func (t *redisTurnTracker) MarkTurn(ctx context.Context, key, turnID string) {
	if t == nil || t.redis == nil || key == "" || turnID == "" {
		return
	}
	if err := t.redis.Set(ctx, key, turnID, turnKeyTTL); err != nil && t.log != nil {
		t.log.Debug("turn marker write failed (fail-open)", zap.Error(err))
	}
}

func (t *redisTurnTracker) IsCurrent(ctx context.Context, key, turnID string) bool {
	if t == nil || t.redis == nil || key == "" || turnID == "" {
		return true
	}
	var stored string
	if err := t.redis.Get(ctx, key, &stored); err != nil {
		// Missing key or transport error → fail open.
		return true
	}
	if stored == "" {
		return true
	}
	return stored == turnID
}
