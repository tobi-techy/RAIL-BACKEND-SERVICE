package ai

import (
	"sort"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// streamingStageOnlyTools are staged ONLY on the streaming chat path
// (isExecutionActionTool); the core non-streaming path doesn't stage them yet.
// They're unioned into the prompt's STAGE & CONFIRM list so the model treats
// them as confirm-first everywhere.
var streamingStageOnlyTools = map[string]bool{
	// BRIJ flight bookings (book_flight is fund-moving, Face ID step-up).
	"create_flight_intent":  true,
	"book_flight":           true,
	"save_travel_passenger": true,
	"request_flight_refund": true,
}

// stageConfirmToolNames returns the full staged set (core + streaming extras),
// sorted for deterministic prompt output.
func stageConfirmToolNames() []string {
	merged := make(map[string]bool, len(core.StageConfirmTools)+len(streamingStageOnlyTools))
	for name := range core.StageConfirmTools {
		merged[name] = true
	}
	for name := range streamingStageOnlyTools {
		merged[name] = true
	}
	return sortedToolNames(merged)
}

func autoExecuteToolNames() []string {
	return sortedToolNames(core.AutoExecuteTools)
}

func sortedToolNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// executionModelSection generates the EXECUTION MODEL block of SystemPromptV2
// from the canonical enforcement sets. The prompt is therefore guaranteed to
// match what isExecutionActionTool / core.Agent.isActionTool actually enforce.
func executionModelSection() string {
	var b strings.Builder
	b.WriteString("EXECUTION MODEL (mirrors how the app enforces confirmations):\n")
	b.WriteString("- AUTO-EXECUTE — call it, then speak as done: ")
	b.WriteString(strings.Join(autoExecuteToolNames(), ", "))
	b.WriteString(". These never move money.\n")
	b.WriteString("- STAGE & CONFIRM — anything that moves money or creates lasting autonomous behavior: ")
	b.WriteString(strings.Join(stageConfirmToolNames(), ", "))
	b.WriteString(". Calling the tool STAGES the move; tell them to approve in-app (Face ID for fund moves). Until the tool reports completion it has NOT happened — describe what's pending, never \"sent/paid/done\".\n")
	b.WriteString("- For copy trading, always research_trader first and present the trades before staging copy_trader.\n")
	b.WriteString("- Never ask permission in chat (\"Want me to…?\"). State the plan as fact; the app's confirmation screen does the asking.")
	return b.String()
}

// isExecutionActionTool reports whether an Execution Engine tool must go
// through the pending-action confirm flow in chat. Derived from the canonical
// core.StageConfirmTools plus streaming-only extras — do not hand-edit tiers here.
func isExecutionActionTool(name string) bool {
	return core.StageConfirmTools[name] || streamingStageOnlyTools[name]
}
