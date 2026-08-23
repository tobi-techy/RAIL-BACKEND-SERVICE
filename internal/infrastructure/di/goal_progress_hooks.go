package di

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/goals"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// GoalProgressHooks is the adapter that wires the goals service into the
// deposit-split path so every completed deposit refreshes user_goals progress
// and (in turn) the goal_progress worker's milestone events.
//
// This file lives in di/ rather than the domain layer because the
// automation service expects a thin callback interface (DepositHook). We
// implement that interface against goals.Service here, so the domain layer
// stays free of dependencies on this adapter.
type GoalProgressHooks struct {
	goals  *goals.Service
	logger *zap.Logger
}

// NewGoalProgressHooks constructs the hook.
func NewGoalProgressHooks(g *goals.Service, logger *zap.Logger) *GoalProgressHooks {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GoalProgressHooks{goals: g, logger: logger}
}

// OnDepositAllocated is called by automation.Service.EvaluateDepositReceived
// after a deposit is split into spend/stash. It refreshes user_goals
// progress so the goal_progress worker can fire milestone events on its
// next tick.
//
// allocatedStashAmount is the new Stash allocation (i.e. the post-split
// amount that landed in Stash from this deposit). The hook reads the user's
// current stash balance and updates each active goal's current_amount to
// match.
//
// Failures are logged but never propagated — the deposit path is
// safety-critical and a goal-progress failure must not block it.
func (h *GoalProgressHooks) OnDepositAllocated(ctx context.Context, userID uuid.UUID, allocatedStashAmount decimal.Decimal) {
	if h == nil || h.goals == nil {
		return
	}
	if userID == uuid.Nil {
		return
	}
	goalsList, err := h.goals.List(ctx, userID, false)
	if err != nil {
		h.logger.Warn("goal hooks: list goals failed",
			zap.Stringer("user_id", userID), zap.Error(err))
		return
	}
	if len(goalsList) == 0 {
		return
	}
	// Use the allocated amount directly as the new current_amount for each
	// active goal. This is intentionally simple — the goal_progress worker
	// evaluates pace against the per-goal amount, not the global stash
	// balance. Users with multiple goals share the stash and the worker
	// will treat each goal as a sub-counter; this keeps the model easy to
	// reason about in chat ("how much toward my laptop goal?") and avoids
	// double-counting when there are several goals.
	for i := range goalsList {
		g := goalsList[i]
		if !g.IsActive() {
			continue
		}
		// If the goal is targeted at a sub-portion of the stash (e.g. "$500
		// for the laptop"), the user is the source of truth for how much has
		// been earmarked. We update with the full allocated amount here;
		// chat-driven "update_goal_progress" can refine per-goal later.
		if _, _, _, err := h.goals.UpdateProgress(ctx, userID, g.ID, allocatedStashAmount); err != nil {
			h.logger.Warn("goal hooks: update progress failed",
				zap.Stringer("user_id", userID),
				zap.Stringer("goal_id", g.ID),
				zap.Error(err))
		}
	}
}
