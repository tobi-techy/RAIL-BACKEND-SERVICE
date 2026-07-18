package core

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ToolRegistry is the interface for tool storage and dispatch.
type ToolRegistry interface {
	Get(name string) *Tool
	GetAll() []*Tool
	GetByCategory(cat ToolCategory) []*Tool
	GetByCategories(cats []ToolCategory) []*Tool
	Execute(ctx context.Context, userID uuid.UUID, name string, args map[string]interface{}, deps *Dependencies) (*ToolResult, error)
	ToInfrastructureTools() []map[string]interface{}
	Count() int
}

// ToolCategory groups tools by domain for intent routing.
type ToolCategory string

const (
	CategoryOverview   ToolCategory = "Overview"
	CategorySpending   ToolCategory = "Spending"
	CategoryAction     ToolCategory = "Action"
	CategoryPlanning   ToolCategory = "Planning"
	CategoryHistory    ToolCategory = "History"
	CategoryAutomation ToolCategory = "Automation"
	CategoryBudget     ToolCategory = "Budget"
	CategoryMemory     ToolCategory = "Memory"
	CategoryVoice      ToolCategory = "Voice"
	CategoryFiling     ToolCategory = "Filing"
	CategorySocial     ToolCategory = "Social"
	CategoryKnowledge  ToolCategory = "Knowledge"
	CategoryEngagement ToolCategory = "Engagement"
	CategoryInvestment ToolCategory = "Investment"
	CategoryPortfolio  ToolCategory = "Portfolio"
	CategoryFull       ToolCategory = "Full"
)

// Tool is a single function the agent can call.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Category    ToolCategory
	Execute     func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *Dependencies) (*ToolResult, error)
}

// ToolResult wraps the output of a tool execution.
type ToolResult struct {
	Data   map[string]interface{}
	Action *PendingAction
	Error  string
}

// PendingAction is an action awaiting user confirmation.
type PendingAction struct {
	Type        string
	Description string
	Params      map[string]interface{}
	ExpiresAt   time.Time
	ConfirmFunc func(ctx context.Context, deps *Dependencies) (*ToolResult, error)
	CancelFunc  func(ctx context.Context, deps *Dependencies) error
}

// Intent represents the classified intent of a user message.
type Intent struct {
	Category    ToolCategory
	Confidence  float64
	SubIntents  []string
	Entities    map[string]string
	RequiresLLM bool
}

// AgentContext is the assembled context for a single turn.
type AgentContext struct {
	UserID  uuid.UUID
	ConvID  uuid.UUID
	Message string
	State   *UserState
	Memory  *MemoryContext
	Intents []Intent
	Options ChatOptions
}

// UserState is the pre-computed state for a user, refreshed on events.
type UserState struct {
	Balances        *Balances
	Budget          *BudgetState
	Goals           []Goal
	Portfolio       *PortfolioSummary
	Automations     []Automation
	PendingActions  []PendingActionInfo
	Streak          *StreakInfo
	FinancialHealth *FinancialHealth
	MoneyState      *MoneyState
	Profile         *UserProfile
}

type Balances struct {
	Spend    decimal.Decimal
	Stash    decimal.Decimal
	Currency string
}

type BudgetState struct {
	Categories  []BudgetCategory
	TotalSpent  decimal.Decimal
	TotalBudget decimal.Decimal
}

type BudgetCategory struct {
	Name      string
	Assigned  decimal.Decimal
	Spent     decimal.Decimal
	Remaining decimal.Decimal
}

type Goal struct {
	ID       uuid.UUID
	Name     string
	Target   decimal.Decimal
	Current  decimal.Decimal
	Deadline time.Time
	Status   string
}

type PortfolioSummary struct {
	TotalValue      decimal.Decimal
	WeeklyReturn    decimal.Decimal
	WeeklyReturnPct decimal.Decimal
	Allocations     []Allocation
	TopMovers       []Mover
}

type Allocation struct {
	Name   string
	Value  decimal.Decimal
	Weight decimal.Decimal
}

