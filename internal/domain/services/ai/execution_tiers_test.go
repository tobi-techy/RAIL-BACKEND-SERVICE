package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// TestExecutionModelSection_MatchesEnforcement verifies the generated EXECUTION
// MODEL block lists exactly the canonical tier sets — prompt can't drift.
func TestExecutionModelSection_MatchesEnforcement(t *testing.T) {
	section := executionModelSection()

	for _, name := range autoExecuteToolNames() {
		if !strings.Contains(section, name) {
			t.Errorf("AUTO-EXECUTE list missing %q", name)
		}
	}
	for _, name := range stageConfirmToolNames() {
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
	if !core.StageConfirmTools["commit_conscious_spending_plan"] {
		t.Error("committing a Conscious Spending Plan must require explicit confirmation")
	}
	if core.StageConfirmTools["build_conscious_spending_plan"] {
		t.Error("building a read-only Conscious Spending Plan snapshot must not require confirmation")
	}
}

// TestIsExecutionActionTool_CoversLegacySwitch pins every tool that was in the
// original hand-written switch so refactoring to the shared map didn't drop any.
func TestIsExecutionActionTool_CoversLegacySwitch(t *testing.T) {
	legacy := []string{
		ToolSetupBillAutopay, ToolCancelSubscription, ToolExecuteInvestment,
		ToolOptimizeYield, ToolBlockMerchant, ToolUnblockMerchant,
		ToolCopyTrader, ToolPauseTradeCopying, ToolResumeTradeCopying,
		ToolStopTradeCopying,
		ToolPayBill, ToolAutomateBill, ToolSaveBillBeneficiary,
		ToolCreateFlightIntent, ToolBookFlight, ToolSaveTravelPassenger, ToolRequestFlightRefund,
		// create_miriam_mandate was removed: no backing registry tool exists —
		// mandates are proposed system-side and accepted via accept_mandate_suggestion.
		"accept_mandate_suggestion",
		"send_money", "split_receipt", "create_automation",
	}
	for _, name := range legacy {
		if !isExecutionActionTool(name) {
			t.Errorf("isExecutionActionTool(%q) = false, was true before refactor", name)
		}
	}
	// Auto-execute tools must NOT be staged.
	for _, name := range []string{"set_budget", "set_savings_goal", "mark_obligation_paid"} {
		if isExecutionActionTool(name) {
			t.Errorf("isExecutionActionTool(%q) = true, but it's an auto-execute tool", name)
		}
	}
}

// TestSystemPromptV2_ContainsGeneratedTiers guards the template wiring.
func TestSystemPromptV2_ContainsGeneratedTiers(t *testing.T) {
	if strings.Contains(SystemPromptV2, "[[EXECUTION_MODEL]]") {
		t.Fatal("template placeholder leaked into final prompt")
	}
	for _, want := range []string{"transfer_funds", "pay_bill", "set_budget"} {
		if !strings.Contains(SystemPromptV2, want) {
			t.Errorf("SystemPromptV2 missing %q from generated tiers", want)
		}
	}
}

// --- fake store for pending-action tests ---

type fakePendingStore struct {
	action *entities.PendingAction
}

func (f *fakePendingStore) Set(_ context.Context, _ uuid.UUID, _ *entities.PendingAction) error {
	return nil
}
func (f *fakePendingStore) Get(_ context.Context, _ uuid.UUID) *entities.PendingAction {
	return f.action
}
func (f *fakePendingStore) Delete(_ context.Context, _ uuid.UUID) {}

// TestAssembleContext_PendingActionOverridesShortMessageBypass: "yeah do it"
// is <15 chars and would normally skip context entirely — with a staged action
// the pending slot must be injected so the model confirms instead of re-staging.
func TestAssembleContext_PendingActionOverridesShortMessageBypass(t *testing.T) {
	o := &AgentAdapter{}
	o.SetPendingActions(&fakePendingStore{action: &entities.PendingAction{
		ID:          "pa1",
		Action:      "transfer_funds",
		Description: "Move $50 from Spend to Stash",
		ExpiresAt:   time.Now().Add(5 * time.Minute),
	}})

	msgs := o.assembleContext(context.Background(), uuid.New(), ContextAssemblyOpts{
		Message: "yeah do it",
		ConvID:  uuid.New(),
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 system message, got %d", len(msgs))
	}
	content := msgs[0].Content
	if !strings.Contains(content, "PENDING ACTION") ||
		!strings.Contains(content, "confirm_action") ||
		!strings.Contains(content, "transfer_funds") {
		t.Errorf("pending context missing required guidance:\n%s", content)
	}
	if !strings.Contains(content, "do NOT call transfer_funds again") {
		t.Errorf("pending context must forbid re-staging:\n%s", content)
	}
}

// TestAssembleContext_ShortMessageStillBypassesWithoutPending keeps the
// latency win for genuinely trivial turns when nothing is staged.
func TestAssembleContext_ShortMessageStillBypassesWithoutPending(t *testing.T) {
	o := &AgentAdapter{}
	o.SetPendingActions(&fakePendingStore{}) // store wired, nothing staged

	msgs := o.assembleContext(context.Background(), uuid.New(), ContextAssemblyOpts{
		Message: "ok cool",
		ConvID:  uuid.New(),
	})
	if len(msgs) != 0 {
		t.Fatalf("expected bypass (0 messages), got %d", len(msgs))
	}
}

// TestAssembleContext_ExpiredPendingIgnored ensures stale actions don't leak.
func TestAssembleContext_ExpiredPendingIgnored(t *testing.T) {
	o := &AgentAdapter{}
	o.SetPendingActions(&fakePendingStore{action: &entities.PendingAction{
		Action:    "send_money",
		ExpiresAt: time.Now().Add(-time.Minute),
	}})

	msgs := o.assembleContext(context.Background(), uuid.New(), ContextAssemblyOpts{
		Message: "yeah do it",
		ConvID:  uuid.New(),
	})
	if len(msgs) != 0 {
		t.Fatalf("expired pending action should be ignored, got %d messages", len(msgs))
	}
}
