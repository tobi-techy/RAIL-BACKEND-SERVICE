package simulation

import (
	"context"
	"sort"
	"time"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// Runner executes scenarios against a booted Harness and grades each one.
type Runner struct {
	h       *Harness
	persona *PersonaDriver
	judge   *Judge
	obs     Observer // optional live observer; nil when not streaming
}

// RunnerOptions configures a Runner.
type RunnerOptions struct {
	// PersonaLLM drives the simulated user. Defaults to the container's provider
	// manager. Set to a stub for offline/deterministic runs.
	PersonaLLM LLM
	// JudgeLLM scores the subjective impact dimension. Defaults to the provider
	// manager. When nil, the judge dimension is skipped.
	JudgeLLM LLM
	// Observer, if set, receives live events as scenarios and turns run.
	Observer Observer
}

// NewRunner builds a Runner. When option LLMs are nil it falls back to the real
// provider manager from the container (so real Miriam is judged by a real model).
func NewRunner(h *Harness, opts RunnerOptions) *Runner {
	var pm LLM
	if h.container.AIProviderManager != nil {
		pm = h.container.AIProviderManager
	}
	personaLLM := opts.PersonaLLM
	if personaLLM == nil {
		personaLLM = pm
	}
	judgeLLM := opts.JudgeLLM
	if judgeLLM == nil {
		judgeLLM = pm
	}
	return &Runner{
		h:       h,
		persona: NewPersonaDriver(personaLLM),
		judge:   NewJudge(judgeLLM),
		obs:     opts.Observer,
	}
}

// RunScenario seeds, drives, and grades a single scenario, returning its scorecard.
func (r *Runner) RunScenario(ctx context.Context, sc *Scenario) Scorecard {
	card := Scorecard{ScenarioID: sc.ID, Title: sc.Title, Weight: sc.Weight}

	start := time.Now()
	eng := newScenarioEngine(r.h, r.persona)
	eng.obs = r.obs
	res := eng.Run(ctx, sc)
	card.DurationMs = time.Since(start).Milliseconds()
	card.MiriamTokens, card.Turns = accountMiriamUsage(res)
	card.Warnings = res.Warnings
	if res.Err != nil {
		card.Error = res.Err.Error()
		card.Safety = SafetyResult{Pass: false, Cap: 0, Violations: []string{"run error: " + res.Err.Error()}}
		card.finalize()
		if r.obs != nil {
			r.obs.ScenarioDone(card)
		}
		return card
	}

	dims, safety := gradeDeterministic(res)
	dims = append(dims, r.judge.Grade(ctx, res))

	card.Dimensions = dims
	card.Safety = safety
	card.finalize()
	if r.obs != nil {
		r.obs.ScenarioDone(card)
	}
	return card
}

// accountMiriamUsage sums Miriam's token cost and counts her turns for cost metering.
func accountMiriamUsage(res *RunResult) (tokens, turns int) {
	for _, t := range res.Transcript {
		if t.Role == roleMiriam {
			turns++
			tokens += t.Tokens
		}
	}
	return tokens, turns
}

// RunSuite runs every scenario in order and returns the aggregate result.
func (r *Runner) RunSuite(ctx context.Context, scenarios []*Scenario) SuiteResult {
	// Stable order by id for reproducible reports.
	sort.SliceStable(scenarios, func(i, j int) bool { return scenarios[i].ID < scenarios[j].ID })

	cards := make([]Scorecard, 0, len(scenarios))
	for i, sc := range scenarios {
		if r.obs != nil {
			r.obs.ScenarioStart(sc, i+1, len(scenarios))
		}
		cards = append(cards, r.RunScenario(ctx, sc))
	}
	res := aggregate(cards)
	if r.obs != nil {
		r.obs.SuiteDone(res)
	}
	return res
}

// compile-time assurance the provider manager satisfies the LLM surface.
var _ LLM = (*infraai.ProviderManager)(nil)
