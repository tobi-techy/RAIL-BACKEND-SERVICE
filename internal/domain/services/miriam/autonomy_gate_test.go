package miriam

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// stubControlLevel is a ControlLevelReader that returns a fixed level/error.
type stubControlLevel struct {
	level string
	err   error
	calls int
}

func (s *stubControlLevel) GetControlLevel(_ context.Context, _ uuid.UUID) (string, error) {
	s.calls++
	return s.level, s.err
}

// ---- Fakes for Evaluate-based mandate execution tests ----

// fakeRepo implements Repository for Evaluate tests. It stores money state
// and active mandates so Evaluate can exercise the full mandate path.
type fakeRepo struct {
	state    *entities.MiriamMoneyState
	mandates []entities.MiriamAutopilotMandate
	executed int
}

func (r *fakeRepo) UpsertMoneyState(_ context.Context, s *entities.MiriamMoneyState) error {
	r.state = s
	return nil
}
func (r *fakeRepo) GetMoneyState(_ context.Context, _ uuid.UUID) (*entities.MiriamMoneyState, error) {
	return r.state, nil
}
func (r *fakeRepo) CreateMandate(_ context.Context, _ *entities.MiriamAutopilotMandate) error {
	return nil
}
func (r *fakeRepo) ListMandates(_ context.Context, _ uuid.UUID) ([]entities.MiriamAutopilotMandate, error) {
	return r.mandates, nil
}
func (r *fakeRepo) ListActiveMandates(_ context.Context, _ uuid.UUID) ([]entities.MiriamAutopilotMandate, error) {
	return r.mandates, nil
}
func (r *fakeRepo) UpdateMandateStatus(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}
func (r *fakeRepo) MarkMandateExecuted(_ context.Context, _ uuid.UUID, _ time.Time) error {
	r.executed++
	return nil
}
func (r *fakeRepo) MandateExecutedAmountSince(_ context.Context, _ uuid.UUID, _ time.Time) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (r *fakeRepo) CreateReceipt(_ context.Context, _ *entities.MiriamDecisionReceipt) error { return nil }
func (r *fakeRepo) ListReceipts(_ context.Context, _ uuid.UUID, _ int) ([]entities.MiriamDecisionReceipt, error) {
	return nil, nil
}
func (r *fakeRepo) CreateEvent(_ context.Context, _ *entities.MiriamEvent) error { return nil }
func (r *fakeRepo) CreateLearningSignal(_ context.Context, _ *entities.MiriamLearningSignal) error {
	return nil
}
func (r *fakeRepo) RecentLearningBias(_ context.Context, _ uuid.UUID, _ time.Time) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (r *fakeRepo) SavePredictionOutcomes(_ context.Context, _ []entities.MiriamPredictionOutcome) error {
	return nil
}
func (r *fakeRepo) GetPendingPredictionOutcomes(_ context.Context, _ uuid.UUID) ([]entities.MiriamPredictionOutcome, error) {
	return nil, nil
}
func (r *fakeRepo) MarkPredictionOutcome(_ context.Context, _ uuid.UUID, _ bool, _ time.Time) error {
	return nil
}
func (r *fakeRepo) BatchMarkPredictionOutcomes(_ context.Context, _ []entities.MiriamPredictionOutcome) error {
	return nil
}
func (r *fakeRepo) GetPredictionHitRate(_ context.Context, _ uuid.UUID, _ string, _ time.Time) (float64, error) {
	return 0, nil
}
func (r *fakeRepo) DeletePredictionsOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (r *fakeRepo) DeleteEvaluatedOutcomesOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// fakeDecisionRepo implements DecisionRepository for Evaluate tests.
type fakeDecisionRepo struct {
	created int
}

func (r *fakeDecisionRepo) CreateDecision(_ context.Context, _ *entities.MiriamDecision) error {
	r.created++
	return nil
}
func (r *fakeDecisionRepo) RecentDecisions(_ context.Context, _ uuid.UUID, _ int) ([]entities.MiriamDecision, error) {
	return nil, nil
}
func (r *fakeDecisionRepo) GetDecision(_ context.Context, _ uuid.UUID) (*entities.MiriamDecision, error) {
	return nil, nil
}
func (r *fakeDecisionRepo) CreateOutcome(_ context.Context, _ *entities.MiriamDecisionOutcome) error {
	return nil
}
func (r *fakeDecisionRepo) GetLearningModel(_ context.Context, _ uuid.UUID, _ string) (*entities.MiriamLearningModel, error) {
	return nil, nil
}
func (r *fakeDecisionRepo) UpsertLearningModel(_ context.Context, _ *entities.MiriamLearningModel) error {
	return nil
}
func (r *fakeDecisionRepo) ListOutcomesSince(_ context.Context, _ uuid.UUID, _ string, _ time.Time) ([]entities.MiriamDecisionOutcome, error) {
	return nil, nil
}

// fakeSafeToSpend implements SafeToSpendProvider.
type fakeSafeToSpend struct {
	daily decimal.Decimal
}

