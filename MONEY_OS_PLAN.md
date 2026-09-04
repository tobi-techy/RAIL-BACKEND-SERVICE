# Money OS Build Plan

## Goal
Make the system an actual money operating system — where money moves automatically within safety guardrails, events trigger real actions (not just analysis), automations work end-to-end, and the system learns from outcomes.

**Excluded for now:** True autonomy gate (control level thresholds remain as-is; this plan enables execution within existing guardrails, not new autonomous behavior).

---

## Phase 1: Fix Dead Automation Triggers

**Problem:** 4 of 9 trigger types (`income_detected`, `spending_spike`, `payday`, `life_event`) are accepted by the API but never evaluated. Automations created with these triggers silently do nothing.

**Files to modify:**
- `internal/domain/entities/automation_entities.go` — add trigger config structs
- `internal/domain/services/automation/service.go` — add 4 evaluation methods + interfaces
- `internal/workers/automation_worker/worker.go` — wire calls into tick loop
- `internal/infrastructure/di/domain_wiring.go` — inject dependencies

### 1a. Add trigger config structs to `automation_entities.go`

```go
type IncomeDetectedTriggerConfig struct {
    EventType string  `json:"event_type,omitempty"` // "income_increase", "income_decrease", "" = any
    Threshold float64 `json:"threshold,omitempty"`   // min change ratio; 0 = any change
}

type SpendingSpikeTriggerConfig struct {
    SpikeRatio float64 `json:"spike_ratio,omitempty"` // e.g. 1.5 = 50% above normal; 0 = default 1.5
}

type PaydayTriggerConfig struct {
    DaysBefore int `json:"days_before,omitempty"` // trigger N days before detected payday
    DaysAfter  int `json:"days_after,omitempty"`  // trigger N days after detected payday
}
```

(`LifeEventTriggerConfig` already exists at line 125.)

### 1b. Add interfaces to `service.go`

Following existing pattern (`BalanceProvider`, `ObligationProvider`):

```go
type IncomeAnalyzer interface {
    GetMoneyFlow(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (*entities.MoneyFlowSummary, error)
}

type AnomalyRunner interface {
    RunAllChecks(ctx context.Context, userID uuid.UUID, now time.Time) []AnomalyResult
}

type PaydaySignalReader interface {
    GetPaydaySignal(ctx context.Context, userID uuid.UUID) (*entities.PaydaySignalData, error)
}

type LifeEventDetector interface {
    DetectLifeEvents(ctx context.Context, userID uuid.UUID, now time.Time) []LifeEvent
}
```

Add `Set*()` setters on Service. Wire in `di/domain_wiring.go`.

### 1c. Evaluation methods

Each follows the same pattern as `EvaluateAllBalanceThresholds`:
1. `repo.ListActiveByTrigger(ctx, triggerType)`
2. Group automations by user to avoid redundant data fetches
3. Check trigger-specific condition per user
4. For each automation that fires: `shouldTrigger()` cooldown check, then `go s.execute(ctx, &automation)`

**`EvaluateIncomeDetected`**: Compare this month's `TotalDeposits` vs last month's via `IncomeAnalyzer.GetMoneyFlow()`. If ratio matches config threshold, fire.

**`EvaluateSpendingSpikes`**: Call `AnomalyRunner.RunAllChecks()`. Filter for `bill_spike` and `spending_acceleration` anomaly types. Fire automations for affected users.

**`EvaluatePaydayTriggers`**: Read `PaydaySignalData` from context signals. Check if today (±DaysBefore/DaysAfter) matches detected `DayOfMonth`. Fire.

**`EvaluateLifeEvents`**: Call `LifeEventDetector.DetectLifeEvents()`. Compare against each automation's `LifeEventTriggerConfig.EventType` and `Threshold`. Fire matches.

### 1d. Worker tick loop

Add to `automation_worker/worker.go` 5-minute tick:
```go
s.automationService.EvaluateIncomeDetected(ctx)
s.automationService.EvaluateSpendingSpikes(ctx)
s.automationService.EvaluatePaydayTriggers(ctx)
s.automationService.EvaluateLifeEvents(ctx)
```

---

## Phase 2: Event-Driven Execution

**Problem:** Real-time events (ledger transactions, income spikes, bill pressure) trigger analysis but never trigger action. The intelligence orchestrator skips mandate execution on `EventMoneyEvent` and `EventAutonomousTick`.

