package gameplay

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AchievementRepository defines the data access interface for achievements
type AchievementRepository interface {
	GetAllAchievements(ctx context.Context) ([]*entities.Achievement, error)
	GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.UserAchievement, error)
	HasAchievement(ctx context.Context, userID, achievementID uuid.UUID) (bool, error)
	UnlockAchievement(ctx context.Context, userID, achievementID uuid.UUID) error
}

// UserStatsProvider provides user stats for achievement evaluation
type UserStatsProvider interface {
	GetDepositCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetTotalBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetRoundupCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetAccountAgeDays(ctx context.Context, userID uuid.UUID) (int, error)
	GetReferralCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetDaysSinceLastStashWithdrawal(ctx context.Context, userID uuid.UUID) (int, error)
	GetUserRank(ctx context.Context, userID uuid.UUID) (int, error) // for early adopter check
}

// AchievementService handles achievement evaluation and unlocking
type AchievementService struct {
	repo      AchievementRepository
	streaks   *StreakService
	rings     *RingsService
	points    *PointsService
	boosts    *BoostService
	gracedays *GraceDayService
	stats     UserStatsProvider
	notifier  PushNotifier
	logger    *zap.Logger
}

func NewAchievementService(repo AchievementRepository, streaks *StreakService, notifier PushNotifier, logger *zap.Logger) *AchievementService {
	return &AchievementService{repo: repo, streaks: streaks, notifier: notifier, logger: logger}
}

func (s *AchievementService) SetNotifier(n PushNotifier) { s.notifier = n }

func (s *AchievementService) SetUserStatsProvider(stats UserStatsProvider) {
	s.stats = stats
}

// SetRingsService wires the rings service for achievement evaluation
func (s *AchievementService) SetRingsService(r *RingsService) { s.rings = r }

// SetPointsService wires the points service for achievement evaluation
func (s *AchievementService) SetPointsService(p *PointsService) { s.points = p }

// SetBoostService wires the boost service for achievement evaluation
func (s *AchievementService) SetBoostService(b *BoostService) { s.boosts = b }

// SetGraceDayService wires the grace day service for achievement evaluation
func (s *AchievementService) SetGraceDayService(g *GraceDayService) { s.gracedays = g }

// CheckAndUnlock evaluates all unearned achievements for a user
func (s *AchievementService) CheckAndUnlock(ctx context.Context, userID uuid.UUID) (int, error) {
	if s.stats == nil {
		return 0, nil
	}

	all, err := s.repo.GetAllAchievements(ctx)
	if err != nil {
		return 0, fmt.Errorf("get achievements: %w", err)
	}

	unlocked := 0
	for _, a := range all {
		has, err := s.repo.HasAchievement(ctx, userID, a.ID)
		if err != nil || has {
			continue
		}
		if s.evaluateCondition(ctx, userID, a) {
			if err := s.UnlockAchievement(ctx, userID, a); err != nil {
				s.logger.Warn("Failed to unlock achievement", zap.String("name", a.Name), zap.Error(err))
				continue
			}
			unlocked++
		}
	}
	return unlocked, nil
}

