package ai

import (
	"regexp"
	"testing"

	"github.com/rail-service/rail_service/internal/domain/services/ai/prompt"
	prompttools "github.com/rail-service/rail_service/internal/domain/services/ai/prompt/tools"
	aitools "github.com/rail-service/rail_service/internal/domain/services/ai/tools"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want ToolCategory
	}{
		// Single-intent messages keep their category.
		{"overview", "how am i doing", CategoryOverview},
		{"spending", "where did my money go", CategorySpending},
		{"history", "show my deposits", CategoryHistory},
		{"planning", "roast me", CategoryPlanning},

		// Action intents that previously lost their tools.
		{"nepa bill", "pay my nepa bill", CategoryAction},
		{"airtime", "buy airtime 500", CategoryAction},
		{"p2p send", "send 5k to @tobi", CategoryAction},
		{"transfer", "move $50 to stash", CategoryAction},
		{"bank transfer", "send 2500 to gtbank 0916473844", CategoryAction},
		{"bank transfer with naira", "send ₦2500 to gtbank 0916473844", CategoryAction},
		{"crypto send", "send 50 usdc to 0x1234567890abcdef", CategoryAction},
		{"crypto send with chain", "send 0.5 eth to 0x1234 on ethereum", CategoryAction},

		// Automation precedence beats action overlap ("move" + "$50").
		{"automation wins", "every friday move $50 to stash", CategoryAutomation},

		// Compound intents escalate so neither half loses its tools.
		{"analyze + move", "how much did I spend this month? move 20k to stash", CategoryFull},
		{"balance + invest", "what's my balance and where should I invest", CategoryFull},

		// Ambiguous falls back to full.
		{"ambiguous", "hmm interesting", CategoryFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyMessage(tt.msg); got != tt.want {
				t.Fatalf("classifyMessage(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestFilterToolsByCategory_ActionIncludesBillsAndP2P(t *testing.T) {
	all := []infraai.Tool{
		{Name: ToolPayBill}, {Name: "send_money"}, {Name: "lookup_recipient"},
		{Name: "list_bill_providers"}, {Name: ToolTransferFunds},
		{Name: ToolGetMoneyFlow}, // not an action tool — must be filtered out
	}
	got := filterToolsByCategory(all, CategoryAction)
	names := map[string]bool{}
	for _, tool := range got {
		names[tool.Name] = true
	}
	for _, want := range []string{ToolPayBill, "send_money", "lookup_recipient", "list_bill_providers"} {
		if !names[want] {
			t.Errorf("CategoryAction missing %q", want)
		}
	}
	if names[ToolGetMoneyFlow] {
		t.Error("CategoryAction should not include get_money_flow")
	}
}

func TestFilterToolsByCategory_FullReturnsEverything(t *testing.T) {
	all := []infraai.Tool{{Name: ToolPayBill}, {Name: ToolGetMoneyFlow}}
	got := filterToolsByCategory(all, CategoryFull)
	if len(got) != len(all) {
		t.Fatalf("CategoryFull returned %d tools, want %d", len(got), len(all))
	}
}

// --- Golden routing tests ---
//
// These build the REAL production tool registry (same registration calls as
// di/ai_wiring.go, zero dependencies — registration is nil-checked internally)
// and assert the routing invariants that keep prompt and router in sync:
//  1. every tool referenced by a routing category actually exists,
//  2. every tool the system prompts mention actually exists,
//  3. representative intents get the tools they need from the real set.

func newProductionToolRegistry() *aitools.Registry {
	reg := aitools.NewRegistry()
	aitools.RegisterPortfolioTools(reg)
	aitools.RegisterAllSpendingAndTransactionTools(reg)
	aitools.RegisterAllRemainingTools(reg)
	aitools.RegisterExecutionTools(reg)
	aitools.RegisterBillTools(reg)
	aitools.RegisterTravelTools(reg)
	aitools.RegisterSavingsGoalsV2Tools(reg)
	aitools.RegisterBankTransferTools(reg)
	return reg
}

func registryNameSet(t *testing.T) map[string]bool {
	t.Helper()
	names := newProductionToolRegistry().ToolNames()
	if len(names) == 0 {
		t.Fatal("production tool registry registered 0 tools")
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func TestToolCategoryMapResolvesAgainstRegistry(t *testing.T) {
	registered := registryNameSet(t)
	for cat, allowed := range toolCategoryMap {
		for name := range allowed {
			if !registered[name] {
				t.Errorf("%v routes to %q which is NOT in the production registry (renamed or removed?)", cat, name)
			}
		}
	}
}

// TestSystemPromptToolsReferencesResolve fails when a prompt mentions a tool
// that no longer exists — the phantom-tool failure mode where Miriam reads
// instructions it can never follow. Catches both ToolName("x") references and
// plain-text mentions of verb-prefixed snake_case names.
func TestSystemPromptToolsReferencesResolve(t *testing.T) {
	registered := registryNameSet(t)
	// Voice wrappers are built separately for the realtime path, not the
	// registry; confirm/cancel actions are handled by the pending-action flow,
	// also outside the registry.
	extra := map[string]bool{
		ToolVoiceMoneyLookup: true,
		// Automation action types mentioned in the prompt as available actions
		// for create_automation — they are not AI tools themselves.
		"pay_utility_bill":   true,
		"pause_card":         true,
		"resume_card":        true,
		"set_budget_alert":   true,
		ToolVoiceMoneyAction: true,
		"confirm_action":     true,
		"cancel_action":      true,
	}

	toolNameRe := regexp.MustCompile(`ToolName\("([a-z_]+)"\)`)
	// Verb-first snake_case tokens are tool mentions in practice; parameter
	// names (amount_ngn, prod_id) never start with these verbs.
	textRe := regexp.MustCompile(`\b(?:get|set|list|create|search|send|pay|move|transfer|book|find|validate|lookup|mark|audit|optimize|protect|block|unblock|pause|resume|stop|start|save|split|initiate|execute|research|copy|suggest|simulate|forget|archive|dismiss|accept|detect|request|update|connect)_[a-z0-9_]+\b`)

	for _, p := range []string{prompttools.SystemPromptTools, prompt.SystemPromptV2} {
		for _, re := range []*regexp.Regexp{toolNameRe, textRe} {
			for _, m := range re.FindAllStringSubmatch(p, -1) {
				name := m[len(m)-1]
				if !registered[name] && !extra[name] {
					t.Errorf("prompt references %q but it is not a registered tool", name)
				}
			}
		}
	}
}

func TestRouteToolsAgainstRealRegistry(t *testing.T) {
	all := newProductionToolRegistry().GetAll()
	fullList := make([]infraai.Tool, 0, len(all))
	for _, tl := range all {
		fullList = append(fullList, infraai.Tool{Name: tl.Name})
	}

	has := func(t *testing.T, tools []infraai.Tool, want string) {
		t.Helper()
		for _, tool := range tools {
			if tool.Name == want {
				return
			}
		}
		t.Errorf("route result missing %q", want)
	}

	t.Run("airtime routes with bill tools", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("buy airtime of 500"))
		has(t, got, ToolPayBill)
	})

	t.Run("p2p send has lookup and send", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("send 5k to @tobi"))
		has(t, got, "send_money")
		has(t, got, "lookup_recipient")
	})

	t.Run("automation excludes one-off transfer", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("every friday move $50 to stash"))
		has(t, got, ToolCreateAutomation)
		for _, tool := range got {
			if tool.Name == ToolTransferFunds {
				t.Error("automation route should not include transfer_funds")
			}
		}
	})

	t.Run("bank transfer routes with send_to_bank and resolve_bank_account", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("send 2500 to gtbank 0916473844"))
		has(t, got, "send_to_bank")
		has(t, got, "resolve_bank_account")
		has(t, got, "list_banks")
	})

	t.Run("crypto send routes with send_crypto", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("send 50 usdc to 0x1234567890abcdef"))
		has(t, got, "send_crypto")
	})

	t.Run("compound escalates to full set", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("how much did I spend this month? move 20k to stash"))
		if len(got) != len(fullList) {
			t.Fatalf("compound intent returned %d tools, want full %d", len(got), len(fullList))
		}
	})

	t.Run("overview subset stays narrow", func(t *testing.T) {
		got := filterToolsByCategory(fullList, classifyMessage("what's my balance"))
		if len(got) >= len(fullList) {
			t.Fatalf("overview route returned %d tools, want a narrow subset of %d", len(got), len(fullList))
		}
		has(t, got, ToolGetAccountSummary)
	})

	t.Run("every category-map entry survived filtering", func(t *testing.T) {
		for cat := ToolCategory(0); cat <= CategoryAutomation; cat++ {
			got := filterToolsByCategory(fullList, cat)
			if len(got) == 0 {
				t.Errorf("category %v filtered to zero tools against real registry", cat)
			}
		}
	})
}
