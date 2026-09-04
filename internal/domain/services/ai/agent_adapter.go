package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aicontext "github.com/rail-service/rail_service/internal/domain/services/ai/context"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
	"github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	promptcontext "github.com/rail-service/rail_service/internal/domain/services/ai/prompt/context"
	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// AgentAdapter is the ChatEngine implementation. All methods use
// *AgentAdapter receiver. Methods served by core.Agent shadow the
// older implementations that route through the AI provider directly.
type AgentAdapter struct {
	agent               *core.Agent
	aiProvider          infraai.AIProvider
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
	voiceLimiter        VoiceDailyLimiterer
	contextSignals      ContextSignalProvider
	memory              *MemoryService
	journey             JourneyStore
	miriamIntelligence  MiriamIntelligenceReader
	bankStatementCtx    *BankStatementContextProvider
	monoAnalysis        MonoAnalysisProvider
	consciousSpendingPlans ConsciousSpendingPlanProvider
	nairaCtx            *nairaCtx
	supermemory         SupermemoryClient
	webSearcher         WebSearcher
	moneyMoveNotifier   MoneyMoveNotifier
	anomalyStore        AnomalyStore
	spendingEnricher    SpendingEnricher
	merchantEnricher    MerchantEnricher
	gameplayProvider    GameplayProvider
	enrichmentSummaryFn func(ctx context.Context, userID uuid.UUID) (string, error)
	redis               cache.RedisClient
	workingMemory       *memory.WorkingMemoryStore
	eventStore          *memory.EventStore
	stepUpVerifier      StepUpVerifier
	travel              core.TravelProvider
	responseGuardOn     bool
	logger              *zap.Logger
	contextDeps         *aicontext.ContextDeps
}

var _ ChatEngine = (*AgentAdapter)(nil)

func NewAgentAdapter(
	agent *core.Agent,
	aiProvider infraai.AIProvider,
	portfolioProvider PortfolioDataProvider,
	activityProvider ActivityDataProvider,
	newsProvider NewsDataProvider,
	logger *zap.Logger,
) *AgentAdapter {
	return &AgentAdapter{
		agent:             agent,
		aiProvider:        aiProvider,
		portfolioProvider: portfolioProvider,
		activityProvider:  activityProvider,
		newsProvider:      newsProvider,
		pending:           newInMemoryPendingActions(),
		logger:            logger,
	}
}

// --- Methods served by core.Agent ---

func (a *AgentAdapter) ChatInContext(ctx context.Context, userID, convID uuid.UUID, message string, history []infraai.Message) (*ChatResponse, error) {
	return a.ChatInContextWithOptions(ctx, userID, convID, message, history, ChatOptions{})
}

