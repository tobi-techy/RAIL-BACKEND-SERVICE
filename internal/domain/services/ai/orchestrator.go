package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Tool names
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
}

// ConversationPersister is the subset of conversation.Service the orchestrator needs.
type ConversationPersister interface {
	BuildContext(ctx context.Context, conv *entities.AIConversation) ([]ai.Message, error)
	RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string, cards []entities.InsightCard) error
	UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error
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

// Orchestrator handles AI interactions with tool calling
type Orchestrator struct {
	aiProvider          ai.AIProvider
	portfolioProvider   PortfolioDataProvider
	activityProvider    ActivityDataProvider
	newsProvider        NewsDataProvider
	conversations       ConversationPersister
	usage               UsageTracker
	knowledge           KnowledgeSearcher
	spending            SpendingAnalyzer
	balanceHistory      BalanceHistoryProvider
	patterns            PatternAnalyzer
	aggregateStats      AggregateStatsProvider
	fundsTransferer     FundsTransferer
	actionAuditor       ActionAuditor
	actionHistory       ActionHistoryReader
	cardTransactions    CardTransactionProvider
	depositHistory      DepositHistoryProvider
	yieldProvider       YieldProvider
	withdrawalHistory   WithdrawalHistoryProvider
	receiptHistory      ReceiptHistoryProvider
	receiptSplitter     ReceiptSplitter
	withdrawalInitiator WithdrawalInitiator
	bankAccountProvider BankAccountProvider
	budgetProvider      BudgetProvider
	financialProfile    FinancialProfileProvider
	obligations         FinancialObligationProvider
	obligationManager   FinancialObligationManager
	automationCreator   AutomationCreator
	obligationCreator   ObligationCreator
	currencyRates       CurrencyRateProvider
	userProfile         UserProfileProvider
	reportEmail         ReportEmailSender
	savingsGoalStore    SavingsGoalStore
	sharedGoalCreator   SharedGoalCreator
	recurringDetector   RecurringExpenseDetector
	warrantyTracker     WarrantyTracker
	receiptChallenges   ReceiptChallengeProvider
	savingsSuggestions  SavingsSuggestionProvider
	priceTracker        PriceTracker
	merchantAnalyzer    MerchantAnalyzer
	pending             PendingActionStore
	accountChecker      UserAccountChecker
	emergencyWithdrawer EmergencyWithdrawer
	automationProvider  AutomationProvider
	goalProtection      GoalProtectionProvider
	voiceLimiter        *VoiceDailyLimiter
	contextSignals      ContextSignalProvider
	memory              *MemoryService
	miriamIntelligence  MiriamIntelligenceReader
	bankStatementCtx    *BankStatementContextProvider
	nairaCtx            *nairaCtx
	supermemory         SupermemoryClient
	webSearcher         WebSearcher
	moneyMoveNotifier   MoneyMoveNotifier
	logger              *zap.Logger
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
func (o *Orchestrator) SetMoneyMoveNotifier(n MoneyMoveNotifier) {
	o.moneyMoveNotifier = n
}

// OrchestratorDeps groups all optional dependencies for the Orchestrator.
// Use NewOrchestratorWithDeps to wire them at construction time for compile-time safety.
type OrchestratorDeps struct {
	Conversations      ConversationPersister
	Usage              UsageTracker
	Knowledge          KnowledgeSearcher
	Spending           SpendingAnalyzer
	BalanceHistory     BalanceHistoryProvider
	Patterns           PatternAnalyzer
	AggregateStats     AggregateStatsProvider
	FundsTransferer    FundsTransferer
	ActionAuditor      ActionAuditor
	ActionHistory      ActionHistoryReader
	CardTransactions   CardTransactionProvider
	DepositHistory     DepositHistoryProvider
	YieldProvider      YieldProvider
	WithdrawalHistory  WithdrawalHistoryProvider
	ReceiptHistory     ReceiptHistoryProvider
	ReceiptSplitter    ReceiptSplitter
	BudgetProvider     BudgetProvider
	FinancialProfile   FinancialProfileProvider
	Obligations        FinancialObligationProvider
	ObligationManager  FinancialObligationManager
	AutomationCreator  AutomationCreator
	ObligationCreator  ObligationCreator
	CurrencyRates      CurrencyRateProvider
	UserProfile        UserProfileProvider
	ReportEmail        ReportEmailSender
	Pending            PendingActionStore
	SavingsGoalStore   SavingsGoalStore
	RecurringDetector  RecurringExpenseDetector
	WarrantyTracker    WarrantyTracker
	ReceiptChallenges  ReceiptChallengeProvider
	SavingsSuggestions SavingsSuggestionProvider
	PriceTracker       PriceTracker
	MerchantAnalyzer   MerchantAnalyzer
	AccountChecker     UserAccountChecker
	AutomationProvider AutomationProvider
	Memory             *MemoryService
	MiriamIntelligence MiriamIntelligenceReader
	Supermemory        SupermemoryClient
}

// NewOrchestratorWithDeps creates a new AI orchestrator with all dependencies provided upfront.
// Prefer this over NewOrchestrator + individual SetX calls.
func NewOrchestratorWithDeps(
	aiProvider ai.AIProvider,
	portfolioProvider PortfolioDataProvider,
	activityProvider ActivityDataProvider,
	newsProvider NewsDataProvider,
	logger *zap.Logger,
	deps OrchestratorDeps,
) *Orchestrator {
	pending := deps.Pending
	if pending == nil {
		pending = newInMemoryPendingActions()
	}
	return &Orchestrator{
		aiProvider:         aiProvider,
		portfolioProvider:  portfolioProvider,
		activityProvider:   activityProvider,
		newsProvider:       newsProvider,
		conversations:      deps.Conversations,
		usage:              deps.Usage,
		knowledge:          deps.Knowledge,
		spending:           deps.Spending,
		balanceHistory:     deps.BalanceHistory,
		patterns:           deps.Patterns,
		aggregateStats:     deps.AggregateStats,
		fundsTransferer:    deps.FundsTransferer,
		actionAuditor:      deps.ActionAuditor,
		actionHistory:      deps.ActionHistory,
		cardTransactions:   deps.CardTransactions,
		depositHistory:     deps.DepositHistory,
		yieldProvider:      deps.YieldProvider,
		withdrawalHistory:  deps.WithdrawalHistory,
		receiptHistory:     deps.ReceiptHistory,
		receiptSplitter:    deps.ReceiptSplitter,
		budgetProvider:     deps.BudgetProvider,
		financialProfile:   deps.FinancialProfile,
		obligations:        deps.Obligations,
		obligationManager:  deps.ObligationManager,
		automationCreator:  deps.AutomationCreator,
		obligationCreator:  deps.ObligationCreator,
		currencyRates:      deps.CurrencyRates,
		userProfile:        deps.UserProfile,
		reportEmail:        deps.ReportEmail,
		pending:            pending,
		savingsGoalStore:   deps.SavingsGoalStore,
		recurringDetector:  deps.RecurringDetector,
		warrantyTracker:    deps.WarrantyTracker,
		receiptChallenges:  deps.ReceiptChallenges,
		savingsSuggestions: deps.SavingsSuggestions,
		priceTracker:       deps.PriceTracker,
		merchantAnalyzer:   deps.MerchantAnalyzer,
		accountChecker:     deps.AccountChecker,
		automationProvider: deps.AutomationProvider,
		memory:             deps.Memory,
		miriamIntelligence: deps.MiriamIntelligence,
		supermemory:        deps.Supermemory,
		logger:             logger,
	}
}

// Deprecated: Use NewOrchestratorWithDeps instead for compile-time dependency safety.
func NewOrchestrator(
	aiProvider ai.AIProvider,
	portfolioProvider PortfolioDataProvider,
	activityProvider ActivityDataProvider,
	newsProvider NewsDataProvider,
	logger *zap.Logger,
) *Orchestrator {
	return &Orchestrator{
		aiProvider:        aiProvider,
		portfolioProvider: portfolioProvider,
		activityProvider:  activityProvider,
		newsProvider:      newsProvider,
		pending:           newInMemoryPendingActions(),
		logger:            logger,
	}
}


// GetTools returns available tools for the AI
func (o *Orchestrator) GetTools() []ai.Tool {
	tools := []ai.Tool{
		{
			Name:        ToolGetAccountSummary,
			Description: "Current balances, this month's totals, budget status, and streak in one call. Use for 'how much do I have', 'what's my balance', 'give me an overview'. NOT for detailed spending breakdown.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetPortfolioStats,
			Description: "Get current portfolio statistics including total value and returns",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"period": map[string]interface{}{"type": "string", "enum": []string{"1w", "1m", "3m", "1y"}},
				},
			},
		},
		{
			Name:        ToolGetTopMovers,
			Description: "Get biggest gainers and losers in the portfolio",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{"type": "integer", "default": 5},
				},
			},
		},
		{
			Name:        ToolGetAllocations,
			Description: "Get current portfolio allocation by basket",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetContributions,
			Description: "Get user contributions (deposits, round-ups, cashback)",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":   map[string]interface{}{"type": "string", "enum": []string{"all", "deposit", "roundup", "cashback"}},
					"period": map[string]interface{}{"type": "string", "enum": []string{"1w", "1m", "3m"}},
				},
			},
		},
		{
			Name:        ToolGetWeeklyNews,
			Description: "Get relevant news for user holdings this week",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
		{
			Name:        ToolGetStreak,
			Description: "Get user's investment streak information",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "required": []string{}, "additionalProperties": false},
		},
	}
	if o.knowledge != nil || o.supermemory != nil {
		tools = append(tools, KnowledgeTool())
	}
	if o.spending != nil {
		tools = append(tools, SpendingTools()...)
	}
	if o.balanceHistory != nil {
		tools = append(tools, BalanceHistoryTool())
	}
	if o.patterns != nil {
		tools = append(tools, SpendingPatternsTool())
	}
	if o.aggregateStats != nil {
		tools = append(tools, ComparativeContextTool())
	}
	// Simulator is always available (pure computation)
	tools = append(tools, SimulateSavingsTool())
	// Action tools (require funds transferer)
	if o.fundsTransferer != nil {
		tools = append(tools, ActionTools()...)
	}
	// Withdrawal tool (voice-triggered)
	if o.withdrawalInitiator != nil {
		tools = append(tools, WithdrawalTool())
	}
	// Bank accounts lookup (for withdrawal confirmation)
	if o.bankAccountProvider != nil {
		tools = append(tools, LinkedBanksTool())
	}
	// Read-only data tools
	_, hasIncomeTrend := o.depositHistory.(DepositIncomeProvider)
	tools = append(tools, ReadOnlyTools(o.cardTransactions != nil, o.depositHistory != nil, hasIncomeTrend, o.yieldProvider != nil, o.withdrawalHistory != nil, o.receiptHistory != nil)...)
	// Tax, email, and goals tools
	tools = append(tools, TaxAndReportTools(o.userProfile != nil, o.reportEmail != nil)...)
	// Budget tools
	if o.budgetProvider != nil {
		tools = append(tools, BudgetTools()...)
	}
	// Durable financial profile tools
	if o.financialProfile != nil {
		tools = append(tools, FinancialProfileTools()...)
	}
	if o.obligationManager != nil {
		tools = append(tools, FinancialObligationTools()...)
	}
	if o.financialProfile != nil && o.aggregateStats != nil {
		tools = append(tools, MoneyOperatingPlanTool())
	}
	// Recurring expense detection
	if o.recurringDetector != nil {
		tools = append(tools, RecurringExpenseTool())
	}
	// Warranty tracking
	if o.warrantyTracker != nil {
		tools = append(tools, WarrantyTool())
	}
	// Receipt challenges & savings suggestions
	if o.receiptChallenges != nil {
		tools = append(tools, ReceiptChallengeTool())
	}
	if o.savingsSuggestions != nil {
		tools = append(tools, SavingsSuggestionTool())
	}
	if o.spending != nil && o.aggregateStats != nil {
		tools = append(tools, FinancialIntelligenceTools(o.actionHistory != nil)...)
		tools = append(tools, MiriamBriefTool())
	}
	if o.miriamIntelligence != nil {
		tools = append(tools, MiriamIntelligenceTools()...)
	}
	// Expanded insight cards (subscriptions, runway, deposits, yield, comparisons)
	if o.spending != nil || o.recurringDetector != nil {
		tools = append(tools, ExpandedInsightTools()...)
	}
	tools = append(tools, FinancialGovernanceTools(o.hasFinancialAdviceProviders(), o.hasFinancialTimelineProviders())...)
	// Price tracking
	if o.priceTracker != nil {
		tools = append(tools, PriceTrackingTool())
	}
	// Merchant intelligence
	if o.merchantAnalyzer != nil {
		tools = append(tools, MerchantInsightsTool())
	}
	// Receipt splitting (action tool — requires confirmation)
	if o.receiptHistory != nil {
		tools = append(tools, SplitReceiptTool())
	}
	// Memory controls (list/forget)
	if o.memory != nil {
		tools = append(tools, MemoryTools()...)
	}
	// Automation tools
	if o.automationProvider != nil {
		tools = append(tools, AutomationTools()...)
	}
	// Expressive + engagement tools — always available; Miriam uses them based on conversation context.
	tools = append(tools, SendMemeTool())
	tools = append(tools, SendVoiceMessageTool())
	tools = append(tools, CelebrateTool())
	tools = append(tools, SendPollTool())
	// Investment products (always available)
	tools = append(tools, InvestmentProductTool())
	// Web search (when Tavily is configured)
	if o.webSearcher != nil {
		tools = append(tools, WebSearchTool())
	}
	return tools
}

