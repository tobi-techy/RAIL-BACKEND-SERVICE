package simulation

import "testing"

// TestScenariosLoad ensures every shipped scenario YAML parses and validates. It runs
// in ordinary `go test` (no database needed).
func TestScenariosLoad(t *testing.T) {
	scs, err := LoadScenarios("../../test/simulation/scenarios", "")
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if len(scs) < 12 {
		t.Fatalf("expected >=12 scenarios, got %d", len(scs))
	}
	seen := map[string]bool{}
	for _, sc := range scs {
		if seen[sc.ID] {
			t.Errorf("duplicate scenario id %q", sc.ID)
		}
		seen[sc.ID] = true
		if sc.Persona.MaxTurns <= 0 {
			t.Errorf("%s: max_turns must be positive after defaulting", sc.ID)
		}
	}
}
