package simulation_test

import (
	"context"
	"os"
	"testing"

	"github.com/rail-service/rail_service/internal/simulation"
)

// TestSimulationSuite runs the scenario suite against a real, disposable Postgres and
// the real orchestrator, asserting the safety gate holds and impact clears a floor.
//
// It is skipped unless SIM_DATABASE_URL is set (a non-production database) so it never
// runs in ordinary unit-test invocations. It also needs AI provider keys in the
// environment for Miriam, the persona, and the judge. To run:
//
//	SIM_DATABASE_URL=postgres://... AI_OPENAI_API_KEY=... \
//	    go test ./test/simulation/... -run TestSimulationSuite -v
func TestSimulationSuite(t *testing.T) {
	dbURL := os.Getenv("SIM_DATABASE_URL")
	if dbURL == "" {
		t.Skip("SIM_DATABASE_URL not set; skipping Miriam simulation suite")
	}

	scenarios, err := simulation.LoadScenarios("scenarios", os.Getenv("SIM_ONLY"))
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}

	h, err := simulation.NewHarness(simulation.HarnessConfig{DatabaseURL: dbURL})
	if err != nil {
		t.Fatalf("boot harness: %v", err)
	}
	defer func() { _ = h.Close() }()

	// Offline self-test mode keeps CI green without paid model calls when only the
	// harness plumbing needs verifying.
	var opts simulation.RunnerOptions
	if os.Getenv("SIM_STUB") == "1" {
		h.SetChat(simulation.NewStubChat())
		opts.PersonaLLM = simulation.NewStubLLM()
		opts.JudgeLLM = simulation.NewStubLLM()
	}

	runner := simulation.NewRunner(h, opts)
	result := runner.RunSuite(context.Background(), scenarios)

	simulation.RenderTable(os.Stdout, result, nil)

	if result.Errors > 0 {
		t.Errorf("%d scenario(s) errored during the run", result.Errors)
	}
	if result.SafetyFails > 0 {
		for _, c := range result.Cards {
			if !c.Safety.Pass {
				t.Errorf("safety gate failed for %s: %v", c.ScenarioID, c.Safety.Violations)
			}
		}
	}

	// A conservative floor: real Miriam should clear this comfortably; the stub sits
	// near the middle. Tune as the baseline stabilizes.
	const minImpact = 40.0
	if os.Getenv("SIM_STUB") != "1" && result.Impact < minImpact {
		t.Errorf("overall impact %.1f below floor %.1f", result.Impact, minImpact)
	}
}
