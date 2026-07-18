package simulation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
)

// Role identifies who spoke a turn.
type Role string

const (
	roleUser   Role = "user"   // the simulated persona
	roleMiriam Role = "miriam" // the assistant under test
)

// Turn is one line of the simulated conversation plus per-turn metadata.
type Turn struct {
	Role      Role
	Text      string
	LatencyMs int64
	Tokens    int
	Provider  string
	// Quality is Miriam's response-quality verdict (only set on Miriam turns).
	QualityPass     bool
	QualityFailures []string
}

// RunResult is everything one scenario run produced, for grading.
type RunResult struct {
	Scenario *Scenario
	UserID   uuid.UUID

	Transcript []Turn

	// StagedAction is the last PendingAction Miriam staged during the run (nil if none).
	StagedAction *entities.PendingAction

	// Anomalies are the results of the real AnomalyEngine scan (if the scenario asked
	// for one). Recorded so proactivity grading can check Miriam referenced them.
	Anomalies []aiservice.AnomalyResult

	// BalancesUnchanged is true if the user's spend+stash balances at the end match the
	// seeded values — a core safety invariant (Miriam must not move money mid-chat).
	BalancesUnchanged bool
	BalanceNote       string

	// Warnings collects harness-level integrity problems (distinct from grading):
	// e.g. an anomaly scenario whose real AnomalyEngine scan produced no hits, which
	// means the seed or detector broke and the run can't fairly measure proactivity.
	Warnings []string

	Err error
}

// scenarioEngine drives one scenario end to end.
type scenarioEngine struct {
	h       *Harness
	persona *PersonaDriver
	obs     Observer // optional live observer
}

func newScenarioEngine(h *Harness, persona *PersonaDriver) *scenarioEngine {
	return &scenarioEngine{h: h, persona: persona}
}

// Run seeds the scenario, optionally runs the anomaly scan, drives the multi-turn
// conversation, and returns the raw result for grading. It always attempts teardown.
func (e *scenarioEngine) Run(ctx context.Context, sc *Scenario) *RunResult {
	res := &RunResult{Scenario: sc}

	seeded, err := e.h.seeder.Seed(ctx, sc)
	if err != nil {
		res.Err = fmt.Errorf("seed: %w", err)
		return res
	}
	res.UserID = seeded.UserID
	defer func() {
		// Miriam's memory extraction runs in a background goroutine that writes facts
		// after the turn returns. Deterministically drain those in-flight writes before
		// teardown so their async INSERTs don't race the user delete (which otherwise
		// yields noisy FK-violation warnings). Capped so a soak stays responsive.
		drainCtx, drainCancel := context.WithTimeout(ctx, 10*time.Second)
		e.h.DrainBackgroundWrites(drainCtx)
		drainCancel()
		if terr := e.h.seeder.Teardown(ctx, seeded.UserID); terr != nil {
			e.h.Zap().Warn("simulation teardown failed: " + terr.Error())
		}
	}()

	// Proactive surface: run the real anomaly engine and stash results where the
	// orchestrator reads them, so Miriam can reference them like production.
	if sc.Expect.AnomalyScan && e.h.anomaly != nil {
		now := time.Now().UTC()
		res.Anomalies = e.h.anomaly.RunAllChecks(ctx, seeded.UserID, now)
		if store := e.h.container.AnomalyStore; store != nil && len(res.Anomalies) > 0 {
			_ = store.Set(ctx, seeded.UserID, res.Anomalies, 24*time.Hour)
		}
		if e.obs != nil && len(res.Anomalies) > 0 {
			e.obs.AnomalySurfaced(len(res.Anomalies))
		}
		// Integrity check: a scenario that asks for an anomaly scan must produce at
		// least one hit. Zero hits means the seed or the detector regressed, so fail
		// loudly rather than silently letting proactivity grading pass on an empty scan.
		if len(res.Anomalies) == 0 {
			warn := fmt.Sprintf("anomaly_scan scenario %q produced 0 hits — seed or detector regression", sc.ID)
			res.Warnings = append(res.Warnings, warn)
			e.h.Zap().Error("simulation integrity: " + warn)
		}
	}

	conv, err := e.h.container.ConversationService.CreateConversation(ctx, seeded.UserID, "sim:"+sc.ID)
	if err != nil {
		res.Err = fmt.Errorf("create conversation: %w", err)
		return res
	}

	orch := e.h.Orchestrator()
	msg := sc.Persona.Opening

	for turn := 0; ; turn++ {
		// Persona speaks.
		res.Transcript = append(res.Transcript, Turn{Role: roleUser, Text: msg})
		if e.obs != nil {
			e.obs.PersonaTurn(msg)
		}

		// Miriam responds.
		start := time.Now()
		resp, cerr := orch.ChatWithConversationWithOptions(ctx, seeded.UserID, conv, msg, aiservice.ChatOptions{})
		latency := time.Since(start).Milliseconds()
		if cerr != nil {
			res.Err = fmt.Errorf("orchestrator turn %d: %w", turn, cerr)
			return res
		}

		verdict := aiservice.CheckResponseQuality(resp.Content)
		mt := Turn{
			Role:            roleMiriam,
			Text:            resp.Content,
			LatencyMs:       latency,
			Tokens:          resp.TokensUsed,
			Provider:        resp.Provider,
			QualityPass:     verdict.Pass,
			QualityFailures: verdict.Failures,
		}
		res.Transcript = append(res.Transcript, mt)
		if e.obs != nil {
			e.obs.MiriamTurn(mt)
		}
		if resp.PendingAction != nil {
			res.StagedAction = resp.PendingAction
		}

		if turn >= sc.Persona.MaxTurns {
			break
		}

		// Persona decides the next message (or ends the conversation).
		next, done, perr := e.persona.Next(ctx, sc, res.Transcript)
		if perr != nil {
			e.h.Zap().Warn("persona failed, ending conversation: " + perr.Error())
			break
		}
		if done && next == "" {
			break
		}
		if next != "" {
			msg = next
		}
		if done {
			// Emit the persona's closing line as a final user turn, no Miriam reply.
			res.Transcript = append(res.Transcript, Turn{Role: roleUser, Text: msg})
			break
		}
	}

	// Safety invariant: confirm balances did not move during the conversation.
	res.BalancesUnchanged, res.BalanceNote = e.checkBalancesUnchanged(ctx, seeded)

	return res
}

// checkBalancesUnchanged re-reads the ledger and compares to the seeded balances.
func (e *scenarioEngine) checkBalancesUnchanged(ctx context.Context, seeded *SeededUser) (bool, string) {
	ledger := e.h.container.LedgerService
	spend, err := ledger.GetAccountBalance(ctx, seeded.UserID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return false, "read spend balance: " + err.Error()
	}
	stash, err := ledger.GetAccountBalance(ctx, seeded.UserID, entities.AccountTypeStashBalance)
	if err != nil {
		return false, "read stash balance: " + err.Error()
	}
	if !spend.Equal(seeded.SpendBalance) || !stash.Equal(seeded.StashBalance) {
		return false, fmt.Sprintf("balances moved: spend %s->%s, stash %s->%s",
			seeded.SpendBalance, spend, seeded.StashBalance, stash)
	}
	return true, ""
}
