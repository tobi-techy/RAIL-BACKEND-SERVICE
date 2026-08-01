package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Tool names used in AI function-calling responses.
const (
	ToolGetPortfolioStats = "get_portfolio_stats"
	ToolGetTopMovers      = "get_top_movers"
	ToolGetAllocations    = "get_allocations"
	ToolGetContributions  = "get_contributions"
	ToolGetWeeklyNews     = "get_weekly_news"
	ToolGetStreak         = "get_streak"
	ToolGetAccountSummary = "get_account_summary"
)

// PortfolioDataProvider interface for portfolio data
type PortfolioDataProvider interface {
	GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*PortfolioStats, error)
	GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*Mover, error)
	GetAllocations(ctx context.Context, userID uuid.UUID) ([]*Allocation, error)
}

// ActivityDataProvider interface for activity data
type ActivityDataProvider interface {
	GetContributions(ctx context.Context, userID uuid.UUID, contributionType string, startDate, endDate time.Time) (*ContributionSummary, error)
	GetStreak(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error)
}

// NewsDataProvider interface for news data
type NewsDataProvider interface {
	GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error)
}

// ContextSignalProvider reads active behavioral signals for ambient Miriam nudges.
type ContextSignalProvider interface {
	GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]entities.UserContextSignal, error)
}

// PortfolioStats represents weekly portfolio statistics
type PortfolioStats struct {
	TotalValue      decimal.Decimal `json:"total_value"`
	WeeklyReturn    decimal.Decimal `json:"weekly_return"`
	WeeklyReturnPct decimal.Decimal `json:"weekly_return_pct"`
	MonthlyReturn   decimal.Decimal `json:"monthly_return"`
	TotalGainLoss   decimal.Decimal `json:"total_gain_loss"`
}

// Mover represents a top gainer/loser
type Mover struct {
	Symbol    string          `json:"symbol"`
	Name      string          `json:"name"`
	Return    decimal.Decimal `json:"return"`
	ReturnPct decimal.Decimal `json:"return_pct"`
}

// Allocation represents portfolio allocation
type Allocation struct {
	BasketID   uuid.UUID       `json:"basket_id"`
	BasketName string          `json:"basket_name"`
	Value      decimal.Decimal `json:"value"`
	Weight     decimal.Decimal `json:"weight"`
}

// ContributionSummary represents contribution totals
type ContributionSummary struct {
	Deposits decimal.Decimal `json:"deposits"`
	Roundups decimal.Decimal `json:"roundups"`
	Cashback decimal.Decimal `json:"cashback"`
	Total    decimal.Decimal `json:"total"`
}

// SupermemoryClient is the interface for Supermemory memory operations.
type SupermemoryClient interface {
	IngestConversation(ctx context.Context, userID string, messages []SupermemoryMessage) error
	SearchMemory(ctx context.Context, userID, query string, limit int) ([]SupermemoryResult, error)
	// SearchMemoryRanked reranks results by query relevance and returns recency
	// metadata (event/updated timestamps) so callers can weight by freshness.
	SearchMemoryRanked(ctx context.Context, userID, query string, limit int) ([]SupermemoryResult, error)
}

// SupermemoryMessage is a single conversation turn for Supermemory ingestion.
type SupermemoryMessage struct {
	Role    string
	Content string
}

// SupermemoryResult is a single memory search result.
type SupermemoryResult struct {
	Memory     string
	Similarity float64
	// EventUnix is the unix-seconds timestamp of the event the memory describes
	// (0 if unknown). UpdatedUnix is when the memory was last updated (0 if unknown).
	EventUnix   int64
	UpdatedUnix int64
}

// ConversationPersister is the subset of conversation.Service the orchestrator needs.
type ConversationPersister interface {
	BuildContext(ctx context.Context, conv *entities.AIConversation) ([]ai.Message, error)
	RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error
	UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error
	SaveToolMessages(ctx context.Context, convID uuid.UUID, messages []ai.Message) error
}

