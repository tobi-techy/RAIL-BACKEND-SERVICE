package miriam

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// HealthScoreRepository persists health scores for trend tracking.
type HealthScoreRepository interface {
	SaveHealthScore(ctx context.Context, s *entities.MiriamFinancialHealthScore) error
	GetRecentScores(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamFinancialHealthScore, error)
	GetLatestScore(ctx context.Context, userID uuid.UUID) (*entities.MiriamFinancialHealthScore, error)
	DeleteScoresOlderThan(ctx context.Context, before time.Time) (int64, error)
}

// HealthScoreTracker persists and tracks financial health scores over time,
// enabling trend analysis ("improving", "stable", "declining").
type HealthScoreTracker struct {
	repo   HealthScoreRepository
	logger *zap.Logger
}

// NewHealthScoreTracker creates a health score tracker.
func NewHealthScoreTracker(repo HealthScoreRepository, logger *zap.Logger) *HealthScoreTracker {
	return &HealthScoreTracker{repo: repo, logger: logger}
}

// RecordScore persists a health score and calculates trend from previous scores.
func (t *HealthScoreTracker) RecordScore(ctx context.Context, userID uuid.UUID, overall, budget, savings, debt, runway, stability int, reasoning string, snapshot map[string]interface{}) (*entities.MiriamFinancialHealthScore, error) {
	// Get previous score for trend calculation
	previousScore := 0
	if t.repo != nil {
		previous, err := t.repo.GetLatestScore(ctx, userID)
		if err == nil && previous != nil {
			previousScore = previous.OverallScore
		}
	}

	// Calculate trend
	trend := calculateTrend(overall, previousScore)

	score := &entities.MiriamFinancialHealthScore{
		ID:             uuid.New(),
		UserID:         userID,
		OverallScore:   overall,
		BudgetScore:    budget,
		SavingsScore:   savings,
		DebtScore:      debt,
		RunwayScore:    runway,
		StabilityScore: stability,
		Trend:          trend,
		PreviousScore:  previousScore,
		Reasoning:      reasoning,
		DataSnapshot:   mustJSON(snapshot),
		CreatedAt:      time.Now().UTC(),
	}

	if t.repo != nil {
		if err := t.repo.SaveHealthScore(ctx, score); err != nil && t.logger != nil {
			t.logger.Warn("health score save failed", zap.String("user_id", userID.String()), zap.Error(err))
			return nil, err
		}
	}

	return score, nil
}

// GetHealthTrend returns the health score trend over time.
func (t *HealthScoreTracker) GetHealthTrend(ctx context.Context, userID uuid.UUID, limit int) ([]entities.MiriamFinancialHealthScore, error) {
	if t.repo == nil {
		return nil, nil
	}
	return t.repo.GetRecentScores(ctx, userID, limit)
}

// GetHealthSummary returns the latest score with trend context.
func (t *HealthScoreTracker) GetHealthSummary(ctx context.Context, userID uuid.UUID) (*HealthSummary, error) {
	if t.repo == nil {
		return nil, nil
	}
	latest, err := t.repo.GetLatestScore(ctx, userID)
	if err != nil || latest == nil {
		return nil, err
	}

	trend, err := t.repo.GetRecentScores(ctx, userID, 5)
	if err != nil {
		trend = nil
	}

	return &HealthSummary{
		Latest: latest,
		Trend:  trend,
	}, nil
}

// HealthSummary is a response containing latest score and trend history.
type HealthSummary struct {
	Latest *entities.MiriamFinancialHealthScore  `json:"latest"`
	Trend  []entities.MiriamFinancialHealthScore `json:"trend"`
}

func calculateTrend(current, previous int) string {
	if previous == 0 {
		return "stable"
	}

	diff := current - previous
	switch {
	case diff >= 10:
		return "improving"
	case diff >= 3:
		return "improving"
	case diff <= -10:
		return "declining"
	case diff <= -3:
		return "declining"
	default:
		return "stable"
	}
}

// ComputeHealthScore calculates a comprehensive health score from financial data.
func ComputeHealthScore(
	spendBalance, stashBalance, monthlyIncome, monthlySpend, monthlySavings,
	budgetLimit, upcomingObligations decimal.Decimal,
	runwayDays int,
) (overall, budget, savings, debt, runwayScore, stability int) {
	// Savings rate score (0-100)
	savingsRate := decimal.Zero
	if monthlyIncome.IsPositive() {
		savingsRate = monthlySavings.Div(monthlyIncome).Mul(decimal.NewFromInt(100))
	}
	sr := savingsRate.InexactFloat64()
	switch {
	case sr >= 30:
		savings = 100
	case sr >= 20:
		savings = 80
	case sr >= 10:
		savings = 60
	case sr >= 5:
		savings = 40
	default:
		savings = 20
	}

	// Budget control score
	budgetControl := decimal.Zero
	if budgetLimit.IsPositive() {
		budgetControl = monthlySpend.Div(budgetLimit).Mul(decimal.NewFromInt(100))
	}
	bc := budgetControl.InexactFloat64()
	switch {
	case bc <= 70:
		budget = 100
	case bc <= 85:
		budget = 80
	case bc <= 100:
		budget = 60
	default:
		budget = 30
	}

	// Runway score
	switch {
	case runwayDays >= 90:
		runwayScore = 100
	case runwayDays >= 60:
		runwayScore = 80
	case runwayDays >= 30:
		runwayScore = 60
	case runwayDays >= 14:
		runwayScore = 40
	default:
		runwayScore = 20
	}

	// Debt/obligation coverage score
	obligationRatio := decimal.Zero
	if spendBalance.IsPositive() {
		obligationRatio = upcomingObligations.Div(spendBalance).Mul(decimal.NewFromInt(100))
	}
	or := obligationRatio.InexactFloat64()
	switch {
	case or <= 30:
		debt = 100
	case or <= 50:
		debt = 80
	case or <= 75:
		debt = 60
	case or <= 100:
		debt = 40
	default:
		debt = 20
	}

	// Stability score (stash as % of monthly spend)
	stabilityRatio := decimal.Zero
	if monthlySpend.IsPositive() {
		stabilityRatio = stashBalance.Div(monthlySpend).Mul(decimal.NewFromInt(100))
	}
	st := stabilityRatio.InexactFloat64()
	switch {
	case st >= 600: // 6 months of expenses
		stability = 100
	case st >= 300: // 3 months
		stability = 80
	case st >= 100: // 1 month
		stability = 60
	case st >= 50:
		stability = 40
	default:
		stability = 20
	}

	// Overall = weighted average
	overall = int(
		float64(savings)*0.25 +
			float64(budget)*0.20 +
			float64(runwayScore)*0.25 +
			float64(debt)*0.15 +
			float64(stability)*0.15,
	)

	return
}

// CleanupOldScores removes health scores older than the retention period (default 30 days).
func (t *HealthScoreTracker) CleanupOldScores(ctx context.Context, retentionDays int) (int64, error) {
	if t.repo == nil {
		return 0, nil
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	before := time.Now().UTC().AddDate(0, 0, -retentionDays)
	return t.repo.DeleteScoresOlderThan(ctx, before)
}
