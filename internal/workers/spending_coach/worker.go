// Package spending_coach implements the weekly worker that turns Miriam's
// canonical brief into a Baby-Step-aware proactive nudge. The worker is
// leader-elected, fires once per user per week in their local 9am window, and
// delivers a single insight + concrete next step through the global
// proactive coordinator so the daily cap holds.
package spending_coach

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/goals"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// CostGate is the minimal surface of the AI cost guard that the worker
// needs. Satisfied by infraai.Guard. When non-nil, the worker refuses to
// deliver a weekly insight to a user who is already at or over their daily
// AI ceiling — sending them a "save more" nudge they can't act on (because
// the chat is rate-limited) would be user-hostile.
//
// nil disables the check. Implementations should be fail-open on Redis errors
// so a transient cache outage doesn't drop weekly nudges.
type CostGate interface {
	GetDailySpentUSD(ctx context.Context, userID uuid.UUID) (float64, error)
	DailyCapUSD() float64
}

// UserLister returns the users eligible for Miriam background workers.
type UserLister interface {
	ListMiriamWorkerUserIDs(ctx context.Context, limit int) ([]uuid.UUID, error)
}

// ActiveGoalProvider is the minimal surface for reading the user's current
// Baby Step. Implemented by goals.Service.
type ActiveGoalProvider interface {
	ListActiveByStep(ctx context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error)
	HasAnyGoal(ctx context.Context, userID uuid.UUID) (bool, error)
}

// BriefProvider returns the canonical Miriam brief. The worker reads only
// the top insight + next action; deeper fields are chat-only.
type BriefProvider interface {
	GetMiriamBrief(ctx context.Context, userID uuid.UUID, country string) (map[string]interface{}, error)
}

// SavingsSuggestionProvider surfaces concrete "what to do instead" tips.
type SavingsSuggestionProvider interface {
	GetSuggestions(ctx context.Context, userID uuid.UUID) (*SavingsSuggestions, error)
}

// SavingsSuggestions is the typed shape of the savings-suggestion tool result.
// Mirrors domain/services/ai.SavingsSuggestions to avoid an import cycle.
type SavingsSuggestions struct {
	Suggestions              []map[string]interface{} `json:"suggestions"`
	TotalPotentialMonthlySav string                   `json:"total_potential_monthly_savings"`
	AnnualStashGrowth        string                   `json:"annual_stash_growth_if_saved"`
	Message                  string                   `json:"message"`
}

// PushSender is the same shape used by every other proactive worker.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// UserCountryResolver returns the user's ISO country code (for local 9am
// window calculation). nil-safe — workers default to UTC when missing.
type UserCountryResolver interface {
	GetCountry(ctx context.Context, userID uuid.UUID) (string, error)
}

// Worker is the weekly spending-coach worker.
type Worker struct {
	userLister   UserLister
	goals        ActiveGoalProvider
	brief        BriefProvider
	suggestions  SavingsSuggestionProvider
	push         PushSender
	coordinator  *platform.ProactiveCoordinator
	redis        cache.RedisClient
	country      UserCountryResolver
	costGate     CostGate
	logger       *zap.Logger
	tickInterval time.Duration
	clock        func() time.Time
}

// New constructs the worker.
func New(
	userLister UserLister,
	goals ActiveGoalProvider,
	brief BriefProvider,
	suggestions SavingsSuggestionProvider,
	push PushSender,
	coordinator *platform.ProactiveCoordinator,
	redis cache.RedisClient,
	country UserCountryResolver,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		userLister:   userLister,
		goals:        goals,
		brief:        brief,
		suggestions:  suggestions,
		push:         push,
		coordinator:  coordinator,
		redis:        redis,
		country:      country,
		logger:       logger,
		tickInterval: 1 * time.Hour,
		clock:        func() time.Time { return time.Now().UTC() },
	}
}

// SetCostGate installs the per-user AI cost ceiling gate. When set, the
// worker refuses to deliver a weekly insight to a user whose daily AI spend
// has already reached the cap. Nil-safe; nil disables the check.
func (w *Worker) SetCostGate(g CostGate) {
	w.costGate = g
}

// SetPushSender late-binds the push sender. The bridge dispatcher is wired
// after the DI container is built, so the worker is constructed with a nil
// sender and the real one is installed before Start.
func (w *Worker) SetPushSender(p PushSender) { w.push = p }

// SetTickInterval overrides the hourly tick. Useful for tests.
func (w *Worker) SetTickInterval(d time.Duration) { w.tickInterval = d }

// SetClock overrides the clock for tests.
func (w *Worker) SetClock(fn func() time.Time) { w.clock = fn }

