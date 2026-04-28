package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Goal statuses
const (
	GoalActive    = "active"
	GoalCompleted = "completed"
	GoalCancelled = "cancelled"
	GoalExpired   = "expired"
)

// Member roles
const (
	MemberRoleCreator = "creator"
	MemberRoleAdmin   = "admin"
	MemberRoleMember  = "member"
)

// Member statuses
const (
	MemberInvited = "invited"
	MemberActive  = "active"
	MemberLeft    = "left"
	MemberRemoved = "removed"
)

// Contribution sources
const (
	ContribManual        = "manual"
	ContribAutomation    = "automation"
	ContribRoundup       = "roundup"
	ContribStashTransfer = "stash_transfer"
)

// SharedGoal represents a collaborative savings goal.
type SharedGoal struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	CreatorID           uuid.UUID       `json:"creator_id" db:"creator_id"`
	Name                string          `json:"name" db:"name"`
	Description         *string         `json:"description,omitempty" db:"description"`
	TargetAmount        decimal.Decimal `json:"target_amount" db:"target_amount"`
	CurrentAmount       decimal.Decimal `json:"current_amount" db:"current_amount"`
	Currency            string          `json:"currency" db:"currency"`
	Deadline            *time.Time      `json:"deadline,omitempty" db:"deadline"`
	Status              string          `json:"status" db:"status"`
	Visibility          string          `json:"visibility" db:"visibility"`
	CoverEmoji          string          `json:"icon_name" db:"icon_name"`
	CelebrationMessage  *string         `json:"celebration_message,omitempty" db:"celebration_message"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at" db:"updated_at"`

	// Joined fields
	Members       []SharedGoalMember       `json:"members,omitempty" db:"-"`
	Contributions []SharedGoalContribution `json:"recent_contributions,omitempty" db:"-"`
	MemberCount   int                      `json:"member_count,omitempty" db:"-"`
}

// ProgressPct returns the goal completion percentage.
func (g *SharedGoal) ProgressPct() float64 {
	if g.TargetAmount.IsZero() {
		return 0
	}
	pct, _ := g.CurrentAmount.Div(g.TargetAmount).Mul(decimal.NewFromInt(100)).Float64()
	if pct > 100 {
		return 100
	}
	return pct
}

// SharedGoalMember represents a participant in a shared goal.
type SharedGoalMember struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	GoalID              uuid.UUID       `json:"goal_id" db:"goal_id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	Role                string          `json:"role" db:"role"`
	TargetContribution  *decimal.Decimal `json:"target_contribution,omitempty" db:"target_contribution"`
	TotalContributed    decimal.Decimal `json:"total_contributed" db:"total_contributed"`
	Status              string          `json:"status" db:"status"`
	InvitedBy           *uuid.UUID      `json:"invited_by,omitempty" db:"invited_by"`
	JoinedAt            *time.Time      `json:"joined_at,omitempty" db:"joined_at"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`

	// Joined
	RailTag  string `json:"rail_tag,omitempty" db:"-"`
	Username string `json:"username,omitempty" db:"-"`
}

// SharedGoalContribution records a single contribution.
type SharedGoalContribution struct {
	ID        uuid.UUID       `json:"id" db:"id"`
	GoalID    uuid.UUID       `json:"goal_id" db:"goal_id"`
	UserID    uuid.UUID       `json:"user_id" db:"user_id"`
	Amount    decimal.Decimal `json:"amount" db:"amount"`
	Note      *string         `json:"note,omitempty" db:"note"`
	Source    string          `json:"source" db:"source"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`

	// Joined
	RailTag string `json:"rail_tag,omitempty" db:"-"`
}

// SharedGoalInvite represents a pending invite.
type SharedGoalInvite struct {
	ID            uuid.UUID  `json:"id" db:"id"`
	GoalID        uuid.UUID  `json:"goal_id" db:"goal_id"`
	InviterID     uuid.UUID  `json:"inviter_id" db:"inviter_id"`
	RailTag       string     `json:"rail_tag" db:"rail_tag"`
	InviteeUserID *uuid.UUID `json:"invitee_user_id,omitempty" db:"invitee_user_id"`
	Status        string     `json:"status" db:"status"`
	Message       *string    `json:"message,omitempty" db:"message"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	RespondedAt   *time.Time `json:"responded_at,omitempty" db:"responded_at"`

	// Joined
	GoalName    string `json:"goal_name,omitempty" db:"-"`
	InviterTag  string `json:"inviter_tag,omitempty" db:"-"`
}