type Mover struct {
	Symbol    string
	Name      string
	ReturnPct decimal.Decimal
}

type Automation struct {
	ID      uuid.UUID
	Name    string
	Trigger string
	Action  string
	Enabled bool
}

type PendingActionInfo struct {
	ID          uuid.UUID
	Type        string
	Description string
	ExpiresAt   time.Time
}

type StreakInfo struct {
	Days       int
	Longest    int
	LastActive time.Time
}

type FinancialHealth struct {
	Score           int
	SavingsRate     decimal.Decimal
	EmergencyFund   decimal.Decimal
	DebtToIncome    decimal.Decimal
	Recommendations []string
}

type MoneyState struct {
	ConfidenceLevel  string
	ConfidenceScore  int
	SafeToSpendDaily decimal.Decimal
	LiquidityRunway  int
	AnomalyCount     int
}

type UserProfile struct {
	Name             string
	IncomeCadence    string
	AvgMonthlyIncome decimal.Decimal
	RiskTolerance    string
	Locale           string
	Currency         string
}

// MemoryContext holds retrieved memories for context assembly.
type MemoryContext struct {
	Facts       []Fact
	Episodic    []Episode
	Supermemory []SupermemoryResult
	Summary     string
}

type Fact struct {
	Category   string
	Fact       string
	Confidence float64
	Source     string
}

type Episode struct {
	Content    string
	Similarity float64
	Timestamp  time.Time
}

type SupermemoryResult struct {
	Memory     string
	Similarity float64
}

// ChatOptions carries per-request options.
type ChatOptions struct {
	ToneMode    string
	ModelHint   string
	MaxRounds   int
	Temperature float64

	// SystemContext holds extra system prompts assembled by the adapter (consolidated
	// personality: voice phase, personality mode, tone; plus the live FX rate). Injected
	// so the non-streaming path matches the streaming path.
	SystemContext []string
	// ControlLevel gates action execution: "monitor" blocks all action tools,
	// "guided"/"full"/"" allow them. Sourced from the user's saved autonomy setting.
	ControlLevel string
}

// ControlLevelMonitor is the autonomy level where Miriam may only observe and
// advise — all action-tool execution is blocked.
const ControlLevelMonitor = "monitor"

// MonitorBlockMessage is returned to the LLM when an action is attempted in Monitor mode.
const MonitorBlockMessage = "You're in Monitor mode — I can only watch and advise, not take actions. If you want me to handle this, switch to Guided or Full Autopilot mode using set_control_level."

// StreamEvent is emitted during streaming responses.
type StreamEvent struct {
	Type       string
	Token      string
	ToolResult *ToolResult
	Cards      []map[string]interface{}
	ActionChip []map[string]interface{}
	Error      string
}

