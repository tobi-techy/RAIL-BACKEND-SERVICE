package goals

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo is an in-memory implementation of repositories.UserGoalRepository
// for service-level tests. It captures every write so assertions can verify
// the audit-trail shape.
type fakeRepo struct {
	mu        sync.Mutex
	goals     map[uuid.UUID]*entities.UserGoal
	events    []entities.UserGoalProgressEvent
	createErr error
	updateErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{goals: map[uuid.UUID]*entities.UserGoal{}}
}

func (r *fakeRepo) Create(_ context.Context, g *entities.UserGoal) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	if g.Source == "" {
		g.Source = entities.GoalSourceManual
	}
	if g.Category == "" {
		g.Category = entities.GoalCategoryFreeform
	}
	g.CreatedAt = time.Now().UTC()
	cp := *g
	r.goals[g.ID] = &cp
	return nil
}

func (r *fakeRepo) GetByID(_ context.Context, userID, goalID uuid.UUID) (*entities.UserGoal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || g.UserID != userID {
		return nil, errors.New("not found")
	}
	cp := *g
	return &cp, nil
}

func (r *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID, includeArchived bool) ([]entities.UserGoal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]entities.UserGoal, 0)
	for _, g := range r.goals {
		if g.UserID != userID {
			continue
		}
		if !includeArchived && g.ArchivedAt != nil {
			continue
		}
		if !includeArchived && g.CompletedAt != nil {
			continue
		}
		out = append(out, *g)
	}
	return out, nil
}

func (r *fakeRepo) ListActiveByStep(_ context.Context, userID uuid.UUID, step int) ([]entities.UserGoal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]entities.UserGoal, 0)
	for _, g := range r.goals {
		if g.UserID != userID {
			continue
		}
		if g.BabyStep == nil || *g.BabyStep != step {
			continue
		}
		if g.CompletedAt != nil || g.ArchivedAt != nil {
			continue
		}
		out = append(out, *g)
	}
	return out, nil
}

func (r *fakeRepo) UpdateProgress(_ context.Context, userID, goalID uuid.UUID, amt decimal.Decimal) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || g.UserID != userID {
		return errors.New("not found")
	}
	g.CurrentAmount = amt
	if amt.GreaterThanOrEqual(g.TargetAmount) && g.CompletedAt == nil {
		now := time.Now().UTC()
		g.CompletedAt = &now
	}
	return nil
}

func (r *fakeRepo) Complete(_ context.Context, userID, goalID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || g.UserID != userID {
		return errors.New("not found")
	}
	if g.CompletedAt == nil {
		now := time.Now().UTC()
		g.CompletedAt = &now
	}
	return nil
}

func (r *fakeRepo) Archive(_ context.Context, userID, goalID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.goals[goalID]
	if !ok || g.UserID != userID {
		return errors.New("not found")
	}
	if g.ArchivedAt == nil {
		now := time.Now().UTC()
		g.ArchivedAt = &now
	}
	return nil
}

func (r *fakeRepo) AppendProgressEvent(_ context.Context, e *entities.UserGoalProgressEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	r.events = append(r.events, *e)
	return nil
}

func (r *fakeRepo) HasAnyGoal(_ context.Context, userID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.goals {
		if g.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) ListAllActiveUsers(_ context.Context) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, g := range r.goals {
		if g.CompletedAt != nil || g.ArchivedAt != nil {
			continue
		}
		if !seen[g.UserID] {
			seen[g.UserID] = true
			out = append(out, g.UserID)
		}
	}
	return out, nil
}

// verify interface compliance at compile time.
var _ (interface {
	Create(context.Context, *entities.UserGoal) error
	GetByID(context.Context, uuid.UUID, uuid.UUID) (*entities.UserGoal, error)
	ListByUser(context.Context, uuid.UUID, bool) ([]entities.UserGoal, error)
	ListActiveByStep(context.Context, uuid.UUID, int) ([]entities.UserGoal, error)
	UpdateProgress(context.Context, uuid.UUID, uuid.UUID, decimal.Decimal) error
	Complete(context.Context, uuid.UUID, uuid.UUID) error
	Archive(context.Context, uuid.UUID, uuid.UUID) error
	AppendProgressEvent(context.Context, *entities.UserGoalProgressEvent) error
	HasAnyGoal(context.Context, uuid.UUID) (bool, error)
	ListAllActiveUsers(context.Context) ([]uuid.UUID, error)
}) = (*fakeRepo)(nil)