func (a *AgentAdapter) ChatInContextWithOptions(ctx context.Context, userID, convID uuid.UUID, message string, history []infraai.Message, opts ChatOptions) (*ChatResponse, error) {
	coreOpts := core.ChatOptions{
		ToneMode:  opts.ToneMode,
		ModelHint: "fast",
	}

	// Parity with the streaming path: inject the consolidated personality block
	// (voice phase, control level, personality mode, tone) and the live FX rate,
	// and gate action execution by the user's control level. Without this, the
	// non-streaming path (iMessage bridge, eval) ran a leaner Miriam that ignored
	// personality modes and, critically, did not enforce Monitor mode.
	var systemContext []string
	if pc := promptcontext.BuildConsolidatedPersonalityContext(promptcontext.ConsolidatedPersonalityDeps{
		BuildControlLevelContext: a.buildControlLevelContext,
		BuildPersonalityModeContext: func(ctx context.Context, userID uuid.UUID) string {
			return promptcontext.BuildPersonalityModeContext(promptcontext.PersonalityModeDeps{
				GetToneProfile: func(ctx context.Context, userID uuid.UUID) (*entities.MiriamToneProfile, error) {
					if a.memory == nil || a.memory.store == nil {
						return nil, nil
					}
					profile, err := a.memory.store.GetToneProfile(ctx, userID)
					if err != nil || profile == nil {
						return nil, err
					}
					return profile, nil
				},
			}, ctx, userID)
		},
		GetMiriamMoneyState: func(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error) {
			if a.miriamIntelligence == nil {
				return nil, nil
			}
			return a.miriamIntelligence.GetMoneyState(ctx, userID)
		},
		GetToneProfile: func(ctx context.Context, userID uuid.UUID) (*entities.MiriamToneProfile, error) {
			if a.memory == nil || a.memory.store == nil {
				return nil, nil
			}
			profile, err := a.memory.store.GetToneProfile(ctx, userID)
			if err != nil || profile == nil {
				return nil, err
			}
			return profile, nil
		},
		GetRecentCallbacks: func(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
			if a.memory == nil {
				return nil, nil
			}
			return a.memory.GetRecentCallbacks(ctx, userID, limit)
		},
	}, ctx, userID, opts.ToneMode); pc != "" {
		systemContext = append(systemContext, pc)
	}
	if rl := a.liveNairaRateLine(ctx); rl != "" {
		systemContext = append(systemContext, rl)
	}
	if coaching := a.buildCoachingContext(ctx, userID); coaching != "" {
		systemContext = append(systemContext, coaching)
	}
	systemContext = append(systemContext, opts.SystemContext...)
	coreOpts.SystemContext = systemContext
	coreOpts.CrossChannelHistory = opts.CrossChannelHistory
	if a.memory != nil {
		if lvl, lerr := a.memory.GetControlLevel(ctx, userID); lerr == nil {
			coreOpts.ControlLevel = lvl
		}
	}

	result, err := a.agent.Chat(ctx, userID, convID, message, coreOpts)
	if err != nil {
		return nil, err
	}

	chatResp := &ChatResponse{
		Content:    result.Content,
		TokensUsed: result.TokensUsed,
		Provider:   result.Provider,
	}

	if len(result.Cards) > 0 {
		cardBytes, err := json.Marshal(result.Cards)
		if err == nil {
			var cards []entities.InsightCard
			if err := json.Unmarshal(cardBytes, &cards); err == nil {
				chatResp.Cards = cards
			}
		}
	}

	// The core agent stages mutating tool calls instead of executing them. Map
	// the staged action onto an entities.PendingAction, store it so ConfirmAction
	// can execute it later (with a server-injected confirm flag), and surface it
	// to the caller (the platform bridge renders a tap-confirm poll, or an in-app
	// Face ID hand-off for fund-moving actions). Without a real conversation to
	// key the pending store, we can still surface the description but cannot offer
	// confirmation, so we only stage when we have a convID.
	if result.PendingAction != nil && result.PendingAction.Type != "" {
		params := result.PendingAction.Params
		if params == nil {
			params = map[string]interface{}{}
		}
		action := &entities.PendingAction{
			ID:             uuid.New().String(),
			ConversationID: convID,
			UserID:         userID,
			Action:         result.PendingAction.Type,
			Description:    executionActionDescription(result.PendingAction.Type, params),
			Params:         params,
			ExpiresAt:      time.Now().Add(pendingActionTTL),
			CreatedAt:      time.Now(),
		}

		// Fund-moving actions get the same account-status precheck as the
		// streaming path before we offer them.
		if IsFundMovingAction(action.Action) {
			if blocked, berr := a.checkUserCanTransact(ctx, userID); berr != nil {
				return nil, berr
			} else if blocked != nil {
				if msg, ok := blocked["error"].(string); ok && msg != "" {
					chatResp.Content = msg
				}
				return chatResp, nil
			}
		}

		if convID != uuid.Nil && a.pending != nil {
			if err := a.pending.Set(ctx, convID, action); err != nil {
				return nil, fmt.Errorf("stage pending %s action: %w", action.Action, err)
			}
		}
		chatResp.PendingAction = action
	}

	return chatResp, nil
}

func (a *AgentAdapter) GetProactiveOpener(ctx context.Context, userID uuid.UUID) *ProactiveOpener {
	result := a.agent.GetProactiveOpener(ctx, userID)
	return &ProactiveOpener{
		Greeting: result["greeting"].(string),
		Severity: result["severity"].(string),
	}
}

func (a *AgentAdapter) GetConversationStarters(ctx context.Context, userID uuid.UUID) []ConversationStarter {
	results := a.agent.GetConversationStarters(ctx, userID)
	starters := make([]ConversationStarter, 0, len(results))
	for _, r := range results {
		starters = append(starters, ConversationStarter{
			Text:     r["text"].(string),
			Category: r["category"].(string),
		})
	}
	return starters
}

func (a *AgentAdapter) ExecuteToolPublic(ctx context.Context, userID uuid.UUID, tc infraai.ToolCall) (map[string]interface{}, error) {
	return a.agent.ExecuteTool(ctx, userID, tc.Name, tc.Arguments)
}

func (a *AgentAdapter) GetTools() []infraai.Tool {
	if a.agent == nil {
		return nil
	}
	return a.agent.GetAllTools()
}

