package ai

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

const (
	ToolVoiceMoneyLookup = "voice_money_lookup"
	ToolVoiceMoneyAction = "voice_money_action"
)

func VoiceTools() []infraai.Tool {
	return []infraai.Tool{
		{
			Name:        ToolVoiceMoneyLookup,
			Description: `Voice-only router for Miriam's read-only chat capabilities. Use this for any voice question about balances, budgets, spending, transactions, deposits, withdrawals, income, yield, taxes, goals, profile, obligations, automations, subscriptions, runway, receipts, audit, financial health, financial advice, timeline, investing, news, market moves, knowledge-base concepts, or memory. Pick the closest underlying tool and pass only the relevant arguments.`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tool": map[string]interface{}{
						"type":        "string",
						"enum":        voiceLookupToolNames(),
						"description": "Underlying read-only chat tool to run.",
					},
					"period":          map[string]interface{}{"type": "string", "enum": []string{"this_month", "last_month", "last_7_days", "last_30_days", "last_90_days", "last_6_months", "last_12_months"}},
					"limit":           map[string]interface{}{"type": "integer"},
					"days":            map[string]interface{}{"type": "integer"},
					"months":          map[string]interface{}{"type": "integer"},
					"year":            map[string]interface{}{"type": "integer"},
					"query":           map[string]interface{}{"type": "string"},
					"intent":          map[string]interface{}{"type": "string", "enum": []string{"overview", "transfer", "budget", "goal", "investment", "tax", "legal"}},
					"proposed_amount": map[string]interface{}{"type": "number"},
					"status":          map[string]interface{}{"type": "string", "enum": []string{"active", "paused", "paid", "cancelled", "all"}},
					"type":            map[string]interface{}{"type": "string"},
				},
				"required": []string{"tool"},
			},
		},
		{
			Name:        ToolVoiceMoneyAction,
			Description: `Voice-only router for Miriam's chat action capabilities. Use only after the user clearly asks Miriam to change something or confirms an action. Supports transfers, withdrawals, budgets, savings goals, automations, obligation reminders, marking obligations paid, subscription protection/cancellation, financial profile updates, sending reports, and receipt split handoff.`,
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"enum":        voiceActionToolNames(),
						"description": "Underlying chat action tool to execute.",
					},
					"params": map[string]interface{}{
						"type":        "object",
						"description": "Arguments for the selected action, using the same fields as the chat tool.",
					},
				},
				"required": []string{"action", "params"},
			},
		},
	}
}

func voiceLookupToolNames() []string {
	return []string{
		ToolGetAccountSummary,
		ToolGetBudget,
		ToolGetMoneyFlow,
		ToolGetSpendingSummary,
		ToolGetSpendingChart,
		ToolGetRecentTransactions,
		ToolGetCardTransactions,
		ToolGetDepositHistory,
		ToolGetIncomeTrend,
		ToolGetWithdrawalHistory,
		ToolGetReceiptHistory,
		ToolGetYieldEarned,
		ToolGetTaxSummary,
		ToolGetTaxCalendar,
		ToolGetSavingsGoals,
		ToolGetFinancialProfile,
		ToolGetPersonaMoneyContext,
		ToolGetMoneyOperatingPlan,
		ToolListFinancialObligations,
		ToolFindObligationPayments,
		ToolGetFinancialHealth,
		ToolGetFinancialAudit,
		ToolGetFinancialPlan,
		ToolGetCashFlowForecast,
		ToolGetActionReceipts,
		ToolGetFinancialAdvice,
		ToolGetFinancialTimeline,
		ToolGetMiriamBrief,
		ToolGetMiriamMoneyState,
		ToolListMiriamMandates,
		ToolGetMiriamDecisionReceipts,
		ToolGetRecurringExpenses,
		ToolGetWarrantyItems,
		ToolGetReceiptChallenges,
		ToolGetSavingsSuggestions,
		ToolGetSubscriptions,
		ToolGetRunway,
		ToolGetDepositPattern,
		ToolGetYieldSummary,
		ToolGetSpendingComparison,
		ToolGetLinkedBanks,
		ToolListAutomations,
		ToolSuggestSmartTiming,
		ToolSuggestAdaptiveAmount,
		ToolListMemory,
		ToolSearchKnowledge,
		ToolSimulateSavings,
		ToolGetBalanceHistory,
		ToolGetSpendingPatterns,
		ToolGetComparativeContext,
		ToolGetMerchantInsights,
		ToolGetPriceChanges,
		ToolGetPortfolioStats,
		ToolGetTopMovers,
		ToolGetAllocations,
		ToolGetContributions,
		ToolGetWeeklyNews,
		ToolGetStreak,
	}
}

