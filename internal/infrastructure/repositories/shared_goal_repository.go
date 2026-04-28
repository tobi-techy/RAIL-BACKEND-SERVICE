package repositories

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

type SharedGoalRepository struct {
	db *sqlx.DB
}

func NewSharedGoalRepository(db *sqlx.DB) *SharedGoalRepository {
	return &SharedGoalRepository{db: db}
}

func (r *SharedGoalRepository) Create(ctx context.Context, g *entities.SharedGoal) error {
	query := `INSERT INTO shared_goals (id, creator_id, name, description, target_amount, currency, deadline, status, visibility, icon_name, celebration_message, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())`
	_, err := r.db.ExecContext(ctx, query, g.ID, g.CreatorID, g.Name, g.Description, g.TargetAmount, g.Currency, g.Deadline, g.Status, g.Visibility, g.CoverEmoji, g.CelebrationMessage)
	return err
}

func (r *SharedGoalRepository) GetByID(ctx context.Context, goalID uuid.UUID) (*entities.SharedGoal, error) {
	var g entities.SharedGoal
	err := r.db.GetContext(ctx, &g, `SELECT * FROM shared_goals WHERE id = $1`, goalID)
	if err != nil {
		return nil, err
	}
	var members []entities.SharedGoalMember
	_ = r.db.SelectContext(ctx, &members, `SELECT * FROM shared_goal_members WHERE goal_id = $1 AND status = 'active' ORDER BY total_contributed DESC`, goalID)
	g.Members = members
	g.MemberCount = len(members)
	return &g, nil
}

func (r *SharedGoalRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]entities.SharedGoal, error) {
	var goals []entities.SharedGoal
	query := `SELECT g.* FROM shared_goals g
		JOIN shared_goal_members m ON m.goal_id = g.id
		WHERE m.user_id = $1 AND m.status = 'active' AND g.status IN ('active', 'completed')
		ORDER BY g.created_at DESC`
	err := r.db.SelectContext(ctx, &goals, query, userID)
	return goals, err
}

func (r *SharedGoalRepository) AddMember(ctx context.Context, m *entities.SharedGoalMember) error {
	query := `INSERT INTO shared_goal_members (id, goal_id, user_id, role, target_contribution, status, invited_by, joined_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (goal_id, user_id) DO UPDATE SET status = $6, joined_at = $8`
	_, err := r.db.ExecContext(ctx, query, m.ID, m.GoalID, m.UserID, m.Role, m.TargetContribution, m.Status, m.InvitedBy, m.JoinedAt)
	return err
}

