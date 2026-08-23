package goals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Service is the domain entrypoint for free-standing savings goals. It owns
// the user_goals + user_goal_progress_events tables through UserGoalRepository
// and emits audit events for every state change.
type Service struct {
	repo   repositories.UserGoalRepository
	logger *zap.Logger
	clock  func() time.Time // injectable for tests
}

// NewService constructs a goals service. Pass a nil clock to use time.Now.
func NewService(repo repositories.UserGoalRepository, logger *zap.Logger) *Service {
	return &Service{repo: repo, logger: logger, clock: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the clock for tests.
func (s *Service) SetClock(fn func() time.Time) { s.clock = fn }

func (s *Service) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UTC()
}

// CreateInput is the create payload. Name is required; the rest have sensible
// defaults applied in Create. Category is one of entities.GoalCategory*; empty
// becomes "freeform".
type CreateInput struct {
	Name           string
	TargetAmount   decimal.Decimal
	TargetCurrency string
	Deadline       *time.Time
	BabyStep       *int
	Category       string
	Source         string
}

// Create inserts a new goal and emits a "created" progress event. The goal is
// returned with ID, CreatedAt, and defaults populated.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in CreateInput) (*entities.UserGoal, error) {
	if userID == uuid.Nil {
		return nil, errors.New("user id is required")
	}
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if !in.TargetAmount.IsPositive() {
		return nil, errors.New("target amount must be positive")
	}
	if in.BabyStep != nil && (*in.BabyStep < 1 || *in.BabyStep > 7) {
		return nil, errors.New("baby step must be between 1 and 7")
	}

	if in.TargetCurrency == "" {
		in.TargetCurrency = "USD"
	}
	if in.Category == "" {
		in.Category = entities.GoalCategoryFreeform
	}
	if in.Source == "" {
		in.Source = entities.GoalSourceManual
	}

	goal := &entities.UserGoal{
		UserID:         userID,
		Name:           in.Name,
		TargetAmount:   in.TargetAmount,
		TargetCurrency: in.TargetCurrency,
		CurrentAmount:  decimal.Zero,
		Deadline:       in.Deadline,
		BabyStep:       in.BabyStep,
		Category:       in.Category,
		Source:         in.Source,
	}
	if err := s.repo.Create(ctx, goal); err != nil {
		return nil, fmt.Errorf("create goal: %w", err)
	}

	if err := s.appendEvent(ctx, goal, entities.ProgressEventCreated, nil); err != nil {
		// Log but don't fail — the goal is created; the audit row is best-effort.
		if s.logger != nil {
			s.logger.Warn("append created progress event", zap.Error(err))
		}
	}
	return goal, nil
}

// List returns goals for the user. includeArchived=false returns active only.
func (s *Service) List(ctx context.Context, userID uuid.UUID, includeArchived bool) ([]entities.UserGoal, error) {
	return s.repo.ListByUser(ctx, userID, includeArchived)
}

// ListActiveByStep returns active goals for the user on a specific Baby Step.
// Used by the goal_progress worker to evaluate step-advance and per-step
// milestone events.
func (s *Service) ListActiveByStep(ctx context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error) {
	return s.repo.ListActiveByStep(ctx, userID, step)
}

// Get returns a single goal by ID, scoped to the user.
func (s *Service) Get(ctx context.Context, userID, goalID uuid.UUID) (*entities.UserGoal, error) {
	return s.repo.GetByID(ctx, userID, goalID)
}

