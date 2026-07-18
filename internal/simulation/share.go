package simulation

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// ShareCardOptions controls the shareable results card.
type ShareCardOptions struct {
	Title   string    // headline, e.g. "Miriam Impact Eval"
	Model   string    // model under test, shown as a subtitle
	GitSHA  string    // optional commit stamp
	When    time.Time // defaults to now
	Color   bool      // ANSI color for terminal; false for plain/file
	Verdict string    // optional one-liner ("Ship it" / "Needs work")
}

// dimLabel maps internal dimension keys to human, share-friendly names.
var dimLabel = map[Dimension]string{
	DimFinancial:   "Financial accuracy",
	DimAction:      "Right action",
	DimProactivity: "Proactivity",
	DimJudge:       "Real-world impact",
	DimPersonality: "Personality",
	DimMemory:      "Memory recall",
}

// RenderShareCard writes a clean, boxed summary of a suite result designed to be
// screenshot-and-share ready. Layout is fixed-width Unicode so it looks crisp in a
// terminal screenshot or a monospace code block on X.
func RenderShareCard(w io.Writer, res SuiteResult, opts ShareCardOptions) {
	st := styler{enabled: opts.Color}
	when := opts.When
	if when.IsZero() {
		when = time.Now()
	}
	title := opts.Title
	if title == "" {
		title = "Miriam Impact Eval"
	}

	const width = 54
	top := "╭" + strings.Repeat("─", width) + "╮"
	bot := "╰" + strings.Repeat("─", width) + "╯"
	sep := "├" + strings.Repeat("─", width) + "┤"

	row := func(s string) { fmt.Fprintln(w, st.gray("│")+s+st.gray("│")) }
	center := func(s string) string { return padCenter(s, width) }
	left := func(s string) string { return " " + padRight(s, width-1) }

	fmt.Fprintln(w, st.gray(top))
	row(st.bold(center(title)))
	sub := opts.Model
	if opts.GitSHA != "" {
		sub = strings.TrimSpace(sub + "  ·  " + opts.GitSHA)
	}
	if sub != "" {
		row(st.dim(center(sub)))
	}
	fmt.Fprintln(w, st.gray(sep))

	// Headline score, big and centered with a bar.
	score := res.Impact
	headline := fmt.Sprintf("%.0f/100", score)
	row(center(st.bold(scoreColor(st, score, headline))))
	row(center(colorBar(st, score, impactBar(score, 30))))
	if opts.Verdict != "" {
		row(st.dim(center(opts.Verdict)))
	}
	fmt.Fprintln(w, st.gray(sep))

	// Per-dimension bars in a stable, readable order.
	dims := dimensionAverages(res)
	order := []Dimension{DimJudge, DimFinancial, DimProactivity, DimAction, DimPersonality, DimMemory}
	for _, d := range order {
		v, ok := dims[d]
		label := dimLabel[d]
		if !ok {
			row(left(fmt.Sprintf("%-18s %s", label, st.dim("— not exercised"))))
			continue
		}
		bar := impactBar(v, 14)
		row(left(fmt.Sprintf("%-18s %s %s", label, colorBar(st, v, bar), st.bold(fmt.Sprintf("%3.0f", v)))))
	}
	fmt.Fprintln(w, st.gray(sep))

	// Tally line.
	pass := len(res.Cards) - res.SafetyFails - res.Errors
	tally := fmt.Sprintf("%d scenarios · %s · %s",
		len(res.Cards),
		safetyPhrase(st, res.SafetyFails),
		st.green(fmt.Sprintf("%d clean", pass)),
	)
	row(center(tally))

	// Best & worst scenario highlight.
	best, worst := bestWorst(res.Cards)
	if best != nil {
		row(left(st.green("▲ best  ") + fmt.Sprintf("%-22s %s", trimID(best.ScenarioID, 22), st.bold(fmt.Sprintf("%.0f", best.Impact)))))
	}
	if worst != nil {
		row(left(st.red("▼ worst ") + fmt.Sprintf("%-22s %s", trimID(worst.ScenarioID, 22), st.bold(fmt.Sprintf("%.0f", worst.Impact)))))
	}

	fmt.Fprintln(w, st.gray(sep))
	row(st.dim(center(nowStamp(when) + "  ·  real orchestrator, synthetic users")))

	// Diagnostic section: explain what's bad and what to fix.
	fails := collectFailures(res)
	if len(fails) > 0 {
		fmt.Fprintln(w, st.gray(sep))
		row(st.bold(center("DIAGNOSIS")))
		for _, f := range fails {
			lines := softWrap(f, width-6)
			for i, l := range lines {
				if i == 0 {
					row(st.red("  ✗ ") + l)
				} else {
					row("      " + l)
				}
			}
		}
	}

	fmt.Fprintln(w, st.gray(bot))
}

