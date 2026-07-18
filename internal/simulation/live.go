package simulation

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Observer receives live events as the simulation runs. All methods are called
// sequentially (no locking needed by callers). A nil observer is a no-op.
type Observer interface {
	ScenarioStart(sc *Scenario, idx, total int)
	PersonaTurn(msg string)
	MiriamTurn(turn Turn)
	AnomalySurfaced(count int)
	ScenarioDone(card Scorecard)
	SuiteDone(res SuiteResult)
}

// styler wraps ANSI escape sequences. When enabled is false every method is a
// passthrough so plain-text output is clean.
type styler struct{ enabled bool }

func (s styler) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}
func (s styler) bold(t string) string   { return s.wrap("1", t) }
func (s styler) dim(t string) string    { return s.wrap("2", t) }
func (s styler) gray(t string) string   { return s.wrap("90", t) }
func (s styler) green(t string) string  { return s.wrap("32", t) }
func (s styler) red(t string) string    { return s.wrap("31", t) }
func (s styler) yellow(t string) string { return s.wrap("33", t) }
func (s styler) cyan(t string) string   { return s.wrap("36", t) }

// ChatTurn is a Miriam reply surfaced to the live reporter.
type ChatTurn struct {
	Message  string
	Latency  time.Duration
	Tokens   int
	Provider string
}

// LiveReporter prints a clean, minimal simulation report to a terminal as it runs.
// Debug noise, quality-repair loops, and anomaly-engine internals are suppressed —
// only the user/Miriam chat turns and final score are shown.
type LiveReporter struct {
	w    io.Writer
	mu   sync.Mutex
	st   styler
	turn int
}

// NewLiveReporter returns a reporter that writes to w. If color is false, ANSI
// escape sequences are stripped.
func NewLiveReporter(w io.Writer, color bool) *LiveReporter {
	return &LiveReporter{w: w, st: styler{enabled: color}}
}

// ---- Observer implementation ----

func (r *LiveReporter) ScenarioStart(sc *Scenario, idx, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn = 0
	fmt.Fprintln(r.w)
	fmt.Fprintln(r.w, r.st.bold(strings.Repeat("─", 64)))
	header := fmt.Sprintf("  %s", sc.Title)
	if sc.Persona.Goal != "" {
		header += "  —  " + sc.Persona.Goal
	}
	fmt.Fprintln(r.w, r.st.bold(header))
	fmt.Fprintln(r.w, r.st.dim(fmt.Sprintf("  Scenario %d/%d · %d turns", idx+1, total, sc.Persona.MaxTurns)))
	fmt.Fprintln(r.w, r.st.bold(strings.Repeat("─", 64)))
	fmt.Fprintln(r.w)
}

func (r *LiveReporter) PersonaTurn(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn++
	text := softWrap(msg, 58)
	prefix := fmt.Sprintf("  [%d] You", r.turn)
	fmt.Fprintln(r.w, r.st.cyan(prefix))
	for _, l := range text {
		fmt.Fprintf(r.w, "  %s\n", l)
	}
	fmt.Fprintln(r.w)
}

func (r *LiveReporter) MiriamTurn(turn Turn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn++
	text := softWrap(turn.Text, 58)
	prefix := fmt.Sprintf("  [%d] Miriam", r.turn)
	fmt.Fprintln(r.w, r.st.green(prefix))
	for _, l := range text {
		fmt.Fprintf(r.w, "  %s\n", l)
	}
	if turn.LatencyMs > 0 || turn.Tokens > 0 {
		meta := fmt.Sprintf("  %s", r.st.dim(turn.Provider))
		if turn.LatencyMs > 0 {
			meta += r.st.dim(fmt.Sprintf("  %s", (time.Duration(turn.LatencyMs) * time.Millisecond).Truncate(time.Millisecond)))
		}
		if turn.Tokens > 0 {
			meta += r.st.dim(fmt.Sprintf("  %dtok", turn.Tokens))
		}
		fmt.Fprintln(r.w, meta)
	}
	fmt.Fprintln(r.w)
}

func (r *LiveReporter) AnomalySurfaced(_ int) {} // suppressed — noisy
func (r *LiveReporter) ScenarioDone(card Scorecard) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bar := impactBar(card.Impact, 30)
	fmt.Fprintln(r.w, r.st.bold(strings.Repeat("─", 64)))
	score := fmt.Sprintf("  Score: %s", r.st.bold(scoreColor(r.st, card.Impact, fmt.Sprintf("%.0f / 100", card.Impact))))
	fmt.Fprintln(r.w, score)
	fmt.Fprintf(r.w, "  %s\n", colorBar(r.st, card.Impact, bar))

	if card.Error != "" {
		fmt.Fprintf(r.w, "  %s %s\n", r.st.red("✗"), "run error: "+truncate(card.Error, 50))
	}
	if !card.Safety.Pass {
		viol := ""
		if len(card.Safety.Violations) > 0 {
			viol = " — " + card.Safety.Violations[0]
		}
		fmt.Fprintf(r.w, "  %s %s\n", r.st.red("✗"), "safety FAIL"+viol)
	}
	for _, w := range card.Warnings {
		fmt.Fprintf(r.w, "  %s %s\n", r.st.yellow("⚠"), w)
	}
	fmt.Fprintln(r.w, r.st.bold(strings.Repeat("─", 64)))
}

func (r *LiveReporter) SuiteDone(_ SuiteResult) {} // final card rendered separately

// ---- helpers ----

// softWrap breaks text into lines of at most width runes, breaking at spaces.
func softWrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	lines = append(lines, cur)
	return lines
}

// impactBar returns a string of full/empty blocks representing a 0-100 score.
func impactBar(score float64, width int) string {
	filled := int(score / 100 * float64(width))
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// colorBar wraps the bar string in green/yellow/red based on the score.
func colorBar(st styler, score float64, bar string) string {
	switch {
	case score >= 70:
		return st.green(bar)
	case score >= 45:
		return st.yellow(bar)
	default:
		return st.red(bar)
	}
}

// nowStamp formats a time for the footer.
func nowStamp(t time.Time) string {
	return t.Format("2006-01-02 15:04 MST")
}

var _ Observer = (*LiveReporter)(nil)
