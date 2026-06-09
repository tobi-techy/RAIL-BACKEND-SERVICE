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

// CheckAndRecord checks if the amount would exceed the daily limit.
// If within limit, records the spend and returns nil.
// If over limit, returns an error with the remaining allowance.
func (l *VoiceDailyLimiter) CheckAndRecord(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	if l == nil || l.redis == nil {
		return nil // limiter not configured — allow
	}

	key := l.key(userID)
	var current float64
	if err := l.redis.Get(ctx, key, &current); err != nil {
		current = 0 // key doesn't exist = first action today
	}

	newTotal := current + amount.InexactFloat64()
	if newTotal > voiceDailyLimitUSD {
		remaining := voiceDailyLimitUSD - current
		if remaining < 0 {
			remaining = 0
		}
		return fmt.Errorf("voice_daily_limit: You've used $%.0f of your $%.0f daily voice limit. Remaining: $%.0f. Open the app for larger transfers.", current, voiceDailyLimitUSD, remaining)
	}

	// Record the spend with 24h TTL (auto-expires at end of day)
	_ = l.redis.Set(ctx, key, newTotal, 24*time.Hour)
	return nil
}
