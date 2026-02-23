package milestone

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// MilestoneRepository manages milestone records
type MilestoneRepository interface {
	Create(ctx context.Context, milestone *entities.InvestmentMilestone) error
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentMilestone, error)
	GetUncelebrated(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentMilestone, error)
	MarkCelebrated(ctx context.Context, id uuid.UUID) error
	HasAchieved(ctx context.Context, userID uuid.UUID, milestoneType entities.MilestoneType, amount decimal.Decimal) (bool, error)
}

// InvestmentRulesRepository retrieves user milestone preferences
type InvestmentRulesRepository interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentRulesConfig, error)
}

// PortfolioProvider retrieves user portfolio value
type PortfolioProvider interface {
	GetTotalValue(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

// NotificationService sends milestone notifications
type NotificationService interface {
	SendMilestoneNotification(ctx context.Context, userID uuid.UUID, milestone *entities.InvestmentMilestone) error
}

// Service handles investment milestone tracking and celebrations
type Service struct {
	milestoneRepo     MilestoneRepository
	rulesRepo         InvestmentRulesRepository
	portfolioProvider PortfolioProvider
	notifier          NotificationService
	logger            *zap.Logger
}

// NewService creates a new milestone service
func NewService(
	milestoneRepo MilestoneRepository,
	rulesRepo InvestmentRulesRepository,
	portfolioProvider PortfolioProvider,
	notifier NotificationService,
	logger *zap.Logger,
) *Service {
	return &Service{
		milestoneRepo:     milestoneRepo,
		rulesRepo:         rulesRepo,
		portfolioProvider: portfolioProvider,
		notifier:          notifier,
		logger:            logger,
	}
}

// CheckAndCelebrateMilestones checks if user has hit any new milestones
func (s *Service) CheckAndCelebrateMilestones(ctx context.Context, userID uuid.UUID) error {
	// Check if user has milestone notifications enabled
	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get investment rules",
			zap.String("user_id", userID.String()),
			zap.Error(err))
		return nil
	}

	if rules == nil || !rules.MilestoneNotifications {
		return nil
	}

	// Get current portfolio value
	portfolioValue, err := s.portfolioProvider.GetTotalValue(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get portfolio value: %w", err)
	}

	// Check balance milestones
	return s.checkBalanceMilestones(ctx, userID, portfolioValue)
}

func (s *Service) checkBalanceMilestones(ctx context.Context, userID uuid.UUID, currentValue decimal.Decimal) error {
	for _, threshold := range entities.MilestoneThresholds {
		if currentValue.GreaterThanOrEqual(threshold) {
			// Check if already achieved
			achieved, err := s.milestoneRepo.HasAchieved(ctx, userID, entities.MilestoneTypeBalance, threshold)
			if err != nil {
				s.logger.Warn("Failed to check milestone achievement",
					zap.String("user_id", userID.String()),
					zap.Error(err))
				continue
			}

			if !achieved {
				// Create and celebrate new milestone
				milestone := &entities.InvestmentMilestone{
					ID:         uuid.New(),
					UserID:     userID,
					Type:       entities.MilestoneTypeBalance,
					Amount:     threshold,
					AchievedAt: time.Now(),
					Celebrated: false,
				}

				if err := s.milestoneRepo.Create(ctx, milestone); err != nil {
					s.logger.Error("Failed to create milestone",
						zap.String("user_id", userID.String()),
						zap.Error(err))
					continue
				}

				// Send celebration notification
				if err := s.celebrateMilestone(ctx, milestone); err != nil {
					s.logger.Error("Failed to celebrate milestone",
						zap.String("milestone_id", milestone.ID.String()),
						zap.Error(err))
				}
			}
		}
	}

	return nil
}

func (s *Service) celebrateMilestone(ctx context.Context, milestone *entities.InvestmentMilestone) error {
	if s.notifier != nil {
		if err := s.notifier.SendMilestoneNotification(ctx, milestone.UserID, milestone); err != nil {
			return err
		}
	}

	// Mark as celebrated
	now := time.Now()
	milestone.Celebrated = true
	milestone.CelebratedAt = &now

	if err := s.milestoneRepo.MarkCelebrated(ctx, milestone.ID); err != nil {
		return fmt.Errorf("failed to mark milestone celebrated: %w", err)
	}

	s.logger.Info("Milestone celebrated",
		zap.String("user_id", milestone.UserID.String()),
		zap.String("type", string(milestone.Type)),
		zap.String("amount", milestone.Amount.String()))

	return nil
}

// RecordContributionMilestone records a contribution milestone
func (s *Service) RecordContributionMilestone(ctx context.Context, userID uuid.UUID, totalContributions decimal.Decimal) error {
	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil || rules == nil || !rules.MilestoneNotifications {
		return nil
	}

	for _, threshold := range entities.MilestoneThresholds {
		if totalContributions.GreaterThanOrEqual(threshold) {
			achieved, _ := s.milestoneRepo.HasAchieved(ctx, userID, entities.MilestoneTypeContribution, threshold)
			if !achieved {
				milestone := &entities.InvestmentMilestone{
					ID:         uuid.New(),
					UserID:     userID,
					Type:       entities.MilestoneTypeContribution,
					Amount:     threshold,
					AchievedAt: time.Now(),
					Celebrated: false,
				}

				if err := s.milestoneRepo.Create(ctx, milestone); err != nil {
					continue
				}

				s.celebrateMilestone(ctx, milestone)
			}
		}
	}

	return nil
}