func (f fakeSafeToSpend) SafeToSpend(_ context.Context, _ uuid.UUID) (entities.SafeToSpendSnapshot, error) {
	return entities.SafeToSpendSnapshot{DailySafeToSpend: f.daily}, nil
}

// fakeTransfer implements TransferExecutor and records calls.
type fakeTransfer struct {
	toStashCalls int
}

func (f *fakeTransfer) TransferSpendingToStash(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	f.toStashCalls++
	return nil
}
func (f *fakeTransfer) TransferStashToSpending(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	return nil
}

// fakeNudgeStore implements ProactiveNudgeStore for Evaluate tests.
type fakeNudgeStore struct{}

func (fakeNudgeStore) CreateNudge(_ context.Context, _ *entities.ProactiveNudge) error { return nil }
func (fakeNudgeStore) ListPendingNudges(_ context.Context, _ uuid.UUID) ([]entities.ProactiveNudge, error) {
	return nil, nil
}
func (fakeNudgeStore) MarkDelivered(_ context.Context, _ uuid.UUID) error    { return nil }
func (fakeNudgeStore) MarkDismissed(_ context.Context, _ uuid.UUID) error    { return nil }
func (fakeNudgeStore) ExpireOldNudges(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (fakeNudgeStore) HasRecentNudgeByType(_ context.Context, _ uuid.UUID, _ string, _ time.Time) (bool, error) {
	return false, nil
}

// buildTestOrchestrator assembles a minimally-wired IntelligenceOrchestrator
// that can run Evaluate end-to-end. The mandate is a transfer_to_stash
// mandate with floors low enough that the decision engine will produce
// DecisionExecute when mandateEvent is true and the gate is open.
func buildTestOrchestrator(t *testing.T, controlLevel string) (*IntelligenceOrchestrator, *fakeTransfer, *fakeRepo) {
	t.Helper()
	uid := uuid.New()

	mandate := entities.MiriamAutopilotMandate{
		ID:                 uuid.New(),
		UserID:             uid,
		Name:               "test stash move",
		ActionType:         entities.MiriamMandateTransferToStash,
		Status:             entities.MiriamMandateStatusActive,
		MaxAmountPerAction: decimal.NewFromInt(50),
		MaxAmountPerDay:    decimal.NewFromInt(100),
		MinSpendBalance:    decimal.NewFromInt(100),
		MinSafeToSpend:     decimal.NewFromInt(10),
	}

	repo := &fakeRepo{mandates: []entities.MiriamAutopilotMandate{mandate}}
	transfer := &fakeTransfer{}
	notifier := &fakeNotifier{}

	svc := NewService(
		repo,
		fakeBalanceProvider{spend: decimal.NewFromInt(1000), stash: decimal.NewFromInt(500)},
		fakeSpendingProvider{},
		fakeObligationProvider{},
		fakeProfileProvider{income: decimal.NewFromInt(2000), cadence: "monthly"},
		fakeSafeToSpend{daily: decimal.NewFromInt(100)},
		transfer,
		notifier,
		zap.NewNop(),
	)

	predEngine := NewPredictiveEngine(
		&fakePredictionRepo{},
		fakeSpendingProvider{},
		fakeObligationProvider{},
		fakeBalanceProvider{spend: decimal.NewFromInt(1000)},
		fakeProfileProvider{},
		zap.NewNop(),
	)

	decEngine := NewDecisionEngine(
		&fakeDecisionRepo{},
		predEngine,
		nil,
		zap.NewNop(),
	)

	suggestEngine := NewMandateSuggestionEngine(
		nil, // repo optional — GenerateSuggestions is nil-safe
		svc, // svc implements MandateProvider via HasActiveMandate
		fakeBalanceProvider{spend: decimal.NewFromInt(1000), stash: decimal.NewFromInt(500)},
		fakeSpendingProvider{},
		fakeObligationProvider{},
		fakeProfileProvider{},
		zap.NewNop(),
	)

	balancesForPred := fakeBalanceProvider{spend: decimal.NewFromInt(1000), stash: decimal.NewFromInt(500)}
	nudgeEngine := NewProactiveNudgeEngine(
		fakeNudgeStore{},
		predEngine,
		balancesForPred,
		nil,
		notifier,
		zap.NewNop(),
	)

	o := &IntelligenceOrchestrator{
		service:     svc,
		decisions:   decEngine,
		predictions: predEngine,
		suggestions: suggestEngine,
		nudges:      nudgeEngine,
		controlLevel: &stubControlLevel{
			level: controlLevel,
		},
		logger: zap.NewNop(),
	}
	return o, transfer, repo
}

// TestAutonomyGateReason verifies that autonomous mandate execution is only
// permitted for Full Autopilot users and fails closed otherwise — a transient
// read failure or missing wiring must never allow money to move.
func TestAutonomyGateReason(t *testing.T) {
	tests := []struct {
		name       string
		reader     ControlLevelReader
		wantReason string
	}{
		{
			name:       "full autopilot permits execution",
			reader:     &stubControlLevel{level: entities.ControlLevelFull},
			wantReason: "",
		},
		{
			name:       "guided blocks execution",
			reader:     &stubControlLevel{level: entities.ControlLevelGuided},
			wantReason: "guided",
		},
		{
			name:       "monitor blocks execution",
			reader:     &stubControlLevel{level: entities.ControlLevelMonitor},
			wantReason: "monitor",
		},
		{
			name:       "unknown level fails closed",
			reader:     &stubControlLevel{level: "something-else"},
			wantReason: "unknown",
		},
		{
			name:       "blank level fails closed",
			reader:     &stubControlLevel{level: ""},
			wantReason: "unknown",
		},
		{
			name:       "lookup error fails closed",
			reader:     &stubControlLevel{err: errors.New("db down")},
			wantReason: "lookup_error",
		},
		{
			name:       "unwired reader fails closed",
			reader:     nil,
			wantReason: "unwired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &IntelligenceOrchestrator{controlLevel: tt.reader}
			got := o.autonomyGateReason(context.Background(), uuid.New())
			assert.Equal(t, tt.wantReason, got)
		})
	}
}