func (a *AgentAdapter) IsUserOverCostCeiling(ctx context.Context, userID uuid.UUID) bool {
	return a.agent.IsUserOverCostCeiling(ctx, userID)
}

// SetAgent wires the core.Agent after construction (circular dep resolution).
func (a *AgentAdapter) SetAgent(agent *core.Agent) {
	a.agent = agent
}

// SetAnomalyStore wires the anomaly store for context assembly.
func (a *AgentAdapter) SetAnomalyStore(store AnomalyStore) {
	a.anomalyStore = store
}

// SetWorkingMemory wires the working memory store for conversation state caching.
func (a *AgentAdapter) SetWorkingMemory(wm *memory.WorkingMemoryStore) {
	a.workingMemory = wm
}

// SetEventStore wires the financial event store for context assembly.
func (a *AgentAdapter) SetEventStore(es *memory.EventStore) {
	a.eventStore = es
}

// SetStepUpVerifier wires the step-up verifier (passcode service) that
// ConfirmAction uses to gate fund-moving actions. When nil, ConfirmAction
// refuses all fund moves (fail-closed).
func (a *AgentAdapter) SetStepUpVerifier(v StepUpVerifier) {
	a.stepUpVerifier = v
}

// SetResponseGuardEnabled toggles the deterministic pre-delivery guard
// (ungrounded-figure strip + anomaly surface + mechanics sanitize) on the
// streaming orchestrator path, mirroring core.Agent's ResponseGuard flag.
func (a *AgentAdapter) SetResponseGuardEnabled(on bool) {
	a.responseGuardOn = on
}

// SetEnrichmentSummaryFn wires the enrichment summary function for context assembly.
func (a *AgentAdapter) SetEnrichmentSummaryFn(fn func(ctx context.Context, userID uuid.UUID) (string, error)) {
	a.enrichmentSummaryFn = fn
}