func voiceActionToolNames() []string {
	return []string{
		ToolTransferFunds,
		ToolSetBudget,
		ToolSetSavingsGoal,
		ToolCreateAutomation,
		ToolCreateObligationReminder,
		ToolMarkObligationPaid,
		ToolProtectSubscription,
		ToolMarkSubscriptionCancelled,
		ToolIgnoreSubscription,
		ToolUpdateFinancialProfile,
		ToolSendReport,
		ToolSplitReceipt,
	}
}

func (o *Orchestrator) executeVoiceMoneyLookup(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	target := strings.TrimSpace(stringArg(args, "tool"))
	if target == "" {
		return map[string]interface{}{"error": "tool is required"}, nil
	}
	if !isVoiceLookupTool(target) {
		return map[string]interface{}{"error": fmt.Sprintf("%s is not available through voice lookup", target)}, nil
	}
	if unavailable := o.voiceLookupUnavailable(target); unavailable != "" {
		return map[string]interface{}{"error": unavailable}, nil
	}
	fwd := voiceForwardArgs(args, "tool")
	coerceNumericStrings(fwd)
	return o.executeToolInner(ctx, userID, infraai.ToolCall{
		Name:      target,
		Arguments: fwd,
	})
}

func (o *Orchestrator) executeVoiceMoneyAction(ctx context.Context, userID uuid.UUID, args map[string]interface{}) (map[string]interface{}, error) {
	action := strings.TrimSpace(stringArg(args, "action"))
	if action == "" {
		return map[string]interface{}{"error": "action is required"}, nil
	}
	if !isVoiceActionTool(action) {
		return map[string]interface{}{"error": fmt.Sprintf("%s is not available through voice action", action)}, nil
	}
	if action == ToolInitiateWithdrawal {
		return map[string]interface{}{
			"error": "Withdrawals from voice need app confirmation with a verified bank destination. Open Withdraw to continue.",
		}, nil
	}
	if !o.canCreateActionTool(action) {
		return map[string]interface{}{"error": fmt.Sprintf("%s is not configured for this user", action)}, nil
	}

	var params map[string]interface{}
	if rawParams, hasParams := args["params"]; hasParams {
		var ok bool
		params, ok = rawParams.(map[string]interface{})
		if !ok || params == nil {
			return map[string]interface{}{"error": "params must be an object"}, nil
		}
	} else {
		params = voiceForwardArgs(args, "action")
	}
	coerceNumericStrings(params)
	voiceAliasParams(action, params)
	return o.executeActionToolDirect(ctx, userID, infraai.ToolCall{
		Name:      action,
		Arguments: params,
	})
}

func isVoiceLookupTool(name string) bool {
	for _, allowed := range voiceLookupToolNames() {
		if name == allowed {
			return true
		}
	}
	return false
}

func isVoiceActionTool(name string) bool {
	for _, allowed := range voiceActionToolNames() {
		if name == allowed {
			return true
		}
	}
	return false
}

func voiceForwardArgs(args map[string]interface{}, excludedKeys ...string) map[string]interface{} {
	excluded := make(map[string]struct{}, len(excludedKeys))
	for _, key := range excludedKeys {
		excluded[key] = struct{}{}
	}
	forwarded := make(map[string]interface{}, len(args))
	for key, value := range args {
		if _, skip := excluded[key]; skip {
			continue
		}
		if key == "params" {
			continue
		}
		forwarded[key] = value
	}
	return forwarded
}

