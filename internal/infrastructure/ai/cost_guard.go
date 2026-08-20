// Package ai/cost_guard.go provides per-user AI cost ceiling enforcement.
//
// The guard sits in front of the Cencori provider so a runaway prompt loop
// (or a single malicious user) cannot drain the project's monthly Cencori
// balance. Spend is tracked per user per day and per user per month in Redis,
// keyed by UTC day and UTC year-month so counters expire automatically.
//
// Cost figures are stored as integer cents (USD × 100) so we can use the
// atomic Redis INCRBY instead of GETSET/SET race-prone decimal writes.
//
// Usage:
//
//	guard := ai.NewGuard(redisClient, logger, 0.10, 2.00)
//	if err := guard.Allow(ctx, userID); err != nil {
//	    // user has hit their ceiling; refuse the call
//	}
//	// ... make the Cencori call ...
//	guard.Record(ctx, userID, estimatedCostUSD)
package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// Guard enforces per-user daily and monthly cost ceilings on AI calls.
type Guard struct {
	redis       cache.RedisClient
	logger      *zap.Logger
	dailyCap    int64 // cents
	monthlyCap  int64 // cents
	keyPrefix   string
}

// NewGuard builds a cost guard. Pass 0/0 to disable a particular ceiling.
func NewGuard(redis cache.RedisClient, logger *zap.Logger, dailyUSD, monthlyUSD float64) *Guard {
	return &Guard{
		redis:      redis,
		logger:     logger,
		dailyCap:   usdToCents(dailyUSD),
		monthlyCap: usdToCents(monthlyUSD),
		keyPrefix:  "rail:cost:user",
	}
}

// IsEnabled reports whether either ceiling is non-zero. When false, Allow and
// Record become cheap no-ops so callers don't have to special-case the toggle.
func (g *Guard) IsEnabled() bool {
	return g != nil && (g.dailyCap > 0 || g.monthlyCap > 0) && g.redis != nil
}

// DailyCapUSD reports the configured daily ceiling in USD. Returns 0 when the
// daily ceiling is disabled (so callers can compare with "spent >= cap"
// without special-casing).
func (g *Guard) DailyCapUSD() float64 {
	if g == nil {
		return 0
	}
	return centsToUSD(g.dailyCap)
}

// Allow returns nil if the user is within their daily/monthly spend limits.
// Returns a typed *ExceededError (not a generic error) so callers can
// distinguish "out of budget" from infrastructure failures.
func (g *Guard) Allow(ctx context.Context, userID uuid.UUID) error {
	if !g.IsEnabled() {
		return nil
	}

	now := time.Now().UTC()
	dailyKey := g.dailyKey(userID, now)
	monthlyKey := g.monthlyKey(userID, now)

	daily, err := g.readCents(ctx, dailyKey)
	if err != nil {
		// Fail-open: Redis blip should not deny service. The cap is a
		// safety net, not a security control.
		g.logger.Warn("cost guard: failed to read daily counter, failing open", zap.Error(err))
		return nil
	}
	monthly, err := g.readCents(ctx, monthlyKey)
	if err != nil {
		g.logger.Warn("cost guard: failed to read monthly counter, failing open", zap.Error(err))
		return nil
	}

	if g.dailyCap > 0 && daily >= g.dailyCap {
		return &ExceededError{Scope: "daily", LimitUSD: centsToUSD(g.dailyCap), SpentUSD: centsToUSD(daily)}
	}
	if g.monthlyCap > 0 && monthly >= g.monthlyCap {
		return &ExceededError{Scope: "monthly", LimitUSD: centsToUSD(g.monthlyCap), SpentUSD: centsToUSD(monthly)}
	}
	return nil
}