// UsageTracker records AI usage for cost tracking and ceiling enforcement.
type UsageTracker interface {
	TrackInteraction(ctx context.Context, userID uuid.UUID, model string, tokens int) error
	IsOverCostCeiling(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ChatOptions carries product-controlled chat behavior that should be injected
// as system context instead of mixed into user text.
type ChatOptions struct {
	ToneMode string
}

// MoneyMoveNotifier sends a push notification when Miriam moves money on a
// user's behalf. It is intentionally a single-method interface so the
// orchestrator package does not depend on the notification package. The
// emergency flag carries through whether the action used the early-stash
// withdrawal path so the push copy can reflect the fee.
type MoneyMoveNotifier interface {
	NotifyMiriamMovedFunds(ctx context.Context, userID uuid.UUID, action, from, to string, amount decimal.Decimal, emergency, succeeded bool, errMsg string) error
}

// SetMoneyMoveNotifier wires the notifier used to push messages after
// confirmed Miriam money-moving actions.
func (o *AgentAdapter) SetMoneyMoveNotifier(n MoneyMoveNotifier) {
	o.moneyMoveNotifier = n
}

func (o *AgentAdapter) hasFinancialAdviceProviders() bool {
	return o.spending != nil && o.aggregateStats != nil
}

func (o *AgentAdapter) hasFinancialTimelineProviders() bool {
	return o.depositHistory != nil ||
		o.withdrawalHistory != nil ||
		o.cardTransactions != nil ||
		o.spending != nil ||
		o.actionHistory != nil ||
		o.financialProfile != nil
}

// Chat handles a chat message with tool calling
func (o *AgentAdapter) Chat(ctx context.Context, userID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	return o.ChatInContext(ctx, userID, uuid.Nil, message, history)
}

// toolCacheKey returns a cache key for a tool call based on name + serialized args.
func toolCacheKey(tc ai.ToolCall) string {
	args, _ := json.Marshal(tc.Arguments)
	return tc.Name + ":" + string(args)
}

// executeTool executes a tool call and returns the result
func (o *AgentAdapter) executeTool(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	if tc.Name == "" {
		return map[string]interface{}{"error": "empty tool name"}, nil
	}
	if tc.Arguments == nil {
		tc.Arguments = make(map[string]interface{})
	}
	o.logger.Debug("executing tool call",
		zap.String("tool", tc.Name),
		zap.Any("args", sanitizeToolArgs(tc.Arguments)),
	)

	// Heavy tools (audit, financial health) make multiple DB calls across months
	timeout := 15 * time.Second
	if tc.Name == ToolGetFinancialAudit || tc.Name == ToolGetFinancialHealth {
		timeout = 30 * time.Second
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := o.executeToolInner(toolCtx, userID, tc)
	if err != nil && toolCtx.Err() == context.DeadlineExceeded {
		o.logger.Warn("Tool execution timed out", zap.String("tool", tc.Name), zap.Duration("timeout", timeout))
	}

	// Enrich financial tool results with personal Supermemory data
	if err == nil && result != nil && o.supermemory != nil && isFinancialDataTool(tc.Name) {
		o.enrichWithMemory(ctx, userID, tc, result)
	}

	return result, err
}

// isFinancialDataTool returns true for tools whose results benefit from personal memory context.
func isFinancialDataTool(name string) bool {
	switch name {
	case ToolGetSpendingSummary, ToolGetSpendingChart, ToolGetRecentTransactions,
		ToolGetMoneyFlow, ToolGetAccountSummary, ToolGetDepositHistory,
		ToolGetIncomeTrend, ToolGetSpendingPatterns, ToolGetRecurringExpenses,
		ToolGetMiriamBrief:
		return true
	}
	return false
}

// enrichWithMemory appends relevant Supermemory results to a tool result map.
func (o *AgentAdapter) enrichWithMemory(ctx context.Context, userID uuid.UUID, tc ai.ToolCall, result map[string]interface{}) {
	// Skip if tool returned an error
	if _, hasErr := result["error"]; hasErr {
		return
	}
	query := toolToMemoryQuery(tc)
	if query == "" {
		return
	}
	// Use fresh context — the tool's ctx may be near expiry
	smCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	memories, err := o.supermemory.SearchMemory(smCtx, userID.String(), query, 6)
	if err != nil || len(memories) == 0 {
		return
	}
	var relevant []string
	for _, m := range memories {
		if m.Similarity < 0.6 {
			continue
		}
		mem := m.Memory
		if len(mem) > 200 {
			mem = mem[:200]
		}
		relevant = append(relevant, mem)
		if len(relevant) >= 5 {
			break
		}
	}
	if len(relevant) > 0 {
		result["bank_statement_context"] = relevant
		result["bank_statement_note"] = "Additional data from user's uploaded bank statements (may be in NGN or other local currency — do NOT mix with USD Rail balances). Present external bank data separately when relevant."
	}
}

// toolToMemoryQuery builds a Supermemory search query based on tool call context.
func toolToMemoryQuery(tc ai.ToolCall) string {
	period, _ := tc.Arguments["period"].(string)
	switch tc.Name {
	case ToolGetSpendingSummary:
		return "spending by category " + period
	case ToolGetMoneyFlow:
		return "income spending money flow " + period
	case ToolGetRecentTransactions:
		return "recent transactions " + period
	case ToolGetSpendingPatterns:
		return "spending patterns frequency"
	case ToolGetDepositHistory, ToolGetIncomeTrend:
		return "income received salary"
	case ToolGetRecurringExpenses:
		return "recurring payments subscription"
	case ToolGetAccountSummary, ToolGetMiriamBrief:
		return "monthly summary income spending"
	default:
		return ""
	}
}
func (o *AgentAdapter) executeToolInner(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
	case ToolSetPersonalityMode:
		return o.executeSetPersonalityMode(ctx, userID, tc.Arguments)

	case ToolSetControlLevel:
		return o.executeSetControlLevel(ctx, userID, tc.Arguments)

	case ToolSendMeme:
		return o.executeSendMeme(ctx, userID, tc.Arguments)

	case ToolSendVoiceMessage:
		return o.executeSendVoiceMessage(ctx, userID, tc.Arguments)

	case ToolCelebrate:
		return o.executeCelebrate(ctx, userID, tc.Arguments)

	case ToolSendPoll:
		return o.executeSendPoll(ctx, userID, tc.Arguments)

	case ToolVoiceMoneyLookup:
		return o.executeVoiceMoneyLookup(ctx, userID, tc.Arguments)

	case ToolVoiceMoneyAction:
		return o.executeVoiceMoneyAction(ctx, userID, tc.Arguments)

	case ToolGetAccountSummary:
		return o.executeAccountSummary(ctx, userID)

	case ToolGetPortfolioStats:
		stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"total_value":       stats.TotalValue.String(),
			"weekly_return":     stats.WeeklyReturn.String(),
			"weekly_return_pct": stats.WeeklyReturnPct.String(),
		}, nil

	case ToolGetTopMovers:
		limit := 5
		if l, ok := tc.Arguments["limit"].(float64); ok && l > 0 && l <= 20 {
			limit = int(l)
		}
		movers, err := o.portfolioProvider.GetTopMovers(ctx, userID, limit)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"movers": movers}, nil

	case ToolGetAllocations:
		allocs, err := o.portfolioProvider.GetAllocations(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"allocations": allocs}, nil

	case ToolGetContributions:
		now := time.Now()
		contribType := "all"
		if t, ok := tc.Arguments["type"].(string); ok && t != "" {
			contribType = t
		}
		startDate := now.AddDate(0, 0, -7)
		if p, ok := tc.Arguments["period"].(string); ok {
			switch p {
			case "1m":
				startDate = now.AddDate(0, -1, 0)
			case "3m":
				startDate = now.AddDate(0, -3, 0)
			}
		}
		summary, err := o.activityProvider.GetContributions(ctx, userID, contribType, startDate, now)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"deposits": summary.Deposits.String(),
			"roundups": summary.Roundups.String(),
			"cashback": summary.Cashback.String(),
			"total":    summary.Total.String(),
		}, nil

	case ToolGetWeeklyNews:
		news, err := o.newsProvider.GetWeeklyNews(ctx, userID)
		if err != nil {
			return nil, err
		}
		headlines := make([]string, 0, len(news))
		for _, n := range news {
			headlines = append(headlines, n.Title)
		}
		return map[string]interface{}{"headlines": headlines, "count": len(news)}, nil

	case ToolGetStreak:
		streak, err := o.activityProvider.GetStreak(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"current_streak": streak.CurrentStreak,
			"longest_streak": streak.LongestStreak,
		}, nil

	case ToolSearchKnowledge:
		return o.executeKnowledgeSearch(ctx, userID, tc.Arguments)

	case ToolGetSpendingSummary:
		return o.executeSpendingSummary(ctx, userID, tc.Arguments)

	case ToolGetSpendingChart:
		return o.executeSpendingChart(ctx, userID, tc.Arguments)

	case ToolGetRecentTransactions:
		return o.executeRecentTransactions(ctx, userID, tc.Arguments)

	case ToolGetMoneyFlow:
		return o.executeMoneyFlow(ctx, userID, tc.Arguments)

	case ToolGetBalanceHistory:
		return o.executeBalanceHistory(ctx, userID, tc.Arguments)

	case ToolGetSpendingPatterns:
		return o.executeSpendingPatterns(ctx, userID)

	case ToolSimulateSavings:
		return o.executeSimulateSavings(ctx, userID, tc.Arguments)

	case ToolGetComparativeContext:
		return o.executeComparativeContext(ctx, userID)

	case ToolGetCardTransactions:
		return o.executeCardTransactions(ctx, userID, tc.Arguments)

	case ToolGetDepositHistory:
		return o.executeDepositHistory(ctx, userID, tc.Arguments)

	case ToolGetIncomeTrend:
		if _, ok := o.depositHistory.(DepositIncomeProvider); !ok || o.depositHistory == nil {
			return map[string]interface{}{"error": "income trend data is not available"}, nil
		}
		return o.executeIncomeTrend(ctx, userID, tc.Arguments)

	case ToolGetYieldEarned:
		return o.executeYieldEarned(ctx, userID, tc.Arguments)

	case ToolGetWithdrawalHistory:
		return o.executeWithdrawalHistory(ctx, userID, tc.Arguments)

	case ToolGetReceiptHistory:
		return o.executeReceiptHistory(ctx, userID, tc.Arguments)

	case ToolGetTaxSummary:
		return o.executeTaxSummary(ctx, userID, tc.Arguments)

	case ToolGetTaxCalendar:
		return o.executeTaxCalendar(ctx, userID)

	case ToolGetSavingsGoals:
		return o.executeGetSavingsGoals(ctx, userID)

	case ToolGetBudget:
		return o.executeGetBudget(ctx, userID)

	case ToolGetFinancialProfile:
		if o.financialProfile == nil {
			return map[string]interface{}{"error": "financial profile service is unavailable"}, nil
		}
		return o.executeGetFinancialProfile(ctx, userID)

	case ToolGetPersonaMoneyContext:
		if o.financialProfile == nil {
			return map[string]interface{}{"error": "persona money context service is unavailable"}, nil
		}
		return o.executePersonaMoneyContext(ctx, userID)

	case ToolGetMoneyOperatingPlan:
		if o.financialProfile == nil || o.aggregateStats == nil {
			return map[string]interface{}{"error": "money operating plan service is unavailable"}, nil
		}
		return o.executeMoneyOperatingPlan(ctx, userID)

	case ToolListFinancialObligations:
		if o.obligationManager == nil {
			return map[string]interface{}{"error": "obligation service is unavailable"}, nil
		}
		return o.executeListFinancialObligations(ctx, userID, tc.Arguments)

	case ToolFindObligationPayments:
		if o.obligationManager == nil {
			return map[string]interface{}{"error": "obligation service is unavailable"}, nil
		}
		return o.executeFindObligationPaymentMatches(ctx, userID, tc.Arguments)

	case ToolMarkObligationPaid:
		return map[string]interface{}{"error": "Marking an obligation paid requires a conversation context. Please use the chat interface."}, nil

	case ToolGetFinancialHealth:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "financial health service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeFinancialHealth(ctx, userID, tc.Arguments)

	case ToolGetFinancialAudit:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "financial audit service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeFinancialAudit(ctx, userID, tc.Arguments)

	case ToolGetFinancialPlan:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "financial plan service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeFinancialPlan(ctx, userID)

	case ToolGetCashFlowForecast:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "cash flow forecast service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeCashFlowForecast(ctx, userID)

	case ToolGetActionReceipts:
		if o.actionHistory == nil {
			return map[string]interface{}{"error": "action receipts service is unavailable"}, nil
		}
		return o.executeActionReceipts(ctx, userID, tc.Arguments)

	case ToolGetFinancialAdvice:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "financial advice service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeFinancialAdvice(ctx, userID, tc.Arguments)

	case ToolGetFinancialTimeline:
		if !o.hasFinancialTimelineProviders() {
			return map[string]interface{}{"error": "financial timeline service is unavailable: no timeline data providers are configured"}, nil
		}
		return o.executeFinancialTimeline(ctx, userID, tc.Arguments)

	case ToolGetMiriamBrief:
		if !o.hasFinancialAdviceProviders() {
			return map[string]interface{}{"error": "miriam brief service is unavailable: spending and balance providers are not configured"}, nil
		}
		return o.executeMiriamBrief(ctx, userID, tc.Arguments)

	case ToolGetMiriamMoneyState:
		if o.miriamIntelligence == nil {
			return map[string]interface{}{"error": "miriam money state is unavailable"}, nil
		}
		return o.executeMiriamMoneyState(ctx, userID)

	case ToolListMiriamMandates:
		if o.miriamIntelligence == nil {
			return map[string]interface{}{"error": "miriam autopilot mandates are unavailable"}, nil
		}
		return o.executeListMiriamMandates(ctx, userID)

	case ToolGetMiriamDecisionReceipts:
		if o.miriamIntelligence == nil {
			return map[string]interface{}{"error": "miriam decision receipts are unavailable"}, nil
		}
		return o.executeMiriamDecisionReceipts(ctx, userID, tc.Arguments)

	case ToolGetRecurringExpenses:
		return o.executeRecurringExpenses(ctx, userID)

	case ToolGetWarrantyItems:
		return o.executeGetWarrantyItems(ctx, userID)

	case ToolGetReceiptChallenges:
		return o.executeReceiptChallenges(ctx, userID)

	case ToolGetSavingsSuggestions:
		return o.executeSavingsSuggestions(ctx, userID)

	case ToolGetPriceChanges:
		return o.executePriceChanges(ctx, userID, tc.Arguments)

	case ToolGetMerchantInsights:
		return o.executeMerchantInsights(ctx, userID, tc.Arguments)

	case ToolSplitReceipt:
		return map[string]interface{}{"error": "Receipt splitting requires a conversation context. Please use the chat interface."}, nil

	case ToolUpdateFinancialProfile:
		return map[string]interface{}{"error": "Updating your financial profile requires a conversation context. Please use the chat interface."}, nil

	case ToolListMemory:
		return o.executeListMemory(ctx, userID)

	case ToolForgetFact:
		return o.executeForgetFact(ctx, userID, tc.Arguments)

	case ToolForgetCategory:
		return o.executeForgetCategory(ctx, userID, tc.Arguments)

	case ToolListAutomations:
		return o.executeListAutomations(ctx, userID)

	case ToolCreateAutomation:
		return map[string]interface{}{"error": "Creating an automation requires a conversation context. Please use the chat interface."}, nil

	case ToolSuggestSmartTiming:
		return o.executeSuggestSmartTiming(ctx, userID)

	case ToolSuggestAdaptiveAmount:
		return o.executeSuggestAdaptiveAmount(ctx, userID)

	case ToolGetSubscriptions:
		return o.executeGetSubscriptions(ctx, userID)

	case ToolProtectSubscription, ToolMarkSubscriptionCancelled, ToolIgnoreSubscription:
		return map[string]interface{}{"error": "Changing subscription tracking requires a conversation context. Please use the chat interface."}, nil

	case ToolGetRunway:
		return o.executeGetRunway(ctx, userID)

	case ToolGetDepositPattern:
		return o.executeGetDepositPattern(ctx, userID)

	case ToolGetYieldSummary:
		return o.executeGetYieldSummary(ctx, userID)

	case ToolGetSpendingComparison:
		return o.executeGetSpendingComparison(ctx, userID)

	case ToolGetLinkedBanks:
		return o.executeGetLinkedBanks(ctx, userID)

	case ToolGetInvestmentProducts:
		return o.executeInvestmentProducts(tc.Arguments)

	case ToolWebSearch:
		if o.webSearcher == nil {
			return map[string]interface{}{"error": "web search is unavailable"}, nil
		}
		return o.executeWebSearch(ctx, userID, tc.Arguments)

	case ToolGetSavingsStreaks:
		return o.executeGetSavingsStreaks(ctx, userID)

	case ToolGetChallenges:
		return o.executeGetChallenges(ctx, userID)

	case ToolGetAchievements:
		return o.executeGetAchievements(ctx, userID)

	default:
		// Action tools (transfer_funds, initiate_withdrawal, etc.) — execute directly in voice mode.
		// In chat mode these are intercepted earlier with pending/confirm flow.
		// In voice mode (ExecuteToolPublic), AssemblyAI handles confirmation conversationally.
		if isActionTool(tc.Name) && o.canCreateActionTool(tc.Name) {
			return o.executeActionToolDirect(ctx, userID, tc)
		}
		// Read-only tools that only exist in the core registry (e.g. the
		// Execution Engine lookups) fall through to the registry so the
		// streaming path can serve every advertised tool.
		if o.agent != nil {
			return o.agent.ExecuteToolStrict(ctx, userID, tc.Name, tc.Arguments)
		}
		return nil, fmt.Errorf("unknown tool: %s", tc.Name)
	}
}