// Dependencies holds all external services the agent and tools need.
type Dependencies struct {
	AIProvider    ai.AIProvider
	ToolRegistry  ToolRegistry
	Memory        MemoryService
	State         StateService
	Conversations ConversationService
	Usage         UsageService
	Portfolio     PortfolioProvider
	Spending      SpendingProvider
	Transactions  TransactionProvider
	FundsTransfer FundsTransferer
	Budget        BudgetProvider
	Goals         GoalStore
	Obligations   ObligationProvider
	Automation    AutomationProvider
	Profile       ProfileProvider
	CurrencyRates CurrencyRateProvider
	WebSearch     WebSearcher
	VoiceLimiter  VoiceLimitService
	Notifier      MoneyMoveNotifier
	NairaCtx      ContextProvider
	BankStatement ContextProvider
	Signals       SignalProvider
	MiriamIntell  MiriamIntelligenceProvider
	Investment    InvestmentProvider
	Receipt       ReceiptProvider
	Warranty      WarrantyProvider
	PriceTracker  PriceTrackerProvider
	Merchant      MerchantProvider
	Cache         CacheClient
	Logger        *zap.Logger
	Config        *Config

	// QualityGate checks a response and returns (pass, correctionHint). Injected by
	// the adapter so core reuses the same quality gate as the streaming path instead
	// of a weaker heuristic. Nil falls back to a basic length check.
	QualityGate func(response string) (pass bool, hint string)

	// ResponseGuard deterministically repairs a drafted reply before delivery:
	// strips currency figures not grounded in tool/context data, surfaces a missed
	// anomaly, and fixes mechanical formatting. Injected by the adapter and only
	// invoked when Config.ResponseGuard is on. grounding holds every real figure the
	// reply may legitimately state; anomalies is the injected anomaly context.
	ResponseGuard func(content, grounding, anomalies string) string

	// Additional providers ported from old orchestrator
	AccountChecker      AccountChecker
	ActionAuditor       ActionAuditor
	WithdrawalInitiator WithdrawalInitiator
	GoalProtector       GoalProtector
	Subscription        SubscriptionProvider
	RecurringExpense    RecurringExpenseProvider
	SavingsSuggestion   SavingsSuggestionProvider
	ReceiptChallenge    ReceiptChallengeProvider
	ReportEmail         ReportEmailSender
	Knowledge           KnowledgeSearcher
	Simulator           Simulator
	Comparative         ComparativeProvider
	Tax                 TaxProvider
	EmergencyWithdraw   EmergencyWithdrawer
	FinancialGovernance FinancialGovernanceProvider
	Nudge               NudgeProvider

	// Execution Engine providers (spec 5.2) — see execution.go.
	BillPay           BillPayProvider
	Bills             BillsProvider
	SubscriptionAudit SubscriptionAuditProvider
	InvestmentExec    InvestmentExecutionProvider
	YieldOpt          YieldOptimizationProvider
	MerchantBlock     MerchantBlockProvider
	TradeCopy         TradeCopyProvider
	Travel            TravelProvider

	// AnomalyContextFn returns a system-prompt string of recent anomaly
	// detections for context assembly. Returns empty string if none.
	AnomalyContextFn func(ctx context.Context, userID uuid.UUID) string

	// WorkingMemory provides Redis-backed conversation state caching.
	// Nil disables working memory for the core agent path.
	WorkingMemory WorkingMemoryStore

	// EventStoreFn returns a system-prompt string of recent financial events.
	// Nil disables financial events in the core agent path.
	EventStoreFn func(ctx context.Context, userID uuid.UUID) string

	// EnrichmentSummaryFn returns a system-prompt string summarising the user's
	// recent enriched transactions (top merchants, essential ratio, detected patterns).
	// Nil disables enrichment context in the core agent path.
	EnrichmentSummaryFn func(ctx context.Context, userID uuid.UUID) (string, error)
}

// WorkingMemoryStore provides read/write to Redis for conversation working memory.
// Implemented by memory.WorkingMemoryStore.
type WorkingMemoryStore interface {
	Get(ctx context.Context, userID uuid.UUID) WorkingMemorySnapshot
	AppendExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string)
}

// WorkingMemorySnapshot is a read-only view of the cached conversation state.
type WorkingMemorySnapshot interface {
	GetSummary() string
	GetTopic() string
	GetMessageCount() int
}

// Ensure the memory package's WorkingMemoryEntry satisfies WorkingMemorySnapshot at compile time.
// This is a no-op — the constraint is checked when the DI container wires the concrete type.
var _ WorkingMemorySnapshot = (WorkingMemorySnapshot)(nil)

// --- Service Interfaces ---

type MemoryService interface {
	ProcessExchange(ctx context.Context, userID uuid.UUID, userMsg, assistantMsg string) error
	SearchMemory(ctx context.Context, userID uuid.UUID, query string, limit int) ([]SupermemoryResult, error)
	SearchFacts(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Fact, error)
	SearchEpisodic(ctx context.Context, userID uuid.UUID, query string, limit int) ([]Episode, error)
	GetToneProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetPersonalityMode(ctx context.Context, userID uuid.UUID) string
	SetPersonalityMode(ctx context.Context, userID uuid.UUID, mode string) error
	GetControlLevel(ctx context.Context, userID uuid.UUID) (string, error)
	SetControlLevel(ctx context.Context, userID uuid.UUID, level string) error
	GetMemorySummary(ctx context.Context, userID uuid.UUID, message string) (string, error)
	RunDecay(ctx context.Context) error
	SaveTransactionPattern(ctx context.Context, userID uuid.UUID, patternType string, data map[string]interface{}) error
}