// Start runs the worker loop until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("spending_coach worker started", zap.Duration("tick", w.tickInterval))
	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("spending_coach worker stopped")
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Warn("spending_coach tick failed", zap.Error(err))
			}
		}
	}
}

// RunOnce performs a single pass. Exposed for tests + eval endpoints.
func (w *Worker) RunOnce(ctx context.Context) error {
	if !w.tryAcquireLeader(ctx) {
		w.logger.Debug("spending_coach: lock held by another replica")
		return nil
	}
	defer w.releaseLeader(ctx)

	users, err := w.userLister.ListMiriamWorkerUserIDs(ctx, 500)
	if err != nil {
		return fmt.Errorf("list Miriam worker users: %w", err)
	}

	for _, userID := range users {
		if err := w.processUser(ctx, userID); err != nil {
			w.logger.Warn("spending_coach: user processing failed",
				zap.Stringer("user_id", userID), zap.Error(err))
		}
	}
	return nil
}

// processUser is the per-user scan. Kept as a method so the eval endpoint can
// invoke it for a single user.
func (w *Worker) processUser(ctx context.Context, userID uuid.UUID) error {
	country := ""
	if w.country != nil {
		if c, err := w.country.GetCountry(ctx, userID); err == nil {
			country = c
		}
	}
	now := w.clock()
	if !dueForWeeklyCoach(country, now, w.clock()) {
		return nil
	}

	// Refuse to deliver a weekly insight when the user has already burned
	// through their daily AI ceiling — they couldn't act on it anyway, and
	// the push would feel out-of-touch with their actual budget.
	if w.costGate != nil {
		spent, err := w.costGate.GetDailySpentUSD(ctx, userID)
		if err != nil {
			// Fail-open: log + proceed.
			w.logger.Debug("spending_coach: cost gate lookup failed, proceeding",
				zap.Stringer("user_id", userID), zap.Error(err))
		} else if cap := w.costGate.DailyCapUSD(); cap > 0 && spent >= cap {
			w.logger.Debug("spending_coach: user over daily AI ceiling, skipping",
				zap.Stringer("user_id", userID),
				zap.Float64("spent_usd", spent),
				zap.Float64("cap_usd", cap),
			)
			return nil
		}
	}

	// Per-user dedup key: only fire once per local week.
	weekKey := "spending-coach:sent:" + userID.String() + ":" + isoWeek(now)
	if w.redis != nil {
		exists, _ := w.redis.Exists(ctx, weekKey)
		if exists {
			return nil
		}
	}

	// Compose the copy.
	step := w.findCurrentStep(ctx, userID)
	title, body, insightID := w.composeCopy(ctx, userID, step, country)
	if title == "" || body == "" {
		return nil
	}

	if !w.coordinator.Allow(ctx, userID, platform.ProactiveCategorySpendingCoach, false) {
		return nil
	}

	data := map[string]interface{}{
		"type":      "spending_coach",
		"step":      step,
		"insight_id": insightID,
	}
	if err := w.push.SendToUser(ctx, userID, title, body, data); err != nil {
		w.logger.Warn("spending_coach: push failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return nil
	}
	if w.redis != nil {
		_ = w.redis.Set(ctx, weekKey, "1", 7*24*time.Hour)
		// Per-insight dedup so the same insight doesn't repeat week-over-week.
		if insightID != "" {
			_ = w.redis.Set(ctx,
				"spending-coach:insight:"+userID.String()+":"+insightID,
				"1", 14*24*time.Hour)
		}
	}
	return nil
}

// composeCopy reads the brief + suggestions and produces a Baby-Step-aware
// nudge. Returns ("", "", "") when there's nothing actionable.
func (w *Worker) composeCopy(ctx context.Context, userID uuid.UUID, step int, country string) (string, string, string) {
	var topInsight map[string]interface{}
	if w.brief != nil {
		raw, err := w.brief.GetMiriamBrief(ctx, userID, country)
		if err == nil {
			topInsight = topInsightFromBrief(raw)
		}
	}

	// Suggestion-driven copy when no insight or insight is below threshold.
	suggestion, suggestionAmount := topSuggestion(ctx, w.suggestions, userID)

	if topInsight != nil {
		title := stringField(topInsight["title"])
		body := stringField(topInsight["body"])
		insightID := stringField(topInsight["id"])
		severity := strings.ToLower(stringField(topInsight["severity"]))
		if title == "" || body == "" {
			// fall through to suggestion
		} else if severity == "info" && suggestionAmount.LessThan(decimal.NewFromInt(20)) {
			return "", "", "" // nothing actionable this week
		} else {
			// Reframe the body via the current step's voice.
			return stepTitle(step), reframeForStep(step, body), insightID
		}
	}

	if !suggestionAmount.LessThan(decimal.NewFromInt(20)) && suggestion != nil {
		cat := stringField(suggestion["category"])
		tip := stringField(suggestion["tip"])
		if cat == "" || tip == "" {
			return "", "", ""
		}
		body := fmt.Sprintf("You could save %s/month on %s. %s.",
			suggestion["potential_savings"], cat, tip)
		return stepTitle(step), reframeForStep(step, body), "savings_suggestion:" + cat
	}

	return "", "", ""
}

// findCurrentStep mirrors goal_progress.findCurrentStep without exposing the
// worker package's internals.
func (w *Worker) findCurrentStep(ctx context.Context, userID uuid.UUID) int {
	for step := 1; step <= 7; step++ {
		list, err := w.goals.ListActiveByStep(ctx, userID, step)
		if err != nil {
			return step
		}
		if len(list) > 0 {
			return step
		}
	}
	return 0
}

// dueForWeeklyCoach returns true when now is in the user's local Monday 9am
// hour. Mirrors the daily_pulse worker pattern. Falls back to UTC Monday 9am
// when country is missing.
func dueForWeeklyCoach(country string, now, refTime time.Time) bool {
	loc := locationForCountry(country)
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	if local.Weekday() != time.Monday {
		return false
	}
	if local.Hour() != 9 {
		return false
	}
	_ = refTime
	return true
}

func locationForCountry(country string) *time.Location {
	tz := timezoneForCountry(country)
	if tz == "" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil
	}
	return loc
}

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
		return ""
	}
}

