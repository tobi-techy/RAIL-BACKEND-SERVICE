package miriam

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeOutcomeRepo implements only the Repository methods the tracker touches;
// the rest satisfy the interface and are never called.
type fakeOutcomeRepo struct {
	Repository

	pending   []entities.MiriamPredictionOutcome
	marked    []entities.MiriamPredictionOutcome
	markErr   error
	markCalls int
	markCtxOK bool
}

func (f *fakeOutcomeRepo) GetPendingPredictionOutcomes(_ context.Context, _ uuid.UUID) ([]entities.MiriamPredictionOutcome, error) {
	return f.pending, nil
}

func (f *fakeOutcomeRepo) BatchMarkPredictionOutcomes(ctx context.Context, outcomes []entities.MiriamPredictionOutcome) error {
	f.markCalls++
	f.markCtxOK = ctx.Err() == nil
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, outcomes...)
	return nil
}

type countingSpendingProvider struct {
	calls   int
	windows [][2]time.Time
	flow    *entities.MoneyFlowSummary
}

func (c *countingSpendingProvider) GetMoneyFlow(_ context.Context, _ uuid.UUID, start, end time.Time) (*entities.MoneyFlowSummary, error) {
	c.calls++
	c.windows = append(c.windows, [2]time.Time{start, end})
	return c.flow, nil
}

type countingObligationProvider struct {
	calls       int
	obligations []entities.FinancialObligation
}

func (c *countingObligationProvider) ListActive(_ context.Context, _ uuid.UUID) ([]entities.FinancialObligation, error) {
	c.calls++
	return c.obligations, nil
}

func duePending(predictionType string) entities.MiriamPredictionOutcome {
	return entities.MiriamPredictionOutcome{
		ID:             uuid.New(),
		UserID:         uuid.New(),
		PredictionType: predictionType,
		HorizonDays:    7,
		CreatedAt:      time.Now().UTC().AddDate(0, 0, -30),
		ThresholdData:  json.RawMessage(`{}`),
	}
}

func newTracker(repo Repository, spending SpendingProvider, obligations ObligationProvider) *OutcomeTracker {
	return NewOutcomeTracker(
		repo,
		spending,
		fakeBalanceProvider{spend: decimal.NewFromInt(100)},
		obligations,
		zap.NewNop(),
	)
}

// The pre-fix loop re-issued these reads once per pending outcome, which blew the
// worker's 30s per-user budget at 100 pending rows.
func TestEvaluateOutcomes_ReadsAreIssuedOncePerSweep(t *testing.T) {
	var pending []entities.MiriamPredictionOutcome
	for i := 0; i < 40; i++ {
		pending = append(pending,
			duePending(entities.PredictionCashShortfall),
			duePending(entities.PredictionBillPressure),
			duePending(entities.PredictionIncomeGap),
			duePending(entities.PredictionSpendingAnomaly),
		)
	}

	repo := &fakeOutcomeRepo{pending: pending}
	spending := &countingSpendingProvider{flow: &entities.MoneyFlowSummary{}}
	obligations := &countingObligationProvider{}
	tracker := newTracker(repo, spending, obligations)

	resolved := tracker.EvaluateOutcomes(context.Background(), uuid.New())

	require.Len(t, resolved, len(pending))
	// upcoming (30d) + imminent (7d) share ListActive but use different windows.
	assert.Equal(t, 2, obligations.calls, "obligations must be fetched at most once per window")
	assert.Equal(t, 2, spending.calls, "money flow must be fetched once per month window")
	require.Len(t, spending.windows, 2)
	assert.True(t, spending.windows[1][1].Equal(spending.windows[0][0]),
		"prior-month window must end where this-month window starts")
	assert.Equal(t, 1, repo.markCalls)
	assert.Len(t, repo.marked, len(pending))
}

