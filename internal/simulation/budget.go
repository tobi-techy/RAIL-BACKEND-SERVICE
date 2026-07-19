package simulation

import (
	"context"
	"fmt"
	"sync"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ErrBudgetExceeded is returned by a metered LLM once a governor cap is hit. The soak
// loop treats it as a graceful stop signal rather than a run failure.
var ErrBudgetExceeded = fmt.Errorf("simulation budget exceeded")

// BudgetConfig sets hard caps for a soak run. A zero value means "no limit" for that
// axis, so a run can be bounded by tokens, dollars, or both.
type BudgetConfig struct {
	MaxTokens      int64   // stop after this many total LLM tokens (0 = unlimited)
	MaxUSD         float64 // stop after this estimated spend (0 = unlimited)
	USDPer1kTokens float64 // blended price estimate used to convert tokens -> dollars
}

// Governor tracks cumulative LLM usage across all workers and enforces the budget.
// It is safe for concurrent use.
type Governor struct {
	cfg BudgetConfig

	mu       sync.Mutex
	tokens   int64
	calls    int64
	stopped  bool
	stopNote string
}

// NewGovernor builds a budget governor. A nil-safe zero config is treated as
// unlimited (useful for tests / stub runs).
func NewGovernor(cfg BudgetConfig) *Governor {
	return &Governor{cfg: cfg}
}

// account records usage from one completed LLM call and returns whether the budget is
// now exhausted. It is the single place that mutates counters.
func (g *Governor) account(tokens int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	g.tokens += int64(tokens)
	if g.stopped {
		return true
	}
	if g.cfg.MaxTokens > 0 && g.tokens >= g.cfg.MaxTokens {
		g.stopped = true
		g.stopNote = fmt.Sprintf("token cap reached (%d/%d)", g.tokens, g.cfg.MaxTokens)
		return true
	}
	if g.cfg.MaxUSD > 0 && g.estimatedUSD() >= g.cfg.MaxUSD {
		g.stopped = true
		g.stopNote = fmt.Sprintf("dollar cap reached ($%.2f/$%.2f)", g.estimatedUSD(), g.cfg.MaxUSD)
		return true
	}
	return false
}

// Exhausted reports whether the budget has been hit.
func (g *Governor) Exhausted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}

// Charge bills tokens that were consumed outside a MeteredLLM (e.g. Miriam's own
// orchestrator turns) so the budget reflects total spend. Returns whether the budget
// is now exhausted.
func (g *Governor) Charge(tokens int) bool {
	if tokens <= 0 {
		return g.Exhausted()
	}
	return g.account(tokens)
}

// estimatedUSD must be called with the lock held.
func (g *Governor) estimatedUSD() float64 {
	if g.cfg.USDPer1kTokens <= 0 {
		return 0
	}
	return float64(g.tokens) / 1000.0 * g.cfg.USDPer1kTokens
}

// Snapshot is an immutable view of governor state for reporting.
type Snapshot struct {
	Tokens       int64   `json:"tokens"`
	Calls        int64   `json:"calls"`
	EstimatedUSD float64 `json:"estimated_usd"`
	Stopped      bool    `json:"stopped"`
	StopNote     string  `json:"stop_note,omitempty"`
}

// Snapshot returns the current usage totals.
func (g *Governor) Snapshot() Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Snapshot{
		Tokens:       g.tokens,
		Calls:        g.calls,
		EstimatedUSD: g.estimatedUSD(),
		Stopped:      g.stopped,
		StopNote:     g.stopNote,
	}
}

// MeteredLLM wraps an LLM, charging every completion against a Governor. Once the
// budget is exhausted it refuses further calls with ErrBudgetExceeded so in-flight
// work drains without starting new spend.
type MeteredLLM struct {
	inner LLM
	gov   *Governor
}

// NewMeteredLLM wraps llm so its usage counts against gov. If gov is nil the wrapper
// is a passthrough (unlimited).
func NewMeteredLLM(llm LLM, gov *Governor) LLM {
	if gov == nil {
		return llm
	}
	return &MeteredLLM{inner: llm, gov: gov}
}

// ChatCompletion enforces the budget then delegates, recording tokens used.
func (m *MeteredLLM) ChatCompletion(ctx context.Context, req *infraai.ChatRequest) (*infraai.ChatResponse, error) {
	if m.gov.Exhausted() {
		return nil, ErrBudgetExceeded
	}
	resp, err := m.inner.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	m.gov.account(resp.TokensUsed)
	return resp, nil
}

// Name delegates to the wrapped LLM.
func (m *MeteredLLM) Name() string { return m.inner.Name() }