// isoWeek returns "YYYY-Www" for the time. Used for per-week dedup.
func isoWeek(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// topInsightFromBrief reads the top insight by importance, matching the brief
// response shape.
func topInsightFromBrief(brief map[string]interface{}) map[string]interface{} {
	raw, ok := brief["insights"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		severity := strings.ToLower(stringField(m["severity"]))
		if severity == "warning" || severity == "critical" {
			return m
		}
	}
	return nil
}

// topSuggestion returns the highest-impact suggestion and its amount.
func topSuggestion(ctx context.Context, p SavingsSuggestionProvider, userID uuid.UUID) (map[string]interface{}, decimal.Decimal) {
	if p == nil {
		return nil, decimal.Zero
	}
	s, err := p.GetSuggestions(ctx, userID)
	if err != nil || s == nil {
		return nil, decimal.Zero
	}
	var best map[string]interface{}
	var bestAmt decimal.Decimal
	for _, item := range s.Suggestions {
		amt, err := decimal.NewFromString(stringField(item["potential_savings"]))
		if err != nil {
			continue
		}
		if amt.GreaterThan(bestAmt) {
			best = item
			bestAmt = amt
		}
	}
	return best, bestAmt
}

func stringField(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// stepTitle returns a brief title shaped for the user's current Baby Step.
func stepTitle(step int) string {
	switch step {
	case 1:
		return "Miriam: starter fund check"
	case 2:
		return "Miriam: sprint phase update"
	case 3:
		return "Miriam: cushion update"
	case 4, 5, 6, 7:
		return "Miriam: long-game check"
	default:
		return "Miriam: weekly check"
	}
}

// reframeForStep prefixes the brief body with a step-aware lead so the same
// insight reads differently across the ladder. Pure text transform — no DB.
func reframeForStep(step int, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return body
	}
	switch step {
	case 1:
		return "Every dollar off Spend goes to your starter. " + body
	case 2:
		return "Sprint phase: " + body + " Throw it at the snowball instead."
	case 3:
		return "Hold the cushion: " + body
	case 4, 5:
		return "Long game: " + body
	case 6:
		return "Momentum check: " + body
	case 7:
		return "Wealth update: " + body
	default:
		return body
	}
}

// tryAcquireLeader / releaseLeader mirror goal_progress.worker.
func (w *Worker) tryAcquireLeader(ctx context.Context) bool {
	if w.redis == nil {
		return true
	}
	key := "spending-coach:leader:" + w.clock().Format("2006-01-02")
	ok, err := w.redis.SetNX(ctx, key, "1", 90*time.Minute)
	if err != nil {
		return true
	}
	return ok
}

func (w *Worker) releaseLeader(ctx context.Context) {
	if w.redis == nil {
		return
	}
	key := "spending-coach:leader:" + w.clock().Format("2006-01-02")
	_ = w.redis.Del(ctx, key)
}

// keepGoals reference live for the goals.Service wiring (compile-time check).
var _ = goals.PaceReport{}
