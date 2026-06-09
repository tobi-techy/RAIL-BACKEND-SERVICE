package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/shopspring/decimal"
)

const (
	voiceDailyLimitUSD    = 100.0 // Max $100/day via voice
	voiceDailySpendPrefix = "voice_daily_spend:"
)

// VoiceDailyLimiter enforces a daily transfer cap for voice sessions.
type VoiceDailyLimiter struct {
	redis cache.RedisClient
}

func NewVoiceDailyLimiter(redis cache.RedisClient) *VoiceDailyLimiter {
	return &VoiceDailyLimiter{redis: redis}
}

func (l *VoiceDailyLimiter) key(userID uuid.UUID) string {
	today := time.Now().UTC().Format("2006-01-02")
	return fmt.Sprintf("%s%s:%s", voiceDailySpendPrefix, userID, today)
}

// luaCheckAndRecord atomically checks the limit and records spend.
// Returns 0 on success, or the current spend if over limit.
var luaCheckAndRecord = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local amount = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local newTotal = current + amount
if newTotal > limit then
    return tostring(current)
end
redis.call('SET', KEYS[1], tostring(newTotal), 'EX', ttl)
return '0'
`

// CheckAndRecord checks if the amount would exceed the daily limit.
// If within limit, atomically records the spend and returns nil.
// If over limit or Redis fails, returns an error (fail-closed).
func (l *VoiceDailyLimiter) CheckAndRecord(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	if l == nil || l.redis == nil {
		return nil // limiter not configured — allow
	}

	key := l.key(userID)
	ttlSeconds := int(24 * time.Hour / time.Second)

	result, err := l.redis.Client().Eval(ctx, luaCheckAndRecord,
		[]string{key},
		amount.InexactFloat64(),
		voiceDailyLimitUSD,
		ttlSeconds,
	).Text()
	if err != nil {
		return fmt.Errorf("voice_daily_limit: redis error (fail-closed): %w", err)
	}

	if result != "0" {
		current, pErr := parseFloat(result)
		if pErr != nil {
			return fmt.Errorf("voice_daily_limit: failed to parse spend (fail-closed): %w", pErr)
		}
		remaining := voiceDailyLimitUSD - current
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Errorf("voice_daily_limit: You've used $%.0f of your $%.0f daily voice limit. Remaining: $%.0f. Open the app for larger transfers.",
			current, voiceDailyLimitUSD, remaining)
	}
	return nil
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	if err != nil {
		return 0, fmt.Errorf("invalid float %q: %w", s, err)
	}
	return f, nil
}