// --- tests ---

func TestService_Create_Validates(t *testing.T) {
	s := NewService(newFakeRepo(), nil)
	ctx := context.Background()
	uid := uuid.New()

	// Missing name.
	_, err := s.Create(ctx, uid, CreateInput{
		TargetAmount: decimal.NewFromInt(100),
	})
	require.Error(t, err)

	// Zero target.
	_, err = s.Create(ctx, uid, CreateInput{
		Name:         "test",
		TargetAmount: decimal.Zero,
	})
	require.Error(t, err)

	// Bad baby step.
	bad := 8
	_, err = s.Create(ctx, uid, CreateInput{
		Name:         "test",
		TargetAmount: decimal.NewFromInt(100),
		BabyStep:     &bad,
	})
	require.Error(t, err)

	// Valid.
	g, err := s.Create(ctx, uid, CreateInput{
		Name:         "starter",
		TargetAmount: decimal.NewFromInt(1000),
	})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Equal(t, entities.GoalCategoryFreeform, g.Category)
	assert.Equal(t, entities.GoalSourceManual, g.Source)
}

func TestService_UpdateProgress_FiresMilestones(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	g, err := s.Create(ctx, uid, CreateInput{
		Name:         "starter",
		TargetAmount: decimal.NewFromInt(1000),
	})
	require.NoError(t, err)

	// First bump — no milestone crossed (0 → 24%).
	updated, _, fired, err := s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(240))
	require.NoError(t, err)
	assert.Empty(t, fired, "no milestones should fire below 25%")

	// 24 → 26 — should fire milestone_25.
	_, _, fired, err = s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(260))
	require.NoError(t, err)
	assert.Equal(t, []string{entities.ProgressEventMilestone25}, fired)

	// 26 → 50 — should fire milestone_50.
	_, _, fired, err = s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(500))
	require.NoError(t, err)
	assert.Equal(t, []string{entities.ProgressEventMilestone50}, fired)

	// 50 → 100 — should fire milestone_75 AND milestone_100 (both crossed).
	updated, _, fired, err = s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(1000))
	require.NoError(t, err)
	assert.Equal(t, []string{entities.ProgressEventMilestone75, entities.ProgressEventMilestone100}, fired)
	assert.NotNil(t, updated.CompletedAt, "goal should be auto-completed at 100%")
}

func TestService_UpdateProgress_RejectsNegative(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	g, err := s.Create(ctx, uid, CreateInput{Name: "x", TargetAmount: decimal.NewFromInt(1000)})
	require.NoError(t, err)

	_, _, _, err = s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(-1))
	require.Error(t, err)
}

func TestService_Archive_Idempotent(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	g, err := s.Create(ctx, uid, CreateInput{Name: "x", TargetAmount: decimal.NewFromInt(1000)})
	require.NoError(t, err)

	require.NoError(t, s.Archive(ctx, uid, g.ID))
	require.NoError(t, s.Archive(ctx, uid, g.ID)) // second call is a no-op

	got, err := repo.GetByID(ctx, uid, g.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ArchivedAt)
}

func TestService_AppendEvent_ShapeIsPreserved(t *testing.T) {
	repo := newFakeRepo()
	s := NewService(repo, nil)
	ctx := context.Background()
	uid := uuid.New()

	g, err := s.Create(ctx, uid, CreateInput{
		Name:         "x",
		TargetAmount: decimal.NewFromInt(1000),
	})
	require.NoError(t, err)

	// Manually fire one to inspect the payload shape.
	updated, _, _, err := s.UpdateProgress(ctx, uid, g.ID, decimal.NewFromInt(500))
	require.NoError(t, err)

	// Inspect the most recent progress_updated event.
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.NotEmpty(t, repo.events)

	var found *entities.UserGoalProgressEvent
	for i := range repo.events {
		if repo.events[i].Kind == entities.ProgressEventProgressUpdated {
			found = &repo.events[i]
			break
		}
	}
	require.NotNil(t, found, "expected progress_updated event in audit log")
	assert.Equal(t, g.ID, found.GoalID)
	assert.Equal(t, uid, found.UserID)
	require.NotNil(t, found.CurrentAmount)
	assert.True(t, found.CurrentAmount.Equal(updated.CurrentAmount))
	// payload should be parseable JSON containing previous + new amount.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(found.Payload, &parsed))
	assert.Contains(t, parsed, "previous_amount")
	assert.Contains(t, parsed, "new_amount")
}