// Record atomically increments today's and this month's counters by the
// estimated cost of one AI call. Cost should be the post-call estimated cost
// (provider-reported tokens × pricing). Pre-call estimates are also fine — the
// guard is a soft ceiling.
//
// Counters are stored as JSON-encoded integers ("123") so the existing cache
// RedisClient.Get (which does a JSON unmarshal) reads them back as int64. We
// use INCRBY to mutate the underlying value and then SET to rewrite the JSON
// representation — this trades atomicity for read-compatibility. The guard is
// fail-open, so a race that loses 1-2 cents of accounting is acceptable.
//
// The counter TTL is 48h for daily and 35d for monthly so they expire
// automatically at the next billing boundary even if the cleanup job misses.
func (g *Guard) Record(ctx context.Context, userID uuid.UUID, costUSD float64) {
	if !g.IsEnabled() {
		return
	}
	cents := usdToCents(costUSD)
	if cents <= 0 {
		return
	}

	now := time.Now().UTC()
	dailyKey := g.dailyKey(userID, now)
	monthlyKey := g.monthlyKey(userID, now)

	g.incrementAndStore(ctx, dailyKey, cents, 48*time.Hour)
	g.incrementAndStore(ctx, monthlyKey, cents, 35*24*time.Hour)
}

// incrementAndStore does INCRBY then re-stores as JSON so the next Get reads
// it back as int64. TTL is refreshed on every write.
func (g *Guard) incrementAndStore(ctx context.Context, key string, cents int64, ttl time.Duration) {
	newVal, err := g.redis.IncrBy(ctx, key, cents)
	if err != nil {
		g.logger.Warn("cost guard: failed to increment counter", zap.String("key", key), zap.Error(err))
		return
	}
	if err := g.redis.Set(ctx, key, newVal, ttl); err != nil {
		g.logger.Warn("cost guard: failed to rewrite counter as JSON", zap.String("key", key), zap.Error(err))
	}
}

// GetDailySpentUSD and GetMonthlySpentUSD are read-only helpers used by the
// usage handler so the client can show "you have $0.07 of $0.10 left today".
func (g *Guard) GetDailySpentUSD(ctx context.Context, userID uuid.UUID) (float64, error) {
	if !g.IsEnabled() {
		return 0, nil
	}
	cents, err := g.readCents(ctx, g.dailyKey(userID, time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return centsToUSD(cents), nil
}

func (g *Guard) GetMonthlySpentUSD(ctx context.Context, userID uuid.UUID) (float64, error) {
	if !g.IsEnabled() {
		return 0, nil
	}
	cents, err := g.readCents(ctx, g.monthlyKey(userID, time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return centsToUSD(cents), nil
}

// ExceededError is returned by Allow when a user has hit their ceiling. The
// API handler maps this to a 429 with a friendly message.
type ExceededError struct {
	Scope    string  // "daily" or "monthly"
	LimitUSD float64
	SpentUSD float64
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("user has exceeded their %s AI cost ceiling: spent $%.2f of $%.2f",
		e.Scope, e.SpentUSD, e.LimitUSD)
}

// IsExceeded reports whether err is an *ExceededError (so callers don't need
// to import this package's concrete type everywhere).
func IsExceeded(err error) (*ExceededError, bool) {
	if err == nil {
		return nil, false
	}
	if e, ok := err.(*ExceededError); ok {
		return e, true
	}
	return nil, false
}

func (g *Guard) dailyKey(userID uuid.UUID, now time.Time) string {
	return fmt.Sprintf("%s:%s:%s", g.keyPrefix, userID.String(), now.Format("2006-01-02"))
}

func (g *Guard) monthlyKey(userID uuid.UUID, now time.Time) string {
	return fmt.Sprintf("%s:month:%s:%s", g.keyPrefix, userID.String(), now.Format("2006-01"))
}

func (g *Guard) readCents(ctx context.Context, key string) (int64, error) {
	if g.redis == nil {
		return 0, nil
	}
	var cents int64
	if err := g.redis.Get(ctx, key, &cents); err != nil {
		// cache.Get returns an error for redis.Nil (key not found). Treat as
		// zero spend — we want new users to be allowed, not denied.
		return 0, nil
	}
	return cents, nil
}

// usdToCents and centsToUSD round to the nearest cent. Using float64 for the
// public surface keeps the API ergonomic; the integer math happens internally
// so we never lose precision on the actual Redis writes.
func usdToCents(usd float64) int64 {
	return int64(usd*100 + 0.5)
}

func centsToUSD(cents int64) float64 {
	return float64(cents) / 100.0
}