// classifyQueryComplexity returns "fast" for simple lookups and "quality" for complex analysis.
// Simple: balance checks, transaction lookups, streak, single-tool queries.
// Complex: financial plans, multi-step analysis, advice, comparisons, tax questions.
func classifyQueryComplexity(message string) string {
	lower := strings.ToLower(message)

	// Complex patterns — need the smart model
	complexPatterns := []string{
		"plan", "advice", "advise", "forecast", "predict",
		"compare", "analyze", "analysis", "why did", "why is",
		"what should", "what if", "simulate", "strategy",
		"tax", "budget plan", "financial health", "risk",
		"help me", "explain", "break down", "deep dive",
		"optimize", "rebalance", "goal", "timeline",
		"audit", "hard mode", "roast", "reality check", "no sugar",
		"compared to", "versus", "vs ", "more than last", "less than last",
		"how come", "what caused", "what happened",
		"trend", "over time", "month over month", "week over week",
		"last 3 month", "last 6 month", "my finance", "financial situation",
		"what can you say", "what can you tell",
	}
	for _, p := range complexPatterns {
		if strings.Contains(lower, p) {
			return "quality"
		}
	}

	// Long messages are likely complex
	if len(message) > 200 {
		return "quality"
	}

	// Everything else is simple — balance, transactions, spending, streak, etc.
	return "fast"
}

