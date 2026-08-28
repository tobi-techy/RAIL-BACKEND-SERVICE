package miriam

import (
	"testing"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestResolvePhase(t *testing.T) {
	tests := []struct {
		name         string
		activeMonths int
		calibration  int // 0-100
		want         Phase
	}{
		{"nil state", 0, 0, PhaseObserver},
		{"month 1", 1, 0, PhaseObserver},
		{"month 2", 2, 50, PhaseObserver},
		{"month 3 enters reader", 3, 50, PhaseReader},
		{"month 5 still reader", 5, 80, PhaseReader},
		{"month 6 high accuracy = confidant", 6, 75, PhaseConfidant},
		{"month 6 low accuracy = humble vet", 6, 60, PhaseHumbleVet},
		{"month 12 high accuracy", 12, 85, PhaseConfidant},
		{"month 12 borderline accuracy = humble vet", 12, 70, PhaseHumbleVet},
		{"month 8 just above threshold", 8, 71, PhaseConfidant},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.activeMonths == 0 && tt.calibration == 0 {
				assert.Equal(t, PhaseObserver, ResolvePhase(nil))
				return
			}
			state := &entities.MiriamMoneyState{
				ActiveMonths:    tt.activeMonths,
				CalibrationScore: decimal.NewFromInt(int64(tt.calibration)),
			}
			assert.Equal(t, tt.want, ResolvePhase(state))
		})
	}
}

func TestPhaseString(t *testing.T) {
	assert.Equal(t, "observer", PhaseObserver.String())
	assert.Equal(t, "reader", PhaseReader.String())
	assert.Equal(t, "confidant", PhaseConfidant.String())
	assert.Equal(t, "humble_vet", PhaseHumbleVet.String())
}

func TestPhaseMessage_ReturnsNonEmpty(t *testing.T) {
	vars := MessageVars{
		Spend:       "$500",
		Stash:       "$200",
		Amount:      "$50",
		Obligations: "$300",
		Safe:        "$20",
		Runway:      14,
		Target:      "$1000",
		Pct:         20,
		Remaining:   "$800",
		Gap:         "$100",
	}

	phases := []Phase{PhaseObserver, PhaseReader, PhaseConfidant, PhaseHumbleVet}
	msgTypes := []MessageType{
		MsgPredictionCashShortfall,
		MsgPredictionBillPressure,
		MsgPredictionIncomeGap,
		MsgPredictionSpendingAnomaly,
		MsgPredictionIdleSurplus,
		MsgPredictionStashOpportunity,
		MsgBillWarning,
		MsgGoalProgress,
		MsgAutopilotAction,
	}

	for _, phase := range phases {
		for _, msgType := range msgTypes {
			msg := PhaseMessage(phase, msgType, vars)
			assert.NotEmpty(t, msg, "phase=%s msgType=%d", phase, msgType)
		}
	}
}

func TestPhaseMessage_FallsBackToReader(t *testing.T) {
	// An undefined phase should fallback to reader templates
	vars := MessageVars{Amount: "$50", Spend: "$100", Obligations: "$200"}
	msg := PhaseMessage(Phase(99), MsgPredictionCashShortfall, vars)
	assert.NotEmpty(t, msg)
}

func TestNudgeFrequencyLimit(t *testing.T) {
	assert.Equal(t, 1, NudgeFrequencyLimit(PhaseObserver))
	assert.Equal(t, 2, NudgeFrequencyLimit(PhaseReader))
	assert.Equal(t, 3, NudgeFrequencyLimit(PhaseConfidant))
	assert.Equal(t, 3, NudgeFrequencyLimit(PhaseHumbleVet))
}

func TestGreetingForPhase(t *testing.T) {
	tests := []struct {
		phase     Phase
		name      string
		timeOfDay string
		contains  []string
	}{
		{PhaseObserver, "Tobi", "morning", []string{"Tobi", "miriam", "still getting to know"}},
		{PhaseObserver, "", "morning", []string{"miriam", "still getting to know"}},
		{PhaseReader, "Ade", "morning", []string{"Ade", "been watching the numbers"}},
		{PhaseReader, "", "evening", []string{"let's look at today"}},
		{PhaseConfidant, "Tobi", "morning", []string{"morning", "Tobi"}},
		{PhaseConfidant, "", "evening", []string{"evening"}},
		{PhaseConfidant, "Ade", "night", []string{"late one", "Ade"}},
		{PhaseHumbleVet, "Jo", "morning", []string{"morning", "Jo", "miriam here"}},
	}

	for _, tt := range tests {
		t.Run(tt.phase.String()+"_"+tt.timeOfDay+"_"+tt.name, func(t *testing.T) {
			greeting := GreetingForPhase(tt.phase, tt.name, tt.timeOfDay)
			for _, want := range tt.contains {
				assert.Contains(t, greeting, want)
			}
		})
	}
}

func TestBuildVarsFromState(t *testing.T) {
	state := &entities.MiriamMoneyState{
		UpcomingObligations: decimal.NewFromInt(300),
		SafeToSpendDaily:    decimal.NewFromInt(20),
		LiquidityRunwayDays: 14,
		StashTarget:         decimal.NewFromInt(1000),
	}
	vars := BuildVarsFromState(state, decimal.NewFromInt(500), decimal.NewFromInt(200), decimal.NewFromInt(50), "$")

	assert.Equal(t, "$500", vars.Spend)
	assert.Equal(t, "$200", vars.Stash)
	assert.Equal(t, "$50", vars.Amount)
	assert.Equal(t, "$300", vars.Obligations)
	assert.Equal(t, "$20", vars.Safe)
	assert.Equal(t, 14, vars.Runway)
	assert.Equal(t, "$1000", vars.Target)
}

func TestBuildVarsFromState_NGN(t *testing.T) {
	state := &entities.MiriamMoneyState{
		UpcomingObligations: decimal.NewFromInt(300),
		SafeToSpendDaily:    decimal.NewFromInt(20),
		LiquidityRunwayDays: 14,
		StashTarget:         decimal.NewFromInt(1000),
	}
	vars := BuildVarsFromState(state, decimal.NewFromInt(500), decimal.NewFromInt(200), decimal.NewFromInt(50), "₦")

	assert.Equal(t, "₦500", vars.Spend)
	assert.Equal(t, "₦200", vars.Stash)
	assert.Equal(t, "₦50", vars.Amount)
	assert.Equal(t, "₦300", vars.Obligations)
	assert.Equal(t, "₦20", vars.Safe)
	assert.Equal(t, 14, vars.Runway)
	assert.Equal(t, "₦1000", vars.Target)
}

func TestPhaseContext_ReturnsNonEmptyForAllPhases(t *testing.T) {
	states := []*entities.MiriamMoneyState{
		{ActiveMonths: 1, CalibrationScore: decimal.NewFromInt(0)},
		{ActiveMonths: 4, CalibrationScore: decimal.NewFromInt(60)},
		{ActiveMonths: 8, CalibrationScore: decimal.NewFromInt(80)},
		{ActiveMonths: 8, CalibrationScore: decimal.NewFromInt(50)},
	}
	expected := []string{"OBSERVER", "READER", "CONFIDANT", "HUMBLE VET"}

	for i, state := range states {
		ctx := PhaseContext(state)
		assert.Contains(t, ctx, expected[i])
		assert.Contains(t, ctx, "BLUNTNESS")
		assert.Contains(t, ctx, "VOICE EXAMPLES")
	}
}
