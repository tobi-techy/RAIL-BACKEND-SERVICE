package ai

import (
	"strings"

	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ToolCategory represents a group of related tools.
type ToolCategory int

const (
	CategoryOverview   ToolCategory = iota // "how am I doing", general check-ins
	CategorySpending                       // "how much did I spend", "where did my money go"
	CategoryAction                         // "move money", "set budget", "create automation"
	CategoryPlanning                       // "advice", "audit me", "forecast", "plan"
	CategoryHistory                        // "show my transactions", "deposits", "withdrawals"
	CategoryAutomation                     // "automations", "schedule", "recurring"
	CategoryFull                           // ambiguous or complex — give all tools
)

// RouteTools classifies the user's message and returns only the tools relevant
// to that category. Falls back to the full set for ambiguous or compound queries.
func (o *AgentAdapter) RouteTools(message string) []ai.Tool {
	category := classifyMessage(message)
	allTools := o.GetTools()

	subset := filterToolsByCategory(allTools, category)
	if len(subset) == 0 {
		return allTools
	}
	return subset
}

// classifyMessage uses keyword matching to determine tool category.
// Fast (no LLM call), runs in <1ms. Errs on the side of CategoryFull for ambiguity.
//
// Compound messages ("how much did I spend this month? move 20k to stash") match
// multiple groups; routing them to a single category would starve half the
// request of its tools, so they escalate to CategoryFull. The known
// automation-over-action overlap ("every friday move $50") is handled by
// precedence, not escalation — it's one intent, not two.
func classifyMessage(msg string) ToolCategory {
	lower := strings.ToLower(msg)

	// Automation wins over action by precedence: "every friday move $50" is an
	// automation, not a one-off transfer.
	if matchesAny(lower, automationPatterns) {
		return CategoryAutomation
	}

	matched := 0
	category := CategoryFull

	// Action intents — user wants to DO something right now
	if matchesAny(lower, actionPatterns) {
		category = CategoryAction
		matched++
	}

	// Planning/advice/audit
	if matchesAny(lower, planningPatterns) {
		category = firstCategory(category, matched, CategoryPlanning)
		matched++
	}

	// Transaction history (check before spending — "deposits" is history not spending)
	if matchesAny(lower, historyPatterns) {
		category = firstCategory(category, matched, CategoryHistory)
		matched++
	}

	// Spending analysis
	if matchesAny(lower, spendingPatterns) {
		category = firstCategory(category, matched, CategorySpending)
		matched++
	}

	// Overview/general
	if matchesAny(lower, overviewPatterns) {
		category = firstCategory(category, matched, CategoryOverview)
		matched++
	}

	if matched > 1 {
		return CategoryFull // compound intent — don't starve either half
	}
	return category
}

// firstCategory records the winning category under single-match precedence:
// the FIRST group that matched in evaluation order wins, matching the original
// early-return behavior for single-intent messages.
func firstCategory(current ToolCategory, matchesSoFar int, candidate ToolCategory) ToolCategory {
	if matchesSoFar == 0 {
		return candidate
	}
	return current
}

var actionPatterns = []string{
	"move $", "move money", "move it", "move to", "transfer", "send",
	"set budget", "set a budget", "save $", "save ₦",
	"to stash", "lock", "set up", "set goal", "savings goal",
	"remind me", "protect", "cancel subscription",
	"withdraw $", "withdraw from", "withdraw to",
	"block", "unblock", "pay my bill", "pay bill", "autopay", "auto-pay",
	"buy $", "sell $", "invest $", "copy trad", "copy trade", "stop copying",
	"pause copying", "cancel my",
	// Nigerian bills + P2P intents — pay_bill/automate_bill/lookup_recipient/
	// send_money must be in scope for these turns.
	"airtime", "buy data", "data plan", "nepa", "electricity", "cable tv",
	"dstv", "gotv", "startimes", "meter", "betting", "top up",
	// Bank transfers + crypto sends — "send 2500 to gtbank 0916473844",
	// "send 50 usdc to 0x...", "transfer to bank".
	"to bank", "bank account", "account number", "gtbank", "access bank",
	"zenith", "uba", "first bank", "kuda", "send to bank",
	"send crypto", "send usdc", "to wallet", "to 0x", "wallet address",
}

var automationPatterns = []string{
	"automation", "automate", "schedule", "every week", "every friday",
	"every month", "recurring", "automatic", "autopilot", "when balance",
	"threshold", "smart timing",
}

var planningPatterns = []string{
	"advice", "audit", "roast", "plan", "forecast", "predict",
	"health score", "what should i do", "reality check", "hard mode",
	"operating plan", "tax", "how can i save", "suggestions",
	"invest", "investment", "where to put", "grow my money",
	"subscriptions", "bills", "yield", "idle cash",
	"debt", "debt payoff", "debt snowball", "baby steps", "sprint phase", "debt free",
	"pay off", "payoff", "interest rate", "minimum payment",
}

var spendingPatterns = []string{
	"spend", "spent", "where did my money", "money go", "breakdown",
	"category", "categories", "food", "transport", "shopping",
	"outflow", "burn rate", "patterns",
}

var historyPatterns = []string{
	"transactions", "show me my", "deposit", "withdrawal",
	"card transactions", "receipt", "income trend", "yield",
	// Phrases only — bare "interest" substring-matched "interesting".
	"interest earned", "how much interest",
}

var overviewPatterns = []string{
	"how am i doing", "what changed", "what matters", "overview",
	"what's up", "update me", "brief", "status", "check in",
	"balance", "how much do i have", "what do i have",
	"streak", "challenges", "challenge", "achievements", "achievement",
	"badges", "milestones", "what have i achieved",
}

// toolCategoryMap defines which tools belong to which category.
var toolCategoryMap = map[ToolCategory]map[string]bool{
	CategoryOverview: {
		ToolGetAccountSummary: true,
		ToolGetYieldStatus:    true,
		ToolGetUpcomingBills:  true,
		ToolGetMiriamBrief:    true,
		ToolGetStreak:         true,
		ToolGetSavingsStreaks: true,
		ToolGetChallenges:     true,
		ToolGetAchievements:   true,
		ToolGetBudget:         true,
		ToolSearchKnowledge:   true,
		ToolGetMoneyFlow:      true,
	},
	CategorySpending: {
		ToolGetMoneyFlow:          true,
		ToolAuditSubscriptions:    true,
		ToolListBlockedMerchants:  true,
		ToolGetSpendingSummary:    true,
		ToolGetRecentTransactions: true,
		ToolGetSpendingPatterns:   true,
		ToolGetRecurringExpenses:  true,
		ToolGetMerchantInsights:   true,
		ToolGetSpendingComparison: true,
		ToolGetAccountSummary:     true,
		ToolSearchKnowledge:       true,
	},
	CategoryAction: {
		ToolTransferFunds:            true,
		ToolSetupBillAutopay:         true,
		ToolGetUpcomingBills:         true,
		ToolCancelSubscription:       true,
		ToolExecuteInvestment:        true,
		ToolGetInvestmentOptions:     true,
		ToolOptimizeYield:            true,
		ToolGetYieldStatus:           true,
		ToolBlockMerchant:            true,
		ToolUnblockMerchant:          true,
		ToolListBlockedMerchants:     true,
		ToolCopyTrader:               true,
		ToolListTradeConductors:      true,
		ToolResearchTrader:           true,
		ToolGetCopyTradingStatus:     true,
		ToolPauseTradeCopying:        true,
		ToolResumeTradeCopying:       true,
		ToolStopTradeCopying:         true,
		ToolSetSavingsGoal:           true,
		ToolSetBudget:                true,
		ToolCreateAutomation:         true,
		ToolCreateObligationReminder: true,
		ToolInitiateWithdrawal:       true,
		ToolGetLinkedBanks:           true,
		ToolGetAccountSummary:        true,
		ToolMarkObligationPaid:       true,
		ToolProtectSubscription:      true,
		ToolSplitReceipt:             true,
		// Nigerian bill payment flow — "buy airtime" / "pay NEPA" previously
		// routed here WITHOUT these, leaving Miriam unable to act.
		ToolPayBill:             true,
		ToolAutomateBill:        true,
		ToolSaveBillBeneficiary: true,
		"list_bill_providers":   true,
		"get_data_plans":        true,
		"get_cable_packages":    true,
		"validate_meter":        true,
		// P2P sends — "send 5k to @tobi" previously had no send path in scope.
		"lookup_recipient": true,
		"send_money":       true,
		// Bank transfers + crypto sends — "send 2500 to gtbank 0916473844"
		// and "send 50 USDC to 0x..." previously had no tool path.
		"list_banks":           true,
		"resolve_bank_account": true,
		"send_to_bank":         true,
		"send_crypto":          true,
		// Obligations — "what do I owe / mark it paid" context.
		ToolListFinancialObligations: true,
		ToolFindObligationPayments:   true,
	},
	CategoryPlanning: {
		ToolGetUpcomingBills:         true,
		ToolAuditSubscriptions:       true,
		ToolGetYieldStatus:           true,
		ToolGetInvestmentOptions:     true,
		ToolListTradeConductors:      true,
		ToolResearchTrader:           true,
		ToolGetFinancialAudit:        true,
		ToolGetFinancialHealth:       true,
		ToolGetFinancialPlan:         true,
		ToolGetCashFlowForecast:      true,
		ToolGetMoneyOperatingPlan:    true,
		ToolGetFinancialProfile:      true,
		ToolGetSavingsSuggestions:    true,
		ToolGetTaxSummary:            true,
		ToolGetAccountSummary:        true,
		ToolGetMiriamBrief:           true,
		ToolSearchKnowledge:          true,
		ToolGetInvestmentProducts:    true,
		ToolWebSearch:                true,
		ToolGetBabySteps:             true,
		ToolGetBankStatementAnalysis: true,
	},
	CategoryHistory: {
		ToolGetRecentTransactions: true,
		ToolGetCardTransactions:   true,
		ToolGetDepositHistory:     true,
		ToolGetWithdrawalHistory:  true,
		ToolGetIncomeTrend:        true,
		ToolGetYieldEarned:        true,
		ToolGetReceiptHistory:     true,
		ToolGetMoneyFlow:          true,
		ToolGetBalanceHistory:     true,
		ToolSearchKnowledge:       true,
	},
	CategoryAutomation: {
		ToolListAutomations:   true,
		ToolCreateAutomation:  true,
		ToolGetAccountSummary: true,
		ToolGetBudget:         true,
		"pause_automation":    true,
		"resume_automation":   true,
		"delete_automation":   true,
	},
}

// alwaysAllowedTools bypass category filtering: expressive/engagement/settings
// tools are intent-agnostic, so Miriam can use them in any conversation.
var alwaysAllowedTools = map[string]bool{
	ToolSendMeme:           true,
	ToolSendVoiceMessage:   true,
	ToolCelebrate:          true,
	ToolSendPoll:           true,
	ToolSetPersonalityMode: true,
	ToolSetControlLevel:    true,
}

// filterToolsByCategory returns only tools that belong to the given category.
func filterToolsByCategory(tools []ai.Tool, category ToolCategory) []ai.Tool {
	if category == CategoryFull {
		return tools
	}
	allowed := toolCategoryMap[category]
	if len(allowed) == 0 {
		return tools
	}
	filtered := make([]ai.Tool, 0, len(allowed)+1)
	for _, t := range tools {
		if allowed[t.Name] || alwaysAllowedTools[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func matchesAny(text string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}
