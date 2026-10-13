package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeStore struct {
	isMember bool
	err      error
}

func (f fakeStore) IsMember(ctx context.Context, actorID uuid.UUID, kind SubjectKind, subjectID uuid.UUID) (bool, error) {
	return f.isMember, f.err
}

func TestGuardRequireMembership(t *testing.T) {
	actor := uuid.New()
	subject := uuid.New()
	ctx := context.Background()

	t.Run("member is allowed", func(t *testing.T) {
		g := NewGuard(fakeStore{isMember: true})
		if err := g.RequireMembership(ctx, actor, SubjectSharedGoal, subject); err != nil {
			t.Fatalf("member refused: %v", err)
		}
	})

	t.Run("non-member is refused with ErrNotAuthorized", func(t *testing.T) {
		g := NewGuard(fakeStore{isMember: false})
		err := g.RequireMembership(ctx, actor, SubjectSharedGoal, subject)
		if !errors.Is(err, ErrNotAuthorized) {
			t.Fatalf("want ErrNotAuthorized, got %v", err)
		}
	})

	t.Run("nil actor is refused without touching the store", func(t *testing.T) {
		g := NewGuard(fakeStore{isMember: true})
		err := g.RequireMembership(ctx, uuid.Nil, SubjectHouseholdGroup, subject)
		if !errors.Is(err, ErrNotAuthorized) {
			t.Fatalf("want ErrNotAuthorized for nil actor, got %v", err)
		}
	})

	t.Run("store error is surfaced, not treated as a refusal", func(t *testing.T) {
		boom := errors.New("db down")
		g := NewGuard(fakeStore{err: boom})
		err := g.RequireMembership(ctx, actor, SubjectSharedGoal, subject)
		if errors.Is(err, ErrNotAuthorized) {
			t.Fatal("a store failure must not be reported as a plain refusal")
		}
		if !errors.Is(err, boom) {
			t.Fatalf("want wrapped store error, got %v", err)
		}
	})
}