**Files to modify:**
- `internal/domain/services/miriam/intelligence_orchestrator.go` — allow mandate execution on select autonomous events
- `internal/domain/services/miriam/service.go` — add `EventIncomeLowerThanUsual` and `EventSpendingSpike` dispatch helpers
- `internal/workers/miriam_event_worker/worker.go` — classify events more precisely
- `internal/domain/services/miriam/intelligence_orchestrator.go` — `IsAutonomousEvent` changes

### 2a. Expand mandate-capable events

Change `intelligence_orchestrator.go` lines 408-424:

```go
// Before: only worker_sweep and income_lower_than_usual
mandateEvent := !isAutonomous && (eventType == EventWorkerSweep || eventType == EventIncomeLowerThanUsual)

// After: also allow spending_spike and bill_pressure for full users
mandateCapable := eventType == EventWorkerSweep ||
    eventType == EventIncomeLowerThanUsual ||
    eventType == EventSpendingSpike ||
    eventType == EventBillPressure

// For autonomous events from real-time path (money_event), only execute
// if there's a matching active mandate AND the event is financially significant
realTimeExecution := isAutonomous && eventType == EventMoneyEvent && hasActiveMandates

if (mandateCapable || realTimeExecution) && controlLevel == "full" {
    // proceed to mandate execution
}
```

**Safety:** The existing guardrails (cooldown, balance floor, day cap, decision engine) are unchanged. This just removes the artificial read-only restriction on certain event types.

### 2b. Dispatch real events from the event worker

In `miriam_event_worker/worker.go`, when a `money_event` arrives, check if it's financially significant (deposit > $50, large outflow, etc.) and classify it:

```go
func classifyEvent(evt *MoneyEvent) string {
    if evt.EventType == "transaction.completed" && evt.Amount > significantDeposit {
        return miriam.EventIncomeLowerThanUsual // or a new EventIncomeDetected
    }
    if evt.EventType == "balance.updated" && evt.BalanceChange < -spikeThreshold {
        return miriam.EventSpendingSpike
    }
    return miriam.EventMoneyEvent
}
```

### 2c. Wire autopilot to dispatch orchestrator events

After `autopilot_service.go` morning scan detects an anomaly, dispatch the appropriate event type to the orchestrator (not just send a notification). This connects the two currently-disconnected systems.

---

## Phase 3: Make Autopilot Act

**Problem:** The autopilot's 3 phases (morning/midday/evening) only observe and report. The evening review explicitly refuses to execute queued transfers.

**Files to modify:**
- `internal/domain/services/ai/autopilot_service.go` — evening review execution path
- `internal/workers/autopilot_worker/worker.go` — possibly add mandate-aware execution

### 3a. Evening review: execute within guardrails

Change the `ToolTransferFunds` case in evening review (line ~381) from summary-only to conditional execution:

```go
case ToolTransferFunds:
    if user.ControlLevel == entities.ControlLevelFull && hasActiveMandates {
        // Execute via SafeExecuteTransferToStash with all existing guardrails
        err := o.intelligence.SafeExecuteTransferToStash(ctx, userID, mandate, amount)
        if err != nil {
            summary = append(summary, fmt.Sprintf("I tried to move $%.2f to Stash but held back: %v", amount, err))
        } else {
            summary = append(summary, fmt.Sprintf("Moved $%.2f to Stash automatically.", amount))
            executed++
        }
    } else {
        // Existing behavior: suggest only
        summary = append(summary, fmt.Sprintf("I held off on moving $%.2f to Stash -- needs approved mandate + Act mode.", amount))
    }
```

**Safety:** Full control level users with active mandates already have the decision engine, cooldown, balance floor, and day cap protecting them. This is the same path `executeMandateAction` uses.

### 3b. Midday surplus → auto-allocate for full users

Instead of queuing `alert_surplus` and waiting for evening, dispatch directly to the orchestrator as a mandate evaluation. The midday check already computes the surplus amount — wire it to the mandate path instead of the Redis queue.

---

## Phase 4: Learning Loop

**Problem:** `DecisionEngine.RecordOutcome` is fully implemented but has zero callers. Mandate execution results don't feed back to adjust the decision model.

**Files to modify:**
- `internal/domain/services/miriam/intelligence_orchestrator.go` — call RecordOutcome after mandate execution
- `internal/domain/services/miriam/decision_engine.go` — no changes needed (already implemented)

### 4a. Wire RecordOutcome after mandate execution

In `executeMandateAction` (line ~611), after the receipt is created:

