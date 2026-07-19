// Command miriam-sim runs Miriam's simulated-environment impact grader. It seeds
// synthetic users into a disposable Postgres, drives the real orchestrator through
// multi-turn conversations, and scores how impactful Miriam is across several
// dimensions. It must never be pointed at a production database.
//
// Usage:
//
//	SIM_DATABASE_URL=postgres://... EVAL... go run ./cmd/miriam-sim \
//	    --scenarios test/simulation/scenarios --out ./sim-out --json
//
// Flags:
//
//	--scenarios DIR   directory of scenario YAML files (required)
//	--only ID         run a single scenario by id
//	--db URL          simulation database URL (else $SIM_DATABASE_URL, else config)
//	--persona-turns N cap on persona replies per scenario (overrides scenario default)
//	--baseline FILE   compare against a saved suite JSON and flag regressions
//	--out DIR         write sim-report.json here
//	--min-impact F    fail (non-zero exit) if overall impact is below F
//	--band F          allowed impact drop vs baseline before failing (default 3)
//	--stub-miriam     use a deterministic offline stub instead of the real model
//	--allow-unsafe-db bypass the production-database guard (dangerous)
//
// Continuous soak mode (run for hours/days over generated scenarios):
//
//	--soak            run the continuous generator+soak daemon instead of the fixed suite
//	--soak-duration D wall-clock stop (e.g. 120h; 0 = unlimited)
//	--soak-runs N     stop after N runs (0 = unlimited)
//	--soak-workers N  parallel workers (default 2)
//	--soak-seed N     generator seed (deterministic; default 1)
//	--budget-tokens N stop after N total LLM tokens (0 = unlimited)
//	--budget-usd F    stop after ~$F estimated spend (0 = unlimited)
//	--usd-per-1k F    price estimate used for the dollar cap (default 0.005)
//	--soak-out DIR    directory for runs.jsonl + checkpoint.json (default ./sim-out)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rail-service/rail_service/internal/simulation"
)

func main() {
	var (
		scenariosDir = flag.String("scenarios", "test/simulation/scenarios", "directory of scenario YAML files")
		only         = flag.String("only", "", "run a single scenario by id")
		dbURL        = flag.String("db", "", "simulation database URL (else $SIM_DATABASE_URL / config)")
		personaTurns = flag.Int("persona-turns", 0, "override persona reply cap per scenario (0 = use scenario default)")
		baselinePath = flag.String("baseline", "", "baseline suite JSON to compare against")
		outDir       = flag.String("out", "", "directory to write sim-report.json")
		minImpact    = flag.Float64("min-impact", 0, "fail if overall impact is below this")
		band         = flag.Float64("band", 3, "allowed impact drop vs baseline before failing")
		stub         = flag.Bool("stub-miriam", false, "use a deterministic offline stub (no API keys/cost)")
		allowUnsafe  = flag.Bool("allow-unsafe-db", false, "bypass the production-database guard")
		live         = flag.Bool("live", false, "stream the conversation and per-scenario verdict as it runs")
		share        = flag.String("share", "", "write a clean, shareable results card to this file (e.g. sim-out/card.txt)")
		noColor      = flag.Bool("no-color", false, "disable ANSI color in live/share output")
		title        = flag.String("title", "Miriam Impact Eval", "headline for the shareable card")

		soak         = flag.Bool("soak", false, "run the continuous generator+soak daemon")
		soakDuration = flag.Duration("soak-duration", 0, "soak wall-clock stop (e.g. 120h; 0 = unlimited)")
		soakRuns     = flag.Int("soak-runs", 0, "stop soak after N runs (0 = unlimited)")
		soakWorkers  = flag.Int("soak-workers", 2, "soak parallel workers")
		soakSeed     = flag.Int64("soak-seed", 1, "soak generator seed")
		budgetTokens = flag.Int64("budget-tokens", 0, "stop soak after N total LLM tokens (0 = unlimited)")
		budgetUSD    = flag.Float64("budget-usd", 0, "stop soak after ~$F estimated spend (0 = unlimited)")
		usdPer1k     = flag.Float64("usd-per-1k", 0.005, "price estimate per 1k tokens for the dollar cap")
		soakOut      = flag.String("soak-out", "./sim-out", "directory for runs.jsonl + checkpoint.json")
		gitSHA       = flag.String("git-sha", os.Getenv("GIT_SHA"), "git SHA stamped into persisted records")
	)
	flag.Parse()

	if *soak {
		cfg := soakParams{
			dbURL: *dbURL, allowUnsafe: *allowUnsafe, stub: *stub,
			duration: *soakDuration, runs: *soakRuns, workers: *soakWorkers, seed: *soakSeed,
			budgetTokens: *budgetTokens, budgetUSD: *budgetUSD, usdPer1k: *usdPer1k,
			outDir: *soakOut, gitSHA: *gitSHA,
		}
		if err := runSoak(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "miriam-sim:", err)
			os.Exit(1)
		}
		return
	}

	if err := run(suiteParams{
		scenariosDir: *scenariosDir, only: *only, dbURL: *dbURL, personaTurns: *personaTurns,
		baselinePath: *baselinePath, outDir: *outDir, minImpact: *minImpact, band: *band,
		stub: *stub, allowUnsafe: *allowUnsafe, live: *live, share: *share,
		color: !*noColor, title: *title, gitSHA: *gitSHA,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "miriam-sim:", err)
		os.Exit(1)
	}
}