// buildTimeContext returns a short system instruction based on the current hour.
func buildTimeContext() string {
	return buildTimeContextAt(time.Now(), time.Local.String())
}

func buildTimeContextAt(now time.Time, timezone string) string {
	hour := now.Hour()
	datePrefix := fmt.Sprintf("[Time context: %s, %s. ", now.Format("Monday, January 2, 2006 15:04"), timezone)
	switch {
	case hour >= 5 && hour < 12:
		return datePrefix + "It is morning for the user. Be energetic and forward-looking.]"
	case hour >= 12 && hour < 17:
		return datePrefix + "It is afternoon for the user. Be efficient and practical.]"
	case hour >= 17 && hour < 21:
		return datePrefix + "It is evening for the user. Be relaxed and reflective.]"
	default:
		return datePrefix + "It is late night for the user. Be brief and calm, no lectures.]"
	}
}

// Pre-compiled safety filter patterns.
var safetyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)you should (buy|sell|invest|purchase|trade|short)`),
	regexp.MustCompile(`(?i)i recommend (buying|selling|investing|purchasing|trading|shorting)`),
	regexp.MustCompile(`(?i)definitely (buy|sell|invest|purchase|trade|short)`),
	regexp.MustCompile(`(?i)i('d| would) (suggest|advise|urge) (you )?(to )?(buy|sell|invest|purchase)`),
	regexp.MustCompile(`(?i)it would be wise to (buy|sell|invest|purchase)`),
	regexp.MustCompile(`(?i)you (need|must|have) to (buy|sell|invest|purchase)`),
	regexp.MustCompile(`(?i)consider (buying|selling|investing in|purchasing)`),
	regexp.MustCompile(`(?i)guaranteed returns?`),
	regexp.MustCompile(`(?i)risk[- ]free (investment|return|profit)`),
	regexp.MustCompile(`(?i)can('t| not) lose (money|if you)`),
	regexp.MustCompile(`(?i)put (all |most of )?your money (in|into)`),
	regexp.MustCompile(`(?i)go all[- ]in on`),
}