// TestAutonomyGateReasonEmptyMeansExecute documents the contract that only an
// empty reason unlocks the mandate execution loop in Evaluate.
func TestAutonomyGateReasonEmptyMeansExecute(t *testing.T) {
	o := &IntelligenceOrchestrator{controlLevel: &stubControlLevel{level: entities.ControlLevelFull}}
	assert.Empty(t, o.autonomyGateReason(context.Background(), uuid.New()),
		"full autopilot must return empty reason so execution proceeds")

	o2 := &IntelligenceOrchestrator{controlLevel: &stubControlLevel{level: entities.ControlLevelGuided}}
	assert.NotEmpty(t, o2.autonomyGateReason(context.Background(), uuid.New()),
		"non-full levels must return a non-empty reason so execution is skipped")
}

// TestIsAutonomousEvent verifies that the always-on event types are classified
// as autonomous (read-only on money) and legacy event types are not.
func TestIsAutonomousEvent(t *testing.T) {
	assert.True(t, IsAutonomousEvent(EventMoneyEvent))
	assert.True(t, IsAutonomousEvent(EventAutonomousTick))
	assert.False(t, IsAutonomousEvent(EventWorkerSweep))
	assert.False(t, IsAutonomousEvent(EventIncomeLowerThanUsual))
	assert.False(t, IsAutonomousEvent(""))
}

// TestEvaluateSkipsMandateExecutionForAutonomousEvents verifies through the
// real Evaluate pipeline that autonomous event types never trigger mandate
// execution (no transfers), while eligible legacy events execute mandates
// only for ControlLevelFull users.
func TestEvaluateSkipsMandateExecutionForAutonomousEvents(t *testing.T) {
	tests := []struct {
		name           string
		eventType      string
		controlLevel   string
		wantActions    int  // expected ActionsExecuted (0 for blocked, >0 for executed)
		wantTransfer   bool // whether TransferSpendingToStash should be called
	}{
		{
			name:         "money event skips mandates even for full users",
			eventType:    EventMoneyEvent,
			controlLevel: entities.ControlLevelFull,
			wantActions:  0,
			wantTransfer: false,
		},
		{
			name:         "autonomous tick skips mandates even for full users",
			eventType:    EventAutonomousTick,
			controlLevel: entities.ControlLevelFull,
			wantActions:  0,
			wantTransfer: false,
		},
		{
			name:         "worker sweep runs mandates for full users",
			eventType:    EventWorkerSweep,
			controlLevel: entities.ControlLevelFull,
			wantActions:  1,
			wantTransfer: true,
		},
		{
			name:         "income-lower event runs mandates for full users",
			eventType:    EventIncomeLowerThanUsual,
			controlLevel: entities.ControlLevelFull,
			wantActions:  1,
			wantTransfer: true,
		},
		{
			name:         "worker sweep skips mandates for guided users",
			eventType:    EventWorkerSweep,
			controlLevel: entities.ControlLevelGuided,
			wantActions:  0,
			wantTransfer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, transfer, repo := buildTestOrchestrator(t, tt.controlLevel)

			result, err := o.Evaluate(context.Background(), uuid.New(), tt.eventType)
			assert.NoError(t, err)
			assert.NotNil(t, result)

			if tt.wantActions > 0 {
				assert.Equal(t, tt.wantActions, result.ActionsExecuted,
					"mandate execution should have run for eligible legacy event")
				assert.Equal(t, 1, transfer.toStashCalls,
					"TransferSpendingToStash should have been called once")
				assert.Equal(t, 1, repo.executed,
					"MarkMandateExecuted should have been called once")
			} else {
				assert.Zero(t, result.ActionsExecuted,
					"mandate execution must not run for this event/level combination")
				assert.Zero(t, transfer.toStashCalls,
					"TransferSpendingToStash must not be called")
				assert.Zero(t, repo.executed,
					"MarkMandateExecuted must not be called")
			}
		})
	}
}
