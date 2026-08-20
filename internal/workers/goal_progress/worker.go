// Package goal_progress implements the leader-elected hourly worker that
// surfaces Baby-Steps-aware goal notifications. It owns three notification
// types:
//
//   1. Milestone (25/50/75/100%) — copy is voice-shaped for the current step.
//   2. Pace-behind — fires when the deadline is <30 days away and projected
//      completion is <90% of target.
//   3. Step advance — fires when every goal on the current step is completed.
//
// All dispatches flow through the ProactiveCoordinator so the global
// per-user daily cap holds.
package goal_progress

import (
	"context"
	"encoding/json"
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

// GoalsService is the minimal surface the worker needs from goals.Service.
// Defined here as an interface so the worker is testable without a real DB.
type GoalsService interface {
	ListActiveUsers(ctx context.Context) ([]uuid.UUID, error)
	List(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]entities.UserGoal, error)
	ListActiveByStep(ctx context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error)
	UpdateProgress(ctx context.Context, userID, goalID uuid.UUID, amount decimal.Decimal) (*entities.UserGoal, goals.PaceReport, []string, error)
	Complete(ctx context.Context, userID, goalID uuid.UUID) error
}

// PushSender sends push notifications. Implemented by the platform
// BridgeDispatcher and the Expo push service.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// UserCountryResolver returns a user's ISO country code (for currency
// formatting + future tone tweaks). nil-safe — workers default to USD when
// the resolver is missing.
type UserCountryResolver interface {
	GetCountry(ctx context.Context, userID uuid.UUID) (string, error)
}

// Worker is the hourly goal-progress worker.
type Worker struct {
	goals     *goals.Service
	push      PushSender
	coordinator *platform.ProactiveCoordinator
	redis     cache.RedisClient
	country   UserCountryResolver
	logger    *zap.Logger
	tickInterval time.Duration
	clock     func() time.Time
}

// New constructs the worker.
func New(
	goals *goals.Service,
	push PushSender,
	coordinator *platform.ProactiveCoordinator,
	redis cache.RedisClient,
	country UserCountryResolver,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		goals:        goals,
		push:         push,
		coordinator:  coordinator,
		redis:        redis,
		country:      country,
		logger:       logger,
		tickInterval: 1 * time.Hour,
		clock:        func() time.Time { return time.Now().UTC() },
	}
}

// SetTickInterval overrides the default hourly tick. Useful for tests.
func (w *Worker) SetTickInterval(d time.Duration) { w.tickInterval = d }

// SetClock overrides the clock. Used by tests.
func (w *Worker) SetClock(fn func() time.Time) { w.clock = fn }

// Start runs the worker loop until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("goal_progress worker started", zap.Duration("tick", w.tickInterval))
	ticker := time.NewTicker(w.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("goal_progress worker stopped")
			return
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Warn("goal_progress tick failed", zap.Error(err))
			}
		}
	}
}

// RunOnce performs a single pass. Exposed for tests + eval endpoints.
func (w *Worker) RunOnce(ctx context.Context) error {
	// Leader election: only one replica runs per tick.
	if !w.tryAcquireLeader(ctx) {
		w.logger.Debug("goal_progress: lock held by another replica")
		return nil
	}
	defer w.releaseLeader(ctx)

	users, err := w.goals.ListActiveUsers(ctx)
	if err != nil {
		return fmt.Errorf("list active users: %w", err)
	}
	w.logger.Info("goal_progress: scanning", zap.Int("users", len(users)))

	var processed, milestoneFired, paceFired, stepAdvanced int
	for _, uid := range users {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		userCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		n := w.processUser(userCtx, uid)
		processed++
		milestoneFired += n.milestones
		paceFired += n.pace
		stepAdvanced += n.steps
		cancel()
	}

	w.logger.Info("goal_progress: tick complete",
		zap.Int("users", processed),
		zap.Int("milestones", milestoneFired),
		zap.Int("pace_alerts", paceFired),
		zap.Int("step_advances", stepAdvanced),
	)
	return nil
}

type userResult struct {
	milestones int
	pace       int
	steps      int
}