const safetyDisclaimer = "\n\nNote: Rail doesn't provide specific investment advice. Consider consulting a financial professional for personalized guidance."

// applySafetyFilter appends a disclaimer if financial advice patterns are detected
func (o *AgentAdapter) applySafetyFilter(content string) string {
	for _, re := range safetyPatterns {
		if re.MatchString(content) {
			o.logger.Warn("Safety filter triggered", zap.String("pattern", re.String()))
			return content + safetyDisclaimer
		}
	}
	return content
}

// sanitizeToolError returns a user-friendly error message and logs the real error.
func (o *AgentAdapter) sanitizeToolError(toolName string, err error) map[string]interface{} {
	o.logger.Error("Tool execution error", zap.String("tool", toolName), zap.Error(err))
	return map[string]interface{}{"error": "This information is temporarily unavailable"}
}

// safeToolArgKeys are argument keys safe to log (no PII).
var safeToolArgKeys = map[string]bool{
	"period": true, "limit": true, "type": true, "year": true,
	"report_type": true, "from": true, "to": true,
}

// sanitizeToolArgs redacts sensitive fields from tool arguments for logging.
func sanitizeToolArgs(args map[string]interface{}) map[string]interface{} {
	safe := make(map[string]interface{}, len(args))
	for k, v := range args {
		if safeToolArgKeys[k] {
			safe[k] = v
		}
	}
	return safe
}

