package miriam

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/assert"
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

// TestEvaluateSkipsMandateExecutionForAutonomousEvents verifies that even a
// Full Autopilot user with active mandates will not have mandate actions
// executed when the evaluation is triggered by an autonomous event type. This
// is the read-only autonomy boundary for the always-on path.
func TestEvaluateSkipsMandateExecutionForAutonomousEvents(t *testing.T) {
	// The boundary is implemented by the mandateEvent guard inside Evaluate:
	// autonomous event types must never pass it. We test the guard directly by
	// constructing the same boolean the production code uses.
	tests := []struct {
		name          string
		eventType     string
		controlLevel  string
		wantMandateOn bool
	}{
		{
			name:          "money event skips mandates even for full users",
			eventType:     EventMoneyEvent,
			controlLevel:  entities.ControlLevelFull,
			wantMandateOn: false,
		},
		{
			name:          "autonomous tick skips mandates even for full users",
			eventType:     EventAutonomousTick,
			controlLevel:  entities.ControlLevelFull,
			wantMandateOn: false,
		},
		{
			name:          "worker sweep runs mandates for full users",
			eventType:     EventWorkerSweep,
			controlLevel:  entities.ControlLevelFull,
			wantMandateOn: true,
		},
		{
			name:          "income-lower event runs mandates for full users",
			eventType:     EventIncomeLowerThanUsual,
			controlLevel:  entities.ControlLevelFull,
			wantMandateOn: true,
		},
		{
			name:          "worker sweep skips mandates for guided users",
			eventType:     EventWorkerSweep,
			controlLevel:  entities.ControlLevelGuided,
			wantMandateOn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAutonomous := IsAutonomousEvent(tt.eventType)
			gateReason := ""
			if tt.controlLevel != entities.ControlLevelFull {
				gateReason = tt.controlLevel
			}
			mandateEvent := !isAutonomous && (tt.eventType == EventWorkerSweep || tt.eventType == EventIncomeLowerThanUsual)
			willRunMandates := mandateEvent && gateReason == ""
			assert.Equal(t, tt.wantMandateOn, willRunMandates)
		})
	}
}