```go
// After creating the receipt with status
outcome := &entities.DecisionOutcome{
    UserID:    userID,
    MandateID: mandate.ID,
    ActionType: mandate.ActionType,
    Amount:    amount,
    Status:    receipt.Status, // "executed" or "failed"
    HealthDelta: 0, // filled in later by self-review
}
o.decisions.RecordOutcome(ctx, outcome)
```

### 4b. Wire self-review health delta into outcomes

In `SelfReviewEngine.gradeActions` (line ~185), after computing `healthDelta`, update the corresponding `MiriamDecisionOutcome` records with the actual health trajectory. This closes the loop: execution → outcome → health delta → learning model.

### 4b. Feed learning model into suggestion confidence

In `GenerateSuggestions`, read the `MiriamLearningModel` bias for each suggestion type. If the model shows consistent rejection of a suggestion type, reduce confidence or skip that suggestion type. This prevents the system from repeatedly suggesting things the user always dismisses.

---

## Phase 5: Parallel Orchestrator (Performance)

**Problem:** The intelligence orchestrator's 13 sub-engines run strictly sequentially. Many are independent (enrichment, predictions, signals).

**Files to modify:**
- `internal/domain/services/miriam/intelligence_orchestrator.go` — parallelize independent steps

### 5a. Group engines by dependency

**Independent group (can run in parallel):**
- `RefreshMoneyState` (must be first — feeds everything)
- `RecordScore` (needs money state)
- `GeneratePredictions` (needs money state)
- `DetectAndUpsert` (needs money state)
- `GetActiveFacts` (independent)

**Sequential dependencies:**
- `RecordPredictions` → needs predictions
- `EvaluateOutcomes` → needs predictions + money state
- `MakeDecision` (per mandate) → needs predictions, memory, learning bias, money state
- `GenerateNudges` → needs predictions, money state, memory
- `GenerateSuggestions` → needs money state, memory, learning bias

### 5b. Implementation

```go
// Phase 1: Get money state (sequential, feeds everything)
moneyState := o.service.RefreshMoneyState(ctx, userID)
o.healthScore.RecordScore(ctx, userID, moneyState)

// Phase 2: Independent analysis (parallel)
var wg sync.WaitGroup
var predictions []Prediction
var signals []Signal
var facts []MemoryFact

wg.Add(3)
go func() { defer wg.Done(); predictions = o.predictions.GeneratePredictions(ctx, userID, moneyState) }()
go func() { defer wg.Done(); signals = o.signals.DetectAndUpsert(ctx, userID, moneyState) }()
go func() { defer wg.Done(); facts = o.memory.GetActiveFacts(ctx, userID) }()
wg.Wait()

// Phase 3: Decision-dependent steps (sequential)
o.outcomeTrack.RecordPredictions(ctx, userID, predictions)
o.outcomeTrack.EvaluateOutcomes(ctx, userID, moneyState)
// ... mandate execution, nudges, suggestions ...
```

---

## Execution Order

| Phase | Depends on | Effort | Risk |
|-------|-----------|--------|------|
| Phase 1: Fix dead triggers | Nothing | Medium | Low — follows existing patterns |
| Phase 2: Event-driven execution | Nothing (parallel with Phase 1) | Medium | Medium — need to test safety gates |
| Phase 3: Autopilot acts | Phase 2 (uses orchestrator events) | Low | Low — guardrails already exist |
| Phase 4: Learning loop | Phase 2 (needs execution to record outcomes) | Low | Low — wiring existing code |
| Phase 5: Parallel orchestrator | Nothing | Low | Low — pure performance, no behavior change |

**Recommended build order:** Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5

---

## What This Achieves

After all phases, the system will:

1. **Automations work end-to-end**: Users can create rules with income_detected, spending_spike, payday, or life_event triggers and they actually fire.
2. **Real-time events trigger actions**: A large deposit → automatic mandate evaluation → stash allocation. A spending spike → bill auto-pay if mandated. Not just analysis.
3. **Autopilot executes within guardrails**: Evening review actually moves money for full-control users with active mandates. Midday surplus detection can trigger immediate action.
4. **System learns from outcomes**: DecisionEngine.RecordOutcome feeds back into the learning model. Suggestion confidence adjusts based on acceptance rates.
5. **Faster evaluations**: Independent sub-engines run in parallel, cutting evaluation latency.

The money OS is no longer just an observer — it acts, learns, and gets smarter.