func (o *Orchestrator) hasFinancialAdviceProviders() bool {
	return o.spending != nil && o.aggregateStats != nil
}

func (o *Orchestrator) hasFinancialTimelineProviders() bool {
	return o.depositHistory != nil ||
		o.withdrawalHistory != nil ||
		o.cardTransactions != nil ||
		o.spending != nil ||
		o.actionHistory != nil ||
		o.financialProfile != nil
}

// Chat handles a chat message with tool calling
func (o *Orchestrator) Chat(ctx context.Context, userID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	return o.ChatInContext(ctx, userID, uuid.Nil, message, history)
}

// toolCacheKey returns a cache key for a tool call based on name + serialized args.
func toolCacheKey(tc ai.ToolCall) string {
	args, _ := json.Marshal(tc.Arguments)
	return tc.Name + ":" + string(args)
}

// ChatInContext handles a chat message with an optional conversation ID for action support.
func (o *Orchestrator) ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	return o.ChatInContextWithOptions(ctx, userID, convID, message, history, ChatOptions{})
}

func (o *Orchestrator) ChatInContextWithOptions(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message, opts ChatOptions) (*ChatResponse, error) {
	// Enforce a total wall-clock timeout to prevent runaway tool loops.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	start := time.Now()

	// Per-request tool result cache to avoid duplicate DB hits within a single chat call
	toolCache := make(map[string]map[string]interface{})

	// Build messages with history (copy to avoid mutating caller's slice)
	messages := make([]ai.Message, len(history), len(history)+12)
	copy(messages, history)

	// Assemble all context in parallel (3s ceiling)
	messages = append(messages, o.assembleContext(ctx, userID, ContextAssemblyOpts{
		ToneMode: opts.ToneMode,
		Message:  message,
	})...)

	// Tool usage rules — skip for very short casual messages to save tokens
	if len(message) > 15 || classifyMessage(message) != CategoryFull {
		messages = append(messages, ai.Message{Role: "system", Content: SystemPromptTools})
	}

	messages = append(messages, ai.Message{Role: "user", Content: message})

	// Initial request
	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPromptV2,
		MaxTokens:    2048,
		Temperature:  ai.Float64(0.6),
		ModelHint:    classifyQueryComplexity(message),
	}

	// Get response with tools
	tools := o.RouteTools(message)
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, tools)
	if err != nil {
		observeChat("unknown", time.Since(start), 0, err)
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Process tool calls — up to 5 rounds of tool calling
	totalTokens := resp.TokensUsed
	toolResults := make([]ToolResult, 0)
	allToolResults := make([]ToolResult, 0)
	for round := 0; round < 5 && len(resp.ToolCalls) > 0; round++ {
		// Separate action tools (sequential) from read-only tools (parallelizable)
		type indexedCall struct {
			index int
			tc    ai.ToolCall
		}
		roundResults := make([]ToolResult, len(resp.ToolCalls))
		var readOnlyCalls []indexedCall

		for i, tc := range resp.ToolCalls {
			// Intercept action tools — create pending action instead of executing
			if isActionTool(tc.Name) && convID != uuid.Nil && o.canCreateActionTool(tc.Name) {
				result, err := o.executeActionTool(ctx, userID, convID, tc)
				observeToolCall(tc.Name, err)
				if err != nil {
					result = o.sanitizeToolError(tc.Name, err)
				}
				// If action requires confirmation, return immediately
				if actionRequired, _ := result["action_required"].(bool); actionRequired {
					pendingRaw, _ := result["pending_action"].(*entities.PendingAction)
					content := resp.Content
					if content == "" {
						content = pendingRaw.Description
					}
					observeChat(resp.Provider, time.Since(start), totalTokens, nil)
					return &ChatResponse{
						Content:       content,
						ToolCalls:     append(allToolResults, ToolResult{Name: tc.Name, Result: result}),
						TokensUsed:    totalTokens,
						Provider:      resp.Provider,
						PendingAction: pendingRaw,
					}, nil
				}
				roundResults[i] = ToolResult{Name: tc.Name, Result: result}
				continue
			}

			// Check per-request cache
			cacheKey := toolCacheKey(tc)
			if cached, ok := toolCache[cacheKey]; ok {
				roundResults[i] = ToolResult{Name: tc.Name, Result: cached}
				continue
			}

			readOnlyCalls = append(readOnlyCalls, indexedCall{index: i, tc: tc})
		}

		// Execute read-only tools in parallel when multiple are pending
		if len(readOnlyCalls) > 1 {
			var wg sync.WaitGroup
			for _, ic := range readOnlyCalls {
				wg.Add(1)
				go func(idx int, tc ai.ToolCall) {
					defer wg.Done()
					result, err := o.executeTool(ctx, userID, tc)
					observeToolCall(tc.Name, err)
					if err != nil {
						o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(err))
						result = o.sanitizeToolError(tc.Name, err)
					}
					roundResults[idx] = ToolResult{Name: tc.Name, Result: result}
				}(ic.index, ic.tc)
			}
			wg.Wait()
			// Cache results after all goroutines complete (no concurrent map writes)
			for _, ic := range readOnlyCalls {
				if r := roundResults[ic.index].Result; r != nil {
					if _, hasErr := r["error"]; !hasErr {
						toolCache[toolCacheKey(ic.tc)] = r
					}
				}
			}
		} else if len(readOnlyCalls) == 1 {
			ic := readOnlyCalls[0]
			result, err := o.executeTool(ctx, userID, ic.tc)
			observeToolCall(ic.tc.Name, err)
			if err != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", ic.tc.Name), zap.Error(err))
				result = o.sanitizeToolError(ic.tc.Name, err)
			} else {
				toolCache[toolCacheKey(ic.tc)] = result
			}
			roundResults[ic.index] = ToolResult{Name: ic.tc.Name, Result: result}
		}

		toolResults = toolResults[:0]
		for _, tr := range roundResults {
			toolResults = append(toolResults, tr)
			allToolResults = append(allToolResults, tr)
		}

		// Build assistant message with tool_calls preserved (required by OpenAI-compatible APIs)
		assistantContent := resp.Content
		if assistantContent == "" {
			assistantContent = "Calling tools..."
		}
		assistantMsg := ai.Message{
			Role:             "assistant",
			Content:          assistantContent,
			ToolCalls:        resp.ToolCalls,
			ReasoningContent: resp.ReasoningContent,
		}
		messages = append(messages, assistantMsg)

		// Append each tool result with its corresponding tool_call_id.
		// roundResults[i] maps 1:1 to resp.ToolCalls[i] by construction.
		for i, tr := range roundResults {
			resultJSON, _ := json.Marshal(tr.Result)
			toolCallID := ""
			if i < len(resp.ToolCalls) {
				toolCallID = resp.ToolCalls[i].ID
			}
			messages = append(messages, ai.Message{
				Role:       "tool",
				Content:    string(resultJSON),
				Name:       tr.Name,
				ToolCallID: toolCallID,
			})
		}

		// Increase MaxTokens for follow-up completions when heavy tools (audit, financial health)
		// produce large payloads that require detailed LLM responses.
		maxFollowUp := 2048
		for _, tr := range roundResults {
			if tr.Name == ToolGetFinancialAudit || tr.Name == ToolGetFinancialHealth {
				maxFollowUp = 4096
				break
			}
		}
		req.MaxTokens = maxFollowUp
		req.Messages = messages
		resp, err = o.aiProvider.ChatCompletionWithTools(ctx, req, tools)
		if err != nil {
			observeChat("unknown", time.Since(start), totalTokens, err)
			return nil, fmt.Errorf("follow-up completion failed: %w", err)
		}
		totalTokens += resp.TokensUsed
		toolResults = toolResults[:0]
	}

	// Apply safety filter
	content := o.applySafetyFilter(resp.Content)

	// Quality gate: catch flat/boring responses and retry once
	if verdict := CheckResponseQuality(content); !verdict.Pass {
		hint := QualityCorrectionHint(verdict.Failures)
		if hint != "" {
			retryMessages := make([]ai.Message, len(req.Messages), len(req.Messages)+2)
			copy(retryMessages, req.Messages)
			retryMessages = append(retryMessages, ai.Message{Role: "assistant", Content: content}, ai.Message{Role: "system", Content: hint})
			retryReq := &ai.ChatRequest{Messages: retryMessages, SystemPrompt: SystemPromptV2, MaxTokens: 2048, Temperature: ai.Float64(0.7), ModelHint: "fast"}
			if retryResp, err := o.aiProvider.ChatCompletion(ctx, retryReq); err == nil && retryResp.Content != "" {
				content = o.applySafetyFilter(retryResp.Content)
				totalTokens += retryResp.TokensUsed
			}
		}
	}

	// Build visual cards from tool results
	cards := buildCardsFromToolResults(allToolResults)

	observeChat(resp.Provider, time.Since(start), totalTokens, nil)

	return &ChatResponse{
		Content:    content,
		Cards:      cards,
		ToolCalls:  allToolResults,
		TokensUsed: totalTokens,
		Provider:   resp.Provider,
	}, nil
}

