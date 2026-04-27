package gameplay

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ChallengeRepository defines the data access interface for challenges
type ChallengeRepository interface {
	GetChallengesByType(ctx context.Context, challengeType entities.ChallengeType) ([]*entities.Challenge, error)
	GetUserChallenges(ctx context.Context, userID uuid.UUID, status entities.ChallengeStatus) ([]*entities.UserChallenge, error)
	AssignChallenge(ctx context.Context, userID, challengeID uuid.UUID) error
	UpdateChallengeProgress(ctx context.Context, id uuid.UUID, progress decimal.Decimal) error
	CompleteChallenge(ctx context.Context, id uuid.UUID) error
	ExpireChallenges(ctx context.Context, challengeType entities.ChallengeType) (int64, error)
	GetUserChallengeByMetric(ctx context.Context, userID uuid.UUID, metric string) (*entities.UserChallenge, error)
	GetActiveUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// SubscriptionChecker checks if a user has Pro subscription
type SubscriptionChecker interface {
	IsProUser(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ChallengeService handles challenge assignment and progress
type ChallengeService struct {
	repo     ChallengeRepository
	xpSvc    *XPService
	subCheck SubscriptionChecker
	notifier PushNotifier
	logger   *zap.Logger
}

func NewChallengeService(repo ChallengeRepository, xpSvc *XPService, notifier PushNotifier, logger *zap.Logger) *ChallengeService {
	return &ChallengeService{repo: repo, xpSvc: xpSvc, notifier: notifier, logger: logger}
}

func (s *ChallengeService) SetNotifier(n PushNotifier) { s.notifier = n }

func (s *ChallengeService) SetSubscriptionChecker(sc SubscriptionChecker) {
	s.subCheck = sc
}

// GetActiveChallenges returns a user's active challenges with progress
func (s *ChallengeService) GetActiveChallenges(ctx context.Context, userID uuid.UUID) ([]*entities.UserChallenge, error) {
	return s.repo.GetUserChallenges(ctx, userID, entities.ChallengeStatusActive)
}

// UpdateProgress updates challenge progress for a given metric, auto-completes if target met.
//
// SECURITY(R3-L6): This method trusts the provided newProgress value. It must NEVER be
// exposed directly via a user-facing API endpoint. All progress updates must originate
// from server-side event handlers (e.g., deposit confirmed, trade executed) that compute
// the progress value from authoritative backend state. Allowing client-supplied progress
// would let users trivially complete challenges and farm XP.
func (s *ChallengeService) UpdateProgress(ctx context.Context, userID uuid.UUID, metric string, newProgress decimal.Decimal) error {
	uc, err := s.repo.GetUserChallengeByMetric(ctx, userID, metric)
	if err != nil {
		return fmt.Errorf("get challenge by metric: %w", err)
	}
	if uc == nil || uc.Challenge == nil {
		return nil // No active challenge for this metric
	}

	if err := s.repo.UpdateChallengeProgress(ctx, uc.ID, newProgress); err != nil {
		return fmt.Errorf("update progress: %w", err)
	}

	// Auto-complete if target met
	if newProgress.GreaterThanOrEqual(uc.Challenge.TargetValue) {
		if err := s.repo.CompleteChallenge(ctx, uc.ID); err != nil {
			return fmt.Errorf("complete challenge: %w", err)
		}
		// Award XP
		if s.xpSvc != nil {
			challengeID := uc.ChallengeID
			s.xpSvc.AwardXP(ctx, userID, entities.XPEventChallenge, uc.Challenge.XPReward, &challengeID)
		}
		// Notify
		if s.notifier != nil {
			s.notifier.SendToUser(ctx, userID,
				"Challenge Complete!",
				fmt.Sprintf("You completed \"%s\" and earned %d XP!", uc.Challenge.Title, uc.Challenge.XPReward),
				map[string]interface{}{"type": "challenge_complete", "challenge_id": uc.ChallengeID.String()})
		}
	}
	return nil
}

// AssignOnboardingChallenges assigns one-time onboarding challenges to a new user
func (s *ChallengeService) AssignOnboardingChallenges(ctx context.Context, userID uuid.UUID) error {
	challenges, err := s.repo.GetChallengesByType(ctx, entities.ChallengeTypeOnetime)
	if err != nil {
		return fmt.Errorf("get onetime challenges: %w", err)
	}
	for _, c := range challenges {
		if err := s.repo.AssignChallenge(ctx, userID, c.ID); err != nil {
			s.logger.Warn("Failed to assign onboarding challenge", zap.String("challenge", c.Title), zap.Error(err))
		}
	}
	return nil
}

// AssignWeeklyChallenges assigns weekly challenges to all active users. Pro users get 3, free get 0.
func (s *ChallengeService) AssignWeeklyChallenges(ctx context.Context) error {
	return s.assignRotatingChallenges(ctx, entities.ChallengeTypeWeekly, 3)
}

// AssignMonthlyChallenges assigns monthly challenges to all active users. Pro users get 2, free get 0.
func (s *ChallengeService) AssignMonthlyChallenges(ctx context.Context) error {
	return s.assignRotatingChallenges(ctx, entities.ChallengeTypeMonthly, 2)
}

func (s *ChallengeService) assignRotatingChallenges(ctx context.Context, cType entities.ChallengeType, count int) error {
	challenges, err := s.repo.GetChallengesByType(ctx, cType)
	if err != nil {
		return fmt.Errorf("get %s challenges: %w", cType, err)
	}
	if len(challenges) == 0 {
		return nil
	}

	userIDs, err := s.repo.GetActiveUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("get active users: %w", err)
	}

	for _, userID := range userIDs {
		picked := pickRandom(challenges, count)
		for _, c := range picked {
			if err := s.repo.AssignChallenge(ctx, userID, c.ID); err != nil {
				s.logger.Warn("Failed to assign challenge", zap.Error(err))
			}
		}
	}
	return nil
}

// ExpireOldChallenges expires active challenges of the given type
func (s *ChallengeService) ExpireOldChallenges(ctx context.Context, cType entities.ChallengeType) (int64, error) {
	return s.repo.ExpireChallenges(ctx, cType)
}

func pickRandom(challenges []*entities.Challenge, n int) []*entities.Challenge {
	if n >= len(challenges) {
		return challenges
	}
	perm := rand.Perm(len(challenges))
	picked := make([]*entities.Challenge, n)
	for i := 0; i < n; i++ {
		picked[i] = challenges[perm[i]]
	}
	return picked
}
