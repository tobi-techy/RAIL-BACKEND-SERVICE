package consciousspending

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/repositories"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type planRepoFake struct {
	plan  *entities.ConsciousSpendingPlan
	items []entities.ConsciousSpendingPlanItem
}

func (r *planRepoFake) GetByUserID(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	plan := *r.plan
	plan.Items = append([]entities.ConsciousSpendingPlanItem(nil), r.items...)
	return &plan, nil
}

func (r *planRepoFake) GetActiveVersion(context.Context, uuid.UUID) (*entities.ConsciousSpendingPlan, error) {
	return r.GetByUserID(context.Background(), uuid.UUID{})
}

func (r *planRepoFake) Commit(_ context.Context, userID uuid.UUID, in repositories.PlanHeaderInput) (*entities.ConsciousSpendingPlan, error) {
	now := time.Now().UTC()
	if in.CommittedAt != nil {
		now = *in.CommittedAt
	}
	version := 1
	if r.plan != nil {
		version = r.plan.Version + 1
	}
	r.items = append([]entities.ConsciousSpendingPlanItem(nil), in.Items...)
	r.plan = &entities.ConsciousSpendingPlan{
		ID: uuid.New(), UserID: userID, Version: version, Status: entities.ConsciousSpendingPlanStatusCommitted,
		BaseCurrency: in.BaseCurrency, TakeHomeIncome: in.TakeHomeIncome, FixedCosts: in.FixedCosts,
		PostTaxInvestments: in.PostTaxInvestments, Savings: in.Savings, GuiltFreeSpending: in.GuiltFreeSpending,
		MiscBufferRate: in.MiscBufferRate, MiscBufferAmount: in.MiscBufferAmount, CheckInCadence: in.CheckInCadence,
		CommittedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	return r.GetByUserID(context.Background(), uuid.UUID{})
}

func (r *planRepoFake) Supersede(context.Context, uuid.UUID, int) (*entities.ConsciousSpendingPlan, error) {
	if r.plan != nil {
		r.plan.Status = entities.ConsciousSpendingPlanStatusSuperseded
	}
	return r.GetByUserID(context.Background(), uuid.UUID{})
}

func (r *planRepoFake) Pause(context.Context, uuid.UUID, int) (*entities.ConsciousSpendingPlan, error) {
	if r.plan != nil {
		r.plan.Status = entities.ConsciousSpendingPlanStatusPaused
	}
	return r.GetByUserID(context.Background(), uuid.UUID{})
}

func (r *planRepoFake) SaveItems(context.Context, uuid.UUID, []entities.ConsciousSpendingPlanItem) error {
	return nil
}

func (r *planRepoFake) SaveNetWorth(context.Context, *entities.ConsciousSpendingNetWorth) error {
	return nil
}

func (r *planRepoFake) ListCommittedCheckIns(context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	if r.plan == nil || r.plan.Status != entities.ConsciousSpendingPlanStatusCommitted {
		return nil, nil
	}
	return []entities.ConsciousSpendingPlanCheckIn{{Plan: *r.plan}}, nil
}

func validHeaderInput() repositories.PlanHeaderInput {
	return repositories.PlanHeaderInput{
		BaseCurrency: "NGN", TakeHomeIncome: decimal.NewFromInt(1000),
		FixedCosts: decimal.NewFromInt(550), MiscBufferRate: decimal.NewFromFloat(0.15),
		PostTaxInvestments: decimal.NewFromInt(100), Savings: decimal.NewFromInt(100),
		GuiltFreeSpending: decimal.NewFromInt(250), CheckInCadence: entities.CheckInCadenceBiweekly,
		Items: []entities.ConsciousSpendingPlanItem{
			{Bucket: entities.CSPItemBucketFixedCost, Name: "Rent", Amount: decimal.NewFromInt(450)},
			{Bucket: entities.CSPItemBucketInvestment, Name: "USDC", Amount: decimal.NewFromInt(100)},
			{Bucket: entities.CSPItemBucketSavings, Name: "Emergency", Amount: decimal.NewFromInt(100)},
		},
	}
}

func TestCommitPersistsHeaderAndLineItems(t *testing.T) {
	repo := &planRepoFake{}
	svc := NewService(repo)

	plan, err := svc.Commit(context.Background(), uuid.New(), validHeaderInput())

	require.NoError(t, err)
	assert.Equal(t, entities.ConsciousSpendingPlanStatusCommitted, plan.Status)
	assert.Equal(t, "NGN", plan.BaseCurrency)
	assert.Equal(t, entities.CheckInCadenceBiweekly, plan.CheckInCadence)
	require.NotNil(t, plan.CommittedAt)
	assert.Equal(t, 1, plan.Version)
	assert.True(t, plan.MiscBufferAmount.Equal(decimal.NewFromInt(75)))
	assert.True(t, plan.FixedCostsSubtotal.Equal(decimal.NewFromInt(575)))
	assert.Len(t, plan.Items, 3)
}

func TestValidateHeaderRejectsInvalidMoney(t *testing.T) {
	tests := []struct {
		name string
		edit func(*repositories.PlanHeaderInput)
		err  error
	}{
		{"non-positive income", func(in *repositories.PlanHeaderInput) { in.TakeHomeIncome = decimal.Zero }, ErrInvalidIncome},
		{"negative bucket", func(in *repositories.PlanHeaderInput) { in.PostTaxInvestments = decimal.NewFromInt(-1) }, ErrNegativeAmount},
		{"buckets do not balance", func(in *repositories.PlanHeaderInput) { in.GuiltFreeSpending = decimal.NewFromInt(200) }, ErrInvalidPlanTotal},
		{"missing base currency", func(in *repositories.PlanHeaderInput) { in.BaseCurrency = "" }, ErrMissingBaseCurrency},
		{"invalid buffer rate", func(in *repositories.PlanHeaderInput) { in.MiscBufferRate = decimal.NewFromInt(2) }, ErrInvalidBufferRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validHeaderInput()
			tt.edit(&in)
			err := ValidateHeaderInput(in)
			require.Error(t, err)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
			}
		})
	}
}

func TestPauseSupersedeUseVersionedPlan(t *testing.T) {
	repo := &planRepoFake{}
	svc := NewService(repo)
	committed, err := svc.Commit(context.Background(), uuid.New(), validHeaderInput())
	require.NoError(t, err)

	paused, err := svc.Pause(context.Background(), committed.UserID, committed.Version)
	require.NoError(t, err)
	assert.Equal(t, entities.ConsciousSpendingPlanStatusPaused, paused.Status)

	next, err := svc.Commit(context.Background(), committed.UserID, validHeaderInput())
	require.NoError(t, err)
	assert.Equal(t, 2, next.Version)

	superseded, err := svc.Supersede(context.Background(), committed.UserID, next.Version)
	require.NoError(t, err)
	assert.Equal(t, entities.ConsciousSpendingPlanStatusSuperseded, superseded.Status)
}
