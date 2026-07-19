// Package simulation is Miriam's simulated-environment test harness. It seeds a
// synthetic-but-realistic user's financial state into a disposable Postgres, drives
// the REAL orchestrator through a multi-turn conversation with an LLM-driven "user
// persona", and grades how impactful Miriam was across several dimensions (financial
// correctness, action correctness, proactivity, safety, personality, memory) using a
// hybrid of deterministic checks and an LLM judge.
//
// It is test tooling only — it runs Miriam for arbitrary synthetic user ids against a
// non-production database and must never be pointed at prod. The binary that drives it
// lives in cmd/miriam-sim; the gated Go test lives in test/simulation.
package simulation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// Scenario is a single simulated environment: a seeded financial situation, a persona
// that talks to Miriam, and the expectations we grade her against.
type Scenario struct {
	ID     string   `yaml:"id"`
	Title  string   `yaml:"title"`
	Tags   []string `yaml:"tags"`
	Weight float64  `yaml:"weight"` // relative weight in the suite aggregate (default 1.0)

	Seed    SeedSpec    `yaml:"seed"`
	Persona PersonaSpec `yaml:"persona"`
	Expect  ExpectSpec  `yaml:"expect"`
	Rubric  RubricSpec  `yaml:"rubric"`
}

// SeedSpec describes the synthetic financial state to materialize before the
// conversation. Amounts are USD/USDC decimals unless noted.
type SeedSpec struct {
	SpendBalance string `yaml:"spend_balance"` // current spending balance
	StashBalance string `yaml:"stash_balance"` // current stash balance

	// History drives the trailing money-flow math (RefreshMoneyState reads backdated
	// ledger entries). Each entry is materialized as a real, double-entry-balanced
	// ledger transaction at the given age.
	Income []FlowEvent `yaml:"income"` // backdated deposits
	Spend  []FlowEvent `yaml:"spend"`  // backdated outflows (modeled as withdrawals)

	// CardSpend is backdated card_transactions. Unlike Spend (ledger withdrawals),
	// card charges carry a merchant + category, so they are what the anomaly engine's
	// duplicate-charge and merchant-pattern detectors actually inspect.
	CardSpend []CardEvent `yaml:"card_spend"`

	Obligations []ObligationSpec `yaml:"obligations"`
	Facts       []FactSpec       `yaml:"facts"`        // Miriam memory facts
	HealthScore *int             `yaml:"health_score"` // optional seeded health-score anchor
}

// FlowEvent is one backdated money movement used to build trailing history.
type FlowEvent struct {
	Amount   string `yaml:"amount"`
	DaysAgo  int    `yaml:"days_ago"`
	Category string `yaml:"category"` // label only, for realism
	Note     string `yaml:"note"`
}

// CardEvent is one backdated completed card charge (merchant purchase).
type CardEvent struct {
	Amount   string `yaml:"amount"`
	DaysAgo  int    `yaml:"days_ago"`
	Merchant string `yaml:"merchant"`
	Category string `yaml:"category"`
}

// ObligationSpec seeds an active recurring financial obligation.
type ObligationSpec struct {
	Type      string `yaml:"type"` // subscription, rent, debt, ... (financial_obligations.type CHECK)
	Name      string `yaml:"name"`
	Amount    string `yaml:"amount"`
	Cadence   string `yaml:"cadence"` // one_time|weekly|biweekly|monthly|quarterly|annual
	DueInDays int    `yaml:"due_in_days"`
	Currency  string `yaml:"currency"` // default USD
}

// FactSpec seeds a Miriam memory fact (miriam_user_facts).
type FactSpec struct {
	Category   string  `yaml:"category"` // goal|habit|preference|... (see migration 205 CHECK)
	Fact       string  `yaml:"fact"`
	Conf       float64 `yaml:"confidence"` // default 1.0
	Importance int     `yaml:"importance"` // 0-10, default 5
}

// PersonaSpec configures the LLM-driven simulated user.
type PersonaSpec struct {
	Profile  string `yaml:"profile"`   // who they are (feeds the persona system prompt)
	Goal     string `yaml:"goal"`      // hidden objective driving the conversation
	Opening  string `yaml:"opening"`   // scripted first message (deterministic anchor)
	MaxTurns int    `yaml:"max_turns"` // persona replies after the opening (default 3)
}

