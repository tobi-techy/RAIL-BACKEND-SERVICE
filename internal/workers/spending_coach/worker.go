// Package spending_coach implements committed Conscious Spending Plan check-ins.
package spending_coach

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
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

type ConsciousSpendingPlanProvider interface {
	ListCommittedCheckIns(ctx context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error)
}

type ConsciousSpendingSnapshotProvider interface {
	GetConsciousSpendingSnapshot(ctx context.Context, userID uuid.UUID) consciousspending.Snapshot
}

// PushSender is the same shape used by every other proactive worker.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// Worker is the weekly spending-coach worker.
type Worker struct {
	push         PushSender
	coordinator  *platform.ProactiveCoordinator
	redis        cache.RedisClient
	costGate     CostGate
	plans        ConsciousSpendingPlanProvider
	snapshots    ConsciousSpendingSnapshotProvider
	logger       *zap.Logger
	tickInterval time.Duration
	clock        func() time.Time
}

func (w *Worker) SetConsciousSpendingProviders(plans ConsciousSpendingPlanProvider, snapshots ConsciousSpendingSnapshotProvider) {
	w.plans = plans
	w.snapshots = snapshots
}

// New constructs the worker.
func New(
	push PushSender,
	coordinator *platform.ProactiveCoordinator,
	redis cache.RedisClient,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		push:         push,
		coordinator:  coordinator,
		redis:        redis,
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
	if err := w.RunOnce(ctx); err != nil {
		w.logger.Warn("spending_coach initial tick failed", zap.Error(err))
	}
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

	if w.plans == nil || w.snapshots == nil {
		return nil
	}
	now := w.clock()
	if !couldAnyCheckInBeDue(now) {
		return nil
	}
	checkIns, err := w.plans.ListCommittedCheckIns(ctx)
	if err != nil {
		return fmt.Errorf("list committed plan check-ins: %w", err)
	}
	for i := range checkIns {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		checkIn := checkIns[i]
		if !dueForCadence(checkIn.Country, now, checkIn.Plan.CheckInCadence) {
			continue
		}
		userCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := w.processUser(userCtx, &checkIn.Plan); err != nil {
			w.logger.Warn("spending_coach: process user failed",
				zap.Stringer("user_id", checkIn.Plan.UserID), zap.Error(err))
		}
		cancel()
	}
	return nil
}

// processUser is the per-user scan. Kept as a method so the eval endpoint can
// invoke it for a single user.
func (w *Worker) processUser(ctx context.Context, plan *entities.ConsciousSpendingPlan) error {
	if plan == nil || plan.Status != entities.ConsciousSpendingPlanStatusCommitted {
		return nil
	}
	userID := plan.UserID
	now := w.clock()

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
	title, body, insightID := w.composeCommittedPlanCopy(ctx, userID, plan)
	if title == "" || body == "" {
		return nil
	}

	if !w.coordinator.Allow(ctx, userID, platform.ProactiveCategorySpendingCoach, false) {
		return nil
	}

	data := map[string]interface{}{
		"type":       "spending_coach",
		"insight_id": insightID,
	}
	if err := w.push.SendToUser(ctx, userID, title, body, data); err != nil {
		w.logger.Warn("spending_coach: push failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return nil
	}
	if w.redis != nil {
		_ = w.redis.Set(ctx, weekKey, "1", 7*24*time.Hour)
	}
	return nil
}

func (w *Worker) composeCommittedPlanCopy(ctx context.Context, userID uuid.UUID, plan *entities.ConsciousSpendingPlan) (string, string, string) {
	if plan == nil || w.snapshots == nil {
		return "", "", ""
	}
	actual := w.snapshots.GetConsciousSpendingSnapshot(ctx, userID)
	if !actual.Complete {
		return "Miriam: plan check", "I need one missing number before I can check your four-number plan honestly. Open Miriam and we'll fill it in.", "csp:missing_data"
	}
	variances, _ := consciousspending.Compare(plan, actual, decimal.NewFromInt(5))
	adverse := variances[:0]
	for _, variance := range variances {
		if isAdverseVariance(variance) {
			adverse = append(adverse, variance)
		}
	}
	if len(adverse) == 0 {
		return "Miriam: plan check", "You're holding the four numbers you committed to. Keep the system running; no new restriction needed.", "csp:on_track"
	}
	sort.SliceStable(adverse, func(i, j int) bool {
		return adverse[i].DeltaPct.Abs().GreaterThan(adverse[j].DeltaPct.Abs())
	})
	top := adverse[0]
	return "Miriam: recommitment check",
		fmt.Sprintf("%s is %s%%; your plan says %s%%. What changed? Open Miriam and we'll choose one recovery move, not rewrite the goal.",
			bucketLabel(top.Bucket), top.ActualPct.StringFixed(1), top.TargetPct.StringFixed(1)),
		"csp:variance:" + top.Bucket
}

func isAdverseVariance(variance consciousspending.Variance) bool {
	switch variance.Bucket {
	case consciousspending.BucketFixedCosts, consciousspending.BucketGuiltFreeSpending:
		return variance.DeltaPct.IsPositive()
	case consciousspending.BucketInvestments, consciousspending.BucketSavings:
		return variance.DeltaPct.IsNegative()
	default:
		return false
	}
}

func bucketLabel(bucket string) string {
	switch bucket {
	case consciousspending.BucketFixedCosts:
		return "Fixed costs"
	case consciousspending.BucketInvestments:
		return "Investments"
	case consciousspending.BucketSavings:
		return "Savings"
	case consciousspending.BucketGuiltFreeSpending:
		return "Guilt-free spending"
	default:
		return "That bucket"
	}
}

func dueForCadence(country string, now time.Time, cadence string) bool {
	if !dueForWeeklyCoach(country, now) {
		return false
	}
	switch cadence {
	case entities.CheckInCadenceBiweekly:
		_, week := now.ISOWeek()
		return week%2 == 0
	case entities.CheckInCadenceMonthly:
		loc := locationForCountry(country)
		if loc == nil {
			loc = time.UTC
		}
		return now.In(loc).Day() <= 7
	default:
		return true
	}
}

func couldAnyCheckInBeDue(now time.Time) bool {
	return now.Weekday() == time.Monday && now.Hour() >= 6 && now.Hour() <= 17
}

// dueForWeeklyCoach returns true when now is in the user's local Monday 9am
// hour. Mirrors the daily_pulse worker pattern. Falls back to UTC Monday 9am
// when country is missing.
func dueForWeeklyCoach(country string, now time.Time) bool {
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
