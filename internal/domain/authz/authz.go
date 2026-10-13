// Package authz is the single object-level authorization seam for the
// platform.
//
// Every read that returns another party's data passes through Guard before the
// data layer hands the row back, so the access decision lives here — not in a
// handler and not in a transport middleware (design doc §6.2 and §11.3: "every
// distributor query passes through one scope check in the data layer. The UI
// never decides access.").
//
// Callers translate ErrNotAuthorized to 404, never 403, so a refusal does not
// disclose that the subject exists.
//
// The consent scope of the tenancy phase plugs into this seam. A ConsentStore
// joins MembershipStore and Guard gains the scope check (for example
// RequireConsent) on top of RequireMembership, so every current caller keeps
// calling one Guard method and the handler stays unchanged. The kind-to-relation
// mapping it will extend lives in the data-layer implementation, one entry per
// subject.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// SubjectKind names the kind of object an actor asks to reach. It selects the
// membership relation in the data layer; it is never derived from request
// input.
type SubjectKind string

const (
	// SubjectSharedGoal is a collaborative savings goal. Membership is
	// shared_goal_members (active rows only).
	SubjectSharedGoal SubjectKind = "shared_goal"

	// SubjectHouseholdGroup is a household expense group. Membership is
	// household_members.
	SubjectHouseholdGroup SubjectKind = "household_group"
)

// ErrNotAuthorized is returned when the actor is not a member of the subject.
// Handlers must translate it to 404, never 403, so the response does not
// reveal that the subject exists.
var ErrNotAuthorized = errors.New("actor is not authorized for this subject")

// MembershipStore answers one question: is actorID a member of the subject
// identified by kind and subjectID? The implementation lives in the
// infrastructure layer and owns the kind-to-relation mapping.
type MembershipStore interface {
	IsMember(ctx context.Context, actorID uuid.UUID, kind SubjectKind, subjectID uuid.UUID) (bool, error)
}

// Authorizer is the seam every read path depends on. Depend on this, not on
// *Guard, so callers can be tested without a database.
type Authorizer interface {
	RequireMembership(ctx context.Context, actorID uuid.UUID, kind SubjectKind, subjectID uuid.UUID) error
}

// Guard is the concrete Authorizer backed by a MembershipStore.
type Guard struct {
	members MembershipStore
}

// NewGuard builds a Guard over the given store.
func NewGuard(members MembershipStore) *Guard {
	return &Guard{members: members}
}

var _ Authorizer = (*Guard)(nil)

// RequireMembership returns nil when actorID belongs to the subject, and
// ErrNotAuthorized when it does not. A nil actor is always refused, so a
// missing identity can never read a subject by omission.
func (g *Guard) RequireMembership(ctx context.Context, actorID uuid.UUID, kind SubjectKind, subjectID uuid.UUID) error {
	if actorID == uuid.Nil {
		return ErrNotAuthorized
	}
	ok, err := g.members.IsMember(ctx, actorID, kind, subjectID)
	if err != nil {
		return fmt.Errorf("membership check %s/%s: %w", kind, subjectID, err)
	}
	if !ok {
		return ErrNotAuthorized
	}
	return nil
}
