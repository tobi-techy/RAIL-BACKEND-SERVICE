package proactive_reacher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func nopLogger() *zap.Logger { return zap.NewNop() }

func linkedIdentity(id, userID uuid.UUID) *entities.PlatformIdentity {
	now := time.Now()
	return &entities.PlatformIdentity{
		ID:         id,
		UserID:     userID,
		Platform:   entities.PlatformIMessage,
		LinkedAt:   &now,
		LastUsedAt: &now,
	}
}

type fakeIdentities struct {
	items []*entities.PlatformIdentity
	err   error
}

func (f *fakeIdentities) ListLinkedByPlatform(_ context.Context, _ entities.Platform) ([]*entities.PlatformIdentity, error) {
	return f.items, f.err
}

type fakeAnalyzer struct {
	outcomes map[uuid.UUID]*ai.PythonProactiveOutcome
	errs     map[uuid.UUID]error
	called   []uuid.UUID
}

func (f *fakeAnalyzer) AnalyzeProactive(_ context.Context, userID uuid.UUID, _, _ string) (*ai.PythonProactiveOutcome, error) {
	f.called = append(f.called, userID)
	if f.errs != nil {
		if err := f.errs[userID]; err != nil {
			return nil, err
		}
	}
	return f.outcomes[userID], nil
}

type fakeGuard struct {
	allow map[uuid.UUID]bool
}

func (f *fakeGuard) CanSendCategory(_ context.Context, userID uuid.UUID, _ string) bool {
	return f.allow[userID]
}

type fakeSender struct {
	sent []uuid.UUID
	err  error
}

func (f *fakeSender) SendChatMessage(_ context.Context, userID uuid.UUID, _ string) error {
	f.sent = append(f.sent, userID)
	return f.err
}

func TestRunOnce_SendsWhenGuardAllowsAndAnalystSaysYes(t *testing.T) {
	guardedUser := uuid.New()
	allowedUser := uuid.New()

	w := NewWorker(
		&fakeIdentities{items: []*entities.PlatformIdentity{
			linkedIdentity(uuid.New(), guardedUser),
			linkedIdentity(uuid.New(), allowedUser),
		}},
		&fakeAnalyzer{outcomes: map[uuid.UUID]*ai.PythonProactiveOutcome{
			guardedUser: {ShouldReachOut: true, Message: "you are short this week", Priority: "high", Category: "cashflow", Reason: "low buffer"},
			allowedUser: {ShouldReachOut: true, Message: "stash idle for 90 days", Priority: "medium", Category: "savings", Reason: "idle balance"},
		}},
		&fakeGuard{allow: map[uuid.UUID]bool{allowedUser: true}},
		&fakeSender{},
		30*time.Minute,
		nopLogger(),
	)

	w.runOnce(context.Background())

	require.Equal(t, []uuid.UUID{allowedUser}, w.analyzer.(*fakeAnalyzer).called)
	require.Equal(t, []uuid.UUID{allowedUser}, w.sender.(*fakeSender).sent)
}

func TestRunOnce_DeduplicatesUserAcrossIdentities(t *testing.T) {
	userID := uuid.New()

	w := NewWorker(
		&fakeIdentities{items: []*entities.PlatformIdentity{
			linkedIdentity(uuid.New(), userID),
			linkedIdentity(uuid.New(), userID),
		}},
		&fakeAnalyzer{outcomes: map[uuid.UUID]*ai.PythonProactiveOutcome{
			userID: {ShouldReachOut: true, Message: "one message only"},
		}},
		&fakeGuard{allow: map[uuid.UUID]bool{userID: true}},
		&fakeSender{},
		0,
		nopLogger(),
	)

	w.runOnce(context.Background())

	require.Len(t, w.analyzer.(*fakeAnalyzer).called, 1)
	require.Len(t, w.sender.(*fakeSender).sent, 1)
}

func TestRunOnce_SkipsStayQuietAndEmptyMessages(t *testing.T) {
	quietUser := uuid.New()
	emptyUser := uuid.New()

	w := NewWorker(
		&fakeIdentities{items: []*entities.PlatformIdentity{
			linkedIdentity(uuid.New(), quietUser),
			linkedIdentity(uuid.New(), emptyUser),
		}},
		&fakeAnalyzer{outcomes: map[uuid.UUID]*ai.PythonProactiveOutcome{
			quietUser: {ShouldReachOut: false, Message: "", Reason: "nothing worth interrupting for"},
			emptyUser: {ShouldReachOut: true, Message: " "},
		}},
		&fakeGuard{allow: map[uuid.UUID]bool{quietUser: true, emptyUser: true}},
		&fakeSender{},
		30*time.Minute,
		nopLogger(),
	)

	w.runOnce(context.Background())

	require.Len(t, w.sender.(*fakeSender).sent, 0)
}

func TestRunOnce_AnalyzerErrorSkipsOnlyThatUser(t *testing.T) {
	failingUser := uuid.New()
	okUser := uuid.New()

	analyzer := &fakeAnalyzer{
		outcomes: map[uuid.UUID]*ai.PythonProactiveOutcome{
			okUser: {ShouldReachOut: true, Message: "hi"},
		},
		errs: map[uuid.UUID]error{failingUser: errors.New("upstream down")},
	}

	w := NewWorker(
		&fakeIdentities{items: []*entities.PlatformIdentity{
			linkedIdentity(uuid.New(), failingUser),
			linkedIdentity(uuid.New(), okUser),
		}},
		analyzer,
		&fakeGuard{allow: map[uuid.UUID]bool{failingUser: true, okUser: true}},
		&fakeSender{},
		30*time.Minute,
		nopLogger(),
	)

	w.runOnce(context.Background())

	require.Len(t, analyzer.called, 2)
	require.Equal(t, []uuid.UUID{okUser}, w.sender.(*fakeSender).sent)
}

func TestRunOnce_ListErrorAbortsTick(t *testing.T) {
	analyzer := &fakeAnalyzer{}
	w := NewWorker(
		&fakeIdentities{err: errors.New("db down")},
		analyzer,
		&fakeGuard{},
		&fakeSender{},
		30*time.Minute,
		nopLogger(),
	)

	w.runOnce(context.Background())

	require.Len(t, analyzer.called, 0)
	require.Len(t, w.sender.(*fakeSender).sent, 0)
}
