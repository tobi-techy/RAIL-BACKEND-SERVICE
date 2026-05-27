package miriam

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakePredictionRepo struct {
	predictions []*entities.MiriamPrediction
}

func (f *fakePredictionRepo) UpsertPrediction(_ context.Context, p *entities.MiriamPrediction) error {
	f.predictions = append(f.predictions, p)
	return nil
}

func (f *fakePredictionRepo) ListActivePredictions(_ context.Context, _ uuid.UUID) ([]entities.MiriamPrediction, error) {
	out := make([]entities.MiriamPrediction, len(f.predictions))
	for i, p := range f.predictions {
		out[i] = *p
	}
	return out, nil
}

func (f *fakePredictionRepo) ExpireOldPredictions(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

type fakeSpendingProvider struct{}

func (fakeSpendingProvider) GetMoneyFlow(_ context.Context, _ uuid.UUID, _, _ time.Time) (*entities.MoneyFlowSummary, error) {
	return &entities.MoneyFlowSummary{}, nil
}

type fakeObligationProvider struct {
	obligations []entities.FinancialObligation
}

func (f fakeObligationProvider) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	return f.obligations, nil
}

type fakeBalanceProvider struct {
	spend decimal.Decimal
	stash decimal.Decimal
}

func (f fakeBalanceProvider) GetAccountBalance(_ context.Context, _ uuid.UUID, at entities.AccountType) (decimal.Decimal, error) {
	switch at {
	case entities.AccountTypeSpendingBalance:
		return f.spend, nil
	case entities.AccountTypeStashBalance:
		return f.stash, nil
	default:
		return decimal.Zero, nil
	}
}

type fakeProfileProvider struct {
	income  decimal.Decimal
	cadence string
}

func (f fakeProfileProvider) GetByUserID(_ context.Context, _ uuid.UUID) (*entities.FinancialProfile, error) {
	return &entities.FinancialProfile{
		MonthlyIncome:   f.income,
		IncomeFrequency: f.cadence,
	}, nil
}

func TestPredictiveEngine_CashShortfall(t *testing.T) {
	repo := &fakePredictionRepo{}
	engine := NewPredictiveEngine(
		repo,
		fakeSpendingProvider{},
		fakeObligationProvider{},
		fakeBalanceProvider{spend: decimal.NewFromFloat(100)},
		fakeProfileProvider{},
		zap.NewNop(),
	)

	state := &entities.MiriamMoneyState{
		UserID:                uuid.New(),
		LiquidityRunwayDays:   5,
		UpcomingObligations:   decimal.NewFromFloat(200),
		RecurringSpendMonthly: decimal.NewFromFloat(300),
		AnomalyCount:          0,
		ConfidenceLevel:       "high",
		AvgMonthlyIncome:      decimal.NewFromFloat(1000),
		IncomeCadence:         "monthly",
	}

	summary, err := engine.GeneratePredictions(context.Background(), uuid.New(), state)
	require.NoError(t, err)
	require.True(t, summary.RiskScore > 0)
	require.NotEmpty(t, summary.TopRisk)
	require.NotEmpty(t, summary.RecommendedAction)
}

func TestPredictiveEngine_NoShortfall(t *testing.T) {
	repo := &fakePredictionRepo{}
	engine := NewPredictiveEngine(
		repo,
		fakeSpendingProvider{},
		fakeObligationProvider{},
		fakeBalanceProvider{spend: decimal.NewFromFloat(5000)},
		fakeProfileProvider{},
		zap.NewNop(),
	)

	state := &entities.MiriamMoneyState{
		UserID:                uuid.New(),
		LiquidityRunwayDays:   90,
		UpcomingObligations:   decimal.NewFromFloat(200),
		RecurringSpendMonthly: decimal.NewFromFloat(300),
		AnomalyCount:          0,
		ConfidenceLevel:       "high",
		AvgMonthlyIncome:      decimal.NewFromFloat(2000),
		IncomeCadence:         "monthly",
	}

	summary, err := engine.GeneratePredictions(context.Background(), uuid.New(), state)
	require.NoError(t, err)
	require.True(t, summary.RiskScore >= 0)
}