func TestEvaluateOutcomes_ResolutionMatchesPerTypeRules(t *testing.T) {
	cases := []struct {
		name           string
		predictionType string
		spend          decimal.Decimal
		obligation     decimal.Decimal
		flow           *entities.MoneyFlowSummary
		threshold      string
		want           bool
	}{
		{
			name:           "shortfall materialises when spend below upcoming obligations",
			predictionType: entities.PredictionCashShortfall,
			spend:          decimal.NewFromInt(50),
			obligation:     decimal.NewFromInt(200),
			want:           true,
		},
		{
			name:           "shortfall misses when spend covers obligations",
			predictionType: entities.PredictionCashShortfall,
			spend:          decimal.NewFromInt(500),
			obligation:     decimal.NewFromInt(200),
			want:           false,
		},
		{
			name:           "bill pressure materialises when spend below imminent bills",
			predictionType: entities.PredictionBillPressure,
			spend:          decimal.NewFromInt(10),
			obligation:     decimal.NewFromInt(80),
			want:           true,
		},
		{
			name:           "income gap materialises when deposits trail projection by 30%",
			predictionType: entities.PredictionIncomeGap,
			spend:          decimal.NewFromInt(100),
			flow:           &entities.MoneyFlowSummary{TotalDeposits: decimal.NewFromInt(400)},
			threshold:      `{"data_snapshot":"{\"projected_income\":\"1000\"}"}`,
			want:           true,
		},
		{
			name:           "income gap misses when deposits land on projection",
			predictionType: entities.PredictionIncomeGap,
			spend:          decimal.NewFromInt(100),
			flow:           &entities.MoneyFlowSummary{TotalDeposits: decimal.NewFromInt(950)},
			threshold:      `{"data_snapshot":"{\"projected_income\":\"1000\"}"}`,
			want:           false,
		},
		{
			name:           "idle surplus is never a negative outcome",
			predictionType: entities.PredictionIdleSurplus,
			spend:          decimal.NewFromInt(1),
			obligation:     decimal.NewFromInt(999),
			want:           false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := duePending(tc.predictionType)
			if tc.threshold != "" {
				outcome.ThresholdData = json.RawMessage(tc.threshold)
			}

			due := time.Now().UTC().AddDate(0, 0, 5)
			obligations := &countingObligationProvider{}
			if tc.obligation.IsPositive() {
				obligations.obligations = []entities.FinancialObligation{{
					Amount:   tc.obligation,
					Currency: "USD",
					DueDate:  &due,
				}}
			}

			flow := tc.flow
			if flow == nil {
				flow = &entities.MoneyFlowSummary{}
			}

			repo := &fakeOutcomeRepo{pending: []entities.MiriamPredictionOutcome{outcome}}
			tracker := NewOutcomeTracker(
				repo,
				&countingSpendingProvider{flow: flow},
				fakeBalanceProvider{spend: tc.spend},
				obligations,
				zap.NewNop(),
			)

			resolved := tracker.EvaluateOutcomes(context.Background(), uuid.New())

			require.Len(t, resolved, 1)
			require.NotNil(t, resolved[0].ActualOutcome)
			assert.Equal(t, tc.want, *resolved[0].ActualOutcome)
			require.NotNil(t, resolved[0].OutcomeObservedAt)
		})
	}
}

func TestEvaluateOutcomes_SkipsOutcomesBeforeHorizon(t *testing.T) {
	notDue := duePending(entities.PredictionCashShortfall)
	notDue.CreatedAt = time.Now().UTC()
	notDue.HorizonDays = 30

	repo := &fakeOutcomeRepo{pending: []entities.MiriamPredictionOutcome{notDue}}
	tracker := newTracker(repo, &countingSpendingProvider{flow: &entities.MoneyFlowSummary{}}, &countingObligationProvider{})

	assert.Empty(t, tracker.EvaluateOutcomes(context.Background(), uuid.New()))
	assert.Zero(t, repo.markCalls)
}

// The write must survive an already-expired parent context, otherwise settled
// outcomes stay pending and are re-evaluated on every sweep.
func TestEvaluateOutcomes_PersistsWithExpiredParentContext(t *testing.T) {
	repo := &fakeOutcomeRepo{pending: []entities.MiriamPredictionOutcome{duePending(entities.PredictionCashShortfall)}}
	tracker := newTracker(repo, &countingSpendingProvider{flow: &entities.MoneyFlowSummary{}}, &countingObligationProvider{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resolved := tracker.EvaluateOutcomes(ctx, uuid.New())

	require.Len(t, resolved, 1)
	assert.Equal(t, 1, repo.markCalls)
	assert.True(t, repo.markCtxOK, "write context must not inherit parent cancellation")
	assert.Len(t, repo.marked, 1)
}

func TestEvaluateOutcomes_ReturnsNilWhenPersistFails(t *testing.T) {
	repo := &fakeOutcomeRepo{
		pending: []entities.MiriamPredictionOutcome{duePending(entities.PredictionCashShortfall)},
		markErr: errors.New("begin tx: context deadline exceeded"),
	}
	tracker := newTracker(repo, &countingSpendingProvider{flow: &entities.MoneyFlowSummary{}}, &countingObligationProvider{})

	assert.Nil(t, tracker.EvaluateOutcomes(context.Background(), uuid.New()))
	assert.Equal(t, 1, repo.markCalls)
}