// RecordGainMilestone records a gain milestone
func (s *Service) RecordGainMilestone(ctx context.Context, userID uuid.UUID, totalGains decimal.Decimal) error {
	if totalGains.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	rules, err := s.rulesRepo.GetByUserID(ctx, userID)
	if err != nil || rules == nil || !rules.MilestoneNotifications {
		return nil
	}

	// Gain milestones at different thresholds
	gainThresholds := []decimal.Decimal{
		decimal.NewFromInt(10),
		decimal.NewFromInt(50),
		decimal.NewFromInt(100),
		decimal.NewFromInt(500),
		decimal.NewFromInt(1000),
		decimal.NewFromInt(5000),
		decimal.NewFromInt(10000),
	}

	for _, threshold := range gainThresholds {
		if totalGains.GreaterThanOrEqual(threshold) {
			achieved, _ := s.milestoneRepo.HasAchieved(ctx, userID, entities.MilestoneTypeGain, threshold)
			if !achieved {
				milestone := &entities.InvestmentMilestone{
					ID:         uuid.New(),
					UserID:     userID,
					Type:       entities.MilestoneTypeGain,
					Amount:     threshold,
					AchievedAt: time.Now(),
					Celebrated: false,
				}

				if err := s.milestoneRepo.Create(ctx, milestone); err != nil {
					continue
				}

				s.celebrateMilestone(ctx, milestone)
			}
		}
	}

	return nil
}

// GetUserMilestones returns all milestones for a user
func (s *Service) GetUserMilestones(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentMilestone, error) {
	return s.milestoneRepo.GetByUserID(ctx, userID)
}

// GetNextMilestone returns the next milestone to achieve
func (s *Service) GetNextMilestone(ctx context.Context, userID uuid.UUID) (*MilestoneProgress, error) {
	portfolioValue, err := s.portfolioProvider.GetTotalValue(ctx, userID)
	if err != nil {
		return nil, err
	}

	nextThreshold := entities.GetNextMilestone(portfolioValue)
	if nextThreshold == nil {
		return &MilestoneProgress{
			CurrentValue:   portfolioValue,
			AllAchieved:    true,
		}, nil
	}

	progress := portfolioValue.Div(*nextThreshold).Mul(decimal.NewFromInt(100))
	remaining := nextThreshold.Sub(portfolioValue)

	return &MilestoneProgress{
		CurrentValue:   portfolioValue,
		NextMilestone:  *nextThreshold,
		ProgressPct:    progress,
		AmountRemaining: remaining,
		AllAchieved:    false,
	}, nil
}

// MilestoneProgress represents progress toward next milestone
type MilestoneProgress struct {
	CurrentValue    decimal.Decimal `json:"current_value"`
	NextMilestone   decimal.Decimal `json:"next_milestone,omitempty"`
	ProgressPct     decimal.Decimal `json:"progress_pct,omitempty"`
	AmountRemaining decimal.Decimal `json:"amount_remaining,omitempty"`
	AllAchieved     bool            `json:"all_achieved"`
}

// GetMilestoneMessages returns celebration messages for different milestones
func GetMilestoneMessage(milestoneType entities.MilestoneType, amount decimal.Decimal) (title, body string) {
	amountStr := amount.StringFixed(0)

	switch milestoneType {
	case entities.MilestoneTypeBalance:
		switch {
		case amount.Equal(decimal.NewFromInt(100)):
			return "🎉 First $100!", "You've hit your first $100 invested! This is just the beginning."
		case amount.Equal(decimal.NewFromInt(500)):
			return "🚀 $500 Milestone!", "Half a thousand dollars working for you. Keep it up!"
		case amount.Equal(decimal.NewFromInt(1000)):
			return "💰 $1,000 Club!", "Welcome to the four-figure club! Your money is growing."
		case amount.Equal(decimal.NewFromInt(5000)):
			return "⭐ $5,000 Achieved!", "Five thousand dollars invested. You're building real wealth."
		case amount.Equal(decimal.NewFromInt(10000)):
			return "🏆 $10,000 Milestone!", "Five figures! You're in the top tier of young investors."
		default:
			return fmt.Sprintf("🎯 $%s Milestone!", amountStr), fmt.Sprintf("You've reached $%s invested. Amazing progress!", amountStr)
		}

	case entities.MilestoneTypeContribution:
		return fmt.Sprintf("💪 $%s Contributed!", amountStr), fmt.Sprintf("You've contributed $%s total. Consistency wins!", amountStr)

	case entities.MilestoneTypeGain:
		return fmt.Sprintf("📈 $%s in Gains!", amountStr), fmt.Sprintf("Your investments have earned $%s. Your money is working!", amountStr)

	default:
		return "🎉 Milestone Achieved!", fmt.Sprintf("You've reached a $%s milestone!", amountStr)
	}
}