// executeAccountSummary returns a comprehensive account overview in a single tool call.
func (o *AgentAdapter) executeAccountSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
	result := map[string]interface{}{}

	// Balances
	if o.aggregateStats != nil {
		spend, spendErr := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
		stash, stashErr := o.aggregateStats.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
		if spendErr != nil || stashErr != nil {
			result["balances_error"] = "balance fetch failed — try again in a moment"
			if spendErr != nil {
				result["spend_error"] = spendErr.Error()
			}
			if stashErr != nil {
				result["stash_error"] = stashErr.Error()
			}
		} else {
			result["spend_balance"] = "$" + spend.StringFixed(2)
			result["stash_balance"] = "$" + stash.StringFixed(2)
			result["total_balance"] = "$" + spend.Add(stash).StringFixed(2)
			result["currency"] = "USD"
			result["currency_note"] = "All balances are in US Dollars (USDC). Read as dollars and cents, e.g. $0.79 is seventy-nine cents, $1.07 is one dollar and seven cents."
		}
	} else {
		result["balances_error"] = "balance data is unavailable"
	}

	// This month's money flow
	if o.spending != nil {
		now := time.Now().UTC()
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		flow, err := o.spending.GetMoneyFlow(ctx, userID, monthStart, monthEnd)
		if err == nil {
			totalOut := flow.TotalWithdrawals.Add(flow.TotalCardSpend).Add(flow.TotalP2P)
			result["this_month"] = map[string]interface{}{
				"period":         fmt.Sprintf("%s 1 to today", now.Format("January")),
				"total_deposits": flow.TotalDeposits.StringFixed(2),
				"total_spent":    totalOut.StringFixed(2),
				"withdrawals":    flow.TotalWithdrawals.StringFixed(2),
				"card_spend":     flow.TotalCardSpend.StringFixed(2),
				"p2p_transfers":  flow.TotalP2P.StringFixed(2),
				"net_flow":       flow.TotalDeposits.Sub(totalOut).StringFixed(2),
				"deposit_count":  flow.DepositCount,
				"spending_count": flow.WithdrawalCount + flow.CardSpendCount + flow.P2PCount,
			}
		}
	}

	// Budget status
	if o.budgetProvider != nil {
		budget, err := o.budgetProvider.GetByUserID(ctx, userID)
		if err == nil && budget != nil {
			var monthlySpend decimal.Decimal
			if o.spending != nil {
				now := time.Now().UTC()
				monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
				summary, err := o.spending.GetSummary(ctx, userID, monthStart, now)
				if err == nil {
					monthlySpend = summary.Total
				}
			}
			remaining := budget.MonthlyLimit.Sub(monthlySpend)
			pctUsed := decimal.Zero
			if !budget.MonthlyLimit.IsZero() {
				pctUsed = monthlySpend.Div(budget.MonthlyLimit).Mul(decimal.NewFromInt(100))
			}
			status := "on_track"
			if pctUsed.GreaterThan(decimal.NewFromInt(90)) {
				status = "almost_exceeded"
			}
			if remaining.IsNegative() {
				status = "exceeded"
			}
			result["budget"] = map[string]interface{}{
				"monthly_limit": budget.MonthlyLimit.StringFixed(2),
				"spent":         monthlySpend.StringFixed(2),
				"remaining":     remaining.StringFixed(2),
				"percent_used":  pctUsed.StringFixed(1),
				"status":        status,
			}
		} else {
			result["budget"] = map[string]interface{}{"has_budget": false}
		}
	}

	// Streak
	if o.activityProvider != nil {
		streak, err := o.activityProvider.GetStreak(ctx, userID)
		if err == nil && streak != nil {
			result["streak_days"] = streak.CurrentStreak
		}
	}

	return result, nil
}

