package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// SoakConfig configures a continuous soak run: generate distinct scenarios, run and
// grade them under a strict budget, and persist every outcome until a stop condition
// (duration, run count, or budget) is reached.
type SoakConfig struct {
	Duration    time.Duration // wall-clock stop (0 = no time limit)
	MaxRuns     int           // stop after N runs (0 = no count limit)
	Concurrency int           // parallel workers (default 1)

	Seed       int64
	Archetypes []Archetype

	Budget BudgetConfig

	JSONLPath      string        // required results log
	CheckpointPath string        // optional resume file
	ProgressEvery  time.Duration // progress log cadence (default 30s)

	GitSHA string // stamped into records for trend/regression
}

// checkpoint captures enough state to resume a crashed soak deterministically.
type checkpoint struct {
	SoakID    string    `json:"soak_id"`
	Seed      int64     `json:"seed"`
	Completed int64     `json:"completed"`
	Tokens    int64     `json:"tokens"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SoakSummary is the final tally of a soak run.
type SoakSummary struct {
	SoakID      string   `json:"soak_id"`
	Completed   int64    `json:"completed"`
	Errors      int64    `json:"errors"`
	SafetyFails int64    `json:"safety_fails"`
	MeanImpact  float64  `json:"mean_impact"`
	Budget      Snapshot `json:"budget"`
	Elapsed     string   `json:"elapsed"`
	StopReason  string   `json:"stop_reason"`
}

// Soak orchestrates the continuous run.
type Soak struct {
	cfg    SoakConfig
	h      *Harness
	runner *Runner
	gov    *Governor
	store  *Store
	gen    *Generator

	completed   int64
	errors      int64
	safetyFails int64
	impactSum   int64 // impact*100, summed atomically to avoid float races

	soakID  string
	started time.Time
}

// NewSoak wires a soak run: metered persona/judge LLMs, a seeded generator, and a
// persistent store. It sweeps orphaned users from prior crashed runs before starting.
func NewSoak(ctx context.Context, h *Harness, cfg SoakConfig) (*Soak, error) {
	if cfg.JSONLPath == "" {
		return nil, fmt.Errorf("soak: JSONLPath is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = 30 * time.Second
	}

	soakID := uuid.NewString()
	gen := NewGenerator(GeneratorConfig{Seed: cfg.Seed, Archetypes: cfg.Archetypes})

	// Resume from a checkpoint if one exists for this JSONL/seed.
	started := time.Now()
	var resumeCompleted int64
	if cfg.CheckpointPath != "" {
		if cp, ok := loadCheckpoint(cfg.CheckpointPath); ok && cp.Seed == cfg.Seed {
			soakID = cp.SoakID
			started = cp.StartedAt
			resumeCompleted = cp.Completed
			// Fast-forward the deterministic generator so resumed runs are distinct.
			for i := int64(0); i < resumeCompleted; i++ {
				gen.Next()
			}
			h.Zap().Info(fmt.Sprintf("soak resuming: soak_id=%s completed=%d", soakID, resumeCompleted))
		}
	}

	gov := NewGovernor(cfg.Budget)

	// Meter persona + judge through the governor. Miriam's own tokens are charged
	// per-run via gov.Charge once we know them.
	var base LLM
	if h.container.AIProvider != nil {
		base = h.container.AIProvider
	}
	runner := NewRunner(h, RunnerOptions{
		PersonaLLM: NewMeteredLLM(base, gov),
		JudgeLLM:   NewMeteredLLM(base, gov),
	})

	store, err := NewStore(ctx, StoreConfig{JSONLPath: cfg.JSONLPath, DB: h.DB()})
	if err != nil {
		return nil, err
	}

	s := &Soak{
		cfg: cfg, h: h, runner: runner, gov: gov, store: store, gen: gen,
		soakID: soakID, started: started, completed: resumeCompleted,
	}

	swept := s.sweepOrphans(ctx)
	if swept > 0 {
		h.Zap().Info(fmt.Sprintf("soak swept %d orphaned sim users before start", swept))
	}
	return s, nil
}

// Run executes the soak until a stop condition triggers or ctx is cancelled.
func (s *Soak) Run(ctx context.Context) SoakSummary {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.cfg.Duration > 0 {
		var tcancel context.CancelFunc
		ctx, tcancel = context.WithTimeout(ctx, s.cfg.Duration)
		defer tcancel()
	}

	var (
		mu         sync.Mutex
		stopReason string
	)
	setStop := func(r string) {
		mu.Lock()
		if stopReason == "" {
			stopReason = r
		}
		mu.Unlock()
		cancel()
	}

	// Progress reporter.
	progressDone := make(chan struct{})
	go s.reportProgress(ctx, progressDone)

	// generation gate: hand out sequential scenarios until a count/budget stop.
	var handedOut int64
	nextScenario := func() (*Scenario, bool) {
		if s.gov.Exhausted() {
			setStop("budget exhausted")
			return nil, false
		}
		if s.cfg.MaxRuns > 0 && atomic.LoadInt64(&handedOut) >= int64(s.cfg.MaxRuns) {
			setStop("max runs reached")
			return nil, false
		}
		atomic.AddInt64(&handedOut, 1)
		return s.gen.Next(), true
	}

	var wg sync.WaitGroup
	for w := 0; w < s.cfg.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				sc, ok := nextScenario()
				if !ok {
					return
				}
				s.runOne(ctx, sc)
				if s.gov.Exhausted() {
					setStop("budget exhausted")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(progressDone)

	if ctx.Err() == context.DeadlineExceeded && stopReason == "" {
		stopReason = "duration reached"
	}
	if stopReason == "" {
		stopReason = "context cancelled"
	}

	_ = s.writeCheckpoint()
	_ = s.store.Close()

	return s.summary(stopReason)
}

// runOne runs, grades, charges, and persists a single scenario.
func (s *Soak) runOne(ctx context.Context, sc *Scenario) {
	card := s.runner.RunScenario(ctx, sc)

	// Charge Miriam's own tokens against the budget (persona/judge already metered).
	s.gov.Charge(card.MiriamTokens)

	atomic.AddInt64(&s.completed, 1)
	atomic.AddInt64(&s.impactSum, int64(card.Impact*100))
	if card.Error != "" {
		atomic.AddInt64(&s.errors, 1)
	}
	if !card.Safety.Pass {
		atomic.AddInt64(&s.safetyFails, 1)
	}

	rec := RunRecord{
		SoakID:       s.soakID,
		ScenarioID:   sc.ID,
		ScenarioHash: scenarioHash(sc),
		Archetype:    archetypeOf(sc),
		GitSHA:       s.cfg.GitSHA,
		Model:        s.h.ModelName(),
		Impact:       card.Impact,
		SafetyPass:   card.Safety.Pass,
		DurationMs:   card.DurationMs,
		MiriamTokens: card.MiriamTokens,
		Turns:        card.Turns,
		Error:        card.Error,
		Card:         card,
	}
	if err := s.store.Append(ctx, rec); err != nil {
		// DB-mirror failures are non-fatal (JSONL still holds the record).
		if IsMirrorError(err) {
			s.h.Zap().Warn("soak store mirror failed: " + err.Error())
		} else {
			s.h.Zap().Error("soak store append failed: " + err.Error())
		}
	}

	// Periodic checkpoint so a crash loses at most a few runs.
	if atomic.LoadInt64(&s.completed)%10 == 0 {
		_ = s.writeCheckpoint()
	}
}

func (s *Soak) reportProgress(ctx context.Context, done <-chan struct{}) {
	t := time.NewTicker(s.cfg.ProgressEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			snap := s.gov.Snapshot()
			c := atomic.LoadInt64(&s.completed)
			s.h.Zap().Info(fmt.Sprintf(
				"soak progress: completed=%d errors=%d safety_fails=%d mean_impact=%.1f tokens=%d est_usd=%.2f elapsed=%s",
				c, atomic.LoadInt64(&s.errors), atomic.LoadInt64(&s.safetyFails),
				s.meanImpact(), snap.Tokens, snap.EstimatedUSD, time.Since(s.started).Round(time.Second),
			))
		}
	}
}

func (s *Soak) meanImpact() float64 {
	c := atomic.LoadInt64(&s.completed)
	if c == 0 {
		return 0
	}
	return float64(atomic.LoadInt64(&s.impactSum)) / 100.0 / float64(c)
}

func (s *Soak) summary(reason string) SoakSummary {
	return SoakSummary{
		SoakID:      s.soakID,
		Completed:   atomic.LoadInt64(&s.completed),
		Errors:      atomic.LoadInt64(&s.errors),
		SafetyFails: atomic.LoadInt64(&s.safetyFails),
		MeanImpact:  round1(s.meanImpact()),
		Budget:      s.gov.Snapshot(),
		Elapsed:     time.Since(s.started).Round(time.Second).String(),
		StopReason:  reason,
	}
}

// sweepOrphans removes leftover synthetic users (and their ledger/card rows) from
// prior crashed runs, keeping the disposable DB from accumulating junk over days.
func (s *Soak) sweepOrphans(ctx context.Context) int {
	rows, err := s.h.DB().QueryContext(ctx,
		`SELECT id FROM users WHERE email LIKE 'sim-%@rail.sim'`)
	if err != nil {
		s.h.Zap().Warn("soak orphan sweep query failed: " + err.Error())
		return 0
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()

	n := 0
	for _, id := range ids {
		if err := s.h.seeder.Teardown(ctx, id); err != nil {
			s.h.Zap().Warn("soak orphan teardown failed: " + err.Error())
			continue
		}
		n++
	}
	return n
}

func (s *Soak) writeCheckpoint() error {
	if s.cfg.CheckpointPath == "" {
		return nil
	}
	cp := checkpoint{
		SoakID:    s.soakID,
		Seed:      s.cfg.Seed,
		Completed: atomic.LoadInt64(&s.completed),
		Tokens:    s.gov.Snapshot().Tokens,
		StartedAt: s.started,
		UpdatedAt: time.Now().UTC(),
	}
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.cfg.CheckpointPath, b, 0o644)
}

func loadCheckpoint(path string) (checkpoint, bool) {
	var cp checkpoint
	b, err := os.ReadFile(path)
	if err != nil {
		return cp, false
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return cp, false
	}
	return cp, cp.SoakID != ""
}

// archetypeOf recovers the archetype label from a generated scenario id
// ("gen-<archetype>-<hash>"). Returns "" for hand-authored scenarios.
func archetypeOf(sc *Scenario) string {
	const prefix = "gen-"
	id := sc.ID
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		return ""
	}
	rest := id[len(prefix):]
	// Trim the trailing "-<hash>".
	if idx := lastDash(rest); idx > 0 {
		return rest[:idx]
	}
	return rest
}

func lastDash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return i
		}
	}
	return -1
}