func (o *Orchestrator) voiceLookupUnavailable(tool string) string {
	switch tool {
	case ToolGetPortfolioStats, ToolGetTopMovers, ToolGetAllocations:
		if o.portfolioProvider == nil {
			return "portfolio data is unavailable"
		}
	case ToolGetContributions, ToolGetStreak:
		if o.activityProvider == nil {
			return "activity data is unavailable"
		}
	case ToolGetWeeklyNews:
		if o.newsProvider == nil {
			return "weekly news is unavailable"
		}
	case ToolSearchKnowledge:
		if o.knowledge == nil && o.supermemory == nil {
			return "knowledge base is unavailable"
		}
	case ToolGetSpendingSummary, ToolGetSpendingChart, ToolGetRecentTransactions, ToolGetMoneyFlow:
		if o.spending == nil {
			return "spending data is unavailable"
		}
	case ToolGetBalanceHistory:
		if o.balanceHistory == nil {
			return "balance history is unavailable"
		}
	case ToolGetSpendingPatterns:
		if o.patterns == nil {
			return "spending patterns are unavailable"
		}
	case ToolGetComparativeContext:
		if o.aggregateStats == nil {
			return "comparison data is unavailable"
		}
	case ToolGetCardTransactions:
		if o.cardTransactions == nil {
			return "card transactions are unavailable"
		}
	case ToolGetDepositHistory:
		if o.depositHistory == nil {
			return "deposit history is unavailable"
		}
	case ToolGetIncomeTrend:
		if _, ok := o.depositHistory.(DepositIncomeProvider); !ok || o.depositHistory == nil {
			return "income trend is unavailable"
		}
	case ToolGetYieldEarned:
		if o.yieldProvider == nil {
			return "yield data is unavailable"
		}
	case ToolGetWithdrawalHistory:
		if o.withdrawalHistory == nil {
			return "withdrawal history is unavailable"
		}
	case ToolGetReceiptHistory:
		if o.receiptHistory == nil {
			return "receipt history is unavailable"
		}
	case ToolGetBudget:
		if o.budgetProvider == nil {
			return "budget data is unavailable"
		}
	case ToolGetFinancialProfile, ToolGetPersonaMoneyContext:
		if o.financialProfile == nil {
			return "financial profile is unavailable"
		}
	case ToolGetMoneyOperatingPlan:
		if o.financialProfile == nil || o.aggregateStats == nil {
			return "money operating plan is unavailable"
		}
	case ToolListFinancialObligations, ToolFindObligationPayments:
		if o.obligationManager == nil {
			return "financial obligations are unavailable"
		}
	case ToolGetFinancialHealth, ToolGetFinancialAudit, ToolGetFinancialPlan, ToolGetCashFlowForecast, ToolGetFinancialAdvice:
		if !o.hasFinancialAdviceProviders() {
			return "financial intelligence is unavailable"
		}
	case ToolGetActionReceipts:
		if o.actionHistory == nil {
			return "action receipts are unavailable"
		}
	case ToolGetMiriamMoneyState, ToolListMiriamMandates, ToolGetMiriamDecisionReceipts:
		if o.miriamIntelligence == nil {
			return "miriam intelligence state is unavailable"
		}
	case ToolGetFinancialTimeline:
		if !o.hasFinancialTimelineProviders() {
			return "financial timeline is unavailable"
		}
	case ToolGetRecurringExpenses:
		if o.recurringDetector == nil {
			return "recurring expenses are unavailable"
		}
	case ToolGetWarrantyItems:
		if o.warrantyTracker == nil {
			return "warranty tracking is unavailable"
		}
	case ToolGetReceiptChallenges:
		if o.receiptChallenges == nil {
			return "receipt challenges are unavailable"
		}
	case ToolGetSavingsSuggestions:
		if o.savingsSuggestions == nil {
			return "savings suggestions are unavailable"
		}
	case ToolGetPriceChanges:
		if o.priceTracker == nil {
			return "price tracking is unavailable"
		}
	case ToolGetMerchantInsights:
		if o.merchantAnalyzer == nil {
			return "merchant insights are unavailable"
		}
	case ToolListAutomations, ToolSuggestSmartTiming, ToolSuggestAdaptiveAmount:
		if o.automationProvider == nil {
			return "automations are unavailable"
		}
	case ToolGetLinkedBanks:
		if o.bankAccountProvider == nil {
			return "linked banks are unavailable"
		}
	case ToolListMemory:
		if o.memory == nil {
			return "memory is unavailable"
		}
	}
	return ""
}

// coerceNumericStrings converts string values that look like numbers to float64.
// ElevenLabs sends all body params as strings; downstream tools expect float64.
func coerceNumericStrings(m map[string]interface{}) {
	for k, v := range m {
		if s, ok := v.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				m[k] = f
			}
		}
	}
}

// voiceAliasParams maps ElevenLabs field names to what action tools expect.
func voiceAliasParams(action string, params map[string]interface{}) {
	switch action {
	case ToolSetBudget:
		if _, has := params["monthly_limit"]; !has {
			if v, ok := params["amount"]; ok {
				params["monthly_limit"] = v
				delete(params, "amount")
			}
		}
	case ToolSetSavingsGoal:
		if _, has := params["target"]; !has {
			if v, ok := params["amount"]; ok {
				params["target"] = v
				delete(params, "amount")
			}
		}
		if _, has := params["deadline"]; !has {
			if v, ok := params["target_date"]; ok {
				params["deadline"] = v
				delete(params, "target_date")
			}
		}
	}
}