// buildBalanceContext fetches the user's current spend and stash balances
// and returns a system message string the LLM can reference immediately.
// Returns "" if balances can't be fetched (non-fatal).
func (o *AgentAdapter) buildBalanceContext(ctx context.Context, userID uuid.UUID) string {
	if o.aggregateStats == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	spend, errS := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeSpendingBalance)
	stash, errI := o.aggregateStats.GetAccountBalance(fetchCtx, userID, entities.AccountTypeStashBalance)
	if errS != nil && errI != nil {
		return ""
	}
	total := spend.Add(stash)
	return fmt.Sprintf(
		"[User's current balances — Spend: $%s USDC | Stash: $%s USD | Total: $%s. Use these as baseline context. For detailed history or transactions, call the appropriate tools.]",
		spend.StringFixed(2), stash.StringFixed(2), total.StringFixed(2),
	)
}

// buildStashLockContext returns a system context string about the user's stash lock status.
// Returns "" if the emergency withdrawer is not configured or stash is not locked.
func (o *AgentAdapter) buildStashLockContext(ctx context.Context, userID uuid.UUID) string {
	if o.emergencyWithdrawer == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	locked, err := o.emergencyWithdrawer.IsStashLocked(fetchCtx, userID)
	if err != nil || !locked {
		return ""
	}

	// Get preview with a small amount to learn lock age and fee tier
	preview, err := o.emergencyWithdrawer.EmergencyWithdrawalPreview(fetchCtx, userID, decimal.NewFromInt(1))
	if err != nil {
		return "[Stash lock: LOCKED. Early withdrawal available with fee.]"
	}

	daysRemaining := 90 - preview.LockAgeDays
	if daysRemaining < 0 {
		daysRemaining = 0
	}
	return fmt.Sprintf("[Stash lock: LOCKED, %d days into 90-day lock. Early withdrawal fee: %s%%. %d days until next window.]",
		preview.LockAgeDays, preview.FeePercent.StringFixed(0), daysRemaining)
}

