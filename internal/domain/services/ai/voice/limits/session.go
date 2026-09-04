package limits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

const (
	voiceSessionLimitPrefix   = "voice_sessions:"
	voiceSessionWindowSeconds = 3600 // 1-hour fixed window
)

// VoiceSessionRateLimiter enforces a per-user cap on voice session creation
// across all instances using a Redis counter (the previous in-memory map
// under-counted under multiple replicas).
//
// Window: a fixed hourly window keyed by yyyy-mm-dd-HH (UTC). The first INCR
// in a window sets a 1-hour EXPIRE, so the counter self-cleans.
//
// Fail-OPEN: unlike VoiceDailyLimiter (which guards money and fails closed),
// this guards session creation. A Redis blip blocking ALL voice for every user
// is worse than a rare over-limit, so on any Redis error we log and ALLOW.
type VoiceSessionRateLimiter struct {
	redis      cache.RedisClient
	maxPerHour int
	logger     *zap.Logger
}

// NewVoiceSessionRateLimiter creates a Redis-backed voice session rate limiter.
func NewVoiceSessionRateLimiter(redis cache.RedisClient, maxPerHour int, logger *zap.Logger) *VoiceSessionRateLimiter {
	return &VoiceSessionRateLimiter{redis: redis, maxPerHour: maxPerHour, logger: logger}
}

func (l *VoiceSessionRateLimiter) key(userID uuid.UUID) string {
	window := time.Now().UTC().Format("2006-01-02-15")
	return fmt.Sprintf("%s%s:%s", voiceSessionLimitPrefix, userID, window)
}

// luaIncrWithExpire atomically increments the counter and sets the window TTL
// on first increment. Returns the new count.
var luaIncrWithExpire = `
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return count
`

// Allow reports whether the user may start another voice session this hour.
// Returns (allowed, error). On Redis error it returns (true, err) — fail-open:
// the caller should allow the session but may log the error.
func (l *VoiceSessionRateLimiter) Allow(ctx context.Context, userID uuid.UUID) (bool, error) {
	if l == nil || l.redis == nil {
		return true, nil // limiter not configured — allow
	}

	count, err := l.redis.Client().Eval(ctx, luaIncrWithExpire,
		[]string{l.key(userID)},
		voiceSessionWindowSeconds,
	).Int64()
	if err != nil {
		// Fail-OPEN: never block voice on a Redis blip.
		if l.logger != nil {
			l.logger.Warn("voice_session_limit: redis error (fail-open, allowing)",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
		return true, err
	}

	return count <= int64(l.maxPerHour), nil
}