// UpdateProgress sets current_amount on a goal and emits a progress_updated
// event. Also detects milestone crossings (25/50/75/100) since the previous
// amount and emits milestone events for any newly crossed thresholds.
//
// Returns the goal, the new PaceReport, and the slice of milestone events
// emitted (so callers can fire notifications). PaceReport is empty when the
// goal has no deadline.
func (s *Service) UpdateProgress(ctx context.Context, userID, goalID uuid.UUID, newAmount decimal.Decimal) (*entities.UserGoal, PaceReport, []string, error) {
	if newAmount.IsNegative() {
		return nil, PaceReport{}, nil, errors.New("new amount must be non-negative")
	}
	goal, err := s.repo.GetByID(ctx, userID, goalID)
	if err != nil {
		return nil, PaceReport{}, nil, err
	}
	if !goal.IsActive() {
		return nil, PaceReport{}, nil, fmt.Errorf("goal %s is not active", goalID)
	}
	prev := goal.CurrentAmount
	prevPct := goal.PercentComplete()

	if err := s.repo.UpdateProgress(ctx, userID, goalID, newAmount); err != nil {
		return nil, PaceReport{}, nil, err
	}
	// Refresh to pick up any auto-set completed_at.
	updated, err := s.repo.GetByID(ctx, userID, goalID)
	if err != nil {
		return nil, PaceReport{}, nil, err
	}

	// Emit progress_updated audit row.
	payload, _ := json.Marshal(map[string]interface{}{
		"previous_amount": prev.StringFixed(2),
		"new_amount":      newAmount.StringFixed(2),
	})
	if err := s.appendEvent(ctx, updated, entities.ProgressEventProgressUpdated, payload); err != nil {
		if s.logger != nil {
			s.logger.Warn("append progress_updated event", zap.Error(err))
		}
	}

	// Detect milestone crossings (25/50/75/100). A milestone fires only when
	// the previous percentage was below it and the new percentage has crossed.
	newPct := updated.PercentComplete()
	milestones := []int{25, 50, 75, 100}
	var fired []string
	for _, m := range milestones {
		threshold := decimal.NewFromInt(int64(m))
		if prevPct.LessThan(threshold) && newPct.GreaterThanOrEqual(threshold) {
			kind := MilestoneKindFor(m)
			if kind == "" {
				continue
			}
			if err := s.appendEvent(ctx, updated, kind, nil); err != nil {
				if s.logger != nil {
					s.logger.Warn("append milestone event", zap.Error(err))
				}
				continue
			}
			fired = append(fired, kind)
		}
	}

	// Compute pace report (no DB calls).
	pace := ProjectPace(updated, s.now())
	return updated, pace, fired, nil
}

// Complete marks a goal as completed and emits a "completed" event. Idempotent
// — calling on an already-completed goal is a no-op.
func (s *Service) Complete(ctx context.Context, userID, goalID uuid.UUID) error {
	goal, err := s.repo.GetByID(ctx, userID, goalID)
	if err != nil {
		return err
	}
	if goal.CompletedAt != nil {
		return nil
	}
	if err := s.repo.Complete(ctx, userID, goalID); err != nil {
		return err
	}
	if err := s.appendEvent(ctx, goal, entities.ProgressEventCompleted, nil); err != nil {
		if s.logger != nil {
			s.logger.Warn("append completed event", zap.Error(err))
		}
	}
	return nil
}

// Archive soft-deletes a goal. Idempotent.
func (s *Service) Archive(ctx context.Context, userID, goalID uuid.UUID) error {
	goal, err := s.repo.GetByID(ctx, userID, goalID)
	if err != nil {
		return err
	}
	if goal.ArchivedAt != nil {
		return nil
	}
	if err := s.repo.Archive(ctx, userID, goalID); err != nil {
		return err
	}
	if err := s.appendEvent(ctx, goal, entities.ProgressEventArchived, nil); err != nil {
		if s.logger != nil {
			s.logger.Warn("append archived event", zap.Error(err))
		}
	}
	return nil
}

// ListActiveUsers returns distinct user IDs with at least one active goal.
// Used by the goal_progress worker to fan out without scanning the whole users
// table.
func (s *Service) ListActiveUsers(ctx context.Context) ([]uuid.UUID, error) {
	return s.repo.ListAllActiveUsers(ctx)
}

// HasAnyGoal returns true when the user has at least one row in user_goals.
// Used by the onboarding seed to avoid double-seeding the Baby Steps ladder.
func (s *Service) HasAnyGoal(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.repo.HasAnyGoal(ctx, userID)
}

// appendEvent writes an audit row for a goal state change. Helpers below
// compute the pct + amounts fields from the goal for events that don't carry
// their own payload.
func (s *Service) appendEvent(ctx context.Context, goal *entities.UserGoal, kind string, payload []byte) error {
	if goal == nil {
		return errors.New("append event: goal is nil")
	}
	pct := goal.PercentComplete()
	current := goal.CurrentAmount
	target := goal.TargetAmount
	if payload == nil {
		payload = []byte("{}")
	}
	event := &entities.UserGoalProgressEvent{
		UserID:        goal.UserID,
		GoalID:        goal.ID,
		Kind:          kind,
		Pct:           &pct,
		CurrentAmount: &current,
		TargetAmount:  &target,
		Payload:       payload,
		CreatedAt:     s.now(),
	}
	return s.repo.AppendProgressEvent(ctx, event)
}
