package execution

import (
	"sort"
	"strings"

	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// stageConfirmToolNames returns the canonical staged set, sorted for
// deterministic prompt output.
func StageConfirmToolNames() []string {
	return sortedToolNames(core.StageConfirmTools)
}

func AutoExecuteToolNames() []string {
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

func ExecutionModelSection() string {
	var b strings.Builder
	b.WriteString("EXECUTION MODEL (mirrors how the app enforces confirmations):\n")
	b.WriteString("- AUTO-EXECUTE, call it then speak as done: ")
	b.WriteString(strings.Join(AutoExecuteToolNames(), ", "))
	b.WriteString(". These never move money.\n")
	b.WriteString("- STAGE & CONFIRM, anything that moves money or creates lasting autonomous behavior: ")
	b.WriteString(strings.Join(StageConfirmToolNames(), ", "))
	b.WriteString(". Calling the tool STAGES the move; tell them to approve in-app (Face ID for fund moves). Until the tool reports completion it has NOT happened. Describe what's pending, never \"sent/paid/done\".\n")
	b.WriteString("- For copy trading, always research_trader first and present the trades before staging copy_trader.\n")
	b.WriteString("- Never ask permission in chat (\"Want me to…?\"). State the plan as fact; the app's confirmation screen does the asking.")
	return b.String()
}

func IsExecutionActionTool(name string) bool {
	return core.StageConfirmTools[name]
}