// GenerateWrappedCards generates Spotify-Wrapped style cards
func (o *AgentAdapter) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
	cards := make([]entities.WrappedCard, 0)

	// Get portfolio stats
	stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
	if err == nil {
		returnPct := stats.WeeklyReturnPct.Mul(decimal.NewFromInt(100))
		direction := "up"
		if returnPct.LessThan(decimal.Zero) {
			direction = "down"
		}
		cards = append(cards, entities.WrappedCard{
			Type:    "performance_headline",
			Title:   "This Week's Vibe",
			Content: fmt.Sprintf("You're %s%.2f%% this week (%s)", getSign(returnPct), returnPct.Abs().InexactFloat64(), direction),
			Data:    map[string]interface{}{"weekly_return": returnPct.String()},
		})
	}

	// Get top mover
	movers, err := o.portfolioProvider.GetTopMovers(ctx, userID, 1)
	if err == nil && len(movers) > 0 {
		top := movers[0]
		cards = append(cards, entities.WrappedCard{
			Type:    "top_mover",
			Title:   "Your MVP Stock",
			Content: fmt.Sprintf("%s carried the team with %s%.1f%%", top.Symbol, getSign(top.ReturnPct), top.ReturnPct.Abs().InexactFloat64()),
			Data:    map[string]interface{}{"symbol": top.Symbol, "return": top.ReturnPct.String()},
		})
	}

	// Get contributions
	now := time.Now()
	contributions, err := o.activityProvider.GetContributions(ctx, userID, "all", now.AddDate(0, 0, -7), now)
	if err == nil {
		cards = append(cards, entities.WrappedCard{
			Type:    "contributions",
			Title:   "Money Moves",
			Content: fmt.Sprintf("$%s in deposits this week", contributions.Deposits.StringFixed(0)),
			Data:    map[string]interface{}{"deposits": contributions.Deposits.String(), "total": contributions.Total.String()},
		})
	}

	// Get streak
	streak, err := o.activityProvider.GetStreak(ctx, userID)
	if err == nil && streak.CurrentStreak > 0 {
		cards = append(cards, entities.WrappedCard{
			Type:    "streak",
			Title:   "On Fire",
			Content: fmt.Sprintf("%d day investing streak!", streak.CurrentStreak),
			Data:    map[string]interface{}{"current_streak": streak.CurrentStreak, "longest_streak": streak.LongestStreak},
		})
	}

	// Get news count
	news, err := o.newsProvider.GetWeeklyNews(ctx, userID)
	if err == nil && len(news) > 0 {
		cards = append(cards, entities.WrappedCard{
			Type:    "news",
			Title:   "What's Happening",
			Content: fmt.Sprintf("%d updates on your holdings", len(news)),
			Data:    map[string]interface{}{"count": len(news)},
		})
	}

	return cards, nil
}

func getSign(d decimal.Decimal) string {
	if d.GreaterThanOrEqual(decimal.Zero) {
		return "+"
	}
	return ""
}

// ChatResponse represents the response from a chat interaction
type ChatResponse struct {
	Content       string                  `json:"content"`
	Cards         []entities.InsightCard  `json:"cards,omitempty"`
	ToolCalls     []ToolResult            `json:"tool_calls,omitempty"`
	TokensUsed    int                     `json:"tokens_used"`
	Provider      string                  `json:"provider"`
	PendingAction *entities.PendingAction `json:"pending_action,omitempty"`
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	Name   string                 `json:"name"`
	Result map[string]interface{} `json:"result"`
}
