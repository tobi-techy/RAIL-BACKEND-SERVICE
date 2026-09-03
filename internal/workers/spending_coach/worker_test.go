package spending_coach

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/consciousspending"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestIsoWeek(t *testing.T) {
	// 2026-01-05 is a Monday, ISO week 2.
	w := isoWeek(parseTime(t, "2026-01-05T00:00:00Z"))
	assert.Equal(t, "2026-W02", w)
}

func TestDueForWeeklyCoach_NotMonday(t *testing.T) {
	// 2026-01-06 is a Tuesday at 09:00 UTC.
	now := parseTime(t, "2026-01-06T09:00:00Z")
	assert.False(t, dueForWeeklyCoach("NG", now))
}

func TestDueForWeeklyCoach_MondayWrongHour(t *testing.T) {
	now := parseTime(t, "2026-01-05T10:00:00Z")
	assert.False(t, dueForWeeklyCoach("NG", now))
}

func TestDueForWeeklyCoach_NGMonday9am(t *testing.T) {
	now := parseTime(t, "2026-01-05T08:00:00Z") // 9am Lagos
	assert.True(t, dueForWeeklyCoach("NG", now))
}

func TestDueForWeeklyCoach_UnknownCountry(t *testing.T) {
	// Unknown country → UTC 9am Monday.
	now := parseTime(t, "2026-01-05T09:00:00Z")
	assert.True(t, dueForWeeklyCoach("", now))
}

func TestDueForCadence(t *testing.T) {
	assert.True(t, dueForCadence("", parseTime(t, "2026-01-05T09:00:00Z"), entities.CheckInCadenceWeekly))
	assert.True(t, dueForCadence("", parseTime(t, "2026-01-05T09:00:00Z"), entities.CheckInCadenceBiweekly))
	assert.False(t, dueForCadence("", parseTime(t, "2026-01-12T09:00:00Z"), entities.CheckInCadenceBiweekly))
	assert.True(t, dueForCadence("", parseTime(t, "2026-01-05T09:00:00Z"), entities.CheckInCadenceMonthly))
	assert.False(t, dueForCadence("", parseTime(t, "2026-01-12T09:00:00Z"), entities.CheckInCadenceMonthly))
}

type snapshotFake struct {
	snapshot consciousspending.Snapshot
}

func (f snapshotFake) GetConsciousSpendingSnapshot(context.Context, uuid.UUID) consciousspending.Snapshot {
	return f.snapshot
}

type plansFake struct {
	checkIns []entities.ConsciousSpendingPlanCheckIn
	calls    int
}

func (f *plansFake) ListCommittedCheckIns(context.Context) ([]entities.ConsciousSpendingPlanCheckIn, error) {
	f.calls++
	return f.checkIns, nil
}

type pushFake struct {
	users []uuid.UUID
}

func (f *pushFake) SendToUser(_ context.Context, userID uuid.UUID, _, _ string, _ map[string]interface{}) error {
	f.users = append(f.users, userID)
	return nil
}

func completeSnapshot(fixed, investments, savings, guiltFree int64) consciousspending.Snapshot {
	known := func(amount int64) consciousspending.ObservedAmount {
		return consciousspending.ObservedAmount{Amount: decimal.NewFromInt(amount), Known: true}
	}
	return consciousspending.CalculateSnapshot(consciousspending.SnapshotInput{
		TakeHomeIncome: known(100), FixedCosts: known(fixed), Investments: known(investments),
		Savings: known(savings), GuiltFreeSpending: known(guiltFree),
	})
}

func committedPlan() *entities.ConsciousSpendingPlan {
	return &entities.ConsciousSpendingPlan{
		FixedCostsPct: decimal.NewFromInt(55), InvestmentsPct: decimal.NewFromInt(10),
		SavingsPct: decimal.NewFromInt(10), GuiltFreeSpendingPct: decimal.NewFromInt(25),
	}
}

