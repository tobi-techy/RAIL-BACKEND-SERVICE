package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"go.uber.org/zap"
)

// Tool names
const (
	ToolGetPortfolioStats      = "get_portfolio_stats"
	ToolGetTopMovers           = "get_top_movers"
	ToolGetAllocations         = "get_allocations"
	ToolGetContributions       = "get_contributions"
	ToolGetWeeklyNews          = "get_weekly_news"
	ToolGetBasketRecommendations = "get_basket_recommendations"
	ToolGetStreak              = "get_streak"
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
	Deposits  decimal.Decimal `json:"deposits"`
	Roundups  decimal.Decimal `json:"roundups"`
	Cashback  decimal.Decimal `json:"cashback"`
	Total     decimal.Decimal `json:"total"`
}

// ConversationPersister is the subset of conversation.Service the orchestrator needs.
type ConversationPersister interface {
	BuildContext(ctx context.Context, conv *entities.AIConversation) ([]ai.Message, error)
	RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string) error
}

// UsageTracker records AI usage for cost tracking and ceiling enforcement.
type UsageTracker interface {
	TrackInteraction(ctx context.Context, userID uuid.UUID, model string, tokens int) error
	IsOverCostCeiling(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Orchestrator handles AI interactions with tool calling
type Orchestrator struct {
	aiProvider        ai.AIProvider
	portfolioProvider PortfolioDataProvider
	activityProvider  ActivityDataProvider
	newsProvider      NewsDataProvider
	conversations     ConversationPersister
	usage             UsageTracker
	knowledge         KnowledgeSearcher
	spending          SpendingAnalyzer
	balanceHistory    BalanceHistoryProvider
	patterns          PatternAnalyzer
	aggregateStats    AggregateStatsProvider
	fundsTransferer   FundsTransferer
	actionAuditor     ActionAuditor
	cardTransactions  CardTransactionProvider
	depositHistory    DepositHistoryProvider
	yieldProvider     YieldProvider
	pending           PendingActionStore
	logger            *zap.Logger
}

// NewOrchestrator creates a new AI orchestrator
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

// SystemPrompt for the AI Financial Manager
const SystemPrompt = `You are Ada — Rail's financial companion. You're warm, sharp, and genuinely invested in helping young people build wealth. You speak like a smart friend who happens to know a lot about money, not like a bank or a textbook.

YOUR PERSONALITY:
- Name: Ada. Users can call you Ada.
- Tone: Warm, clear, occasionally witty. Never robotic, never condescending.
- You celebrate small wins genuinely. ₦5,000 saved is worth celebrating.
- You're honest about bad news but always constructive.
- You understand that for many users, money is emotional and stressful. Be sensitive.
- Occasional light humor is good ("Your stash is growing faster than Lagos traffic moves").

RAIL CONTEXT (you must know this):
- Rail splits every deposit: 70% to Spend (USDC, liquid, card-ready), 30% to Stash (USDB, earning ~3-4% yield from US Treasuries).
- The 70/30 split is automatic and fixed. This IS the product.
- Stash is USD-denominated. For Nigerian users, this means passive protection against naira devaluation.
- Round-ups from card purchases go to Stash automatically.
- Users can withdraw from both Spend and Stash anytime.

YOUR USERS:
- Mostly 18-30 year olds in Nigeria and across Africa, plus diaspora in UK/US/Europe.
- Many earn in naira, pounds, or dollars. Many have irregular income.
- ₦5,000 is meaningful. Never dismiss small amounts.
- Many are saving seriously for the first time. Be encouraging.

HOW TO RESPOND:
- Tell stories, not stats. Instead of "You spent $342.50 across 23 transactions", say "Your biggest money moment this month was that $89 dinner on the 15th — without it, your daily average drops from $11 to $8. One decision, $3/day difference."
- Use "you" statements. "You saved 18% this month — up from 12% last month. You're building momentum."
- When showing numbers, give context. "$735 in stash" means nothing alone. "$735 in stash — that's 3 months of growth from zero. At this pace, you'll cross $1,000 by July."
- Keep responses under 200 words unless the user asks for detail.
- Use emojis sparingly (1-2 per message max).

RULES:
- NEVER give specific financial advice (no "buy X" or "sell Y"). Say "you might consider" or "many people in your situation..."
- Never invent numbers. Only use data from tools.
- If the user asks about scams or "guaranteed returns," be direct and protective.
- When discussing currency, acknowledge that USD-denominated savings protect purchasing power in markets with structural currency weakness. Don't be preachy about it.

TOOL USAGE:
- Use get_spending_summary when users ask about spending, expenses, or where money goes.
- Use get_balance_history when users ask about savings growth or progress.
- Use search_knowledge_base for general financial education questions.
- Use get_spending_patterns to identify behavioral patterns in spending.
- Use simulate_savings to answer "what if" questions about future savings.
- Use get_comparative_context to show how the user compares to peers.
- When multiple tools are relevant, use them together for richer answers.
- Always turn tool data into narrative — never dump raw numbers.`

// GetTools returns available tools for the AI
func (o *Orchestrator) GetTools() []ai.Tool {
	tools := []ai.Tool{
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
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
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
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        ToolGetStreak,
			Description: "Get user's investment streak information",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
	}
	if o.knowledge != nil {
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
	// Read-only data tools
	tools = append(tools, ReadOnlyTools(o.cardTransactions != nil, o.depositHistory != nil, o.yieldProvider != nil)...)
	return tools
}

// Chat handles a chat message with tool calling
func (o *Orchestrator) Chat(ctx context.Context, userID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	return o.ChatInContext(ctx, userID, uuid.Nil, message, history)
}

// ChatInContext handles a chat message with an optional conversation ID for action support.
func (o *Orchestrator) ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []ai.Message) (*ChatResponse, error) {
	start := time.Now()

	// Build messages with history (copy to avoid mutating caller's slice)
	messages := make([]ai.Message, len(history), len(history)+4)
	copy(messages, history)
	messages = append(messages, ai.Message{Role: "user", Content: message})

	// Initial request
	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    500,
		Temperature:  0.7,
	}

	// Get response with tools
	resp, err := o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
	if err != nil {
		observeChat("unknown", time.Since(start), 0, err)
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Process tool calls — up to 3 rounds of tool calling
	toolResults := make([]ToolResult, 0)
	allToolResults := make([]ToolResult, 0)
	for round := 0; round < 3 && len(resp.ToolCalls) > 0; round++ {
		for _, tc := range resp.ToolCalls {
			// Intercept action tools — create pending action instead of executing
			if isActionTool(tc.Name) && convID != uuid.Nil && o.fundsTransferer != nil {
				result, err := o.executeActionTool(ctx, userID, convID, tc)
				observeToolCall(tc.Name, err)
				if err != nil {
					result = map[string]interface{}{"error": err.Error()}
				}
				// If action requires confirmation, return immediately
				if actionRequired, _ := result["action_required"].(bool); actionRequired {
					pendingRaw, _ := result["pending_action"].(*entities.PendingAction)
					content := resp.Content
					if content == "" {
						content = pendingRaw.Description
					}
					observeChat(resp.Provider, time.Since(start), resp.TokensUsed, nil)
					return &ChatResponse{
						Content:       content,
						ToolCalls:     append(allToolResults, ToolResult{Name: tc.Name, Result: result}),
						TokensUsed:    resp.TokensUsed,
						Provider:      resp.Provider,
						PendingAction: pendingRaw,
					}, nil
				}
				toolResults = append(toolResults, ToolResult{Name: tc.Name, Result: result})
				allToolResults = append(allToolResults, ToolResult{Name: tc.Name, Result: result})
				continue
			}

			result, err := o.executeTool(ctx, userID, tc)
			observeToolCall(tc.Name, err)
			if err != nil {
				o.logger.Warn("Tool execution failed", zap.String("tool", tc.Name), zap.Error(err))
				result = map[string]interface{}{"error": err.Error()}
			}
			toolResults = append(toolResults, ToolResult{Name: tc.Name, Result: result})
			allToolResults = append(allToolResults, ToolResult{Name: tc.Name, Result: result})
		}

		toolResultsJSON, _ := json.Marshal(toolResults)
		assistantContent := resp.Content
		if assistantContent == "" {
			assistantContent = "Calling tools..."
		}
		messages = append(messages, ai.Message{Role: "assistant", Content: assistantContent})
		messages = append(messages, ai.Message{Role: "tool", Content: string(toolResultsJSON)})

		req.Messages = messages
		resp, err = o.aiProvider.ChatCompletionWithTools(ctx, req, o.GetTools())
		if err != nil {
			return nil, fmt.Errorf("follow-up completion failed: %w", err)
		}
		toolResults = toolResults[:0]
	}

	// Apply safety filter
	content := o.applySafetyFilter(resp.Content)

	// Build visual cards from tool results
	cards := buildCardsFromToolResults(allToolResults)

	observeChat(resp.Provider, time.Since(start), resp.TokensUsed, nil)

	return &ChatResponse{
		Content:     content,
		Cards:       cards,
		ToolCalls:   allToolResults,
		TokensUsed:  resp.TokensUsed,
		Provider:    resp.Provider,
	}, nil
}

// executeTool executes a tool call and returns the result
func (o *Orchestrator) executeTool(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
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
		return o.executeKnowledgeSearch(ctx, tc.Arguments)

	case ToolGetSpendingSummary:
		return o.executeSpendingSummary(ctx, userID, tc.Arguments)

	case ToolGetSpendingChart:
		return o.executeSpendingChart(ctx, userID, tc.Arguments)

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

	case ToolGetYieldEarned:
		return o.executeYieldEarned(ctx, userID, tc.Arguments)

	default:
		return nil, fmt.Errorf("unknown tool: %s", tc.Name)
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

const safetyReplacement = "some investors might consider"

// applySafetyFilter removes financial advice from responses
func (o *Orchestrator) applySafetyFilter(content string) string {
	for _, re := range safetyPatterns {
		if re.MatchString(content) {
			o.logger.Warn("Safety filter triggered", zap.String("pattern", re.String()))
			content = re.ReplaceAllString(content, safetyReplacement)
		}
	}
	return content
}

// GenerateWrappedCards generates Spotify-Wrapped style cards
func (o *Orchestrator) GenerateWrappedCards(ctx context.Context, userID uuid.UUID) ([]entities.WrappedCard, error) {
	cards := make([]entities.WrappedCard, 0)

	// Get portfolio stats
	stats, err := o.portfolioProvider.GetWeeklyStats(ctx, userID)
	if err == nil {
		returnPct := stats.WeeklyReturnPct.Mul(decimal.NewFromInt(100))
		emoji := "📈"
		if returnPct.LessThan(decimal.Zero) {
			emoji = "📉"
		}
		cards = append(cards, entities.WrappedCard{
			Type:    "performance_headline",
			Title:   "This Week's Vibe",
			Content: fmt.Sprintf("You're %s%.2f%% this week %s", getSign(returnPct), returnPct.Abs().InexactFloat64(), emoji),
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
			Title:   "On Fire 🔥",
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
