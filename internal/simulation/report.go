package simulation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// WriteJSON serializes the suite result to w (for baselines and CI artifacts).
func WriteJSON(w io.Writer, sr SuiteResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(sr)
}

// SaveJSON writes the suite result to a file path.
func SaveJSON(path string, sr SuiteResult) error {
	f, err := os.Create(path) //nolint:gosec // operator-supplied output path, test tooling
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return WriteJSON(f, sr)
}

// LoadBaseline reads a previously saved suite result for regression comparison.
func LoadBaseline(path string) (*SuiteResult, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, err
	}
	var sr SuiteResult
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, err
	}
	return &sr, nil
}

// RenderTable prints a human-readable scorecard to w.
func RenderTable(w io.Writer, sr SuiteResult, baseline *SuiteResult) {
	fmt.Fprintln(w, "\n=== Miriam Simulation Scorecard ===")
	fmt.Fprintf(w, "Overall Impact: %.1f / 100", sr.Impact)
	if baseline != nil {
		fmt.Fprintf(w, "   (baseline %.1f, %s)", baseline.Impact, deltaStr(sr.Impact-baseline.Impact))
	}
	fmt.Fprintf(w, "\nScenarios: %d   Safety fails: %d   Errors: %d\n\n", len(sr.Cards), sr.SafetyFails, sr.Errors)

	// Per-scenario rows.
	fmt.Fprintf(w, "%-28s %8s  %-8s %s\n", "SCENARIO", "IMPACT", "SAFETY", "NOTES")
	fmt.Fprintln(w, strings.Repeat("-", 88))
	for _, c := range sr.Cards {
		safety := "ok"
		if !c.Safety.Pass {
			safety = "FAIL"
		}
		note := ""
		if c.Error != "" {
			note = "error: " + truncate(c.Error, 40)
		} else if !c.Safety.Pass && len(c.Safety.Violations) > 0 {
			note = truncate(c.Safety.Violations[0], 44)
		} else {
			note = weakestDim(c)
		}
		base := ""
		if baseline != nil {
			if bc, ok := findCard(baseline, c.ScenarioID); ok {
				base = " " + deltaStr(c.Impact-bc.Impact)
			}
		}
		fmt.Fprintf(w, "%-28s %7.1f%s  %-8s %s\n", truncate(c.ScenarioID, 28), c.Impact, base, safety, note)
	}

	// Per-dimension summary.
	fmt.Fprintln(w, "\nBy dimension (mean of applicable scenarios):")
	dims := make([]Dimension, 0, len(sr.ByDim))
	for d := range sr.ByDim {
		dims = append(dims, d)
	}
	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
	for _, d := range dims {
		fmt.Fprintf(w, "  %-22s %5.1f\n", d, sr.ByDim[d])
	}
	fmt.Fprintln(w)
}

// RegressionExit returns a non-zero-worthy reason if the suite fails thresholds or
// regresses against the baseline beyond the tolerance band, else "".
func RegressionExit(sr SuiteResult, baseline *SuiteResult, minImpact float64, band float64) string {
	if sr.SafetyFails > 0 {
		return fmt.Sprintf("%d scenario(s) failed the safety gate", sr.SafetyFails)
	}
	if sr.Errors > 0 {
		return fmt.Sprintf("%d scenario(s) errored", sr.Errors)
	}
	if minImpact > 0 && sr.Impact < minImpact {
		return fmt.Sprintf("overall impact %.1f below threshold %.1f", sr.Impact, minImpact)
	}
	if baseline != nil && sr.Impact < baseline.Impact-band {
		return fmt.Sprintf("overall impact %.1f regressed >%.1f below baseline %.1f", sr.Impact, band, baseline.Impact)
	}
	return ""
}

func findCard(sr *SuiteResult, id string) (Scorecard, bool) {
	for _, c := range sr.Cards {
		if c.ScenarioID == id {
			return c, true
		}
	}
	return Scorecard{}, false
}

func weakestDim(c Scorecard) string {
	worst := ""
	worstScore := 101.0
	for _, d := range c.Dimensions {
		if d.Applicable && d.Score < worstScore {
			worstScore = d.Score
			worst = string(d.Dimension)
		}
	}
	if worst == "" {
		return ""
	}
	return fmt.Sprintf("weakest: %s %.0f", worst, worstScore)
}

func deltaStr(delta float64) string {
	switch {
	case delta > 0.05:
		return fmt.Sprintf("+%.1f", delta)
	case delta < -0.05:
		return fmt.Sprintf("%.1f", delta)
	default:
		return "=="
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// RenderSoakSummary prints the final tally of a continuous soak run.
func RenderSoakSummary(w io.Writer, s SoakSummary) {
	fmt.Fprintln(w, "\n──────── Miriam soak summary ────────")
	fmt.Fprintf(w, "soak id     : %s\n", s.SoakID)
	fmt.Fprintf(w, "stop reason : %s\n", s.StopReason)
	fmt.Fprintf(w, "completed   : %d runs\n", s.Completed)
	fmt.Fprintf(w, "errors      : %d\n", s.Errors)
	fmt.Fprintf(w, "safety fails: %d\n", s.SafetyFails)
	fmt.Fprintf(w, "mean impact : %.1f / 100\n", s.MeanImpact)
	fmt.Fprintf(w, "LLM tokens  : %d (est $%.2f over %d calls)\n", s.Budget.Tokens, s.Budget.EstimatedUSD, s.Budget.Calls)
	fmt.Fprintf(w, "elapsed     : %s\n", s.Elapsed)
	if s.Budget.StopNote != "" {
		fmt.Fprintf(w, "budget note : %s\n", s.Budget.StopNote)
	}
	fmt.Fprintln(w, "─────────────────────────────────────")
}
