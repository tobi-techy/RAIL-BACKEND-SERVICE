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

// BoostRepository defines data access for boosts
type BoostRepository interface {
	GetAvailableBoosts(ctx context.Context) ([]*entities.Boost, error)
	GetBoostByID(ctx context.Context, id uuid.UUID) (*entities.Boost, error)
	GetActiveUserBoost(ctx context.Context, userID uuid.UUID) (*entities.UserBoost, error)
	CreateUserBoost(ctx context.Context, ub *entities.UserBoost) error
	UpdateUserBoost(ctx context.Context, ub *entities.UserBoost) error
	ExpireBoosts(ctx context.Context) (int64, error)
	GetUserBoostHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.UserBoost, error)
	CountCompletedBoosts(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
}

// BoostService handles boost activation and evaluation
type BoostService struct {
	repo     BoostRepository
	notifier PushNotifier
	logger   *zap.Logger
}

func NewBoostService(repo BoostRepository, notifier PushNotifier, logger *zap.Logger) *BoostService {
	return &BoostService{repo: repo, notifier: notifier, logger: logger}
}

func (s *BoostService) SetNotifier(n PushNotifier) { s.notifier = n }

// GetAvailableBoosts returns all active boost templates
func (s *BoostService) GetAvailableBoosts(ctx context.Context) ([]*entities.Boost, error) {
	return s.repo.GetAvailableBoosts(ctx)
}

// GetActiveBoost returns the user's currently active boost (only one at a time)
func (s *BoostService) GetActiveBoost(ctx context.Context, userID uuid.UUID) (*entities.UserBoost, error) {
	return s.repo.GetActiveUserBoost(ctx, userID)
}

// ActivateBoost activates a boost for a user. Only one active at a time.
func (s *BoostService) ActivateBoost(ctx context.Context, userID, boostID uuid.UUID) (*entities.UserBoost, error) {
	// Check no active boost
	active, err := s.repo.GetActiveUserBoost(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check active boost: %w", err)
	}
	if active != nil {
		boostName := "unknown"
		if active.Boost != nil {
			boostName = active.Boost.Name
		}
		return nil, fmt.Errorf("already have an active boost: %s", boostName)
	}

	boost, err := s.repo.GetBoostByID(ctx, boostID)
	if err != nil || boost == nil {
		return nil, fmt.Errorf("boost not found")
	}
	if !boost.IsActive {
		return nil, fmt.Errorf("boost is not available")
	}

	now := time.Now()
	ub := &entities.UserBoost{
		ID:          uuid.New(),
		UserID:      userID,
		BoostID:     boostID,
		Status:      entities.BoostStatusActive,
		ActivatedAt: now,
		ExpiresAt:   now.AddDate(0, 0, boost.DurationDays),
		Progress:    decimal.Zero,
		RewardEarned: decimal.Zero,
	}
	ub.Boost = boost

	if err := s.repo.CreateUserBoost(ctx, ub); err != nil {
		return nil, fmt.Errorf("create user boost: %w", err)
	}
	return ub, nil
}

// EvaluateTransaction checks if a card transaction contributes to the active boost.
// Returns (completedBoostID, error) — non-nil UUID means boost completed, caller should trigger OnBoostComplete.
func (s *BoostService) EvaluateTransaction(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, category string) (*uuid.UUID, error) {
	ub, err := s.repo.GetActiveUserBoost(ctx, userID)
	if err != nil || ub == nil || ub.Boost == nil {
		return nil, nil
	}

	boost := ub.Boost
	matched := false

	switch boost.ConditionType {
	case "category_spend":
		if boost.Category != nil && *boost.Category == category {
			matched = true
			ub.Progress = ub.Progress.Add(amount)
		}
	case "category_count":
		if boost.Category != nil && *boost.Category == category {
			matched = true
			ub.Progress = ub.Progress.Add(decimal.NewFromInt(1))
		}
	case "card_tx_count":
		matched = true
		ub.Progress = ub.Progress.Add(decimal.NewFromInt(1))
	case "any_spend":
		matched = true
		ub.Progress = ub.Progress.Add(amount)
	}

	if !matched {
		return nil, nil
	}

	var completedID *uuid.UUID
	// Check if boost condition is met
	if boost.ConditionValue.GreaterThan(decimal.Zero) && ub.Progress.GreaterThanOrEqual(boost.ConditionValue) {
		ub.Status = entities.BoostStatusCompleted
		ub.RewardEarned = boost.RewardValue
		id := ub.BoostID
		completedID = &id
		if s.notifier != nil {
			s.notifier.SendToUser(ctx, userID, "Boost Complete!",
				fmt.Sprintf("You completed \"%s\" and earned your reward!", boost.Name),
				map[string]interface{}{"type": "boost_complete", "boost": boost.Name})
		}
	}

	return completedID, s.repo.UpdateUserBoost(ctx, ub)
}

// ExpireOldBoosts expires all boosts past their expiry date
func (s *BoostService) ExpireOldBoosts(ctx context.Context) (int64, error) {
	return s.repo.ExpireBoosts(ctx)
}

// GetHistory returns a user's boost history
func (s *BoostService) GetHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.UserBoost, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.repo.GetUserBoostHistory(ctx, userID, limit)
}

// CountCompleted returns how many boosts a user completed since a date
func (s *BoostService) CountCompleted(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	return s.repo.CountCompletedBoosts(ctx, userID, since)
}