// ExecuteToolPublic exposes tool execution for the voice handler.
func (o *Orchestrator) ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	if tc.Name == ToolVoiceMoneyAction {
		return o.executeVoiceMoneyAction(ctx, userID, tc.Arguments)
	}
	if isActionTool(tc.Name) && o.canCreateActionTool(tc.Name) {
		return o.executeActionToolDirect(ctx, userID, tc)
	}
	return o.executeTool(ctx, userID, tc)
}

// executeTool executes a tool call and returns the result
func (o *Orchestrator) executeTool(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
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
func (o *Orchestrator) enrichWithMemory(ctx context.Context, userID uuid.UUID, tc ai.ToolCall, result map[string]interface{}) {
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
func (o *Orchestrator) executeToolInner(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
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

	default:
		// Action tools (transfer_funds, initiate_withdrawal, etc.) — execute directly in voice mode.
		// In chat mode these are intercepted earlier with pending/confirm flow.
		// In voice mode (ExecuteToolPublic), AssemblyAI handles confirmation conversationally.
		if isActionTool(tc.Name) && o.canCreateActionTool(tc.Name) {
			return o.executeActionToolDirect(ctx, userID, tc)
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
func (o *Orchestrator) applySafetyFilter(content string) string {
	for _, re := range safetyPatterns {
		if re.MatchString(content) {
			o.logger.Warn("Safety filter triggered", zap.String("pattern", re.String()))
			return content + safetyDisclaimer
		}
	}
	return content
}

// sanitizeToolError returns a user-friendly error message and logs the real error.
func (o *Orchestrator) sanitizeToolError(toolName string, err error) map[string]interface{} {
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
func (o *Orchestrator) executeAccountSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error) {
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
func (o *Orchestrator) buildBalanceContext(ctx context.Context, userID uuid.UUID) string {
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
func (o *Orchestrator) buildStashLockContext(ctx context.Context, userID uuid.UUID) string {
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
func (o *Orchestrator) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
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
