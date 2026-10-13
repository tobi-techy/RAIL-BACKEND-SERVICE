package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/rail-service/rail_service/internal/domain/authz"
)

// AuthzRepository is the SQL implementation of authz.MembershipStore. The
// subject-kind-to-relation mapping lives here and nowhere else, so adding a
// subject kind is one entry in membershipQueries rather than another inline
// EXISTS query in a handler.
type AuthzRepository struct {
	db *sqlx.DB
}

// NewAuthzRepository builds the store over the given database handle.
func NewAuthzRepository(db *sqlx.DB) *AuthzRepository {
	return &AuthzRepository{db: db}
}

// membershipQueries map a subject kind to the one query that answers "is this
// user a member?". Each query is a fixed string taking ($1 = subject id,
// $2 = user id), so nothing about it is derived from request input.
//
// The shared-goal query requires an active membership: a member who left or was
// removed must not read the goal.
var membershipQueries = map[authz.SubjectKind]string{
	authz.SubjectSharedGoal: `SELECT EXISTS(
		SELECT 1 FROM shared_goal_members
		WHERE goal_id = $1 AND user_id = $2 AND status = 'active')`,
	authz.SubjectHouseholdGroup: `SELECT EXISTS(
		SELECT 1 FROM household_members
		WHERE group_id = $1 AND user_id = $2)`,
}

var _ authz.MembershipStore = (*AuthzRepository)(nil)

// IsMember reports whether actorID belongs to the subject.
func (r *AuthzRepository) IsMember(ctx context.Context, actorID uuid.UUID, kind authz.SubjectKind, subjectID uuid.UUID) (bool, error) {
	query, ok := membershipQueries[kind]
	if !ok {
		return false, fmt.Errorf("unknown subject kind %q", kind)
	}

	var isMember bool
	if err := r.db.GetContext(ctx, &isMember, query, subjectID, actorID); err != nil {
		return false, err
	}
	return isMember, nil
}