func TestComposeCommittedPlanCopyOnTrack(t *testing.T) {
	worker := &Worker{snapshots: snapshotFake{snapshot: completeSnapshot(55, 10, 10, 25)}}

	title, body, insight := worker.composeCommittedPlanCopy(context.Background(), uuid.New(), committedPlan())

	assert.Equal(t, "Miriam: plan check", title)
	assert.Contains(t, body, "holding")
	assert.Equal(t, "csp:on_track", insight)
}

func TestComposeCommittedPlanCopyAsksWhatChangedForLargestAdverseVariance(t *testing.T) {
	worker := &Worker{snapshots: snapshotFake{snapshot: completeSnapshot(55, 5, 5, 35)}}

	title, body, insight := worker.composeCommittedPlanCopy(context.Background(), uuid.New(), committedPlan())

	assert.Equal(t, "Miriam: recommitment check", title)
	assert.Contains(t, body, "Guilt-free spending is 35.0%")
	assert.Contains(t, body, "What changed?")
	assert.Equal(t, "csp:variance:guilt_free_spending", insight)
}

func TestComposeCommittedPlanCopyDoesNotInventMissingNumbers(t *testing.T) {
	worker := &Worker{snapshots: snapshotFake{snapshot: consciousspending.Snapshot{Complete: false}}}

	title, body, insight := worker.composeCommittedPlanCopy(context.Background(), uuid.New(), committedPlan())

	require.NotEmpty(t, title)
	assert.Contains(t, body, "missing number")
	assert.Equal(t, "csp:missing_data", insight)
}

func TestDueForCadenceUsesLocalFirstMonday(t *testing.T) {
	now := time.Date(2026, time.February, 2, 8, 0, 0, 0, time.UTC)
	assert.True(t, dueForCadence("NG", now, entities.CheckInCadenceMonthly))
}

func TestCouldAnyCheckInBeDueSkipsMostHourlyScans(t *testing.T) {
	assert.False(t, couldAnyCheckInBeDue(parseTime(t, "2026-01-06T09:00:00Z")))
	assert.False(t, couldAnyCheckInBeDue(parseTime(t, "2026-01-05T05:00:00Z")))
	assert.True(t, couldAnyCheckInBeDue(parseTime(t, "2026-01-05T08:00:00Z")))
}

func TestRunOnceFansOutOnlyDueCommittedPlans(t *testing.T) {
	dueUser := uuid.New()
	notDueUser := uuid.New()
	plans := &plansFake{checkIns: []entities.ConsciousSpendingPlanCheckIn{
		{Plan: entities.ConsciousSpendingPlan{
			UserID: dueUser, Status: entities.ConsciousSpendingPlanStatusCommitted,
			CheckInCadence: entities.CheckInCadenceWeekly,
			FixedCostsPct:  decimal.NewFromInt(55), InvestmentsPct: decimal.NewFromInt(10),
			SavingsPct: decimal.NewFromInt(10), GuiltFreeSpendingPct: decimal.NewFromInt(25),
		}, Country: "NG"},
		{Plan: entities.ConsciousSpendingPlan{
			UserID: notDueUser, Status: entities.ConsciousSpendingPlanStatusCommitted,
			CheckInCadence: entities.CheckInCadenceWeekly,
		}, Country: "US"},
	}}
	push := &pushFake{}
	worker := New(push, nil, nil, zap.NewNop())
	worker.SetConsciousSpendingProviders(plans, snapshotFake{snapshot: completeSnapshot(55, 10, 10, 25)})
	worker.SetClock(func() time.Time { return parseTime(t, "2026-01-05T08:00:00Z") }) // 9am Lagos, 3am New York

	require.NoError(t, worker.RunOnce(context.Background()))
	assert.Equal(t, 1, plans.calls)
	assert.Equal(t, []uuid.UUID{dueUser}, push.users)
}

func TestRunOnceSkipsDatabaseOutsideAnyCheckInWindow(t *testing.T) {
	plans := &plansFake{}
	worker := New(&pushFake{}, nil, nil, zap.NewNop())
	worker.SetConsciousSpendingProviders(plans, snapshotFake{})
	worker.SetClock(func() time.Time { return parseTime(t, "2026-01-06T09:00:00Z") })

	require.NoError(t, worker.RunOnce(context.Background()))
	assert.Zero(t, plans.calls)
}
