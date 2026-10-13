package platform

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
)

// InboundDeduper makes one provider message one turn across restarts and
// replicas. The bridge's own dedupe is a 60-second in-memory map: a replay past
// it, a restart, or a second replica would otherwise be a fresh model turn and
// a second reply.
//
// A message is claimed before any work happens. Done keeps the claim for a
// long window once the turn is delivered; Release gives it up when the turn
// fails so the bridge's retry runs.
type InboundDeduper interface {
	Claim(ctx context.Context, key string) ClaimState
	Done(ctx context.Context, key string)
	Release(ctx context.Context, key string)
}

// ClaimState is the answer to a claim on a provider message.
type ClaimState int

const (
	// Claimed: this caller owns the message and must handle it.
	Claimed ClaimState = iota
	// AlreadyHandled: a turn for this message finished; acknowledge and stop.
	AlreadyHandled
	// InFlight: another delivery is mid-turn. The caller must not ack it as
	// handled: if the bridge timed out and retried, the first delivery's request
	// was cancelled and will give its claim back, and acknowledging the retry
	// here would lose the message.
	InFlight
)

const (
	inboundKeyPrefix = "miriam:inbound:"
	// inboundProcessingTTL holds the claim for a turn in flight. A statement
	// read keeps the request open ~150s; a replica that dies mid-turn frees the
	// message when this expires.
	inboundProcessingTTL = 3 * time.Minute
	// inboundDoneTTL is how long a handled message is remembered: longer than
	// any provider redelivery or signed-webhook replay window.
	inboundDoneTTL    = 24 * time.Hour
	inboundDone       = "done"
	inboundProcessing = "processing"
)

type redisInboundDeduper struct {
	redis cache.RedisClient
	log   *zap.Logger
}

// NewInboundDeduper builds a Redis-backed deduper. It fails open: when Redis
// errors the message is handled, because a lost reply costs the sender more than
// a duplicate one. A nil client returns nil, which Process treats as no dedupe.
func NewInboundDeduper(redis cache.RedisClient, log *zap.Logger) InboundDeduper {
	if redis == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &redisInboundDeduper{redis: redis, log: log}
}

func (d *redisInboundDeduper) Claim(ctx context.Context, key string) ClaimState {
	claimed, err := d.redis.SetNX(ctx, key, inboundProcessing, inboundProcessingTTL)
	if err != nil {
		d.log.Warn("inbound dedupe unavailable; handling the message", zap.Error(err))
		return Claimed
	}
	if claimed {
		return Claimed
	}
	var state string
	if err := d.redis.Get(ctx, key, &state); err != nil {
		// The holder released between our two calls, or Redis blinked: handle
		// it. A duplicate turn is recoverable, a lost message is not.
		return Claimed
	}
	if state == inboundDone {
		return AlreadyHandled
	}
	return InFlight
}

func (d *redisInboundDeduper) Done(ctx context.Context, key string) {
	if err := d.redis.Set(ctx, key, inboundDone, inboundDoneTTL); err != nil {
		d.log.Warn("inbound dedupe could not record a handled message", zap.Error(err))
	}
}

func (d *redisInboundDeduper) Release(ctx context.Context, key string) {
	if err := d.redis.Del(ctx, key); err != nil {
		d.log.Warn("inbound dedupe could not release a claim", zap.Error(err))
	}
}

// inboundDedupeKey is the provider message id scoped by platform, or "" when
// the message carries none (nothing to dedupe on).
func inboundDedupeKey(platform entities.Platform, msgID string) string {
	if msgID == "" {
		return ""
	}
	return inboundKeyPrefix + string(platform) + ":" + msgID
}
