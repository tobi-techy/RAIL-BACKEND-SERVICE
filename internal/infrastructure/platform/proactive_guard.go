package platform

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// Proactive message categories for category-aware Allow.
const (
	ProactiveCategoryBriefing = "briefing"
	ProactiveCategoryRisk     = "risk"
	ProactiveCategoryNudge    = "nudge"
	ProactiveCategoryFollowup = "followup"
	ProactiveCategoryReceipt  = "receipt"
)

// ProactivePrefs is the subset of miriam_preferences the guard needs.
type ProactivePrefs struct {
	QuietEnabled   bool
	QuietStart     int
	QuietEnd       int
	DailyCap       int
	Timezone       string // IANA; empty → fall back to country resolver
	AllowBriefings bool
	AllowRisk      bool
	AllowNudges    bool
	AllowFollowups bool
}

// PreferencesResolver returns per-user proactive prefs (defaults on miss).
type PreferencesResolver interface {
	ProactivePrefs(ctx context.Context, userID uuid.UUID) ProactivePrefs
}

// ProactiveGuard enforces "presence, not a notification machine": a per-user
// daily cap on proactive messages plus quiet hours in the user's local time.
// It is Redis-backed and fails open — infrastructure trouble must never silence
// a genuinely important alert.
type ProactiveGuard struct {
	redis      cache.RedisClient
	tz         UserTimezoneResolver
	prefs      PreferencesResolver
	defaultLoc *time.Location
	dailyCap   int
	quietStart int // inclusive local hour [0-23]
	quietEnd   int // exclusive local hour [0-23]
	logger     *zap.Logger
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

// SetPreferencesResolver attaches per-user prefs (optional).
func (g *ProactiveGuard) SetPreferencesResolver(p PreferencesResolver) {
	g.prefs = p
}

func (g *ProactiveGuard) resolvePrefs(ctx context.Context, userID uuid.UUID) ProactivePrefs {
	if g.prefs != nil {
		return g.prefs.ProactivePrefs(ctx, userID)
	}
	return ProactivePrefs{
		QuietEnabled:   true,
		QuietStart:     g.quietStart,
		QuietEnd:       g.quietEnd,
		DailyCap:       g.dailyCap,
		AllowBriefings: true,
		AllowRisk:      true,
		AllowNudges:    true,
		AllowFollowups: true,
	}
}

func (g *ProactiveGuard) location(ctx context.Context, userID uuid.UUID, p ProactivePrefs) *time.Location {
	if p.Timezone != "" {
		if loc, err := time.LoadLocation(p.Timezone); err == nil && loc != nil {
			return loc
		}
	}
	if g.tz != nil {
		if loc := g.tz.UserTimezone(ctx, userID); loc != nil {
			return loc
		}
	}
	return g.defaultLoc
}

// inQuietHoursPrefs reports quiet hours using per-user start/end when quiet is enabled.
func inQuietHoursPrefs(local time.Time, quietEnabled bool, quietStart, quietEnd int) bool {
	if !quietEnabled || quietStart == quietEnd {
		return false
	}
	h := local.Hour()
	if quietStart < quietEnd {
		return h >= quietStart && h < quietEnd
	}
	return h >= quietStart || h < quietEnd
}

// categoryAllowed checks per-category allow_* flags.
func categoryAllowed(p ProactivePrefs, category string) bool {
	switch category {
	case ProactiveCategoryBriefing:
		return p.AllowBriefings
	case ProactiveCategoryRisk:
		return p.AllowRisk
	case ProactiveCategoryNudge:
		return p.AllowNudges
	case ProactiveCategoryFollowup:
		return p.AllowFollowups
	case ProactiveCategoryReceipt:
		return true // money receipts always allowed (still may be critical)
	default:
		return true
	}
}

// Allow reports whether a proactive message may be sent. critical=true bypasses
// quiet hours and the daily cap (money risks / receipts).
func (g *ProactiveGuard) Allow(ctx context.Context, userID uuid.UUID, critical bool) bool {
	return g.AllowCategory(ctx, userID, ProactiveCategoryNudge, critical)
}

// AllowCategory is category-aware: checks allow_* flags, quiet hours, daily cap.
func (g *ProactiveGuard) AllowCategory(ctx context.Context, userID uuid.UUID, category string, critical bool) bool {
	p := g.resolvePrefs(ctx, userID)

	// Receipts and money-safety risks always deliver when marked critical.
	if critical || category == ProactiveCategoryReceipt {
		return true
	}

	if !categoryAllowed(p, category) {
		if g.logger != nil {
			g.logger.Debug("proactive suppressed: category disabled",
				zap.Stringer("user_id", userID), zap.String("category", category))
		}
		return false
	}

	loc := g.location(ctx, userID, p)
	local := time.Now().In(loc)

	if inQuietHoursPrefs(local, p.QuietEnabled, p.QuietStart, p.QuietEnd) {
		if g.logger != nil {
			g.logger.Debug("proactive suppressed: quiet hours",
				zap.Stringer("user_id", userID), zap.Int("local_hour", local.Hour()))
		}
		return false
	}

	cap := p.DailyCap
	if cap <= 0 {
		cap = g.dailyCap
	}
	if cap <= 0 || g.redis == nil {
		return true
	}

	key := "miriam:proactive:count:" + userID.String() + ":" + local.Format("2006-01-02")
	count, err := g.redis.Incr(ctx, key)
	if err != nil {
		return true
	}
	if count == 1 {
		_ = g.redis.Expire(ctx, key, 48*time.Hour)
	}
	if int(count) > cap {
		if g.logger != nil {
			g.logger.Debug("proactive suppressed: daily cap reached",
				zap.Stringer("user_id", userID), zap.Int64("count", count), zap.Int("cap", cap))
		}
		return false
	}
	return true
}

// PrefsFromEntity maps domain prefs into the guard shape.
func PrefsFromEntity(p entities.MiriamPreferences) ProactivePrefs {
	tz := ""
	if p.Timezone != nil {
		tz = *p.Timezone
	}
	return ProactivePrefs{
		QuietEnabled:   p.QuietEnabled,
		QuietStart:     p.QuietStart,
		QuietEnd:       p.QuietEnd,
		DailyCap:       p.DailyCap,
		Timezone:       tz,
		AllowBriefings: p.AllowBriefings,
		AllowRisk:      p.AllowRisk,
		AllowNudges:    p.AllowNudges,
		AllowFollowups: p.AllowFollowups,
	}
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
