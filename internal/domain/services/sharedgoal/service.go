package sharedgoal

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// UserLookup resolves rail tags to user IDs.
type UserLookup interface {
	GetUserIDByRailTag(ctx context.Context, tag string) (uuid.UUID, error)
}

// Service manages collaborative goals.
type Service struct {
	repo   *repositories.SharedGoalRepository
	users  UserLookup
	logger *zap.Logger
}

func NewService(repo *repositories.SharedGoalRepository, users UserLookup, logger *zap.Logger) *Service {
	return &Service{repo: repo, users: users, logger: logger}
}

// Create creates a new shared goal and adds the creator as a member.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, req *CreateGoalRequest) (*entities.SharedGoal, error) {
	target, err := decimal.NewFromString(req.TargetAmount)
	if err != nil || !target.IsPositive() {
		return nil, fmt.Errorf("invalid target amount")
	}

	goal := &entities.SharedGoal{
		ID:           uuid.New(),
		CreatorID:    userID,
		Name:         req.Name,
		Description:  req.Description,
		TargetAmount: target,
		Currency:     coalesceStr(req.Currency, "USD"),
		Status:       entities.GoalActive,
		Visibility:   coalesceStr(req.Visibility, "members"),
		CoverEmoji:   coalesceStr(req.CoverEmoji, "target-02"),
	}
	if req.Deadline != nil {
		t, _ := time.Parse("2006-01-02", *req.Deadline)
		if !t.IsZero() {
			goal.Deadline = &t
		}
	}

	if err := s.repo.Create(ctx, goal); err != nil {
		return nil, fmt.Errorf("create goal: %w", err)
	}

	// Add creator as member
	now := time.Now()
	member := &entities.SharedGoalMember{
		ID:       uuid.New(),
		GoalID:   goal.ID,
		UserID:   userID,
		Role:     entities.MemberRoleCreator,
		Status:   entities.MemberActive,
		JoinedAt: &now,
	}
	if err := s.repo.AddMember(ctx, member); err != nil {
		return nil, err
	}

	goal.Members = []entities.SharedGoalMember{*member}
	goal.MemberCount = 1
	return goal, nil
}

// Get returns a goal with members.
func (s *Service) Get(ctx context.Context, goalID uuid.UUID) (*entities.SharedGoal, error) {
	return s.repo.GetByID(ctx, goalID)
}

// List returns all goals for a user.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]entities.SharedGoal, error) {
	return s.repo.ListByUser(ctx, userID)
}

// Contribute adds money to a goal.
func (s *Service) Contribute(ctx context.Context, userID, goalID uuid.UUID, req *ContributeRequest) (*entities.SharedGoalContribution, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() {
		return nil, fmt.Errorf("invalid amount")
	}

	// Verify user is a member
	_, err = s.repo.GetMember(ctx, goalID, userID)
	if err != nil {
		return nil, fmt.Errorf("not a member of this goal")
	}

	contrib := &entities.SharedGoalContribution{
		ID:     uuid.New(),
		GoalID: goalID,
		UserID: userID,
		Amount: amount,
		Note:   req.Note,
		Source: coalesceStr(req.Source, entities.ContribManual),
	}

	if err := s.repo.Contribute(ctx, contrib); err != nil {
		return nil, fmt.Errorf("contribute: %w", err)
	}
	return contrib, nil
}

// InviteMembers sends invites to rail tags.
func (s *Service) InviteMembers(ctx context.Context, userID, goalID uuid.UUID, tags []string, message *string) ([]entities.SharedGoalInvite, error) {
	var invites []entities.SharedGoalInvite
	for _, tag := range tags {
		inviteeID, _ := s.users.GetUserIDByRailTag(ctx, tag)

		inv := &entities.SharedGoalInvite{
			ID:        uuid.New(),
			GoalID:    goalID,
			InviterID: userID,
			RailTag:   tag,
			Status:    "pending",
			Message:   message,
		}
		if inviteeID != uuid.Nil {
			inv.InviteeUserID = &inviteeID
		}

		if err := s.repo.CreateInvite(ctx, inv); err != nil {
			s.logger.Warn("failed to create invite", zap.String("tag", tag), zap.Error(err))
			continue
		}
		invites = append(invites, *inv)
	}
	return invites, nil
}

// RespondToInvite accepts or declines an invite.
func (s *Service) RespondToInvite(ctx context.Context, userID, inviteID uuid.UUID, accept bool) error {
	status := "declined"
	if accept {
		status = "accepted"
	}
	if err := s.repo.RespondToInvite(ctx, inviteID, status); err != nil {
		return err
	}

	if accept {
		// Look up the invite to get goal ID
		invites, _ := s.repo.GetPendingInvites(ctx, userID)
		for _, inv := range invites {
			if inv.ID == inviteID {
				now := time.Now()
				member := &entities.SharedGoalMember{
					ID:        uuid.New(),
					GoalID:    inv.GoalID,
					UserID:    userID,
					Role:      entities.MemberRoleMember,
					Status:    entities.MemberActive,
					InvitedBy: &inv.InviterID,
					JoinedAt:  &now,
				}
				return s.repo.AddMember(ctx, member)
			}
		}
	}
	return nil
}

// GetPendingInvites returns invites for a user.
func (s *Service) GetPendingInvites(ctx context.Context, userID uuid.UUID) ([]entities.SharedGoalInvite, error) {
	return s.repo.GetPendingInvites(ctx, userID)
}

// GetContributions returns recent contributions for a goal.
func (s *Service) GetContributions(ctx context.Context, goalID uuid.UUID, limit int) ([]entities.SharedGoalContribution, error) {
	return s.repo.GetContributions(ctx, goalID, limit)
}

// GetLeaderboard returns members ranked by contribution.
func (s *Service) GetLeaderboard(ctx context.Context, goalID uuid.UUID) ([]entities.SharedGoalMember, error) {
	return s.repo.GetLeaderboard(ctx, goalID)
}

// Leave removes a user from a goal.
func (s *Service) Leave(ctx context.Context, userID, goalID uuid.UUID) error {
	return s.repo.RemoveMember(ctx, goalID, userID)
}

// --- Request types ---

type CreateGoalRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description *string `json:"description"`
	TargetAmount string `json:"target_amount" binding:"required"`
	Currency    string  `json:"currency"`
	Deadline    *string `json:"deadline"`
	Visibility  string  `json:"visibility"`
	CoverEmoji  string  `json:"icon_name"`
}

type ContributeRequest struct {
	Amount string  `json:"amount" binding:"required"`
	Note   *string `json:"note"`
	Source string  `json:"source"`
}

func coalesceStr(val, def string) string {
	if val != "" {
		return val
	}
	return def
}
