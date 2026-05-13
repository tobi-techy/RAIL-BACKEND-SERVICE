package gameplay

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// XP award amounts
const (
	XPFirstDeposit = 100
	XPDepositMin   = 10
	XPDepositMax   = 50
	XPStreakDay    = 5
	XPRoundup      = 2
	XPMilestoneMin = 50
	XPMilestoneMax = 200
	XPReferral     = 200
	XPFirstCardTx  = 50
)

// XPRepository defines the data access interface for XP
type XPRepository interface {
	GetUserXP(ctx context.Context, userID uuid.UUID) (*entities.UserXP, error)
	AwardXP(ctx context.Context, userID uuid.UUID, amount int, newLevel int) error
	CreateXPEvent(ctx context.Context, e *entities.XPEvent) error
	GetXPHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.XPEvent, error)
	XPEventExists(ctx context.Context, userID uuid.UUID, eventType string, sourceID uuid.UUID) (bool, error)
}

// PushNotifier sends push notifications
type PushNotifier interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// XPService handles XP calculation and leveling
type XPService struct {
	repo     XPRepository
	notifier PushNotifier
	logger   *zap.Logger
}

func NewXPService(repo XPRepository, notifier PushNotifier, logger *zap.Logger) *XPService {
	return &XPService{repo: repo, notifier: notifier, logger: logger}
}

// SetNotifier sets the push notifier (called after DI wiring resolves push provider)
func (s *XPService) SetNotifier(n PushNotifier) { s.notifier = n }

// AwardXP adds XP to a user and checks for level-up
func (s *XPService) AwardXP(ctx context.Context, userID uuid.UUID, eventType string, amount int, sourceID *uuid.UUID) error {
	if amount <= 0 {
		return nil
	}

	// Idempotency: skip if this exact event was already awarded
	if sourceID != nil {
		exists, err := s.repo.XPEventExists(ctx, userID, eventType, *sourceID)
		if err != nil {
			return fmt.Errorf("check xp event exists: %w", err)
		}
		if exists {
			return nil
		}
	}

	// Get current XP to calculate new level
	current, err := s.repo.GetUserXP(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user xp: %w", err)
	}

	oldLevel := 1
	var newTotalXP int64
	if current != nil {
		oldLevel = current.CurrentLevel
		newTotalXP = current.TotalXP + int64(amount)
	} else {
		newTotalXP = int64(amount)
	}

	newLevel, newTitle := entities.LevelForXP(newTotalXP)

	// Atomically award XP (uses INSERT ON CONFLICT UPDATE with total_xp + amount)
	if err := s.repo.AwardXP(ctx, userID, amount, newLevel); err != nil {
		return fmt.Errorf("award xp: %w", err)
	}

	// Record event
	desc := fmt.Sprintf("+%d XP: %s", amount, eventType)
	event := &entities.XPEvent{
		ID:          uuid.New(),
		UserID:      userID,
		EventType:   eventType,
		XPAmount:    amount,
		SourceID:    sourceID,
		Description: desc,
	}
	if err := s.repo.CreateXPEvent(ctx, event); err != nil {
		s.logger.Warn("Failed to create XP event", zap.Error(err))
	}

	// Notify on level-up
	if newLevel > oldLevel && s.notifier != nil {
		title := fmt.Sprintf("Level %d: %s", newLevel, newTitle)
		body := fmt.Sprintf("Miriam saw the level-up. Level %d looks good on you.", newLevel)
		if err := s.notifier.SendToUser(ctx, userID, title, body, map[string]interface{}{
			"type":  "level_up",
			"level": newLevel,
		}); err != nil {
			s.logger.Warn("Failed to send level-up notification", zap.Error(err))
		}
	}

	return nil
}

// GetUserXP returns the user's current XP and level info
func (s *XPService) GetUserXP(ctx context.Context, userID uuid.UUID) (*entities.UserXP, error) {
	xp, err := s.repo.GetUserXP(ctx, userID)
	if err != nil {
		return nil, err
	}
	if xp == nil {
		return &entities.UserXP{UserID: userID, TotalXP: 0, CurrentLevel: 1}, nil
	}
	return xp, nil
}

// GetXPHistory returns recent XP events
func (s *XPService) GetXPHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.XPEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.GetXPHistory(ctx, userID, limit, offset)
}
