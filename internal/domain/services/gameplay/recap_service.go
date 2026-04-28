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

// RecapRepository defines data access for weekly recaps
type RecapRepository interface {
	CreateWeeklyRecap(ctx context.Context, recap *entities.WeeklyRecap) error
	GetLatestWeeklyRecap(ctx context.Context, userID uuid.UUID) (*entities.WeeklyRecap, error)
	GetWeeklyRecaps(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.WeeklyRecap, error)
}

// RecapDataProvider provides aggregated data for recap generation
type RecapDataProvider interface {
	GetWeeklyDeposits(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, error)
	GetWeeklySpend(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, error)
	GetWeeklySaved(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, error)
	GetWeeklyGrown(ctx context.Context, userID uuid.UUID, start, end time.Time) (decimal.Decimal, error)
}

// RecapService generates NRC-style weekly coaching summaries
type RecapService struct {
	repo   RecapRepository
	rings  *RingsService
	points *PointsService
	logger *zap.Logger
}

func NewRecapService(repo RecapRepository, rings *RingsService, points *PointsService, logger *zap.Logger) *RecapService {
	return &RecapService{repo: repo, rings: rings, points: points, logger: logger}
}

// GetLatest returns the most recent weekly recap
func (s *RecapService) GetLatest(ctx context.Context, userID uuid.UUID) (*entities.WeeklyRecap, error) {
	return s.repo.GetLatestWeeklyRecap(ctx, userID)
}

// GetHistory returns recent weekly recaps
func (s *RecapService) GetHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.WeeklyRecap, error) {
	if limit <= 0 || limit > 52 {
		limit = 12
	}
	return s.repo.GetWeeklyRecaps(ctx, userID, limit)
}

// Generate creates a weekly recap for the given week. Called by the weekly recap worker.
func (s *RecapService) Generate(ctx context.Context, userID uuid.UUID, weekStart time.Time,
	deposited, spent, saved, grown decimal.Decimal, prevSpent decimal.Decimal,
	streakDays, badgesEarned int) error {

	weekEnd := weekStart.AddDate(0, 0, 6)

	// Spend comparison
	var spendPct decimal.Decimal
	if !prevSpent.IsZero() {
		spendPct = spent.Sub(prevSpent).Div(prevSpent).Mul(decimal.NewFromInt(100))
	}

	// Count rings closed this week
	ringsClosed, _ := s.rings.CountAllClosedSince(ctx, userID, weekStart)

	// Points earned this week
	pointsEarned, _ := s.points.EarnedSince(ctx, userID, weekStart)

	// Generate coaching message
	msg := s.generateCoachingMessage(spent, prevSpent, saved, ringsClosed, streakDays)

	recap := &entities.WeeklyRecap{
		ID:                 uuid.New(),
		UserID:             userID,
		WeekStart:          weekStart,
		WeekEnd:            weekEnd,
		TotalDeposited:     deposited,
		TotalSpent:         spent,
		TotalSaved:         saved,
		TotalGrown:         grown,
		SpendVsLastWeekPct: spendPct,
		RingsClosed:        ringsClosed,
		StreakDays:          streakDays,
		PointsEarned:       pointsEarned,
		BadgesEarned:       badgesEarned,
		CoachingMessage:    msg,
	}

	return s.repo.CreateWeeklyRecap(ctx, recap)
}

func (s *RecapService) generateCoachingMessage(spent, prevSpent, saved decimal.Decimal, ringsClosed, streakDays int) string {
	if prevSpent.IsZero() {
		if saved.GreaterThan(decimal.Zero) {
			return fmt.Sprintf("Your first week on Rail! You saved %s and closed %d rings. Keep building.", saved.StringFixed(2), ringsClosed)
		}
		return "Welcome to Rail. Your money is now working for you. Let's build momentum."
	}

	diff := spent.Sub(prevSpent)
	if diff.IsNegative() {
		pct := diff.Abs().Div(prevSpent).Mul(decimal.NewFromInt(100))
		msg := fmt.Sprintf("This was your strongest week yet. You spent %s%% less than last week", pct.StringFixed(0))
		if saved.GreaterThan(decimal.Zero) {
			msg += fmt.Sprintf(" and set aside %s automatically.", saved.StringFixed(2))
		} else {
			msg += "."
		}
		return msg
	}

	if ringsClosed >= 5 {
		return fmt.Sprintf("Solid week — %d rings closed and a %d-day streak. You're building real momentum.", ringsClosed, streakDays)
	}

	if streakDays > 0 {
		return fmt.Sprintf("You stayed on Rail for %d days this week. Every day counts.", streakDays)
	}

	return "A new week starts now. Small moves, big results. Stay on Rail."
}