func TestPredictiveEngine_BillPressure(t *testing.T) {
	repo := &fakePredictionRepo{}
	day := 15
	obligations := []entities.FinancialObligation{
		{
			ID:       uuid.New(),
			UserID:   uuid.New(),
			Amount:   decimal.NewFromFloat(500),
			Currency: "USD",
			Cadence:  "monthly",
			DueDay:   &day,
		},
	}
	engine := NewPredictiveEngine(
		repo,
		fakeSpendingProvider{},
		fakeObligationProvider{obligations: obligations},
		fakeBalanceProvider{spend: decimal.NewFromFloat(200)},
		fakeProfileProvider{},
		zap.NewNop(),
	)

	state := &entities.MiriamMoneyState{
		UserID:                uuid.New(),
		LiquidityRunwayDays:   10,
		UpcomingObligations:   decimal.NewFromFloat(500),
		RecurringSpendMonthly: decimal.Zero,
		AnomalyCount:          0,
		ConfidenceLevel:       "high",
		AvgMonthlyIncome:      decimal.NewFromFloat(1000),
		IncomeCadence:         "monthly",
	}

	summary, err := engine.GeneratePredictions(context.Background(), uuid.New(), state)
	require.NoError(t, err)
	require.True(t, summary.RiskScore > 0)
}

func TestComputeRiskScore(t *testing.T) {
	tests := []struct {
		name        string
		predictions []entities.MiriamPrediction
		wantMin     int
		wantMax     int
	}{
		{
			name:        "empty",
			predictions: nil,
			wantMin:     0,
			wantMax:     0,
		},
		{
			name: "single critical",
			predictions: []entities.MiriamPrediction{
				{Severity: entities.SeverityCritical, Probability: decimal.NewFromFloat(0.9)},
			},
			wantMin: 30,
			wantMax: 40,
		},
		{
			name: "single low",
			predictions: []entities.MiriamPrediction{
				{Severity: entities.SeverityLow, Probability: decimal.NewFromFloat(0.3)},
			},
			wantMin: 2,
			wantMax: 3,
		},
		{
			name: "multiple mixed",
			predictions: []entities.MiriamPrediction{
				{Severity: entities.SeverityCritical, Probability: decimal.NewFromFloat(0.9)},
				{Severity: entities.SeverityMedium, Probability: decimal.NewFromFloat(0.5)},
			},
			wantMin: 40,
			wantMax: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeRiskScore(tt.predictions)
			require.GreaterOrEqual(t, score, tt.wantMin)
			require.LessOrEqual(t, score, tt.wantMax)
		})
	}
}

func TestRecommendAction(t *testing.T) {
	tests := []struct {
		topRisk string
		want    string
	}{
		{entities.PredictionCashShortfall, "Review spending"},
		{entities.PredictionBillPressure, "Check upcoming bills"},
		{entities.PredictionIncomeGap, "Income projection"},
		{entities.PredictionIdleSurplus, "Idle money"},
		{entities.PredictionStashOpportunity, "Stash is below"},
		{"", "Review your financial position."},
	}

	for _, tt := range tests {
		t.Run(tt.topRisk, func(t *testing.T) {
			summary := &entities.PredictionSummary{TopRisk: tt.topRisk, RiskScore: 50}
			action := recommendAction(summary)
			if tt.want == "" {
				require.Empty(t, action)
			} else {
				require.Contains(t, action, tt.want)
			}
		})
	}
}

func TestDaysUntilNextPayday(t *testing.T) {
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

	require.Equal(t, 5, daysUntilNextPayday("biweekly", now)) // 15th - 10th = 5
	require.Equal(t, 21, daysUntilNextPayday("monthly", now)) // 31 - 10 = 21
}

func TestProjectedMonthlyIncome(t *testing.T) {
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)

	state := &entities.MiriamMoneyState{
		AvgMonthlyIncome: decimal.NewFromInt(2000),
		IncomeCadence:    "monthly",
	}

	// Mid-month, monthly income — projected income is 0 (likely already received)
	proj := projectedMonthlyIncome(state, now)
	// May 10 is before the 25th cutoff, so projected income = full amount
	require.Equal(t, decimal.NewFromInt(2000), proj)
}