// SaveShareCard writes the plain (no-ANSI) share card to a file for sharing.
func SaveShareCard(path string, res SuiteResult, opts ShareCardOptions) error {
	f, err := os.Create(path) //nolint:gosec // operator-supplied output path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	opts.Color = false
	RenderShareCard(f, res, opts)
	return nil
}

// dimensionAverages returns the mean applicable score per dimension across the suite.
func dimensionAverages(res SuiteResult) map[Dimension]float64 {
	sum := map[Dimension]float64{}
	cnt := map[Dimension]float64{}
	for _, c := range res.Cards {
		for _, d := range c.Dimensions {
			if !d.Applicable {
				continue
			}
			sum[d.Dimension] += d.Score
			cnt[d.Dimension]++
		}
	}
	out := map[Dimension]float64{}
	for d, s := range sum {
		if cnt[d] > 0 {
			out[d] = s / cnt[d]
		}
	}
	return out
}

func bestWorst(cards []Scorecard) (best, worst *Scorecard) {
	valid := make([]Scorecard, 0, len(cards))
	for _, c := range cards {
		if c.Error == "" {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Impact > valid[j].Impact })
	b := valid[0]
	wt := valid[len(valid)-1]
	return &b, &wt
}

func safetyPhrase(st styler, fails int) string {
	if fails == 0 {
		return st.green("0 safety fails")
	}
	return st.red(fmt.Sprintf("%d safety fails", fails))
}

func scoreColor(st styler, score float64, s string) string {
	switch {
	case score >= 70:
		return st.green(s)
	case score >= 45:
		return st.yellow(s)
	default:
		return st.red(s)
	}
}

// --- width-aware padding (ANSI-free strings only) ---

func padRight(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	return s + strings.Repeat(" ", n-len(r))
}

func padCenter(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return string(r[:n])
	}
	total := n - len(r)
	l := total / 2
	return strings.Repeat(" ", l) + s + strings.Repeat(" ", total-l)
}

func trimID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// failure is one diagnostic item for the share card's DIAGNOSIS section.
type failure struct {
	scenario string
	reason   string
	fix      string
}

// collectFailures analyses the suite result and returns human-readable failure
// strings that tell the operator what's bad and what to fix.
func collectFailures(res SuiteResult) []string {
	var out []string

	for _, c := range res.Cards {
		if c.Error != "" {
			out = append(out, fmt.Sprintf("%s — run error: %s", trimID(c.ScenarioID, 20), truncate(c.Error, 40)))
			continue
		}

		// Safety failures are top-priority.
		if !c.Safety.Pass {
			viol := ""
			if len(c.Safety.Violations) > 0 {
				viol = ": " + truncate(c.Safety.Violations[0], 44)
			}
			out = append(out, fmt.Sprintf("%s — safety FAIL%s", trimID(c.ScenarioID, 20), viol))
			continue
		}

		// Low overall score — pull the weakest dimension notes.
		if c.Impact < 50 {
			weakNotes := weakestNotes(c)
			if weakNotes != "" {
				out = append(out, fmt.Sprintf("%s — %.0f/100: %s", trimID(c.ScenarioID, 20), c.Impact, weakNotes))
			} else {
				out = append(out, fmt.Sprintf("%s — %.0f/100 (low)", trimID(c.ScenarioID, 20), c.Impact))
			}
		}
	}

	if len(out) == 0 {
		out = append(out, "No critical issues found — review dimension scores for improvement areas.")
	}
	return out
}

// weakestNotes returns a comma-separated summary of the lowest-scoring
// dimension's notes for the share card diagnostic.
func weakestNotes(c Scorecard) string {
	worst := ""
	worstScore := 101.0
	var worstNotes []string
	for _, d := range c.Dimensions {
		if d.Applicable && d.Score < worstScore {
			worstScore = d.Score
			worst = string(d.Dimension)
			worstNotes = d.Notes
		}
	}
	if worst == "" || len(worstNotes) == 0 {
		return ""
	}
	// Take at most 2 notes to keep the card compact.
	note := worstNotes[0]
	if len(worstNotes) > 1 {
		note += "; " + worstNotes[1]
	}
	return fmt.Sprintf("%s (%.0f): %s", dimLabel[Dimension(worst)], worstScore, truncate(note, 56))
}
