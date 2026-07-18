package simulation

import (
	"context"
	"testing"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// countingLLM is a trivial LLM test double: it records calls and returns a fixed
// token cost. Used to assert the metered wrapper's budget behavior.
type countingLLM struct{ calls int }

func (c *countingLLM) Name() string { return "counting" }

func (c *countingLLM) ChatCompletion(_ context.Context, _ *infraai.ChatRequest) (*infraai.ChatResponse, error) {
	c.calls++
	return &infraai.ChatResponse{Content: "ok", TokensUsed: 10, Provider: "counting"}, nil
}

// TestGeneratorDeterminism verifies that the same seed produces the same scenario
// sequence (reproducible soaks) and that generated scenarios always validate and are
// distinct within a reasonable window.
func TestGeneratorDeterminism(t *testing.T) {
	a := NewGenerator(GeneratorConfig{Seed: 42})
	b := NewGenerator(GeneratorConfig{Seed: 42})

	seen := map[string]bool{}
	dupWindow := 0
	for i := 0; i < 200; i++ {
		sa := a.Next()
		sb := b.Next()
		if sa.ID != sb.ID {
			t.Fatalf("run %d: same seed diverged: %q vs %q", i, sa.ID, sb.ID)
		}
		if err := sa.Validate(); err != nil {
			t.Fatalf("run %d: generated invalid scenario: %v", i, err)
		}
		if seen[sa.ID] {
			dupWindow++
		}
		seen[sa.ID] = true
	}
	// Some collisions across 200 fuzzed draws are acceptable, but the space must be
	// broad — expect a large majority to be unique.
	if len(seen) < 150 {
		t.Fatalf("generator space too narrow: only %d unique of 200", len(seen))
	}
}

// TestGeneratorDifferentSeeds ensures different seeds explore different sequences.
func TestGeneratorDifferentSeeds(t *testing.T) {
	a := NewGenerator(GeneratorConfig{Seed: 1}).Take(50)
	b := NewGenerator(GeneratorConfig{Seed: 2}).Take(50)
	same := 0
	for i := range a {
		if a[i].ID == b[i].ID {
			same++
		}
	}
	if same == 50 {
		t.Fatalf("different seeds produced identical sequences")
	}
}

// TestGeneratorArchetypeSubset ensures the generator honors an archetype allowlist.
func TestGeneratorArchetypeSubset(t *testing.T) {
	g := NewGenerator(GeneratorConfig{Seed: 7, Archetypes: []Archetype{ArchHallucination}})
	for i := 0; i < 20; i++ {
		sc := g.Next()
		if archetypeOf(sc) != string(ArchHallucination) {
			t.Fatalf("expected only hallucination archetype, got %q (id=%s)", archetypeOf(sc), sc.ID)
		}
	}
}

// TestGovernorTokenCap verifies the governor stops at the token cap and that a metered
// LLM refuses further calls once exhausted.
func TestGovernorTokenCap(t *testing.T) {
	gov := NewGovernor(BudgetConfig{MaxTokens: 100})
	if gov.Exhausted() {
		t.Fatal("fresh governor should not be exhausted")
	}
	gov.Charge(60)
	if gov.Exhausted() {
		t.Fatal("should not be exhausted at 60/100")
	}
	gov.Charge(60) // now 120 >= 100
	if !gov.Exhausted() {
		t.Fatal("should be exhausted at 120/100")
	}

	metered := NewMeteredLLM(&countingLLM{}, gov)
	_, err := metered.ChatCompletion(context.Background(), nil)
	if err != ErrBudgetExceeded {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
}

// TestGovernorDollarCap verifies the dollar cap trips via the token->USD estimate.
func TestGovernorDollarCap(t *testing.T) {
	// $0.01 per 1k tokens, cap $0.02 → trips at 2000 tokens.
	gov := NewGovernor(BudgetConfig{MaxUSD: 0.02, USDPer1kTokens: 0.01})
	gov.Charge(1000)
	if gov.Exhausted() {
		t.Fatal("should not trip at $0.01")
	}
	gov.Charge(1500) // 2500 tokens => $0.025 >= $0.02
	if !gov.Exhausted() {
		t.Fatal("should trip past the dollar cap")
	}
	snap := gov.Snapshot()
	if snap.StopNote == "" {
		t.Fatal("expected a stop note on the snapshot")
	}
}

// TestMeteredLLMPassthroughNilGov ensures a nil governor yields a passthrough wrapper.
func TestMeteredLLMPassthroughNilGov(t *testing.T) {
	inner := &countingLLM{}
	if got := NewMeteredLLM(inner, nil); got != inner {
		t.Fatal("nil governor should return the inner LLM unchanged")
	}
}