func (r *SharedGoalRepository) Contribute(ctx context.Context, c *entities.SharedGoalContribution) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Insert contribution
	_, err = tx.ExecContext(ctx, `INSERT INTO shared_goal_contributions (id, goal_id, user_id, amount, note, source, created_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		c.ID, c.GoalID, c.UserID, c.Amount, c.Note, c.Source)
	if err != nil {
		return err
	}

	// Update goal current_amount
	_, err = tx.ExecContext(ctx, `UPDATE shared_goals SET current_amount = current_amount + $1, updated_at = NOW() WHERE id = $2`, c.Amount, c.GoalID)
	if err != nil {
		return err
	}

	// Update member total_contributed
	_, err = tx.ExecContext(ctx, `UPDATE shared_goal_members SET total_contributed = total_contributed + $1 WHERE goal_id = $2 AND user_id = $3`, c.Amount, c.GoalID, c.UserID)
	if err != nil {
		return err
	}

	// Check if goal is now completed
	var goal entities.SharedGoal
	if err := tx.GetContext(ctx, &goal, `SELECT * FROM shared_goals WHERE id = $1`, c.GoalID); err == nil {
		if goal.CurrentAmount.GreaterThanOrEqual(goal.TargetAmount) && goal.Status == entities.GoalActive {
			tx.ExecContext(ctx, `UPDATE shared_goals SET status = 'completed', updated_at = NOW() WHERE id = $1`, c.GoalID)
		}
	}

	return tx.Commit()
}

func (r *SharedGoalRepository) GetContributions(ctx context.Context, goalID uuid.UUID, limit int) ([]entities.SharedGoalContribution, error) {
	var contribs []entities.SharedGoalContribution
	err := r.db.SelectContext(ctx, &contribs, `SELECT * FROM shared_goal_contributions WHERE goal_id = $1 ORDER BY created_at DESC LIMIT $2`, goalID, limit)
	return contribs, err
}

func (r *SharedGoalRepository) CreateInvite(ctx context.Context, inv *entities.SharedGoalInvite) error {
	query := `INSERT INTO shared_goal_invites (id, goal_id, inviter_id, rail_tag, invitee_user_id, status, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())`
	_, err := r.db.ExecContext(ctx, query, inv.ID, inv.GoalID, inv.InviterID, inv.RailTag, inv.InviteeUserID, inv.Status, inv.Message)
	return err
}

func (r *SharedGoalRepository) GetPendingInvites(ctx context.Context, userID uuid.UUID) ([]entities.SharedGoalInvite, error) {
	var invites []entities.SharedGoalInvite
	err := r.db.SelectContext(ctx, &invites, `SELECT i.*, g.name as goal_name FROM shared_goal_invites i JOIN shared_goals g ON g.id = i.goal_id WHERE i.invitee_user_id = $1 AND i.status = 'pending'`, userID)
	return invites, err
}

func (r *SharedGoalRepository) RespondToInvite(ctx context.Context, inviteID uuid.UUID, status string) error {
	now := time.Now()
	_, err := r.db.ExecContext(ctx, `UPDATE shared_goal_invites SET status = $1, responded_at = $2 WHERE id = $3`, status, now, inviteID)
	return err
}

func (r *SharedGoalRepository) UpdateGoal(ctx context.Context, g *entities.SharedGoal) error {
	_, err := r.db.ExecContext(ctx, `UPDATE shared_goals SET name=$1, description=$2, target_amount=$3, deadline=$4, visibility=$5, icon_name=$6, celebration_message=$7, updated_at=NOW() WHERE id=$8`,
		g.Name, g.Description, g.TargetAmount, g.Deadline, g.Visibility, g.CoverEmoji, g.CelebrationMessage, g.ID)
	return err
}

func (r *SharedGoalRepository) GetLeaderboard(ctx context.Context, goalID uuid.UUID) ([]entities.SharedGoalMember, error) {
	var members []entities.SharedGoalMember
	err := r.db.SelectContext(ctx, &members, `SELECT m.*, u.rail_tag FROM shared_goal_members m LEFT JOIN users u ON u.id = m.user_id WHERE m.goal_id = $1 AND m.status = 'active' ORDER BY m.total_contributed DESC`, goalID)
	return members, err
}

func (r *SharedGoalRepository) GetMember(ctx context.Context, goalID, userID uuid.UUID) (*entities.SharedGoalMember, error) {
	var m entities.SharedGoalMember
	err := r.db.GetContext(ctx, &m, `SELECT * FROM shared_goal_members WHERE goal_id = $1 AND user_id = $2`, goalID, userID)
	return &m, err
}

func (r *SharedGoalRepository) RemoveMember(ctx context.Context, goalID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE shared_goal_members SET status = 'left' WHERE goal_id = $1 AND user_id = $2`, goalID, userID)
	return err
}

// GetGoalStats returns aggregate stats for a goal.
func (r *SharedGoalRepository) GetGoalStats(ctx context.Context, goalID uuid.UUID) (memberCount int, totalContributed decimal.Decimal, err error) {
	err = r.db.GetContext(ctx, &memberCount, `SELECT COUNT(*) FROM shared_goal_members WHERE goal_id = $1 AND status = 'active'`, goalID)
	if err != nil {
		return
	}
	err = r.db.GetContext(ctx, &totalContributed, `SELECT COALESCE(SUM(amount), 0) FROM shared_goal_contributions WHERE goal_id = $1`, goalID)
	return
}