// BuildContextDeps assembles the ContextDeps struct from the adapter's wired
// providers. Called once before context assembly; cached for the lifetime of
// the adapter or until a setter changes a dependency.
func (a *AgentAdapter) BuildContextDeps() *aicontext.ContextDeps {
	if a.contextDeps != nil {
		return a.contextDeps
	}
	deps := &aicontext.ContextDeps{
		GetBalanceFn: func(ctx context.Context, userID uuid.UUID, t entities.AccountType) (decimal.Decimal, error) {
			if a.aggregateStats == nil {
				return decimal.Zero, nil
			}
			return a.aggregateStats.GetAccountBalance(ctx, userID, t)
		},
		GetUserCountryFn: func(ctx context.Context, userID uuid.UUID) (string, error) {
			if a.userProfile == nil {
				return "", nil
			}
			return a.userProfile.GetCountry(ctx, userID)
		},
		GetFinancialProfileFn: func(ctx context.Context, userID uuid.UUID) (*entities.FinancialProfile, error) {
			if a.financialProfile == nil {
				return nil, nil
			}
			return a.financialProfile.GetByUserID(ctx, userID)
		},
		ListActiveObligationsFn: func(ctx context.Context, userID uuid.UUID) ([]entities.FinancialObligation, error) {
			if a.obligations == nil {
				return nil, nil
			}
			return a.obligations.ListActive(ctx, userID)
		},
		GetPortfolioStatsFn: func(ctx context.Context, userID uuid.UUID) (*aicontext.PortfolioStats, error) {
			if a.portfolioProvider == nil {
				return nil, nil
			}
			stats, err := a.portfolioProvider.GetWeeklyStats(ctx, userID)
			if err != nil || stats == nil {
				return nil, err
			}
			return &aicontext.PortfolioStats{TotalValue: stats.TotalValue}, nil
		},
		GetLatestRateFn: func(ctx context.Context, from, to string) (decimal.Decimal, error) {
			if a.currencyRates == nil {
				return decimal.Zero, nil
			}
			return a.currencyRates.GetLatestRate(ctx, from, to)
		},
		GetMoneyStateFn: func(ctx context.Context, userID uuid.UUID) (*entities.MiriamMoneyState, error) {
			if a.miriamIntelligence == nil {
				return nil, nil
			}
			return a.miriamIntelligence.GetMoneyState(ctx, userID)
		},
		SearchMemoryRankedFn: func(ctx context.Context, userID, query string, limit int) ([]string, error) {
			if a.supermemory == nil {
				return nil, nil
			}
			results, err := a.supermemory.SearchMemoryRanked(ctx, userID, query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]string, len(results))
			for i, r := range results {
				out[i] = r.Memory
			}
			return out, nil
		},
		GetAnomaliesFn: func(ctx context.Context, userID uuid.UUID) ([]aicontext.AnomalyResult, error) {
			if a.anomalyStore == nil {
				return nil, nil
			}
			results, err := a.anomalyStore.Get(ctx, userID)
			if err != nil {
				return nil, err
			}
			out := make([]aicontext.AnomalyResult, len(results))
			for i, r := range results {
				out[i] = aicontext.AnomalyResult{
					Severity:    string(r.Severity),
					Title:       r.Title,
					Description: r.Description,
				}
			}
			return out, nil
		},
		GetWorkingMemoryFn: func(ctx context.Context, userID uuid.UUID) *memory.WorkingMemoryEntry {
			if a.workingMemory == nil {
				return nil
			}
			return a.workingMemory.Get(ctx, userID)
		},
		GetFinancialEventsFn: func(ctx context.Context, userID uuid.UUID) string {
			if a.eventStore == nil {
				return ""
			}
			return a.eventStore.BuildEventsContext(ctx, userID)
		},
		GetEnrichmentSummaryFn: a.enrichmentSummaryFn,
		GetPendingActionFn: func(ctx context.Context, convID uuid.UUID) *entities.PendingAction {
			if a.pending == nil {
				return nil
			}
			return a.pending.Get(ctx, convID)
		},
		GetMonoSpendingFn: func(ctx context.Context, userID uuid.UUID, days int) (*aicontext.MonoSpendingAnalysis, error) {
			if a.monoAnalysis == nil {
				return nil, nil
			}
			analysis, err := a.monoAnalysis.GetSpendingAnalysis(ctx, userID, days)
			if err != nil || analysis == nil {
				return nil, err
			}
			return &aicontext.MonoSpendingAnalysis{
				TotalDebits:      analysis.TotalDebits,
				TotalCredits:     analysis.TotalCredits,
				SavingsRate:      analysis.SavingsRate,
				TransactionCount: analysis.TransactionCount,
				Period:           struct{ Days int }{Days: analysis.Period.Days},
			}, nil
		},
		GetBankUploadSummaryFn: func(ctx context.Context, userID uuid.UUID) (int, []string, error) {
			if a.bankStatementCtx == nil || a.bankStatementCtx.provider == nil {
				return 0, nil, nil
			}
			return a.bankStatementCtx.provider.GetCompletedUploadSummary(ctx, userID)
		},
		BuildMemoryContextFn: func(ctx context.Context, userID uuid.UUID, message string) string {
			if a.memory == nil {
				return ""
			}
			return a.memory.BuildMemoryContextWithSummary(ctx, userID, message)
		},
		ToneProfileFn: func(ctx context.Context, userID uuid.UUID) *aicontext.ToneProfile {
			if a.memory == nil {
				return nil
			}
			profile, err := a.memory.store.GetToneProfile(ctx, userID)
			if err != nil || profile == nil {
				return nil
			}
			return &aicontext.ToneProfile{
				SampleCount:   profile.SampleCount,
				PreferredName: profile.PreferredName,
				Brevity:       profile.Brevity,
				LanguageStyle: profile.LanguageStyle,
				LocaleStyle:   profile.LocaleStyle,
			}
		},
		MemoryCallbacksFn: func(ctx context.Context, userID uuid.UUID, limit int) ([]string, error) {
			if a.memory == nil {
				return nil, nil
			}
			return a.memory.GetRecentCallbacks(ctx, userID, limit)
		},
		ControlLevelFn: func(ctx context.Context, userID uuid.UUID) string {
			if a.memory == nil {
				return ""
			}
			lvl, err := a.memory.GetControlLevel(ctx, userID)
			if err != nil {
				return ""
			}
			return fmt.Sprintf("[CONTROL LEVEL: %s]", lvl)
		},
		BankStatementBuildFn: func(ctx context.Context, userID uuid.UUID) string {
			if a.bankStatementCtx == nil {
				return ""
			}
			return a.bankStatementCtx.BuildContext(ctx, userID)
		},
		GetActiveThreadFn: func(ctx context.Context, userID uuid.UUID) string {
			if a.workingMemory == nil {
				return ""
			}
			entry := a.workingMemory.Get(ctx, userID)
			if entry == nil {
				return ""
			}
			return strings.TrimSpace(entry.ActiveThread)
		},
		Cache:  aicontext.NewContextCache(),
		Logger: a.logger,
	}
	if a.nairaCtx != nil && a.nairaCtx.provider != nil {
		p := a.nairaCtx.provider
		deps.GetNairaOrdersFn = func(ctx context.Context, userID uuid.UUID, limit int) ([]aicontext.NairaOrderSummary, error) {
			orders, err := p.GetRecentOrders(ctx, userID, limit)
			if err != nil {
				return nil, err
			}
			result := make([]aicontext.NairaOrderSummary, len(orders))
			for i, o := range orders {
				result[i] = aicontext.NairaOrderSummary{
					OrderType:   o.OrderType,
					FiatAmount:  o.FiatAmount,
					TokenAmount: o.TokenAmount,
					Rate:        o.Rate,
					Currency:    o.Currency,
					CreatedAt:   o.CreatedAt,
				}
			}
			return result, nil
		}
	}
	if a.journey != nil {
		deps.JourneySignalsFn = func(ctx context.Context, userID uuid.UUID) (aicontext.JourneySignals, bool) {
			sigs, ok := a.gatherJourneySignals(ctx, userID)
			if !ok {
				return aicontext.JourneySignals{}, false
			}
			return aicontext.JourneySignals{
				User:         sigs.user,
				Phase:        aicontext.OnboardingPhase(sigs.phase),
				MessageCount: sigs.messageCount,
				HasFunded:    sigs.hasFunded,
				DepositCount: sigs.depositCount,
				MonoLinked:   sigs.monoLinked,
			}, true
		}
		deps.JourneyBlockFn = func(ctx context.Context, userID uuid.UUID, sigs aicontext.JourneySignals) string {
			rootSigs := journeySignals{
				user:         sigs.User,
				phase:        OnboardingPhase(sigs.Phase),
				messageCount: sigs.MessageCount,
				hasFunded:    sigs.HasFunded,
				depositCount: sigs.DepositCount,
				monoLinked:   sigs.MonoLinked,
			}
			return a.buildJourneyBlock(ctx, userID, rootSigs)
		}
	}
	a.contextDeps = deps
	return deps
}

