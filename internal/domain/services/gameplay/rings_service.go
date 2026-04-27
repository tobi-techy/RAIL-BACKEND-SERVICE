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

// RingsRepository defines data access for daily rings
type RingsRepository interface {
	UpsertDailyRing(ctx context.Context, ring *entities.DailyRing) error
	GetDailyRing(ctx context.Context, userID uuid.UUID, date time.Time) (*entities.DailyRing, error)
	GetRingsForWeek(ctx context.Context, userID uuid.UUID, weekStart time.Time) ([]*entities.DailyRing, error)
	CountAllClosedRings(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
	CountSpendClosedRings(ctx context.Context, userID uuid.UUID, since time.Time) (int, error)
}

// SpendDataProvider provides daily spend/save/grow data for ring calculation
type SpendDataProvider interface {
	GetDailySpend(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error)
	GetDailySave(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error)
	GetDailyGrow(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error)
}

// Default daily ring targets (can be personalized later)
var (
	DefaultSaveTarget = decimal.NewFromFloat(1.0)
	DefaultGrowTarget = decimal.NewFromFloat(0.01) // any yield/growth counts
)

// RingsService handles daily ring progress
type RingsService struct {
	repo   RingsRepository
	logger *zap.Logger
}

func NewRingsService(repo RingsRepository, logger *zap.Logger) *RingsService {
	return &RingsService{repo: repo, logger: logger}
}

// GetTodayRings returns today's ring progress for a user
func (s *RingsService) GetTodayRings(ctx context.Context, userID uuid.UUID) (*entities.DailyRing, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	ring, err := s.repo.GetDailyRing(ctx, userID, today)
	if err != nil {
		return nil, fmt.Errorf("get daily ring: %w", err)
	}
	if ring == nil {
		// Return empty ring for today
		return &entities.DailyRing{
			UserID:   userID,
			RingDate: today,
		}, nil
	}
	return ring, nil
}

// GetWeekRings returns ring data for the current week (Monday-Sunday)
func (s *RingsService) GetWeekRings(ctx context.Context, userID uuid.UUID) ([]*entities.DailyRing, error) {
	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	// Calculate Monday of this week
	offset := int(now.Weekday()) - int(time.Monday)
	if offset < 0 {
		offset += 7 // Sunday: weekday=0, Monday=1, so offset=-1 → 6
	}
	weekStart := today.AddDate(0, 0, -offset)
	return s.repo.GetRingsForWeek(ctx, userID, weekStart)
}

// EvaluateAndClose calculates ring progress for a date and closes completed rings.
// Called by the daily_metrics worker at end of day.
func (s *RingsService) EvaluateAndClose(ctx context.Context, userID uuid.UUID, date time.Time, dailySpend, dailySave, dailyGrow decimal.Decimal, spendLimit decimal.Decimal) error {
	dateOnly := date.UTC().Truncate(24 * time.Hour)

	// Spend ring: closed if spent <= limit (inverted — spending less is good)
	spendTarget := spendLimit
	if spendTarget.IsZero() {
		spendTarget = decimal.NewFromInt(50) // fallback
	}
	spendClosed := dailySpend.LessThanOrEqual(spendTarget)

	// Save ring: closed if any amount was saved/set aside
	saveClosed := dailySave.GreaterThan(decimal.Zero)

	// Grow ring: closed if any yield/growth occurred
	growClosed := dailyGrow.GreaterThan(decimal.Zero)

	allClosed := spendClosed && saveClosed && growClosed

	ring := &entities.DailyRing{
		ID:          uuid.New(),
		UserID:      userID,
		RingDate:    dateOnly,
		SpendTarget: spendTarget,
		SpendActual: dailySpend,
		SaveTarget:  DefaultSaveTarget,
		SaveActual:  dailySave,
		GrowTarget:  DefaultGrowTarget,
		GrowActual:  dailyGrow,
		SpendClosed: spendClosed,
		SaveClosed:  saveClosed,
		GrowClosed:  growClosed,
		AllClosed:   allClosed,
	}

	return s.repo.UpsertDailyRing(ctx, ring)
}

// CountAllClosedSince returns how many days all 3 rings were closed since a date
func (s *RingsService) CountAllClosedSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	return s.repo.CountAllClosedRings(ctx, userID, since)
}

// CountSpendClosedSince returns how many days the spend ring was closed since a date
func (s *RingsService) CountSpendClosedSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	return s.repo.CountSpendClosedRings(ctx, userID, since)
}