// ExpectSpec holds the deterministic expectations graders check.
type ExpectSpec struct {
	// Action: the staged PendingAction the conversation should (or should not) produce.
	Action       string            `yaml:"action"`        // e.g. "transfer_funds"; "" or "none" = expect none
	ActionParams map[string]string `yaml:"action_params"` // substring/normalized checks on staged params

	// Numbers that a genuinely-grounded answer should surface (matched with tolerance).
	CitesNumbers []string `yaml:"cites_numbers"`

	// Proactivity: signal Miriam is expected to surface unprompted.
	// One of: cash_shortfall, anomaly, surplus, bill_pressure, spending_acceleration, "".
	SurfaceSignal string `yaml:"surface_signal"`
	// AnomalyScan asks the harness to run the real AnomalyEngine and expect >=1 hit
	// that the orchestrator can then reference.
	AnomalyScan bool `yaml:"anomaly_scan"`

	// Memory: planted facts/keywords the answer should reference.
	RecallKeywords []string `yaml:"recall_keywords"`

	// Safety expectations (any violation caps the scenario score — see graders.go).
	// MustNotExecuteFunds: a fund move must be STAGED for confirmation, never executed inline.
	MustNotExecuteFunds bool `yaml:"must_not_execute_funds"`
	// MustStepUp: withdrawals must require in-app authorization (rail://authorize), not tap-confirm.
	MustStepUp bool `yaml:"must_step_up"`
	// NoFabrication: the answer must not cite a dollar figure absent from seeded state
	// (guards against hallucinated numbers). Checked against a tolerance band.
	NoFabrication bool `yaml:"no_fabrication"`
}

// RubricSpec tunes the LLM judge for this scenario.
type RubricSpec struct {
	// Focus is appended to the judge prompt to steer what "impact" means here.
	Focus string `yaml:"focus"`
}

func mustDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// SpendBalanceDec returns the seeded spend balance as a decimal.
func (s SeedSpec) SpendBalanceDec() (decimal.Decimal, error) { return mustDecimal(s.SpendBalance) }

// StashBalanceDec returns the seeded stash balance as a decimal.
func (s SeedSpec) StashBalanceDec() (decimal.Decimal, error) { return mustDecimal(s.StashBalance) }

// Validate applies defaults and checks required fields.
func (sc *Scenario) Validate() error {
	if sc.ID == "" {
		return fmt.Errorf("scenario missing id")
	}
	if sc.Weight == 0 {
		sc.Weight = 1.0
	}
	if sc.Persona.MaxTurns == 0 {
		sc.Persona.MaxTurns = 3
	}
	if sc.Persona.Opening == "" {
		return fmt.Errorf("scenario %s: persona.opening is required (deterministic anchor)", sc.ID)
	}
	for i := range sc.Seed.Obligations {
		if sc.Seed.Obligations[i].Currency == "" {
			sc.Seed.Obligations[i].Currency = "USD"
		}
	}
	for i := range sc.Seed.Facts {
		if sc.Seed.Facts[i].Conf == 0 {
			sc.Seed.Facts[i].Conf = 1.0
		}
	}
	return nil
}

// LoadScenario reads and validates a single scenario YAML file.
func LoadScenario(path string) (*Scenario, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied scenario path, test tooling only
	if err != nil {
		return nil, fmt.Errorf("read scenario %s: %w", path, err)
	}
	var sc Scenario
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}

// LoadScenarios loads every *.yaml / *.yml scenario in dir. When only is non-empty,
// it keeps just the scenario whose id matches.
func LoadScenarios(dir, only string) ([]*Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read scenarios dir %s: %w", dir, err)
	}
	var out []*Scenario
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		sc, lerr := LoadScenario(filepath.Join(dir, name))
		if lerr != nil {
			return nil, lerr
		}
		if only != "" && sc.ID != only {
			continue
		}
		out = append(out, sc)
	}
	if len(out) == 0 {
		if only != "" {
			return nil, fmt.Errorf("no scenario with id %q in %s", only, dir)
		}
		return nil, fmt.Errorf("no scenarios found in %s", dir)
	}
	return out, nil
}

// backdated returns a UTC timestamp `daysAgo` days before now, jittered by index so
// multiple same-day events get distinct created_at values (money-flow counts rows).
func backdated(now time.Time, daysAgo, idx int) time.Time {
	return now.Add(-time.Duration(daysAgo)*24*time.Hour + time.Duration(idx)*time.Minute)
}
