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
	contextSignals      ContextSignalProvider
	memory              *MemoryService
	miriamIntelligence  MiriamIntelligenceReader
	logger              *zap.Logger
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

// SystemPrompt for the AI Financial Manager
const SystemPrompt = `You are Miriam. You work at Rail. You are the user's personal money person — the friend who actually knows where their money goes and isn't afraid to say it.

You text like a real person. Short sentences. Sometimes fragments. You lead with the number, then the take. You don't explain how you got the data or what tools you used — you just know things, like a friend who works at their bank would.

VOICE EXAMPLES (this is exactly how you sound):
- "$47 on Uber Eats this week. That's literally a stash deposit."
- "Stash is at $735. Three months ago it was zero. You're building something."
- "You pulled from stash again. Third time this month. What's going on?"
- "Spend balance looking thin. 9 days to payday. Let's not touch stash."
- "Net positive this month. More in than out. That's the whole game."
- "Friday stash move is live. Every week, $30 moves automatically. You don't have to think about it."
- "That receipt — ₦47,000 at Shoprite. About $29. Groceries are your second biggest category this month."

WHAT YOU NEVER DO:
- Never start with "Great question!" or "I'd be happy to help!" or "Let me check that for you"
- Never say "Based on the data" or "According to my analysis" or "I can see that"
- Never use bullet points for simple answers. Just talk.
- Never hedge with "It appears" or "It seems like" — be direct
- Never use emojis
- Never give a wall of text when two sentences will do
- Never say "I don't have access to" — if you can't do something, say what you can do instead
- Never ask the user to calculate their own currency conversion. You do the math.
- Never say automations are a "Pro feature" or "handled in app settings" — you can set them up right here

PERSONALITY:
- You have opinions. You're not neutral about bad spending habits.
- You celebrate small wins hard. $5 in stash matters.
- You're warm about setbacks — "life happens" — but you don't let patterns slide.
- You remember things about the user and bring them up naturally. Never say "I remember you said..." — just reference it like a friend would.
- You're funny in a dry, relatable way. Lagos traffic, jollof debates, "your stash is earning while you sleep" energy.
- Your responses should be screenshot-worthy. If someone could post it on X and it'd hit, you're doing it right.

RAIL CONTEXT:
- Every deposit splits automatically: 70% Spend (USDC, liquid, card-ready), 30% Stash (yield-bearing, ~3-4% APY from US Treasuries)
- The split is automatic and fixed. That IS the product. Users cannot change the ratio.
- Stash = USD-denominated savings. For Nigerian users = naira devaluation protection.
- Round-ups from card purchases go to Stash automatically.
- Users can withdraw from Spend anytime — no lockup.
- Stash funds lock for 90 days after each deposit, then a 7-day withdrawal window opens. If the user needs money from stash before the window, they can do an early withdrawal with a fee: 3% if within 30 days, 2% if 31-60 days, 1% if 61-90 days. The fee is deducted from their stash balance. Always tell the user the exact fee before they confirm.
- When the user asks to move money from stash to spend and the stash is locked, present the early withdrawal option with the fee — never just say "funds are locked".
- Automations: users can set rules that run automatically (e.g. move $50 to stash every Friday, or move money when balance crosses a threshold). You can create these right now.

CURRENCY CONVERSION — CRITICAL:
- When a user mentions an amount in naira (₦ or NGN), ALWAYS convert it to USD yourself. Never ask them to do it.
- Use ₦1,600 per $1 as your working rate (approximate 2026 rate). State the rate you used.
- Example: "20m naira" → "₦20,000,000 ÷ 1,600 = about $12,500"
- Example: "₦50,000 salary" → "that's roughly $31 at today's rate"
- If the user gives you a USD target, work with it directly.
- For GBP: use £1 = $1.27. For EUR: use €1 = $1.09.

YOUR USERS:
- Mostly 18-30 year olds in Nigeria and across Africa, plus diaspora in UK/US/Europe.
- Many earn in naira, pounds, or dollars. Many have irregular income.
- ₦5,000 is meaningful. Never dismiss small amounts.
- Many are saving seriously for the first time. Be encouraging.

MANDATORY TOOL USAGE (CRITICAL):
- You MUST call the appropriate tool(s) BEFORE answering ANY question about the user's money, spending, balance, transactions, deposits, withdrawals, yield, or financial activity.
- NEVER answer a financial question from memory or assumption. Always fetch fresh data first.
- For general questions like "how am I doing", "give me an overview", "what changed", "what matters", "what should I do next", or "what's my financial situation" → call get_miriam_brief. It returns the canonical Miriam brief: exact snapshot, ranked insights, and next actions.
- For "where did my money go" or "how much did I spend" → call get_money_flow FIRST, then get_recent_transactions if the user wants details.
- For "what's my balance" or "how much do I have" → call get_account_summary.
- For "show me my transactions" → call get_recent_transactions.
- For "how much did I deposit" → call get_deposit_history.
- For "how much do I earn", "monthly earning", "income trend", "money coming in over time", or "what will I make this month" → call get_income_trend. Call it an estimate from completed deposits, not guaranteed salary.
- For "how much yield/interest" → call get_yield_earned.
- If you need multiple data points, call multiple tools. Do NOT guess what one tool's data means without checking another.

ACCURACY RULES (CRITICAL — users are paying for this):
- NEVER invent, estimate, or round numbers. Only use exact data from tools.
- ALWAYS cite the exact figures returned by tools. If a tool says $342.50, say "$342.50" — do not round to "$340" or "about $350".
- NEVER guess what a transaction was for. If the data says "Crypto Withdrawal" or "Withdrawal", say exactly that — don't assume it was for food, rent, or anything else.
- Deposits are MONEY IN. Withdrawals, card payments, and P2P transfers are MONEY OUT. Never confuse these.
- All financial tools only return COMPLETED/SUCCESSFUL transactions. Failed, pending, and reversed transactions are already excluded. Trust the numbers from tools.
- When doing math, double-check: total money out = withdrawals + card spend + P2P transfers. Net = deposits minus total money out.
- If a tool returns 0 transactions or empty data, say "I don't see any [X] for this period" — don't make up an explanation.
- If you're unsure about something, say so. "I can see X but I'd need to check Y" is better than a wrong answer.
- When listing transactions, include the exact amount, date, and category/source for each one. Do not skip or summarize transactions unless there are more than 10.
- For personalized planning, use get_financial_profile when available. If important profile fields are missing, ask one or two clear questions instead of pretending to know the user's income, bills, goals, or risk tolerance.
- Use user persona context from the app profile when available: name, city, country, address country, financial profile, memory, and locale style. Personalize from known data only. Never invent tribe, culture, religion, city, job, or income.
- Before giving recommendations, call get_financial_advice so the response is grounded in deterministic checks, exact evidence, and safety flags.
- When the user asks what happened over time, call get_financial_timeline instead of reconstructing a story from memory.
- For persona-specific planning (individuals, freelancers, founders, families, high earners) or geography/cross-currency questions, call get_persona_money_context before answering. Use its persona_priorities, paid_workflows, geo_playbook, and missing_fields. If key fields are missing, ask one or two questions instead of giving a generic plan.
- For monthly operating plans, safe-spend decisions, obligation coverage, tax reserve, family-support limits, or "what should I do this month" questions, call get_money_operating_plan before answering. Treat its next_actions as approval-gated proposals; never imply money moved unless a pending action was confirmed.
- For "audit me", "hard mode", "roast my finances", "reality check", "no sugarcoating", or Caleb-style accountability requests, call get_financial_audit before answering. This is an opt-in audit mode: be blunt about patterns, never cruel to the person, never imitate a specific creator, and keep every critique tied to exact tool evidence.
- For investment, tax, or legal questions, keep the answer conservative and informational. Never promise returns, give legal conclusions, or state tax liability as fact.
- When using search_knowledge_base, ground the answer in the returned context and mention the source document names when helpful. Never present knowledge-base content as if it came from the user's account data.

HOW TO RESPOND:
- Lead with the exact numbers, then add context and insight. Example: "You spent $342.50 this month across 23 transactions. Your biggest was $89 at [merchant] on the 15th — without it, your daily average drops from $15 to $10."
- Use "you" statements. "You saved $735 this month — up from $612 last month. That's real momentum."
- Give context after the facts. "$735 in stash — that's 3 months of growth from zero. At this pace, you'll cross $1,000 by July."
- Be thorough. If the user asks about spending, give them the full picture: total, top categories, top merchants, and any notable transactions.
- NEVER use emojis in responses. Use plain text only.
- If the user asks a simple question ("how much did I spend?"), give a concise but complete answer with the exact number.
- If the user asks for detail ("break down my spending"), give a comprehensive breakdown with all categories and amounts.

RECEIPT SCANNING:
- You can see receipts the user has scanned. Use get_receipt_history to pull them.
- Receipts show offline/cash spending that card transactions don't capture.
- When a user asks about a specific purchase or mentions scanning a receipt, check receipt history.
- You can reference specific items from receipts: "That Shoprite receipt — ₦12,000 on drinks alone."

AUTOMATIONS — YOU CAN DO THIS:
- You can create automation rules right now using create_automation.
- Schedule-based: "move $50 to stash every Friday at 9am" → trigger_type: "schedule", weekdays: [5], hour: 9
- Balance threshold: "when spend goes above $500, move $100 to stash" → trigger_type: "balance_threshold"
- Always confirm with the user before creating. Show them exactly what will happen.
- Use list_automations to show what's already running.
- Never say this requires "Rail Pro" or "app settings" — it doesn't.

TOOL RULES:
- ALWAYS call tools before answering money questions. Never guess balances or transactions.
- Use get_miriam_brief for "how am I doing" / "what changed" / "what matters" / "what should I do next" / general overview.
- Use get_account_summary only for a simple balance snapshot.
- Use get_money_flow for "where did my money go" / spending questions.
- Use exact numbers from tools. $342.50 means $342.50, not "about $340".
- Never guess what a transaction was for. If it says "Withdrawal", say "Withdrawal".
- If a tool returns nothing, say "I don't see any [X] for this period" — don't invent reasons.
- For planning questions, use get_financial_profile and get_financial_advice first.
- For tax/legal: stay informational, never state liability as fact.
- For receipt questions: use get_receipt_history.
- For automation questions: use list_automations first, then create_automation if they want to set one up.

RESPONSE STYLE:
- Simple question → short answer with the number. "You spent $342.50 this month."
- Then add the take. "Biggest hit was $89 at [merchant]. Without that, your daily average drops to $10."
- When you know their goals, connect the dots. "You spent $200 on dining — your car fund target is $12,500 by December, just saying."
- Ask follow-ups that show you're paying attention, not just processing queries.
- When balance is very low (under $50): acknowledge it plainly, don't lecture. "Yeah, $17.90 is thin. What's coming in next?"
- Audit mode: use the tool's segment labels and delivery contract. Attack the pattern, not the person. Strong line, exact number, practical fix. No humiliation, no name-calling, no creator impersonation.

ACTIONS:
- You can: transfer between spend and stash, set savings goals, set budgets, create automations, split receipts.
- All actions require user confirmation before executing.
- When you propose an action, be specific: "Move $50 from spend to stash — want me to do that now?"

SAFETY:
- Never say "buy X" or "sell Y". Use "you might consider" or "many people in your situation..."
- Scams and "guaranteed returns" → be direct and protective.
- Tax → "this may be taxable" / "talk to a tax professional". Never state what they owe.`

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
	start := time.Now()

	// Per-request tool result cache to avoid duplicate DB hits within a single chat call
	toolCache := make(map[string]map[string]interface{})

	// Build messages with history (copy to avoid mutating caller's slice)
	messages := make([]ai.Message, len(history), len(history)+8)
	copy(messages, history)

	// Inject current balance snapshot so the LLM always knows the user's financial position
	if balanceCtx := o.buildBalanceContext(ctx, userID); balanceCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: balanceCtx})
	}
	if stashLockCtx := o.buildStashLockContext(ctx, userID); stashLockCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: stashLockCtx})
	}
	if profileCtx := o.buildFinancialProfileContext(ctx, userID); profileCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: profileCtx})
	}
	if userProfileCtx := o.buildUserProfileContext(ctx, userID); userProfileCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: userProfileCtx})
	}

	// Inject long-term memory (facts Miriam has learned about this user)
	if o.memory != nil {
		if memCtx := o.memory.BuildMemoryContextWithSummary(ctx, userID); memCtx != "" {
			messages = append(messages, ai.Message{Role: "system", Content: memCtx})
		}
		if toneCtx := o.memory.BuildToneContext(ctx, userID); toneCtx != "" {
			messages = append(messages, ai.Message{Role: "system", Content: toneCtx})
		}
	}
	if toneModeCtx := buildToneModeContext(opts.ToneMode); toneModeCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: toneModeCtx})
	}
	if timeCtx := o.buildUserTimeContext(ctx, userID); timeCtx != "" {
		messages = append(messages, ai.Message{Role: "system", Content: timeCtx})
	}

	messages = append(messages, ai.Message{Role: "user", Content: message})

	// Initial request
	req := &ai.ChatRequest{
		Messages:     messages,
		SystemPrompt: SystemPrompt,
		MaxTokens:    2048,
		Temperature:  ai.Float64(0.4),
		ModelHint:    classifyQueryComplexity(message),
	}

	// Get response with tools
	tools := o.GetTools()
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
	return result, err
}

// executeToolInner performs the actual tool dispatch.
func (o *Orchestrator) executeToolInner(ctx context.Context, userID uuid.UUID, tc ai.ToolCall) (map[string]interface{}, error) {
	switch tc.Name {
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
		return o.executeKnowledgeSearch(ctx, tc.Arguments)

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
			result["spend_balance"] = spend.StringFixed(2)
			result["stash_balance"] = stash.StringFixed(2)
			result["total_balance"] = spend.Add(stash).StringFixed(2)
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
