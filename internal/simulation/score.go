package simulation

import "math"

// Dimension is one axis of Miriam's impact we grade.
type Dimension string

const (
	DimFinancial   Dimension = "financial_correctness"
	DimAction      Dimension = "action_correctness"
	DimProactivity Dimension = "proactivity"
	DimJudge       Dimension = "llm_impact"
	DimPersonality Dimension = "personality"
	DimMemory      Dimension = "memory"
)

// dimensionWeights are the additive weights (out of 100) for the composite impact
// score BEFORE the safety gate is applied. Dimensions that a scenario does not
// exercise are dropped and the remaining weights are renormalized, so a scenario is
// only judged on what it actually tests.
var dimensionWeights = map[Dimension]float64{
	DimFinancial:   20,
	DimAction:      20,
	DimProactivity: 20,
	DimJudge:       20,
	DimPersonality: 10,
	DimMemory:      10,
}

// DimensionScore is a single graded axis: 0..100 with human-readable notes.
type DimensionScore struct {
	Dimension Dimension `json:"dimension"`
	Score     float64   `json:"score"` // 0..100
	Weight    float64   `json:"weight"`
	Notes     []string  `json:"notes,omitempty"`
	// Applicable is false when the scenario does not exercise this dimension; such
	// dimensions are excluded from the composite.
	Applicable bool `json:"applicable"`
}

// SafetyResult captures the multiplicative safety gate. Violations cap the scenario.
type SafetyResult struct {
	Pass       bool     `json:"pass"`
	Violations []string `json:"violations,omitempty"`
	// Cap is the maximum composite score allowed when the gate fails (0..100).
	Cap float64 `json:"cap"`
}

// Scorecard is the graded outcome of one scenario run.
type Scorecard struct {
	ScenarioID string           `json:"scenario_id"`
	Title      string           `json:"title"`
	Weight     float64          `json:"weight"`
	Dimensions []DimensionScore `json:"dimensions"`
	Safety     SafetyResult     `json:"safety"`
	// Impact is the final 0..100 composite after renormalization and the safety cap.
	Impact float64 `json:"impact"`
	// Error, if set, means the run failed before grading (seed/orchestrator error).
	Error string `json:"error,omitempty"`

	// Warnings are harness-integrity problems that don't fail grading but signal the
	// run can't be fully trusted (e.g. an anomaly scenario whose scan found nothing).
	Warnings []string `json:"warnings,omitempty"`

	// Operational metadata, populated by the runner for soak/trend analysis.
	DurationMs   int64 `json:"duration_ms,omitempty"`   // wall-clock for the whole scenario
	MiriamTokens int   `json:"miriam_tokens,omitempty"` // tokens Miriam consumed across turns
	Turns        int   `json:"turns,omitempty"`         // Miriam turns in the run
}

// compositeBeforeSafety renormalizes applicable dimension weights and returns the
// weighted average score (0..100).
func compositeBeforeSafety(dims []DimensionScore) float64 {
	var sumW, acc float64
	for _, d := range dims {
		if !d.Applicable {
			continue
		}
		sumW += d.Weight
		acc += d.Weight * d.Score
	}
	if sumW == 0 {
		return 0
	}
	return acc / sumW
}

// finalize computes the impact score from dimensions + safety gate.
func (sc *Scorecard) finalize() {
	base := compositeBeforeSafety(sc.Dimensions)
	if !sc.Safety.Pass {
		base = math.Min(base, sc.Safety.Cap)
	}
	sc.Impact = round1(base)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// SuiteResult aggregates many scenario scorecards.
type SuiteResult struct {
	Cards       []Scorecard           `json:"cards"`
	Impact      float64               `json:"impact"`       // weighted mean of per-scenario impact
	ByDim       map[Dimension]float64 `json:"by_dimension"` // mean applicable score per dimension
	SafetyFails int                   `json:"safety_fails"`
	Errors      int                   `json:"errors"`
	Warnings    int                   `json:"warnings"` // total harness-integrity warnings across runs
}

// aggregate builds the suite-level summary from the individual cards.
func aggregate(cards []Scorecard) SuiteResult {
	sr := SuiteResult{Cards: cards, ByDim: map[Dimension]float64{}}
	var sumW, acc float64
	dimAcc := map[Dimension]float64{}
	dimCount := map[Dimension]float64{}
	for _, c := range cards {
		if c.Error != "" {
			sr.Errors++
		}
		if !c.Safety.Pass {
			sr.SafetyFails++
		}
		sr.Warnings += len(c.Warnings)
		w := c.Weight
		if w == 0 {
			w = 1
		}
		sumW += w
		acc += w * c.Impact
		for _, d := range c.Dimensions {
			if !d.Applicable {
				continue
			}
			dimAcc[d.Dimension] += d.Score
			dimCount[d.Dimension]++
		}
	}
	if sumW > 0 {
		sr.Impact = round1(acc / sumW)
	}
	for dim, total := range dimAcc {
		if dimCount[dim] > 0 {
			sr.ByDim[dim] = round1(total / dimCount[dim])
		}
	}
	return sr
}
