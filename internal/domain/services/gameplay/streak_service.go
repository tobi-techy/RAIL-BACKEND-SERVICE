package gameplay

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// StreakRepository defines the data access interface for streaks
type StreakRepository interface {
	UpsertStreak(ctx context.Context, s *entities.UserStreak) error
	GetStreaksByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error)
	GetStreak(ctx context.Context, userID uuid.UUID, streakType entities.StreakType) (*entities.UserStreak, error)
	ResetStreak(ctx context.Context, id uuid.UUID) error
	GetExpiredStreaks(ctx context.Context) ([]*entities.UserStreak, error)
	GetNearBreakingStreaks(ctx context.Context) ([]*entities.UserStreak, error)
}

// StreakService handles streak tracking
type StreakService struct {
	repo   StreakRepository
	logger *zap.Logger
}

func NewStreakService(repo StreakRepository, logger *zap.Logger) *StreakService {
	return &StreakService{repo: repo, logger: logger}
}

// RecordActivity increments or starts a streak for the given type
func (s *StreakService) RecordActivity(ctx context.Context, userID uuid.UUID, streakType entities.StreakType) error {
	existing, err := s.repo.GetStreak(ctx, userID, streakType)
	if err != nil {
		return fmt.Errorf("get streak: %w", err)
	}

	now := time.Now()

	if existing == nil {
		// Start new streak
		streak := &entities.UserStreak{
			ID:             uuid.New(),
			UserID:         userID,
			StreakType:     streakType,
			CurrentCount:   1,
			LongestCount:   1,
			LastActivityAt: &now,
			StartedAt:      &now,
		}
		return s.repo.UpsertStreak(ctx, streak)
	}

	// Check if activity is on a new day (avoid double-counting same day)
	if existing.LastActivityAt != nil && sameDay(*existing.LastActivityAt, now) {
		return nil // Already recorded today
	}

	// Check if streak was broken (exceeded reset window)
	resetDays := entities.StreakResetDays[streakType]
	if existing.LastActivityAt != nil && now.Sub(*existing.LastActivityAt) > time.Duration(resetDays)*24*time.Hour {
		// Streak was broken, start fresh
		existing.CurrentCount = 1
		existing.StartedAt = &now
	} else {
		existing.CurrentCount++
	}

	existing.LastActivityAt = &now
	if existing.CurrentCount > existing.LongestCount {
		existing.LongestCount = existing.CurrentCount
	}

	return s.repo.UpsertStreak(ctx, existing)
}

// GetUserStreaks returns all streaks for a user
func (s *StreakService) GetUserStreaks(ctx context.Context, userID uuid.UUID) ([]*entities.UserStreak, error) {
	return s.repo.GetStreaksByUserID(ctx, userID)
}

// ResetStreakByID resets a specific streak by ID
func (s *StreakService) ResetStreakByID(ctx context.Context, id uuid.UUID) error {
	return s.repo.ResetStreak(ctx, id)
}

// CheckAndResetStreaks finds and resets all broken streaks. Returns count of reset streaks.
func (s *StreakService) CheckAndResetStreaks(ctx context.Context) (int, error) {
	expired, err := s.repo.GetExpiredStreaks(ctx)
	if err != nil {
		return 0, fmt.Errorf("get expired streaks: %w", err)
	}
	for _, streak := range expired {
		if err := s.repo.ResetStreak(ctx, streak.ID); err != nil {
			s.logger.Warn("Failed to reset streak", zap.String("id", streak.ID.String()), zap.Error(err))
		}
	}
	return len(expired), nil
}

// GetNearBreakingStreaks returns streaks that are about to expire (for reminder notifications)
func (s *StreakService) GetNearBreakingStreaks(ctx context.Context) ([]*entities.UserStreak, error) {
	return s.repo.GetNearBreakingStreaks(ctx)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}
