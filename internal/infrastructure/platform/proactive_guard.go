package platform

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// ProactiveGuard enforces "presence, not a notification machine": a per-user
// daily cap on proactive messages plus quiet hours in the user's local time.
// It is Redis-backed and fails open — infrastructure trouble must never silence
// a genuinely important alert.
type ProactiveGuard struct {
	redis         cache.RedisClient
	tz            UserTimezoneResolver
	defaultLoc    *time.Location
	dailyCap      int
	quietStart    int // inclusive local hour [0-23]
	quietEnd      int // exclusive local hour [0-23]
	logger        *zap.Logger
}

// UserTimezoneResolver returns a user's local timezone. Optional — when nil the
// guard uses its default location.
type UserTimezoneResolver interface {
	UserTimezone(ctx context.Context, userID uuid.UUID) *time.Location
}

// NewProactiveGuard builds a guard. dailyCap<=0 disables the cap; a quiet window
// where start==end disables quiet hours.
func NewProactiveGuard(redis cache.RedisClient, tz UserTimezoneResolver, defaultTZ string, dailyCap, quietStart, quietEnd int, logger *zap.Logger) *ProactiveGuard {
	loc, err := time.LoadLocation(defaultTZ)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return &ProactiveGuard{
		redis: redis, tz: tz, defaultLoc: loc,
		dailyCap: dailyCap, quietStart: quietStart, quietEnd: quietEnd, logger: logger,
	}
}

func (g *ProactiveGuard) location(ctx context.Context, userID uuid.UUID) *time.Location {
	if g.tz != nil {
		if loc := g.tz.UserTimezone(ctx, userID); loc != nil {
			return loc
		}
	}
	return g.defaultLoc
}

// InQuietHours reports whether the user's local time is inside the quiet window.
func (g *ProactiveGuard) inQuietHours(local time.Time) bool {
	if g.quietStart == g.quietEnd {
		return false
	}
	h := local.Hour()
	if g.quietStart < g.quietEnd {
		return h >= g.quietStart && h < g.quietEnd
	}
	// Window wraps past midnight (e.g. 22 → 6).
	return h >= g.quietStart || h < g.quietEnd
}

// Allow reports whether a proactive message may be sent to the user right now.
// When allowed it consumes one unit of the user's daily budget. critical=true
// bypasses both quiet hours and the daily cap (used for genuine money risks).
func (g *ProactiveGuard) Allow(ctx context.Context, userID uuid.UUID, critical bool) bool {
	if critical {
		return true
	}

	loc := g.location(ctx, userID)
	local := time.Now().In(loc)

	if g.inQuietHours(local) {
		if g.logger != nil {
			g.logger.Debug("proactive suppressed: quiet hours",
				zap.Stringer("user_id", userID), zap.Int("local_hour", local.Hour()))
		}
		return false
	}

	if g.dailyCap <= 0 || g.redis == nil {
		return true
	}

	key := "miriam:proactive:count:" + userID.String() + ":" + local.Format("2006-01-02")
	count, err := g.redis.Incr(ctx, key)
	if err != nil {
		// Fail open: delivery matters more than the cap when Redis is down.
		return true
	}
	if count == 1 {
		_ = g.redis.Expire(ctx, key, 48*time.Hour)
	}
	if int(count) > g.dailyCap {
		if g.logger != nil {
			g.logger.Debug("proactive suppressed: daily cap reached",
				zap.Stringer("user_id", userID), zap.Int64("count", count), zap.Int("cap", g.dailyCap))
		}
		return false
	}
	return true
}

// userTimezoneAdapter resolves a user's timezone from their stored country.
type userTimezoneAdapter struct {
	countryOf func(ctx context.Context, userID uuid.UUID) string
}

// NewUserTimezoneResolver builds a resolver from a country lookup closure.
func NewUserTimezoneResolver(countryOf func(ctx context.Context, userID uuid.UUID) string) UserTimezoneResolver {
	return &userTimezoneAdapter{countryOf: countryOf}
}

func (a *userTimezoneAdapter) UserTimezone(ctx context.Context, userID uuid.UUID) *time.Location {
	if a.countryOf == nil {
		return nil
	}
	loc, err := time.LoadLocation(timezoneForCountry(a.countryOf(ctx, userID)))
	if err != nil {
		return nil
	}
	return loc
}

// timezoneForCountry maps an ISO country code to an IANA timezone. Mirrors the
// mapping used by the daily pulse worker so proactive timing is consistent.
func timezoneForCountry(country string) string {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "NG":
		return "Africa/Lagos"
	case "GH":
		return "Africa/Accra"
	case "KE":
		return "Africa/Nairobi"
	case "ZA":
		return "Africa/Johannesburg"
	case "GB", "UK":
		return "Europe/London"
	case "US":
		return "America/New_York"
	case "CA":
		return "America/Toronto"
	case "DE":
		return "Europe/Berlin"
	case "FR":
		return "Europe/Paris"
	default:
		return "Africa/Lagos"
	}
}