// SetMerchantEnricher wires the merchant enrichment lookup for spending tools.
func (a *AgentAdapter) SetMerchantEnricher(e MerchantEnricher) {
	a.merchantEnricher = e
}

// WaitForBackgroundWrites drains in-flight memory-extraction goroutines so a
// caller (the simulation harness) can delete the user without racing the async
// INSERTs. No-op when memory is not wired. Returns ctx.Err() on timeout.
func (a *AgentAdapter) WaitForBackgroundWrites(ctx context.Context) error {
	if a.memory == nil {
		return nil
	}
	return a.memory.WaitForPendingWrites(ctx)
}

// GetPersonalizedSuggestions returns contextual suggestions based on user data.
func (a *AgentAdapter) GetPersonalizedSuggestions(ctx context.Context, userID uuid.UUID) []string {
	suggestions := []string{"Where did my money go this month?"}
	if a.spending != nil && a.aggregateStats != nil && a.financialProfile != nil {
		suggestions = append(suggestions,
			"What's my financial health score?",
			"Forecast my end-of-month balance",
			"Build my personal money plan",
			"Check my financial risks",
			"Show my financial timeline",
		)
	}

	if a.portfolioProvider != nil {
		stats, err := a.portfolioProvider.GetWeeklyStats(ctx, userID)
		if err == nil && stats != nil {
			if stats.WeeklyReturnPct.IsNegative() {
				suggestions = append(suggestions, "Why is my portfolio down this week?")
			} else {
				suggestions = append(suggestions, "How is my portfolio doing?")
			}
		}
	}

	if a.activityProvider != nil {
		streak, err := a.activityProvider.GetStreak(ctx, userID)
		if err == nil && streak != nil && streak.CurrentStreak > 3 {
			suggestions = append(suggestions, "How long is my investing streak?")
		} else {
			suggestions = append(suggestions, "How can I build a saving habit?")
		}
	}

	if a.patterns != nil {
		suggestions = append(suggestions, "What are my spending patterns?")
	}

	suggestions = append(suggestions, "What if I save $50 every week for a year?")

	if a.aggregateStats != nil {
		suggestions = append(suggestions, "How am I doing financially?")
	}

	if a.balanceHistory != nil {
		suggestions = append(suggestions, "Show me how my savings have grown")
	}

	if a.knowledge != nil {
		suggestions = append(suggestions, "What's the best way to start investing?")
	}

	suggestions = append(suggestions,
		"Set up an automation to save every Friday",
		"Create a savings goal with friends",
	)

	if len(suggestions) > 8 {
		suggestions = suggestions[:8]
	}
	return suggestions
}
