package execution

import (
	"strings"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// TestExecutionModelSection_MatchesEnforcement verifies the generated EXECUTION
// MODEL block lists exactly the canonical tier sets — prompt can't drift.
func TestExecutionModelSection_MatchesEnforcement(t *testing.T) {
	section := ExecutionModelSection()

	for _, name := range AutoExecuteToolNames() {
		if !strings.Contains(section, name) {
			t.Errorf("AUTO-EXECUTE list missing %q", name)
		}
	}
	for _, name := range StageConfirmToolNames() {
		if !strings.Contains(section, name) {
			t.Errorf("STAGE & CONFIRM list missing %q", name)
		}
	}
	if !strings.Contains(section, "confirm") || !strings.Contains(section, "STAGES") {
		t.Error("section must explain staging semantics")
	}
}

// TestTierSets_Disjoint ensures no tool is both auto-execute and staged.
func TestTierSets_Disjoint(t *testing.T) {
	for name := range core.AutoExecuteTools {
		if core.StageConfirmTools[name] {
			t.Errorf("%q is in BOTH tiers — enforcement would be ambiguous", name)
		}
	}
}

// TestIsExecutionActionTool_CoversLegacySwitch pins every tool that was in the
// original hand-written switch so refactoring to the shared map didn't drop any.
func TestIsExecutionActionTool_CoversLegacySwitch(t *testing.T) {
	legacy := []string{
		"setup_bill_autopay", "cancel_subscription", "execute_investment",
		"optimize_yield", "block_merchant", "unblock_merchant",
		"copy_trader", "pause_trade_copying", "resume_trade_copying",
		"stop_trade_copying",
		"pay_bill", "automate_bill", "save_bill_beneficiary",
		"create_flight_intent", "book_flight", "save_travel_passenger", "request_flight_refund",
		"accept_mandate_suggestion",
		"send_money", "split_receipt", "create_automation",
	}
	for _, name := range legacy {
		if !IsExecutionActionTool(name) {
			t.Errorf("IsExecutionActionTool(%q) = false, was true before refactor", name)
		}
	}
	// Auto-execute tools must NOT be staged.
	for _, name := range []string{"set_budget", "set_savings_goal", "mark_obligation_paid"} {
		if IsExecutionActionTool(name) {
			t.Errorf("IsExecutionActionTool(%q) = true, but it's an auto-execute tool", name)
		}
	}
}
