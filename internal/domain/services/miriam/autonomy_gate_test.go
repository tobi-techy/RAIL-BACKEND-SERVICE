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
