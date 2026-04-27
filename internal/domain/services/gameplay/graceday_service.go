package gameplay

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// GraceDayRepository defines data access for grace days
type GraceDayRepository interface {
	GetGraceDay(ctx context.Context, userID uuid.UUID) (*entities.GraceDay, error)
	AddGraceDays(ctx context.Context, userID uuid.UUID, count int) error
	UseGraceDay(ctx context.Context, userID uuid.UUID) error
}

// GraceDayService handles streak freeze (grace day) logic
type GraceDayService struct {
	repo     GraceDayRepository
	points   *PointsService
	notifier PushNotifier
	logger   *zap.Logger
}

func NewGraceDayService(repo GraceDayRepository, points *PointsService, notifier PushNotifier, logger *zap.Logger) *GraceDayService {
	return &GraceDayService{repo: repo, points: points, notifier: notifier, logger: logger}
}

func (s *GraceDayService) SetNotifier(n PushNotifier) { s.notifier = n }

// GetStatus returns the user's grace day inventory
func (s *GraceDayService) GetStatus(ctx context.Context, userID uuid.UUID) (*entities.GraceDay, error) {
	gd, err := s.repo.GetGraceDay(ctx, userID)
	if err != nil {
		return nil, err
	}
	if gd == nil {
		return &entities.GraceDay{UserID: userID}, nil
	}
	return gd, nil
}

// Purchase buys a grace day using rail points
func (s *GraceDayService) Purchase(ctx context.Context, userID uuid.UUID) error {
	cost := int64(entities.GraceDayPointCost)
	if err := s.points.Spend(ctx, userID, cost, entities.PointSourceGraceDayPurchase, "Purchased 1 Grace Day"); err != nil {
		return fmt.Errorf("not enough points (need %d): %w", cost, err)
	}
	if err := s.repo.AddGraceDays(ctx, userID, 1); err != nil {
		// Refund points on failure
		s.points.Earn(ctx, userID, cost, "grace_day_refund", nil, "Refund: grace day purchase failed")
		return fmt.Errorf("add grace day: %w", err)
	}
	return nil
}

// Consume uses a grace day to protect a streak from breaking.
// Called by the streak evaluator when a streak is about to break.
// Returns true if a grace day was consumed.
func (s *GraceDayService) Consume(ctx context.Context, userID uuid.UUID) (bool, error) {
	gd, err := s.repo.GetGraceDay(ctx, userID)
	if err != nil || gd == nil || gd.Remaining <= 0 {
		return false, nil
	}
	if err := s.repo.UseGraceDay(ctx, userID); err != nil {
		return false, err
	}
	if s.notifier != nil {
		s.notifier.SendToUser(ctx, userID, "Grace Day Used",
			"Your streak was about to break — a Grace Day saved it!",
			map[string]interface{}{"type": "grace_day_used"})
	}
	return true, nil
}
