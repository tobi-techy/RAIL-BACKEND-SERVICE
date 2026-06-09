package ai

import (
	"strings"

	"github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// ToolCategory represents a group of related tools.
type ToolCategory int

const (
	CategoryOverview    ToolCategory = iota // "how am I doing", general check-ins
	CategorySpending                       // "how much did I spend", "where did my money go"
	CategoryAction                         // "move money", "set budget", "create automation"
	CategoryPlanning                       // "advice", "audit me", "forecast", "plan"
	CategoryHistory                        // "show my transactions", "deposits", "withdrawals"
	CategoryAutomation                     // "automations", "schedule", "recurring"
	CategoryFull                           // ambiguous or complex — give all tools
)

// RouteTools classifies the user's message and returns only the tools relevant
// to that category. Falls back to the full set for ambiguous queries.
func (o *Orchestrator) RouteTools(message string) []ai.Tool {
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
func classifyMessage(msg string) ToolCategory {
	lower := strings.ToLower(msg)

	// Automation-specific (check before action since "move every friday" is automation not one-off)
	if matchesAny(lower, automationPatterns) {
		return CategoryAutomation
	}

	// Action intents — user wants to DO something right now
	if matchesAny(lower, actionPatterns) {
		return CategoryAction
	}

	// Planning/advice/audit
	if matchesAny(lower, planningPatterns) {
		return CategoryPlanning
	}

	// Transaction history (check before spending — "deposits" is history not spending)
	if matchesAny(lower, historyPatterns) {
		return CategoryHistory
	}

	// Spending analysis
	if matchesAny(lower, spendingPatterns) {
		return CategorySpending
	}

	// Overview/general
	if matchesAny(lower, overviewPatterns) {
		return CategoryOverview
	}

	return CategoryFull
}

var actionPatterns = []string{
	"move $", "move money", "move it", "move to", "transfer", "send",
	"set budget", "set a budget", "save $", "save ₦",
	"to stash", "lock", "set up", "set goal", "savings goal",
	"remind me", "protect", "cancel subscription",
	"withdraw $", "withdraw from", "withdraw to",
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
}

var spendingPatterns = []string{
	"spend", "spent", "where did my money", "money go", "breakdown",
	"category", "categories", "food", "transport", "shopping",
	"outflow", "burn rate", "patterns",
}

var historyPatterns = []string{
	"transactions", "show me my", "deposit", "withdrawal",
	"card transactions", "receipt", "income trend", "yield", "interest",
}

var overviewPatterns = []string{
	"how am i doing", "what changed", "what matters", "overview",
	"what's up", "update me", "brief", "status", "check in",
	"balance", "how much do i have", "what do i have",
}

// toolCategoryMap defines which tools belong to which category.
var toolCategoryMap = map[ToolCategory]map[string]bool{
	CategoryOverview: {
		ToolGetAccountSummary:  true,
		ToolGetMiriamBrief:     true,
		ToolGetStreak:          true,
		ToolGetBudget:          true,
		ToolSearchKnowledge:    true,
		ToolGetMoneyFlow:       true,
	},
	CategorySpending: {
		ToolGetMoneyFlow:          true,
		ToolGetSpendingSummary:    true,
		ToolGetSpendingChart:      true,
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
	},
	CategoryPlanning: {
		ToolGetFinancialAdvice:     true,
		ToolGetFinancialAudit:      true,
		ToolGetFinancialHealth:     true,
		ToolGetFinancialPlan:       true,
		ToolGetCashFlowForecast:    true,
		ToolGetMoneyOperatingPlan:  true,
		ToolGetFinancialProfile:    true,
		ToolGetPersonaMoneyContext: true,
		ToolGetSavingsSuggestions:  true,
		ToolGetRunway:              true,
		ToolGetTaxSummary:         true,
		ToolGetAccountSummary:      true,
		ToolGetMiriamBrief:         true,
		ToolSearchKnowledge:        true,
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
		ToolListAutomations:       true,
		ToolCreateAutomation:      true,
		ToolSuggestSmartTiming:    true,
		ToolSuggestAdaptiveAmount: true,
		ToolGetAccountSummary:     true,
		ToolGetBudget:             true,
	},
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
	filtered := make([]ai.Tool, 0, len(allowed))
	for _, t := range tools {
		if allowed[t.Name] {
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
