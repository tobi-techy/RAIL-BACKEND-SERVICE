package consciousspending

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type planRepoFake struct {
	plan *entities.ConsciousSpendingPlan
}

func (r *planRepoFake) GetByUserID(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return r.plan, nil
}

func (r *planRepoFake) Upsert(_ context.Context, plan *entities.ConsciousSpendingPlan) error {
	copy := *plan
	r.plan = &copy
	return nil
}

func (r *planRepoFake) ListCommittedCheckIns(context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	if r.plan != nil && r.plan.Status == entities.ConsciousSpendingPlanStatusCommitted {
		return []entities.ConsciousSpendingPlanCheckIn{{Plan: *r.plan}}, nil
	}
	return nil, nil
}

func validPlanInput() PlanInput {
	return PlanInput{
		TakeHomeIncome: decimal.NewFromInt(1000), Currency: "ngn",
		FixedCosts: decimal.NewFromInt(550), Investments: decimal.NewFromInt(100),
		Savings: decimal.NewFromInt(100), GuiltFreeSpending: decimal.NewFromInt(250),
		CheckInCadence: entities.CheckInCadenceBiweekly,
	}
}

func TestCommitPersistsExactPlanAndCommitment(t *testing.T) {
	repo := &planRepoFake{}
	svc := NewService(repo)
	now := time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	plan, err := svc.Commit(context.Background(), uuid.New(), validPlanInput())

	require.NoError(t, err)
	assert.Equal(t, entities.ConsciousSpendingPlanStatusCommitted, plan.Status)
	assert.Equal(t, "NGN", plan.Currency)
	assert.Equal(t, entities.CheckInCadenceBiweekly, plan.CheckInCadence)
	require.NotNil(t, plan.CommittedAt)
	assert.Equal(t, now, *plan.CommittedAt)
	assert.True(t, plan.FixedCostsPct.Equal(decimal.NewFromInt(55)))
	assert.True(t, plan.GuiltFreeSpendingPct.Equal(decimal.NewFromInt(25)))
}

func TestValidateInputRejectsInvalidMoney(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PlanInput)
		err  error
	}{
		{"non-positive income", func(in *PlanInput) { in.TakeHomeIncome = decimal.Zero }, ErrInvalidIncome},
		{"negative bucket", func(in *PlanInput) { in.Savings = decimal.NewFromInt(-1) }, nil},
		{"buckets do not balance", func(in *PlanInput) { in.GuiltFreeSpending = decimal.NewFromInt(200) }, ErrInvalidPlanTotal},
		{"missing currency", func(in *PlanInput) { in.Currency = "" }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validPlanInput()
			tt.edit(&in)
			err := ValidateInput(in)
			require.Error(t, err)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
			}
		})
	}
}

func TestPausePreservesNumbersAndStopsCommitment(t *testing.T) {
	repo := &planRepoFake{}
	svc := NewService(repo)
	committed, err := svc.Commit(context.Background(), uuid.New(), validPlanInput())
	require.NoError(t, err)

	paused, err := svc.Pause(context.Background(), committed.UserID)

	require.NoError(t, err)
	assert.Equal(t, entities.ConsciousSpendingPlanStatusPaused, paused.Status)
	assert.True(t, paused.TakeHomeIncome.Equal(committed.TakeHomeIncome))
}