// processUser runs the per-user scan. Returns counts of notifications fired.
func (w *Worker) processUser(ctx context.Context, userID uuid.UUID) userResult {
	var out userResult
	now := w.clock()

	// Discover the user's current Baby Step: smallest step number with at
	// least one active goal. If none, default to step 1.
	step := w.findCurrentStep(ctx, userID)
	if step < 1 {
		step = 1
	}

	goalsList, err := w.goals.ListActiveByStep(ctx, userID, step)
	if err != nil {
		w.logger.Warn("goal_progress: list active goals failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return out
	}
	if len(goalsList) == 0 {
		return out
	}
	activeGoals := goalsList

	// 1. Per-goal milestone + pace checks.
	allComplete := true
	for i := range activeGoals {
		g := activeGoals[i]
		pct := g.PercentComplete()
		milestone := goals.NextMilestone(pct)
		if milestone > 0 && cooldownOK(ctx, w.redis, milestoneCooldownKey(g.ID, milestone)) {
			kind := goals.MilestoneKindFor(milestone)
			if kind != "" {
				w.fireMilestone(ctx, userID, &g, milestone, kind)
				out.milestones++
			}
		}

		// Pace check — only meaningful when the goal has a deadline.
		if g.Deadline != nil {
			daysLeft := int(g.Deadline.Sub(now).Hours() / 24)
			if daysLeft < 30 && pct.LessThan(decimal.NewFromInt(90)) {
				if cooldownOK(ctx, w.redis, paceCooldownKey(g.ID)) {
					w.firePaceBehind(ctx, userID, &g, daysLeft)
					out.pace++
				}
			}
		}

		if !g.IsCompleted() {
			allComplete = false
		}
	}

	// 2. Step-advance check — fires only when every goal on the current step
	// is completed AND we haven't fired the step-advance for this step yet.
	if allComplete && cooldownOK(ctx, w.redis, stepAdvanceCooldownKey(userID, step)) {
		next := step + 1
		if next <= 7 {
			w.fireStepAdvanced(ctx, userID, step, next)
			out.steps++
		}
	}

	return out
}

// findCurrentStep returns the smallest Baby Step number with at least one
// active goal. Returns 0 when the user has no active goals on any step.
func (w *Worker) findCurrentStep(ctx context.Context, userID uuid.UUID) int {
	for step := 1; step <= 7; step++ {
		list, err := w.goals.ListActiveByStep(ctx, userID, step)
		if err != nil {
			w.logger.Warn("goal_progress: list step goals failed",
				zap.Stringer("user_id", userID), zap.Int("step", step), zap.Error(err))
			return step
		}
		if len(list) > 0 {
			return step
		}
	}
	return 0
}

// fireMilestone sends a step-aware milestone push through the coordinator.
func (w *Worker) fireMilestone(ctx context.Context, userID uuid.UUID, goal *entities.UserGoal, milestone int, kind string) {
	if !w.coordinator.Allow(ctx, userID, platform.ProactiveCategoryGoalProgress, false) {
		return
	}
	title, body := milestoneCopy(goal, milestone)
	data := map[string]interface{}{
		"type":     "goal_milestone",
		"goal_id":  goal.ID.String(),
		"milestone": milestone,
		"kind":     kind,
		"category": goal.Category,
	}
	if err := w.push.SendToUser(ctx, userID, title, body, data); err != nil {
		w.logger.Warn("goal_progress: push failed",
			zap.Stringer("user_id", userID),
			zap.String("kind", kind), zap.Error(err))
		return
	}
	markCooldown(ctx, w.redis, milestoneCooldownKey(goal.ID, milestone), 24*time.Hour)
}

// firePaceBehind sends a "you're behind" notification framed by the goal's
// Baby Step category.
func (w *Worker) firePaceBehind(ctx context.Context, userID uuid.UUID, goal *entities.UserGoal, daysLeft int) {
	if !w.coordinator.Allow(ctx, userID, platform.ProactiveCategoryGoalProgress, false) {
		return
	}
	title, body := paceBehindCopy(goal, daysLeft)
	data := map[string]interface{}{
		"type":    "goal_pace_behind",
		"goal_id": goal.ID.String(),
		"days_left": daysLeft,
		"category": goal.Category,
	}
	if err := w.push.SendToUser(ctx, userID, title, body, data); err != nil {
		w.logger.Warn("goal_progress: pace push failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return
	}
	markCooldown(ctx, w.redis, paceCooldownKey(goal.ID), 24*time.Hour)
}

// fireStepAdvanced sends the step-advance notification.
func (w *Worker) fireStepAdvanced(ctx context.Context, userID uuid.UUID, from, to int) {
	if !w.coordinator.Allow(ctx, userID, platform.ProactiveCategoryGoalProgress, false) {
		return
	}
	title, body := stepAdvanceCopy(from, to)
	data := map[string]interface{}{
		"type":      "goal_step_advanced",
		"from_step": from,
		"to_step":   to,
	}
	if err := w.push.SendToUser(ctx, userID, title, body, data); err != nil {
		w.logger.Warn("goal_progress: step-advance push failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return
	}
	markCooldown(ctx, w.redis, stepAdvanceCooldownKey(userID, from), 7*24*time.Hour)
}

// tryAcquireLeader attempts a single-replica lock. Fails open when Redis is
// missing — same pattern as the autopilot service.
func (w *Worker) tryAcquireLeader(ctx context.Context) bool {
	if w.redis == nil {
		return true
	}
	key := "goal-progress:leader:" + w.clock().Format("2006-01-02")
	ok, err := w.redis.SetNX(ctx, key, "1", 90*time.Minute)
	if err != nil {
		w.logger.Warn("goal_progress: lock acquire failed, failing open",
			zap.String("key", key), zap.Error(err))
		return true
	}
	return ok
}

func (w *Worker) releaseLeader(ctx context.Context) {
	if w.redis == nil {
		return
	}
	key := "goal-progress:leader:" + w.clock().Format("2006-01-02")
	_ = w.redis.Del(ctx, key)
}

// cooldownOK returns true when the Redis key does not exist. Missing Redis is
// treated as "not cooled down" — fail-open, never block notifications on a
// cache blip.
func cooldownOK(ctx context.Context, redis cache.RedisClient, key string) bool {
	if redis == nil {
		return true
	}
	exists, err := redis.Exists(ctx, key)
	if err != nil {
		return true
	}
	return !exists
}

func markCooldown(ctx context.Context, redis cache.RedisClient, key string, ttl time.Duration) {
	if redis == nil {
		return
	}
	_ = redis.Set(ctx, key, "1", ttl)
}

func milestoneCooldownKey(goalID uuid.UUID, milestone int) string {
	return fmt.Sprintf("goal-progress:milestone:%s:%d", goalID.String(), milestone)
}

func paceCooldownKey(goalID uuid.UUID) string {
	return "goal-progress:pace:" + goalID.String()
}

func stepAdvanceCooldownKey(userID uuid.UUID, step int) string {
	return fmt.Sprintf("goal-progress:step:%s:%d", userID.String(), step)
}

// --- Copy helpers (Baby-Steps voice, no brand attribution) ---

// milestoneCopy returns the title + body for a milestone push. Copy is voice-
// shaped for the goal's category so chat and notifications agree.
func milestoneCopy(g *entities.UserGoal, milestone int) (string, string) {
	amount := g.CurrentAmount.StringFixed(2)
	target := g.TargetAmount.StringFixed(2)
	category := strings.ToLower(g.Category)
	name := g.Name

	switch {
	case milestone == 25 && category == entities.GoalCategoryStarterEmergency:
		return "Quarter way there",
			fmt.Sprintf("$%s into your starter fund. $%s to go.", amount, target)
	case milestone == 50 && category == entities.GoalCategoryStarterEmergency:
		return "Halfway on the starter",
			fmt.Sprintf("$%s saved of $%s starter. Keep the pace.", amount, target)
	case milestone == 75 && category == entities.GoalCategoryStarterEmergency:
		return "Starter fund is close",
			fmt.Sprintf("You're $%s away from your $%s starter. Final stretch.", target, target)
	case milestone == 100 && category == entities.GoalCategoryStarterEmergency:
		return "Starter fund complete",
			"$1K starter is in. Now the snowball starts."

	case milestone == 25 && category == entities.GoalCategoryDebtPayoff:
		return "Sprint phase: 25% on " + name,
			fmt.Sprintf("$%s gone on %s. Keep attacking the smallest.", amount, name)
	case milestone == 50 && category == entities.GoalCategoryDebtPayoff:
		return "Halfway on " + name,
			fmt.Sprintf("$%s of $%s gone. Don't slow down.", amount, target)
	case milestone == 75 && category == entities.GoalCategoryDebtPayoff:
		return "Almost free of " + name,
			fmt.Sprintf("Only $%s left on %s. Stay aggressive.", target, name)
	case milestone == 100 && category == entities.GoalCategoryDebtPayoff:
		return name + " — paid off",
			"That one's gone. Roll the payment into the next debt."

	case milestone == 25 && category == entities.GoalCategoryFullEmergency:
		return "Emergency fund: 25%",
			fmt.Sprintf("$%s into your 3-month fund. Keep building.", amount)
	case milestone == 50 && category == entities.GoalCategoryFullEmergency:
		return "Halfway to fully funded",
			fmt.Sprintf("$%s of $%s emergency. One and a half months in the bank.", amount, target)
	case milestone == 75 && category == entities.GoalCategoryFullEmergency:
		return "3-month fund: 75%",
			fmt.Sprintf("Almost there. $%s left to a full cushion.", target)
	case milestone == 100 && category == entities.GoalCategoryFullEmergency:
		return "Fully funded emergency",
			"3 months of expenses saved. Sleep better at night."

	case milestone == 25 && category == entities.GoalCategoryRetirement:
		return "Retirement: 25%",
			fmt.Sprintf("$%s started. 15%% of income is the magic number.", amount)
	case milestone == 50 && category == entities.GoalCategoryRetirement:
		return "Halfway on retirement",
			fmt.Sprintf("$%s of $%s. Compounding is doing the work.", amount, target)
	case milestone == 75 && category == entities.GoalCategoryRetirement:
		return "Retirement: 75%",
			fmt.Sprintf("$%s left. Don't stop — time in market beats timing.", target)
	case milestone == 100 && category == entities.GoalCategoryRetirement:
		return "Retirement target hit",
			"On track. Hold the 15% and let compounding do the rest."

	case milestone == 100 && category == entities.GoalCategoryCollege:
		return "College milestone hit",
			"Every dollar started early does the heavy lifting later."
	default:
		// Generic fallback (freeform goals or unmapped categories).
		if milestone == 100 {
			return name + " — done", fmt.Sprintf("$%s of $%s saved. Goal complete.", amount, target)
		}
		return fmt.Sprintf("%d%% on %s", milestone, name),
			fmt.Sprintf("$%s of $%s. %d%% done.", amount, target, milestone)
	}
}

// paceBehindCopy returns the title + body for a "you're behind" push.
func paceBehindCopy(g *entities.UserGoal, daysLeft int) (string, string) {
	category := strings.ToLower(g.Category)
	name := g.Name
	amount := g.CurrentAmount.StringFixed(2)
	target := g.TargetAmount.StringFixed(2)
	if daysLeft < 1 {
		daysLeft = 1
	}
	switch category {
	case entities.GoalCategoryStarterEmergency:
		return "Starter fund is at risk",
			fmt.Sprintf("$%s of $%s with %d days left. Every dollar counts now.", amount, target, daysLeft)
	case entities.GoalCategoryDebtPayoff:
		return name + " is behind",
			fmt.Sprintf("$%s of $%s paid, %d days to go. Throw what you can at it.", amount, target, daysLeft)
	case entities.GoalCategoryFullEmergency:
		return "Emergency fund is behind",
			fmt.Sprintf("$%s of $%s with %d days left. Hold the line.", amount, target, daysLeft)
	case entities.GoalCategoryRetirement:
		return "Retirement pace is off",
			fmt.Sprintf("$%s of $%s with %d days left. Bump the contribution.", amount, target, daysLeft)
	default:
		return name + " is behind",
			fmt.Sprintf("$%s of $%s with %d days left. Time to lean in.", amount, target, daysLeft)
	}
}

// stepAdvanceCopy returns the title + body for a step-advance push.
func stepAdvanceCopy(from, to int) (string, string) {
	switch to {
	case 2:
		return "Step 1 cleared — snowball starts",
			"Starter fund done. Now: smallest debt first, every extra dollar at it."
	case 3:
		return "Step 2 cleared — debts gone",
			"All debts paid. Next: 3 months of expenses saved."
	case 4:
		return "Step 3 cleared — fully funded",
			"Emergency fund is set. Next: 15% of income to retirement."
	case 5:
		return "Step 4 cleared — retirement on track",
			"You're investing consistently. Next: kids' college if applicable."
	case 6:
		return "Step 5 cleared — college funded",
			"Next: pay off the home early."
	case 7:
		return "Step 6 cleared — house paid",
			"Last step: build wealth and give. The fun part."
	default:
		return fmt.Sprintf("Step %d done", from),
			fmt.Sprintf("Moving to Step %d.", to)
	}
}

// marshalEvent is reserved for future use — keeps the json import live for the
// audit log payloads that may flow through this worker later.
var _ = json.Marshal
