package gameplay

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// PointsRepository defines data access for rail points
type PointsRepository interface {
	GetRailPoints(ctx context.Context, userID uuid.UUID) (*entities.RailPoints, error)
	EarnPoints(ctx context.Context, userID uuid.UUID, amount int64) error
	SpendPoints(ctx context.Context, userID uuid.UUID, amount int64) error
	CreatePointEvent(ctx context.Context, e *entities.RailPointEvent) error
	GetPointHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.RailPointEvent, error)
	SumPointsEarnedSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error)
	PointEventExistsBySource(ctx context.Context, userID uuid.UUID, source string, sourceID uuid.UUID) (bool, error)
}

// PointsService handles rail point earning and spending
type PointsService struct {
	repo   PointsRepository
	logger *zap.Logger
}

func NewPointsService(repo PointsRepository, logger *zap.Logger) *PointsService {
	return &PointsService{repo: repo, logger: logger}
}

// GetBalance returns the user's current point balance
func (s *PointsService) GetBalance(ctx context.Context, userID uuid.UUID) (*entities.RailPoints, error) {
	rp, err := s.repo.GetRailPoints(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rp == nil {
		return &entities.RailPoints{UserID: userID}, nil
	}
	return rp, nil
}

// Earn adds points and records the event. Idempotent when sourceID is provided.
func (s *PointsService) Earn(ctx context.Context, userID uuid.UUID, amount int64, source string, sourceID *uuid.UUID, desc string) error {
	if amount <= 0 {
		return nil
	}
	// Idempotency: skip if this exact event was already recorded
	if sourceID != nil {
		exists, err := s.repo.PointEventExistsBySource(ctx, userID, source, *sourceID)
		if err != nil {
			return fmt.Errorf("check point event exists: %w", err)
		}
		if exists {
			return nil
		}
	}
	if err := s.repo.EarnPoints(ctx, userID, amount); err != nil {
		return fmt.Errorf("earn points: %w", err)
	}
	event := &entities.RailPointEvent{
		ID:          uuid.New(),
		UserID:      userID,
		EventType:   entities.PointEventEarn,
		Amount:      amount,
		Source:      source,
		SourceID:    sourceID,
		Description: desc,
	}
	if err := s.repo.CreatePointEvent(ctx, event); err != nil {
		s.logger.Warn("Failed to create point event", zap.Error(err))
	}
	return nil
}

// Spend deducts points and records the event
func (s *PointsService) Spend(ctx context.Context, userID uuid.UUID, amount int64, source string, desc string) error {
	if amount <= 0 {
		return nil
	}
	if err := s.repo.SpendPoints(ctx, userID, amount); err != nil {
		return err
	}
	event := &entities.RailPointEvent{
		ID:          uuid.New(),
		UserID:      userID,
		EventType:   entities.PointEventSpend,
		Amount:      amount,
		Source:      source,
		Description: desc,
	}
	if err := s.repo.CreatePointEvent(ctx, event); err != nil {
		s.logger.Warn("Failed to create point spend event", zap.Error(err))
	}
	return nil
}

// GetHistory returns paginated point events
func (s *PointsService) GetHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.RailPointEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.GetPointHistory(ctx, userID, limit, offset)
}

// EarnedSince returns total points earned since a date
func (s *PointsService) EarnedSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error) {
	return s.repo.SumPointsEarnedSince(ctx, userID, since)
}