// suiteParams bundles the fixed-suite run flags.
type suiteParams struct {
	scenariosDir string
	only         string
	dbURL        string
	personaTurns int
	baselinePath string
	outDir       string
	minImpact    float64
	band         float64
	stub         bool
	allowUnsafe  bool
	live         bool
	share        string
	color        bool
	title        string
	gitSHA       string
}

// soakParams bundles the flags the soak mode needs.
type soakParams struct {
	dbURL        string
	allowUnsafe  bool
	stub         bool
	duration     time.Duration
	runs         int
	workers      int
	seed         int64
	budgetTokens int64
	budgetUSD    float64
	usdPer1k     float64
	outDir       string
	gitSHA       string
}

// runSoak boots the harness and runs the continuous soak daemon until a stop
// condition (duration/runs/budget) or Ctrl-C. Results stream to runs.jsonl.
func runSoak(p soakParams) error {
	if p.stub {
		return fmt.Errorf("--soak requires the real model; remove --stub-miriam")
	}
	if p.budgetTokens == 0 && p.budgetUSD == 0 && p.duration == 0 && p.runs == 0 {
		return fmt.Errorf("refusing to soak with no stop condition: set at least one of --budget-tokens/--budget-usd/--soak-duration/--soak-runs")
	}

	h, err := simulation.NewHarness(simulation.HarnessConfig{DatabaseURL: p.dbURL, AllowUnsafeDB: p.allowUnsafe})
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	if err := os.MkdirAll(p.outDir, 0o750); err != nil {
		return fmt.Errorf("create soak out dir: %w", err)
	}

	// Graceful stop on SIGINT/SIGTERM so an operator can stop a multi-day run and
	// still get a summary + checkpoint.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := simulation.NewSoak(ctx, h, simulation.SoakConfig{
		Duration:       p.duration,
		MaxRuns:        p.runs,
		Concurrency:    p.workers,
		Seed:           p.seed,
		Budget:         simulation.BudgetConfig{MaxTokens: p.budgetTokens, MaxUSD: p.budgetUSD, USDPer1kTokens: p.usdPer1k},
		JSONLPath:      filepath.Join(p.outDir, "runs.jsonl"),
		CheckpointPath: filepath.Join(p.outDir, "checkpoint.json"),
		GitSHA:         p.gitSHA,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "miriam-sim: soak starting (workers=%d, out=%s)\n", p.workers, p.outDir)
	summary := s.Run(ctx)
	simulation.RenderSoakSummary(os.Stdout, summary)
	return nil
}

func run(p suiteParams) error {
	scenarios, err := simulation.LoadScenarios(p.scenariosDir, p.only)
	if err != nil {
		return err
	}
	if p.personaTurns > 0 {
		for _, sc := range scenarios {
			sc.Persona.MaxTurns = p.personaTurns
		}
	}

	h, err := simulation.NewHarness(simulation.HarnessConfig{DatabaseURL: p.dbURL, AllowUnsafeDB: p.allowUnsafe, Quiet: p.live})
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	var opts simulation.RunnerOptions
	if p.stub {
		h.SetChat(simulation.NewStubChat())
		opts.PersonaLLM = simulation.NewStubLLM()
		opts.JudgeLLM = simulation.NewStubLLM()
		fmt.Fprintln(os.Stderr, "miriam-sim: --stub-miriam is ON — results do not reflect real model quality")
	}
	if p.live {
		opts.Observer = simulation.NewLiveReporter(os.Stdout, p.color)
	}

	runner := simulation.NewRunner(h, opts)
	result := runner.RunSuite(context.Background(), scenarios)

	var baseline *simulation.SuiteResult
	if p.baselinePath != "" {
		baseline, err = simulation.LoadBaseline(p.baselinePath)
		if err != nil {
			return fmt.Errorf("load baseline %s: %w", p.baselinePath, err)
		}
	}

	// When streaming live, the per-scenario verdicts already scrolled by; show the
	// polished share card as the finale instead of the dense table.
	if p.live {
		simulation.RenderShareCard(os.Stdout, result, simulation.ShareCardOptions{
			Title: p.title, Model: h.ModelName(), GitSHA: p.gitSHA, Color: p.color,
			Verdict: verdictFor(result.Impact),
		})
	} else {
		simulation.RenderTable(os.Stdout, result, baseline)
	}

	if p.outDir != "" {
		if err := os.MkdirAll(p.outDir, 0o750); err != nil {
			return fmt.Errorf("create out dir: %w", err)
		}
		reportPath := filepath.Join(p.outDir, "sim-report.json")
		if err := simulation.SaveJSON(reportPath, result); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Fprintf(os.Stdout, "Wrote %s\n", reportPath)
	}

	if p.share != "" {
		if err := os.MkdirAll(filepath.Dir(p.share), 0o750); err != nil {
			return fmt.Errorf("create share dir: %w", err)
		}
		if err := simulation.SaveShareCard(p.share, result, simulation.ShareCardOptions{
			Title: p.title, Model: h.ModelName(), GitSHA: p.gitSHA, Verdict: verdictFor(result.Impact),
		}); err != nil {
			return fmt.Errorf("write share card: %w", err)
		}
		fmt.Fprintf(os.Stdout, "Wrote %s\n", p.share)
	}

	if reason := simulation.RegressionExit(result, baseline, p.minImpact, p.band); reason != "" {
		return fmt.Errorf("suite failed: %s", reason)
	}
	return nil
}

// verdictFor is a light one-liner for the shareable card.
func verdictFor(impact float64) string {
	switch {
	case impact >= 75:
		return "Strong — ship-ready"
	case impact >= 60:
		return "Solid — a few gaps"
	case impact >= 45:
		return "Mixed — needs work"
	default:
		return "Early — real gaps found"
	}
}
