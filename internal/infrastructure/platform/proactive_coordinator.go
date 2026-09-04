package platform

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// ProactiveCategory enumerates the proactive-message categories used for
// per-category caps. The string values match the ones in ProactiveGuard so
// callers can pass either set interchangeably.
const (
	ProactiveCategoryGoalProgress   = "goal_progress"
	ProactiveCategorySpendingCoach  = "spending_coach"
	ProactiveCategorySavingsSuggest = "savings_suggestion"
)

// AllProactiveCategories is the canonical set. Used for tests that need to
// enumerate; do not mutate.
var AllProactiveCategories = []string{
	ProactiveCategoryBriefing,
	ProactiveCategoryRisk,
	ProactiveCategoryNudge,
	ProactiveCategoryFollowup,
	ProactiveCategoryReceipt,
	ProactiveCategoryGoalProgress,
	ProactiveCategorySpendingCoach,
	ProactiveCategorySavingsSuggest,
}

// ProactiveCoordinator is the single per-user daily cap across ALL proactive
// sources. It wraps the existing per-category ProactiveGuard so the global
// cap holds even when multiple workers race to send the same user.
//
// Counts are stored in Redis as a single global counter and one per-category
// counter. Both expire automatically at the next local-day boundary so we
// don't need a cron sweep.
type ProactiveCoordinator struct {
	redis               cache.RedisClient
	logger              *zap.Logger
	dailyCap            int
	categoryCapOverride map[string]int // category -> per-day cap; 0 means use dailyCap
	clock               func() time.Time
}

// NewProactiveCoordinator builds a coordinator. dailyCap<=0 disables the
// global cap (every Allow call passes the global check). categoryCapOverride
// lets specific categories carry their own lower cap (e.g. spending_coach: 1).
func NewProactiveCoordinator(redis cache.RedisClient, logger *zap.Logger, dailyCap int, categoryCapOverride map[string]int) *ProactiveCoordinator {
	if categoryCapOverride == nil {
		categoryCapOverride = map[string]int{}
	}
	return &ProactiveCoordinator{
		redis:               redis,
		logger:              logger,
		dailyCap:            dailyCap,
		categoryCapOverride: categoryCapOverride,
		clock:               func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the clock for tests.
func (c *ProactiveCoordinator) SetClock(fn func() time.Time) { c.clock = fn }

// Allow reports whether a proactive message may be sent to the user. The
// caller passes category + critical. critical=true bypasses the global cap
// (but still respects the per-category cap, except ProactiveCategoryReceipt
// which is always allowed). Returns false if either cap is reached.
func (c *ProactiveCoordinator) Allow(ctx context.Context, userID uuid.UUID, category string, critical bool) bool {
	if c == nil {
		return true
	}
	// Receipts and critical money-safety signals always deliver.
	if critical || category == ProactiveCategoryReceipt {
		return true
	}
	if c.redis == nil {
		return true
	}

	now := c.clock()
	day := now.Format("2006-01-02")

	// Global cap (across all non-critical categories).
	if c.dailyCap > 0 {
		globalKey := globalCounterKey(userID, day)
		count, err := c.redis.Incr(ctx, globalKey)
		if err != nil {
			if c.logger != nil {
				c.logger.Debug("proactive coordinator: global incr failed, failing open",
					zap.Stringer("user_id", userID), zap.Error(err))
			}
			return true
		}
		if count == 1 {
			_ = c.redis.Expire(ctx, globalKey, 48*time.Hour)
		}
		if int(count) > c.dailyCap {
			if c.logger != nil {
				c.logger.Debug("proactive coordinator: global cap reached",
					zap.Stringer("user_id", userID), zap.Int64("count", count), zap.Int("cap", c.dailyCap))
			}
			return false
		}
	}

	// Per-category cap (override or fall back to daily cap).
	catCap := c.categoryCapOverride[category]
	if catCap <= 0 {
		catCap = c.dailyCap
	}
	if catCap > 0 {
		catKey := categoryCounterKey(userID, category, day)
		count, err := c.redis.Incr(ctx, catKey)
		if err != nil {
			return true
		}
		if count == 1 {
			_ = c.redis.Expire(ctx, catKey, 48*time.Hour)
		}
		if int(count) > catCap {
			if c.logger != nil {
				c.logger.Debug("proactive coordinator: category cap reached",
					zap.Stringer("user_id", userID), zap.String("category", category),
					zap.Int64("count", count), zap.Int("cap", catCap))
			}
			return false
		}
	}
	return true
}

// CategoryCount returns the current global counter for a category on today's
// date. Used by tests + the eval endpoints.
func (c *ProactiveCoordinator) CategoryCount(ctx context.Context, userID uuid.UUID, category string) (int64, error) {
	if c == nil || c.redis == nil {
		return 0, nil
	}
	key := categoryCounterKey(userID, category, c.clock().Format("2006-01-02"))
	var v int64
	if err := c.redis.Get(ctx, key, &v); err != nil {
		return 0, nil
	}
	return v, nil
}

func globalCounterKey(userID uuid.UUID, day string) string {
	return "proactive:global:" + userID.String() + ":" + day
}

func categoryCounterKey(userID uuid.UUID, category, day string) string {
	return "proactive:cat:" + category + ":" + userID.String() + ":" + day
}