type StateService interface {
	GetState(ctx context.Context, userID uuid.UUID) (*UserState, error)
	RefreshState(ctx context.Context, userID uuid.UUID) error
	InvalidateCache(ctx context.Context, userID uuid.UUID) error
}

type ConversationService interface {
	BuildContext(ctx context.Context, convID uuid.UUID, userID uuid.UUID) ([]map[string]interface{}, error)
	RecordExchange(ctx context.Context, convID uuid.UUID, userMsg, assistantMsg string, tokens int, cost decimal.Decimal, model string) error
	UpdateTitle(ctx context.Context, convID uuid.UUID, title string) error
	SaveToolMessages(ctx context.Context, convID uuid.UUID, messages []map[string]interface{}) error
}

type UsageService interface {
	TrackInteraction(ctx context.Context, userID uuid.UUID, model string, tokens int) error
	IsOverCostCeiling(ctx context.Context, userID uuid.UUID) bool
}

type PortfolioProvider interface {
	GetWeeklyStats(ctx context.Context, userID uuid.UUID) (*PortfolioStats, error)
	GetTopMovers(ctx context.Context, userID uuid.UUID, limit int) ([]*Mover, error)
	GetAllocations(ctx context.Context, userID uuid.UUID) ([]*Allocation, error)
	GetStreak(ctx context.Context, userID uuid.UUID) (*StreakInfo, error)
	GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type SpendingProvider interface {
	GetSpendingSummary(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error)
	GetRecentTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	GetMoneyFlow(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error)
	GetSpendingPatterns(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
}

type TransactionProvider interface {
	GetCardTransactions(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	GetDepositHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	GetWithdrawalHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	GetIncomeTrend(ctx context.Context, userID uuid.UUID, months int) (map[string]interface{}, error)
	GetReceiptHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
	GetYieldEarned(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetBalanceHistory(ctx context.Context, userID uuid.UUID, days int) ([]map[string]interface{}, error)
}

type FundsTransferer interface {
	Transfer(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error
	Withdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, destination string) error
	SetSavingsGoal(ctx context.Context, userID uuid.UUID, name string, target decimal.Decimal) error
}

type BudgetProvider interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	Upsert(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error
}

type GoalStore interface {
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]Goal, error)
	Create(ctx context.Context, userID uuid.UUID, goal *Goal) error
	GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

type ObligationProvider interface {
	List(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	Create(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error
	FindPaymentMatches(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	MarkPaid(ctx context.Context, userID, obligationID uuid.UUID) error
}

type AutomationProvider interface {
	List(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	Create(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error
}

type ProfileProvider interface {
	GetFinancialProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	UpdateFinancialProfile(ctx context.Context, userID uuid.UUID, data map[string]interface{}) error
	GetUserProfile(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
}

type CurrencyRateProvider interface {
	GetLatestRate(ctx context.Context, from, to string) (decimal.Decimal, error)
}

type WebSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]map[string]interface{}, error)
}

type VoiceLimitService interface {
	Allow(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) (bool, error)
}

type MoneyMoveNotifier interface {
	NotifyMiriamMovedFunds(ctx context.Context, userID uuid.UUID, action, from, to string, amount decimal.Decimal, emergency, succeeded bool, errMsg string) error
}

type ContextProvider interface {
	GetContext(ctx context.Context, userID uuid.UUID) (string, error)
}

type SignalProvider interface {
	GetActiveByUser(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type MiriamIntelligenceProvider interface {
	GetMoneyState(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetMandates(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	GetDecisionReceipts(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
}

type InvestmentProvider interface {
	GetProducts(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type ReceiptProvider interface {
	Split(ctx context.Context, receiptID uuid.UUID, participants []string) (map[string]interface{}, error)
}

type WarrantyProvider interface {
	GetItems(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type PriceTrackerProvider interface {
	GetChanges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type MerchantProvider interface {
	GetInsights(ctx context.Context, merchantName string) (map[string]interface{}, error)
}

type CacheClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type AccountChecker interface {
	CheckAccount(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
}

type ActionAuditor interface {
	RecordAction(ctx context.Context, userID uuid.UUID, action string, params map[string]interface{}, status string) error
	GetActionHistory(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]interface{}, error)
}

type WithdrawalInitiator interface {
	InitiateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, bankAccountID uuid.UUID) error
	GetLinkedBanks(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type GoalProtector interface {
	GetWithdrawableStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	GetUnallocatedStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
}

type SubscriptionProvider interface {
	ListSubscriptions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
	ProtectSubscription(ctx context.Context, userID, subID uuid.UUID) error
	MarkCancelled(ctx context.Context, subID uuid.UUID) error
	IgnoreSubscription(ctx context.Context, subID uuid.UUID) error
}

type RecurringExpenseProvider interface {
	GetRecurring(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type SavingsSuggestionProvider interface {
	GetSuggestions(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type ReceiptChallengeProvider interface {
	GetChallenges(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error)
}

type ReportEmailSender interface {
	SendReport(ctx context.Context, userID uuid.UUID, reportType string, data map[string]interface{}) error
}

type KnowledgeSearcher interface {
	Search(ctx context.Context, userID uuid.UUID, query string) (map[string]interface{}, error)
}

type Simulator interface {
	SimulateSavings(ctx context.Context, userID uuid.UUID, monthlyAmount decimal.Decimal, years int) (map[string]interface{}, error)
}

type ComparativeProvider interface {
	GetComparativeContext(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetSpendingComparison(ctx context.Context, userID uuid.UUID, period string) (map[string]interface{}, error)
}

type TaxProvider interface {
	GetTaxSummary(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetTaxCalendar(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
}

type EmergencyWithdrawer interface {
	EmergencyWithdraw(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error
}

type FinancialGovernanceProvider interface {
	GetFinancialHealth(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetFinancialPlan(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetCashFlowForecast(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
	GetFinancialAudit(ctx context.Context, userID uuid.UUID) (map[string]interface{}, error)
}

type NudgeProvider interface {
	GenerateNudge(ctx context.Context, userID uuid.UUID, req map[string]interface{}) (map[string]interface{}, error)
}

// Config holds configuration for the agent.
type Config struct {
	SystemPrompt       string
	MaxToolRounds      int
	ContextTimeout     time.Duration
	StateCacheTTL      time.Duration
	MaxTokens          int
	DefaultTemperature float64
	FastTemperature    float64
	UserCostCeiling    decimal.Decimal
	VoiceDailyLimitUSD decimal.Decimal

	// ResponseGuard enables the deterministic pre-delivery guard (grounded-number
	// stripping, anomaly surfacing, mechanical sanitizing). Default off in
	// production; the simulation harness turns it on so the weakest model still
	// lands clean, non-fabricated answers.
	ResponseGuard bool
}

func DefaultConfig() *Config {
	return &Config{
		MaxToolRounds:      5,
		ContextTimeout:     1500 * time.Millisecond,
		StateCacheTTL:      5 * time.Minute,
		MaxTokens:          2048,
		DefaultTemperature: 0.6,
		FastTemperature:    0.3,
		UserCostCeiling:    decimal.NewFromFloat(2.00),
		VoiceDailyLimitUSD: decimal.NewFromFloat(500.00),
	}
}

// PortfolioStats mirrors old package type.
type PortfolioStats struct {
	TotalValue      decimal.Decimal
	WeeklyReturn    decimal.Decimal
	WeeklyReturnPct decimal.Decimal
	MonthlyReturn   decimal.Decimal
	TotalGainLoss   decimal.Decimal
}