func (s *AchievementService) evaluateCondition(ctx context.Context, userID uuid.UUID, a *entities.Achievement) bool {
	target := a.ConditionValue.IntPart()

	switch a.ConditionType {
	case "first_deposit":
		count, err := s.stats.GetDepositCount(ctx, userID)
		return err == nil && count >= int(target)

	case "stash_balance":
		bal, err := s.stats.GetStashBalance(ctx, userID)
		return err == nil && bal.GreaterThanOrEqual(a.ConditionValue)

	case "total_balance":
		bal, err := s.stats.GetTotalBalance(ctx, userID)
		return err == nil && bal.GreaterThanOrEqual(a.ConditionValue)

	case "deposit_streak":
		streaks, err := s.streaks.GetUserStreaks(ctx, userID)
		if err != nil {
			return false
		}
		for _, st := range streaks {
			if st.StreakType == entities.StreakTypeDeposit && st.LongestCount >= int(target) {
				return true
			}
		}
		return false

	case "roundup_count":
		count, err := s.stats.GetRoundupCount(ctx, userID)
		return err == nil && count >= int(target)

	case "no_stash_withdrawal_days":
		days, err := s.stats.GetDaysSinceLastStashWithdrawal(ctx, userID)
		return err == nil && days >= int(target)

	case "account_age_days":
		days, err := s.stats.GetAccountAgeDays(ctx, userID)
		return err == nil && days >= int(target)

	case "referral_count":
		count, err := s.stats.GetReferralCount(ctx, userID)
		return err == nil && count >= int(target)

	case "early_adopter":
		rank, err := s.stats.GetUserRank(ctx, userID)
		return err == nil && rank > 0 && rank <= int(target)

	// --- V2 achievement conditions ---
	case "spend_control_days":
		// Count days where spend ring was closed (stayed within budget)
		if s.rings == nil {
			return false
		}
		count, err := s.rings.CountSpendClosedSince(ctx, userID, time.Now().AddDate(0, 0, -int(target)))
		return err == nil && count >= int(target)

	case "stash_balance_ngn":
		bal, err := s.stats.GetStashBalance(ctx, userID)
		return err == nil && bal.GreaterThanOrEqual(a.ConditionValue)

	case "no_panic_withdrawal_days":
		streaks, err := s.streaks.GetUserStreaks(ctx, userID)
		if err != nil {
			return false
		}
		for _, st := range streaks {
			if st.StreakType == entities.StreakTypeNoPanicWithdrawal && st.CurrentCount >= int(target) {
				return true
			}
		}
		return false

	case "salary_split_count":
		// First salary deposit handled — check deposit count >= 1 (simplified)
		count, err := s.stats.GetDepositCount(ctx, userID)
		return err == nil && count >= int(target)

	case "comeback_count":
		// User had a broken streak and rebuilt it — check if any streak was broken then restarted
		streaks, err := s.streaks.GetUserStreaks(ctx, userID)
		if err != nil {
			return false
		}
		for _, st := range streaks {
			if st.BrokenAt != nil && st.CurrentCount > 0 {
				return true
			}
		}
		return false

	case "rings_all_closed_days":
		if s.rings == nil {
			return false
		}
		count, err := s.rings.CountAllClosedSince(ctx, userID, time.Now().AddDate(0, -1, 0))
		return err == nil && count >= int(target)

	case "lifetime_points":
		if s.points == nil {
			return false
		}
		rp, err := s.points.GetBalance(ctx, userID)
		return err == nil && rp != nil && rp.LifetimeEarned >= target

	case "boosts_completed":
		if s.boosts == nil {
			return false
		}
		count, err := s.boosts.CountCompleted(ctx, userID, time.Time{})
		return err == nil && count >= int(target)

	case "grace_days_used":
		if s.gracedays == nil {
			return false
		}
		gd, err := s.gracedays.GetStatus(ctx, userID)
		return err == nil && gd != nil && gd.UsedTotal >= int(target)
	}
	return false
}

// UnlockAchievement records the unlock and sends a notification
func (s *AchievementService) UnlockAchievement(ctx context.Context, userID uuid.UUID, a *entities.Achievement) error {
	if err := s.repo.UnlockAchievement(ctx, userID, a.ID); err != nil {
		return err
	}
	if s.notifier != nil {
		s.notifier.SendToUser(ctx, userID,
			fmt.Sprintf("%s Unlocked!", a.Name),
			a.Description,
			map[string]interface{}{"type": "achievement_unlocked", "achievement": a.Name, "rarity": string(a.Rarity)})
	}
	return nil
}

// GetUserAchievements returns all achievements with locked/unlocked status
func (s *AchievementService) GetUserAchievements(ctx context.Context, userID uuid.UUID) ([]*entities.Achievement, []*entities.UserAchievement, error) {
	all, err := s.repo.GetAllAchievements(ctx)
	if err != nil {
		return nil, nil, err
	}
	earned, err := s.repo.GetUserAchievements(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return all, earned, nil
}
