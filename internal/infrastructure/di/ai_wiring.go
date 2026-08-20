package di

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	aicore "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	aimemory "github.com/rail-service/rail_service/internal/domain/services/ai/memory"
	aitools "github.com/rail-service/rail_service/internal/domain/services/ai/tools"
	conversationsvc "github.com/rail-service/rail_service/internal/domain/services/conversation"
	knowledgesvc "github.com/rail-service/rail_service/internal/domain/services/knowledge"
	miriamservice "github.com/rail-service/rail_service/internal/domain/services/miriam"
	newsservice "github.com/rail-service/rail_service/internal/domain/services/news"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	usagesvc "github.com/rail-service/rail_service/internal/domain/services/usage"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/embeddings"
	"github.com/rail-service/rail_service/internal/infrastructure/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/enrichment"
	platform "github.com/rail-service/rail_service/internal/infrastructure/platform"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	supermemoryclient "github.com/rail-service/rail_service/internal/infrastructure/supermemory"
	"github.com/rail-service/rail_service/internal/infrastructure/vector"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func (c *Container) initializeAIServices(sqlxDB *sqlx.DB, positionRepo *repositories.PositionRepository, allocationRepo *repositories.AllocationRepository, basketRepo *repositories.BasketRepository) error {
	// Check if AI is configured
	if c.Config.AI.Cencori.APIKey == "" {
		return fmt.Errorf("no AI provider configured")
	}

	// Helper to resolve timeout from config with a sensible default
	resolveTimeout := func(seconds int) time.Duration {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 10 * time.Second
	}

	// Initialize AI provider — Cencori gateway (single provider, gateway handles
	// multi-provider routing, failover, PII detection, and prompt injection).
	if strings.TrimSpace(c.Config.AI.Cencori.APIKey) == "" {
		return fmt.Errorf("CENCORI_API_KEY is required")
	}
	cencoriConfig := &ai.CencoriConfig{
		APIKey:           strings.TrimSpace(c.Config.AI.Cencori.APIKey),
		ModelSmart:       c.Config.AI.Cencori.ModelSmart,
		ModelFast:        c.Config.AI.Cencori.ModelFast,
		MaxTokens:        c.Config.AI.Cencori.MaxTokens,
		MaxContextTokens: c.Config.AI.Cencori.MaxContextTokens,
		Temperature:      c.Config.AI.Cencori.Temperature,
		TopP:             c.Config.AI.Cencori.TopP,
		Timeout:          resolveTimeout(c.Config.AI.Cencori.TimeoutSeconds),
		RateLimitRPM:     c.Config.AI.Cencori.RateLimitRPM,
	}

	// Optional Cloudflare AI Gateway: when enabled, point the Cencori client at
	// the gateway URL and authenticate with the gateway-scoped key. The gateway
	// proxies to Cencori with response caching, rate limiting, and cost analytics.
	gatewayBaseURL, gatewayAPIKey := c.cloudflareGateway(cencoriConfig.APIKey)
	if gatewayBaseURL != "" {
		cencoriConfig.BaseURL = gatewayBaseURL
		cencoriConfig.APIKey = gatewayAPIKey
	}
	c.AIProvider = ai.NewCencoriProvider(cencoriConfig, c.ZapLog)

	// Per-user daily/monthly cost ceiling enforcement. Refuses Cencori calls
	// before they hit the gateway when a user has burned through their cap.
	// Fail-open on Redis errors so a Redis blip doesn't deny service.
	if c.Config.AI.CostGuard.Enabled && c.RedisClient != nil {
		costGuard := ai.NewGuard(
			c.RedisClient,
			c.ZapLog,
			c.Config.AI.CostGuard.DailyCeilingUSD,
			c.Config.AI.CostGuard.MonthlyCeilingUSD,
		)
		if cp, ok := c.AIProvider.(*ai.CencoriProvider); ok {
			cp.SetCostGuard(costGuard)
		}
		// Hold a reference so core.Agent.Dependencies.CostGuard and the
		// spending_coach worker can share the same guard instance (single
		// source of truth for running totals).
		c.AICostGuard = costGuard
		c.ZapLog.Info("AI cost guard enabled",
			zap.Float64("daily_usd", c.Config.AI.CostGuard.DailyCeilingUSD),
			zap.Float64("monthly_usd", c.Config.AI.CostGuard.MonthlyCeilingUSD),
		)
	} else if c.Config.AI.CostGuard.Enabled {
		c.ZapLog.Warn("AI cost guard configured but Redis unavailable — running unenforced")
	}

	// Validate Cencori connectivity at startup (non-blocking)
	go func() {
		checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if !c.AIProvider.IsAvailable(checkCtx) {
			c.ZapLog.Warn("Cencori AI gateway is not available",
				zap.String("hint", "Check that CENCORI_API_KEY is valid and has not expired"),
			)
		} else {
			c.ZapLog.Info("Cencori AI gateway is available")
		}
	}()

	// Initialize repositories for AI services
	userNewsRepo := repositories.NewUserNewsRepository(c.DB, c.ZapLog)
	streakRepo := repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)
	contributionsRepo := repositories.NewUserContributionsRepository(c.DB, c.ZapLog)
	portfolioRepo := repositories.NewPortfolioRepository(c.DB, c.ZapLog)

	// Initialize data providers
	c.PortfolioDataProvider = aiservice.NewPortfolioDataProvider(
		&portfolioValueAdapter{repo: portfolioRepo},
		positionRepo,
		c.ZapLog,
	)

	c.ActivityDataProvider = aiservice.NewActivityDataProvider(
		&contributionRepoAdapter{repo: contributionsRepo},
		&streakRepoAdapter{repo: streakRepo},
		c.ZapLog,
	)

	// Initialize news service
	c.NewsService = newsservice.NewService(
		&alpacaNewsAdapter{client: c.AlpacaClient},
		userNewsRepo,
		positionRepo,
		c.ZapLog,
	)
	// AgentAdapter wraps core.Agent (new) and all legacy services.

	// All Set* calls below wire providers into AgentAdapter.
	adapter := aiservice.NewAgentAdapter(
		nil, // wired below after the agent is built
		c.AIProvider,
		c.PortfolioDataProvider,
		c.ActivityDataProvider,
		&newsProviderAdapter{svc: c.NewsService},
		c.ZapLog,
	)
	c.AIOrchestrator = adapter

	// Initialize basket recommender
	c.AIRecommender = aiservice.NewRecommender(
		c.AIProvider,
		&basketRepoAdapter{repo: basketRepo},
		c.PortfolioDataProvider,
		c.ZapLog,
	)

	// Initialize conversation persistence
	c.ConversationRepo = repositories.NewConversationRepository(c.DB, c.ZapLog)
	c.ConversationService = conversationsvc.NewService(c.ConversationRepo, c.AIProvider, c.ZapLog)
	c.AIOrchestrator.SetConversations(c.ConversationService)

	// Initialize Miriam's long-term memory (fact extraction + tone calibration)
	memoryRepo := repositories.NewMiriamMemoryRepository(sqlxDB)
	c.MiriamMemoryRepo = memoryRepo
	memorySvc := aiservice.NewMemoryService(memoryRepo, c.AIProvider, c.ZapLog)
	c.AIOrchestrator.SetMemory(memorySvc)
	c.MemoryService = memorySvc

	// Miriam preferences (discretion + autonomy) — defaults on miss; syncs control_level.
	c.MiriamPreferencesRepo = repositories.NewMiriamPreferencesRepository(sqlxDB, c.ZapLog)
	c.MiriamPreferencesService = miriamservice.NewPreferencesService(c.MiriamPreferencesRepo, memorySvc, c.ZapLog)
	if c.proactiveGuard != nil {
		c.proactiveGuard.SetPreferencesResolver(&prefsResolverAdapter{svc: c.MiriamPreferencesService})
	}

	// Defer-wire MemoryReader into Miriam intelligence services (initialized before memory service).
	if c.MiriamDecisionEngine != nil {
		c.MiriamDecisionEngine.SetMemory(memorySvc)
	}
	if c.MiriamProactiveNudgeEngine != nil {
		c.MiriamProactiveNudgeEngine.SetMemory(memorySvc)
	}
	if c.MiriamIntelligenceOrchestrator != nil {
		c.MiriamIntelligenceOrchestrator.SetMemory(memorySvc)
		// Gate autonomous mandate execution on the user's control level. Only Full
		// Autopilot users get money moved for them by the 15-min worker sweep;
		// guided/monitor users still receive advice. memorySvc.GetControlLevel
		// backs both this and the chat-path gate, so they stay consistent.
		c.MiriamIntelligenceOrchestrator.SetControlLevel(memorySvc)
	}

	// Wire transaction enrichment sidecar into Miriam
	if c.Config.Enrichment.Enabled && c.Config.Enrichment.ServiceURL != "" && c.MiriamIntelligenceOrchestrator != nil {
		enrichClient := enrichment.NewClient(c.Config.Enrichment.ServiceURL)
		enrichSQLx := sqlx.NewDb(c.DB, "postgres")
		enrichRepo := repositories.NewEnrichmentRepository(enrichSQLx)
		txnRepo := repositories.NewTransactionRepository(enrichSQLx)
		txnProvider := repositories.NewTransactionProviderAdapter(txnRepo)
		enricher := miriamservice.NewTransactionEnricher(enrichClient, c.AIProvider, enrichRepo, txnProvider, c.ZapLog)
		c.MiriamIntelligenceOrchestrator.SetEnricher(enricher)

		// Wire spending enricher into the AI orchestrator for plain-English transaction descriptions
		spendingEnricher := miriamservice.NewSpendingEnricher(enrichClient, c.ZapLog)
		c.AIOrchestrator.SetSpendingEnricher(spendingEnricher)

		// Wire transaction pattern analyzer — detects family relationships,
		// recurring recipients, and behavioral clusters from P2P transfers.
		patternDB := sqlx.NewDb(c.DB, "postgres")
		userProvider := repositories.NewPatternUserProvider(patternDB)
		transferProvider := repositories.NewPatternTransferProvider(patternDB)
		patternAnalyzer := miriamservice.NewTransactionPatternAnalyzer(userProvider, transferProvider, c.ZapLog)
		c.MiriamIntelligenceOrchestrator.SetPatternAnalyzer(patternAnalyzer)
	}

	// Initialize usage tracking
	c.UsageRepo = repositories.NewAIUsageRepository(c.DB, c.ZapLog)
	c.UsageService = usagesvc.NewService(c.UsageRepo, c.ZapLog)
	c.AIOrchestrator.SetUsageTracker(c.UsageService)

	// Wire bank statement context into Miriam
	if c.BankStatementRepo != nil {
		c.AIOrchestrator.SetBankStatementContext(aiservice.NewBankStatementContextProvider(c.BankStatementRepo))
	}

	// Initialize knowledge base (RAG)
	// Embeddings route through Cencori gateway for provider failover + billing,
	// or through Cloudflare Workers AI (BGE model) when that is configured.
	if c.Config.AI.Cencori.APIKey != "" {
		embBaseURL, embAPIKey := c.cloudflareGateway(strings.TrimSpace(c.Config.AI.Cencori.APIKey))
		c.EmbeddingsClient = embeddings.NewCencoriEmbeddingsClient(embAPIKey, "", embBaseURL, c.ZapLog)
		c.KnowledgeRepo = repositories.NewKnowledgeRepository(c.DB, c.ZapLog)
		c.KnowledgeService = knowledgesvc.NewService(c.KnowledgeRepo, c.EmbeddingsClient, c.RedisClient, c.ZapLog)
		c.AIOrchestrator.SetKnowledge(c.KnowledgeService)
	}

	// Wire Supermemory for long-term personal memory
	if smKey := strings.TrimSpace(c.Config.AI.Supermemory.APIKey); smKey != "" {
		c.SupermemoryClient = supermemoryclient.New(smKey)
		c.AIOrchestrator.SetSupermemory(&supermemoryAdapter{client: c.SupermemoryClient})
	}

	// Wire Qdrant vector store for episodic + fact memory (replaces Supermemory when configured)
	if baseURL := strings.TrimSpace(c.Config.AI.Qdrant.BaseURL); baseURL != "" && c.EmbeddingsClient != nil {
		qCfg := c.Config.AI.Qdrant
		dim := qCfg.DefaultDim
		if dim == 0 {
			dim = 768
		}
		c.QdrantStore = vector.NewQdrantStore(&vector.QdrantConfig{
			BaseURL:          baseURL,
			APIKey:           strings.TrimSpace(qCfg.APIKey),
			DefaultDim:       dim,
			CollectionPrefix: strings.TrimSpace(qCfg.CollectionPrefix),
		}, c.EmbeddingsClient, c.ZapLog)
		// QdrantStore is used by the memory adapter (agent_wiring.go) for SearchEpisodic/SearchFacts
		c.ZapLog.Info("Qdrant vector store initialized", zap.String("base_url", baseURL))
	}

	// Cloudflare Vectorize: serverless alternative to Qdrant for episodic + fact
	// memory. Pairs best with the Workers AI embedder (same account).
	cf := c.Config.AI.Cloudflare
	if cf.Vectorize.Enabled && strings.TrimSpace(cf.AccountID) != "" && strings.TrimSpace(cf.APIToken) != "" {
		// Prefer the Workers AI BGE embedder for memory vectors when enabled.
		if cf.WorkersAI.Enabled && cf.WorkersAI.EmbeddingsEnabled && strings.TrimSpace(cf.WorkersAI.EmbeddingsModel) != "" {
			embedder, err := embeddings.NewWorkersAIEmbedder(
				strings.TrimSpace(cf.AccountID),
				strings.TrimSpace(cf.APIToken),
				strings.TrimSpace(cf.WorkersAI.EmbeddingsModel),
				"",
				c.ZapLog,
			)
			if err != nil {
				c.ZapLog.Warn("Workers AI embedder not initialized", zap.Error(err))
			} else {
				c.EmbeddingsClient = embedder
				c.ZapLog.Info("Workers AI embedder initialized",
					zap.String("model", cf.WorkersAI.EmbeddingsModel))
			}
		}
		if c.EmbeddingsClient != nil {
			dim := cf.Vectorize.DefaultDim
			if dim == 0 {
				dim = 768
			}
			vs, err := vector.NewVectorizeStore(&vector.VectorizeConfig{
				AccountID:        strings.TrimSpace(cf.AccountID),
				APIToken:         strings.TrimSpace(cf.APIToken),
				DefaultDim:       dim,
				CollectionPrefix: strings.TrimSpace(cf.Vectorize.CollectionPrefix),
			}, c.EmbeddingsClient, c.ZapLog)
			if err != nil {
				c.ZapLog.Warn("Cloudflare Vectorize not initialized", zap.Error(err))
			} else {
				c.VectorizeStore = vs
				// VectorizeStore is used by the memory adapter (agent_wiring.go) for SearchEpisodic/SearchFacts
				c.ZapLog.Info("Cloudflare Vectorize initialized", zap.Int("default_dim", dim))
			}
		}
	}

	// Wire Tavily web search (powers web_search: places, flights, products, recs)
	if tavilyKey := strings.TrimSpace(c.Config.AI.Tavily.APIKey); tavilyKey != "" {
		c.AIOrchestrator.SetWebSearcher(ai.NewTavilyClient(tavilyKey))
	} else {
		c.ZapLog.Warn("Tavily API key not set — web_search (places/flights/recommendations) will be unavailable")
	}

	// Wire embedder to memory service now that EmbeddingsClient is initialized
	if c.EmbeddingsClient != nil && c.MemoryService != nil {
		c.MemoryService.SetEmbedder(c.EmbeddingsClient)
	}

	// Initialize spending analysis (all outflows: card, withdrawal, p2p)
	spendingSvc := spendingsvc.NewService(c.LedgerSpendingRepo)
	c.AIOrchestrator.SetSpending(spendingSvc)

	c.ContextSignalRepo = repositories.NewContextSignalRepository(sqlxDB)
	c.AIOrchestrator.SetContextSignals(c.ContextSignalRepo)

	// Initialize balance history (stash growth chart)
	if c.yieldRepo != nil {
		c.AIOrchestrator.SetBalanceHistory(c.yieldRepo)
	}

	// Initialize pattern analysis
	if c.CardRepo != nil {
		c.AIOrchestrator.SetPatterns(c.CardRepo)
	}

	// Initialize comparative context (uses ledger for balances)
	if c.LedgerService != nil {
		c.AIOrchestrator.SetAggregateStats(c.LedgerService)
	}

	// Initialize action tools (funds transfer + audit)
	if c.LedgerService != nil {
		c.AIOrchestrator.SetFundsTransferer(&fundsTransfererAdapter{ledger: c.LedgerService, blendRouter: c.BlendDepositRouter, logger: c.ZapLog})
		auditRepo := repositories.NewActionAuditRepository(sqlxDB, c.ZapLog)
		c.AIOrchestrator.SetActionAuditor(auditRepo)
	}

	// Wire account checker for fraud/freeze checks on AI-initiated transfers
	if c.UserRepo != nil {
		c.AIOrchestrator.SetAccountChecker(&accountCheckerAdapter{repo: c.UserRepo})
	}

	// Wire emergency withdrawer for stash-to-spend transfers during lock period
	if c.WithdrawalService != nil {
		c.AIOrchestrator.SetEmergencyWithdrawer(c.WithdrawalService)
		// WithdrawalService also satisfies WithdrawalInitiator (fiat bank withdrawals via voice)
		c.AIOrchestrator.SetWithdrawalInitiator(c.WithdrawalService)
	}
	// Goal protection: warns before withdrawals that impact goal-allocated funds
	c.AIOrchestrator.SetGoalProtectionProvider(c.LedgerService)

	// Voice daily transfer cap ($100/day via voice)
	if c.RedisClient != nil {
		c.AIOrchestrator.SetVoiceDailyLimiter(aiservice.NewVoiceDailyLimiter(c.RedisClient, 500))
		// Redis client for best-effort short-TTL caching of voice hot-path reads
		// (realtime dynamic vars, cost-ceiling).
		c.AIOrchestrator.SetRedisCache(c.RedisClient)

		// Working memory: Redis-backed conversation state cache for continuity.
		wm := aimemory.NewWorkingMemoryStore(c.RedisClient, c.ZapLog)
		c.AIOrchestrator.SetWorkingMemory(wm)
		c.WorkingMemoryStore = wm
	}

	// Financial event timeline store
	eventStore := aimemory.NewEventStore(sqlxDB)
	c.AIOrchestrator.SetEventStore(eventStore)
	c.EventStore = eventStore

	// Memory quality metrics
	c.MemoryMetrics = aimemory.NewMetrics(sqlxDB)

	// Push notifications when Miriam moves money on the user's behalf
	if c.NotificationService != nil {
		c.AIOrchestrator.SetMoneyMoveNotifier(c.NotificationService)
	}

	// Use Redis for pending actions (survives restarts, works across instances)
	if c.RedisClient != nil {
		c.AIOrchestrator.SetPendingActions(aiservice.NewRedisPendingActions(c.RedisClient, c.ZapLog))
		// Redis-backed savings goal store (persists user goals across sessions)
		c.AIOrchestrator.SetSavingsGoalStore(aiservice.NewRedisSavingsGoalStore(c.RedisClient, c.ZapLog))
	}
	if c.SharedGoalService != nil {
		c.AIOrchestrator.SetSharedGoalCreator(&sharedGoalCreatorAdapter{svc: c.SharedGoalService})
	}

	// Wire read-only data tools
	if c.CardRepo != nil {
		c.AIOrchestrator.SetCardTransactions(c.CardRepo)
	}
	if c.DepositRepo != nil {
		c.AIOrchestrator.SetDepositHistory(c.DepositRepo)
	}
	if c.WithdrawalRepo != nil {
		c.AIOrchestrator.SetWithdrawalHistory(c.WithdrawalRepo)
	}
	if c.ReceiptRepo != nil {
		c.AIOrchestrator.SetReceiptHistory(c.ReceiptRepo)
	}
	c.AIOrchestrator.SetBudgetProvider(c.BudgetRepo)
	c.AIOrchestrator.SetFinancialProfileProvider(c.FinancialProfileRepo)
	c.AIOrchestrator.SetFinancialObligationProvider(c.FinancialObligationService)
	c.AIOrchestrator.SetAutomationCreator(&automationCreatorAdapter{service: c.AutomationService})
	c.AIOrchestrator.SetAutomationProvider(&automationProviderAdapter{svc: c.AutomationService})
	c.AIOrchestrator.SetMiriamIntelligenceProvider(c.MiriamIntelligenceService)
	c.AIOrchestrator.SetObligationCreator(&obligationCreatorAdapter{service: c.FinancialObligationService})
	c.AIOrchestrator.SetFinancialObligationManager(&obligationManagerAdapter{service: c.FinancialObligationService})
	c.AIOrchestrator.SetCurrencyRateProvider(c.ExchangeRateRepo)
	warrantyRepo := repositories.NewWarrantyRepository(sqlxDB)
	c.AIOrchestrator.SetWarrantyTracker(warrantyRepo)

	// Wire recurring expense detector
	recurringRepo := repositories.NewRecurringExpenseRepository(sqlxDB)
	c.AIOrchestrator.SetRecurringDetector(recurringRepo)

	// Wire receipt challenges and savings suggestions
	if c.ReceiptRepo != nil {
		c.AIOrchestrator.SetReceiptChallenges(aiservice.NewReceiptChallengeProvider(c.ReceiptRepo, c.BudgetRepo, spendingSvc))
		c.AIOrchestrator.SetSavingsSuggestions(aiservice.NewSavingsSuggestionProvider(c.ReceiptRepo, spendingSvc))
	}

	// Wire price tracking
	priceRepo := repositories.NewPriceTrackingRepository(sqlxDB)
	c.AIOrchestrator.SetPriceTracker(priceRepo)

	// Wire merchant intelligence
	merchantRepo := repositories.NewMerchantRepository(sqlxDB)
	c.AIOrchestrator.SetMerchantAnalyzer(merchantRepo)

	if c.yieldRepo != nil {
		c.AIOrchestrator.SetYieldProvider(c.yieldRepo)
	}

	// Wire tax, email, and goals tools
	c.AIOrchestrator.SetUserProfile(&userProfileAdapter{userRepo: c.UserRepo})
	if c.EmailService != nil {
		c.AIOrchestrator.SetReportEmailSender(c.EmailService)
	}

	c.ZapLog.Info("AI Financial Manager services initialized",
		zap.String("provider", c.AIProvider.Name()),
	)

	// --- New Agent initialization ---
	{
		toolRegistry := aitools.NewRegistry()

		// Register all tools (nil-checked internally)
		aitools.RegisterPortfolioTools(toolRegistry)
		aitools.RegisterAllSpendingAndTransactionTools(toolRegistry)
		aitools.RegisterAllRemainingTools(toolRegistry)
		aitools.RegisterExecutionTools(toolRegistry)
		aitools.RegisterBillTools(toolRegistry)
		aitools.RegisterTravelTools(toolRegistry)
		aitools.RegisterSavingsGoalsV2Tools(toolRegistry)

		c.NewToolRegistry = toolRegistry

		memSvc := buildNewMemoryService(c)
		agentDeps := &aicore.Dependencies{
			AIProvider:          c.AIProvider,
			ToolRegistry:        toolRegistry,
			Memory:              memSvc,
			IntentClassifier:    c.intentClassifier(),
			State:               buildStateService(c),
			Conversations:       buildConversationService(c),
			Usage:               buildUsageService(c),
			CostGuard:           buildAgentCostGuard(c),
			Portfolio:           buildPortfolioProvider(c),
			Spending:            buildSpendingProvider(c),
			Transactions:        buildTransactionProvider(c),
			FundsTransfer:       buildFundsTransferer(c),
			Budget:              buildBudgetProvider(c),
			Goals:               buildGoalStore(c),
			Obligations:         buildObligationProvider(c),
			Automation:          buildAutomationProvider(c),
			Profile:             buildProfileProvider(c),
			CurrencyRates:       buildCurrencyRateProvider(c),
			WebSearch:           nil, // wired below when available
			VoiceLimiter:        nil,
			Notifier:            nil,
			NairaCtx:            buildNairaContextProvider(c),
			BankStatement:       buildBankStatementProvider(c),
			Signals:             buildSignalProvider(c),
			MiriamIntell:        buildMiriamIntelligenceProvider(c),
			Investment:          buildInvestmentProvider(c),
			Receipt:             buildReceiptProvider(c),
			P2P:                 buildP2PProvider(c),
			Warranty:            buildWarrantyProvider(c),
			PriceTracker:        buildPriceTracker(c),
			Merchant:            buildMerchantProvider(c),
			Cache:               buildCacheClient(c),
			Logger:              c.ZapLog,
			Config:              aicore.DefaultConfig(),
			AccountChecker:      buildAccountChecker(c),
			ActionAuditor:       buildActionAuditor(c),
			WithdrawalInitiator: buildWithdrawalInitiator(c),
			GoalProtector:       buildGoalProtector(c),
			Subscription:        buildSubscriptionProvider(c),
			RecurringExpense:    buildRecurringExpenseProvider(c),
			SavingsSuggestion:   buildSavingsSuggestionProvider(c),
			ReceiptChallenge:    buildReceiptChallengeProvider(c),
			ReportEmail:         buildReportEmailSender(c),
			Knowledge:           buildKnowledgeSearcher(c),
			Simulator:           buildSimulator(c),
			Comparative:         buildComparativeProvider(c),
			Tax:                 buildTaxProvider(c),
			EmergencyWithdraw:   buildEmergencyWithdrawer(c),
			FinancialGovernance: buildFinancialGovernance(c),
			Nudge:               nil,
			WorkingMemory:       &workingMemoryAdapter{store: c.WorkingMemoryStore},
			EventStoreFn: func(ctx context.Context, userID uuid.UUID) string {
				if c.EventStore == nil {
					return ""
				}
				return c.EventStore.BuildEventsContext(ctx, userID)
			},
		}

		// Execution Engine (spec 5.2) providers — wrap the same domain
		// services the app endpoints use, so Miriam's actions inherit their
		// balance, ownership, and KYC checks.
		agentDeps.BillPay = buildBillPayProvider(c)
		agentDeps.SubscriptionAudit = buildSubscriptionAuditProvider(c, recurringRepo)
		agentDeps.InvestmentExec = buildInvestmentExecProvider(c)
		if c.LedgerService != nil {
			agentDeps.YieldOpt = buildYieldOptProvider(c, &fundsTransfererAdapter{ledger: c.LedgerService, blendRouter: c.BlendDepositRouter, logger: c.ZapLog})
		}
		agentDeps.MerchantBlock = buildMerchantBlockProvider(c)
		agentDeps.TradeCopy = buildTradeCopyProvider(c)
		// agentDeps.Bills is wired later (after Airbills/billpay init) via
		// c.AgentDeps, since Circle/ChainRails come up after AI services.

		// Prefer Redis so multi-instance deploys share anomaly detections for chat
		// context; fall back to in-memory for local/eval without Redis.
		if c.RedisClient != nil {
			c.AnomalyStore = aiservice.NewRedisAnomalyStore(c.RedisClient)
		} else {
			c.AnomalyStore = aiservice.NewInMemoryAnomalyStore()
		}
		agentDeps.AnomalyContextFn = func(ctx context.Context, userID uuid.UUID) string {
			results, err := c.AnomalyStore.Get(ctx, userID)
			if err != nil || len(results) == 0 {
				return ""
			}
			text := "[ANOMALIES DETECTED — YOU MUST MENTION THESE PROACTIVELY. The user may not know about them yet. Lead with the most severe one. Cite actual charge amounts and merchants from the descriptions below. Never restate projected run-rates or trailing averages as dollar figures — describe them qualitatively (e.g. \"way above your usual\"), since those are estimates, not real transactions.]"
			for _, r := range results {
				text += fmt.Sprintf("\n[%s] %s — %s", strings.ToUpper(string(r.Severity)), r.Title, r.Description)
			}
			return text
		}
		if c.AIOrchestrator != nil {
			c.AIOrchestrator.SetAnomalyStore(c.AnomalyStore)
		}

		// Wire enrichment summary into both the core agent and the streaming adapter
		// so Miriam has proactive awareness of spending patterns without tool calls.
		if c.Config.Enrichment.Enabled && c.Config.Enrichment.ServiceURL != "" {
			enrichSQLx := sqlx.NewDb(c.DB, "postgres")
			enrichRepo := repositories.NewEnrichmentRepositoryWithLogger(enrichSQLx, c.ZapLog)
			agentDeps.EnrichmentSummaryFn = enrichRepo.GetUserEnrichmentSummary
			if c.AIOrchestrator != nil {
				c.AIOrchestrator.SetEnrichmentSummaryFn(enrichRepo.GetUserEnrichmentSummary)
				c.AIOrchestrator.SetMerchantEnricher(enrichRepo)
			}
		}

		// Reuse the same quality gate as the streaming path so core's non-streaming
		// responses are held to the same standard (not a weaker length heuristic).
		agentDeps.QualityGate = func(response string) (bool, string) {
			v := aiservice.CheckResponseQuality(response)
			return v.Pass, aiservice.QualityCorrectionHint(v.Failures)
		}

		// Deterministic pre-delivery guard: strips ungrounded currency figures,
		// surfaces a missed anomaly, and sanitizes formatting. Only fires when the
		// ai.response_guard flag is on (default off in prod; on in the simulation).
		agentDeps.ResponseGuard = func(content, grounding, anomalies string) string {
			return aiservice.GuardResponse(content, grounding, anomalies)
		}

		// Core had no base system prompt — non-streaming Miriam ran with no persona.
		// Give it the same base as the streaming path (persona + tool rules).
		agentConfig := aicore.DefaultConfig()
		agentConfig.SystemPrompt = aiservice.SystemPromptV2 + "\n\n" + aiservice.SystemPromptTools
		agentConfig.ResponseGuard = c.Config.AI.ResponseGuard

		// ChatEngine delegation providers — these bridge the core.Agent to
		// the orchestrator's existing implementations for Phase 1 thin delegation.
		if c.AIOrchestrator != nil {
			// Pending actions store
			if c.RedisClient != nil {
				agentDeps.PendingActions = aiservice.NewRedisPendingActions(c.RedisClient, c.ZapLog)
			}

			// Supermemory client
			agentDeps.Supermemory = &coreSupermemoryAdapter{client: c.SupermemoryClient}

			// Aggregate stats (account balances)
			agentDeps.AggregateStats = c.LedgerService

			// User profile providers
			profileAdapter := &userProfileAdapter{userRepo: c.UserRepo}
			agentDeps.FullUserProfile = profileAdapter
			agentDeps.UserProfile = profileAdapter

			// Goal protection
			agentDeps.GoalProtection = c.LedgerService

			// Obligation manager
			agentDeps.ObligationManager = &obligationManagerAdapter{service: c.FinancialObligationService}

			// Savings goals store (legacy single-goal Redis path; preserved for
			// backward compat with the existing set_savings_goal tool).
			if c.RedisClient != nil {
				inner := aiservice.NewRedisSavingsGoalStore(c.RedisClient, c.ZapLog)
				agentDeps.SavingsGoals = &coreSavingsGoalStoreAdapter{inner: inner}
			}

			// User goals store (new Postgres-backed multi-goal). Wired here so
			// the v2 tools (create_user_goal, list_user_goals, etc.) work for
			// every chat turn once the registry is registered.
			if c.UserGoalRepo != nil && c.GoalsService != nil {
				agentDeps.UserGoals = &coreUserGoalStoreAdapter{svc: c.GoalsService}
			}

			// Conversation persister
			agentDeps.ConversationsPersister = c.ConversationService

			// Usage tracker
			agentDeps.UsageTrackerFn = c.UsageService

			// Spending analyzer (time-range based)
			agentDeps.SpendingAnalyzer = &coreSpendingAnalyzerAdapter{inner: spendingSvc}

			// Balance history
			agentDeps.BalanceHistory = &coreBalanceHistoryAdapter{yieldRepo: c.yieldRepo}

			// Patterns
			agentDeps.Patterns = &corePatternAnalyzerAdapter{cardRepo: c.CardRepo}

			// Financial profile
			agentDeps.FinancialProfile = &coreFinancialProfileAdapter{financialProfileRepo: c.FinancialProfileRepo}

			// Activity data (already constructed at container init)
			agentDeps.Activity = &coreActivityDataAdapter{inner: c.ActivityDataProvider}

			// News data (reuse the existing adapter)
			agentDeps.News = &newsProviderAdapter{svc: c.NewsService}

			// Context signals
			agentDeps.ContextSignals = c.ContextSignalRepo

			// Shared goal creator
			agentDeps.SharedGoalCreator = &sharedGoalCreatorAdapter{svc: c.SharedGoalService}

			// Obligation creator
			agentDeps.ObligationCreator = &coreObligationCreatorAdapter{service: c.FinancialObligationService}

			// Withdrawal history
			agentDeps.WithdrawalHistory = &coreWithdrawalHistoryAdapter{withdrawalRepo: c.WithdrawalRepo}

			// Bank statement context
			if c.BankStatementRepo != nil {
				inner := aiservice.NewBankStatementContextProvider(c.BankStatementRepo)
				agentDeps.BankStatementCtx = &coreBankStatementContextAdapter{inner: inner}
			}

			// Memory store (tone profiles) — repository implements GetToneProfile
			agentDeps.MemoryStore = c.MiriamMemoryRepo

			// Redis
			agentDeps.Redis = c.RedisClient

			// Function pointer delegations to orchestrator
			agentDeps.BuildRealtimeGreetingFn = c.AIOrchestrator.BuildRealtimeGreeting
			agentDeps.BuildRealtimeInstructionsFn = c.AIOrchestrator.BuildRealtimeInstructions
			agentDeps.BuildRealtimeDynamicVarsFn = c.AIOrchestrator.BuildRealtimeDynamicVars
			agentDeps.GenerateEnhancedNudgeFn = func(ctx context.Context, userID uuid.UUID, screen, amount, currency, timeOfDay string, dayOfWeek int, daysUntilPayday int, merchantHint string) (map[string]interface{}, error) {
				resp, err := c.AIOrchestrator.GenerateEnhancedNudge(ctx, userID, entities.EnhancedNudgeRequest{
					Screen:          screen,
					Amount:          amount,
					Currency:        currency,
					TimeOfDay:       timeOfDay,
					DayOfWeek:       dayOfWeek,
					DaysUntilPayday: daysUntilPayday,
					MerchantHint:    merchantHint,
				})
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"message":  resp.Message,
					"severity": resp.Severity,
					"show":     resp.Show,
				}, nil
			}
			agentDeps.GenerateNudgeFn = func(ctx context.Context, userID uuid.UUID, screen, amount, currency string) (map[string]interface{}, error) {
				resp, err := c.AIOrchestrator.GenerateNudge(ctx, userID, aiservice.NudgeRequest{
					Screen:   screen,
					Amount:   amount,
					Currency: currency,
				})
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"show":     resp.Show,
					"message":  resp.Message,
					"severity": resp.Severity,
					"shake":    resp.Shake,
				}, nil
			}
			agentDeps.GetProactiveVoiceInsightFn = c.AIOrchestrator.GetProactiveVoiceInsight
			agentDeps.GenerateWrappedCardsFn = func(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
				cards, err := c.AIOrchestrator.GenerateWrappedCards(ctx, userID)
				if err != nil {
					return nil, err
				}
				result := make([]map[string]interface{}, len(cards))
				for i, card := range cards {
					result[i] = map[string]interface{}{
						"type":    card.Type,
						"title":   card.Title,
						"content": card.Content,
						"data":    card.Data,
					}
				}
				return result, nil
			}
			agentDeps.GetPersonalizedSuggestionsFn = c.AIOrchestrator.GetPersonalizedSuggestions
			agentDeps.StageOperatingPlanActionFn = c.AIOrchestrator.StageOperatingPlanAction
			agentDeps.ConfirmActionFn = c.AIOrchestrator.ConfirmAction
			agentDeps.CancelActionFn = c.AIOrchestrator.CancelAction
			agentDeps.PrepareVoiceActionFn = c.AIOrchestrator.PrepareVoiceAction
			agentDeps.QuickReplyFn = c.AIOrchestrator.QuickReply
			agentDeps.GetConversationStartersFn = c.AIOrchestrator.PredictiveConversationStarters
		}

		// Wire gameplay provider so Miriam can reference streaks, challenges,
		// and achievements conversationally.
		if c.GameplayStreakService != nil && c.GameplayChallengeService != nil && c.GameplayAchievementService != nil {
			gpAdapter := &gameplayProviderAdapter{
				streaks:      c.GameplayStreakService,
				challenges:   c.GameplayChallengeService,
				achievements: c.GameplayAchievementService,
			}
			agentDeps.Gameplay = gpAdapter
			if c.AIOrchestrator != nil {
				c.AIOrchestrator.SetGameplayProvider(gpAdapter)
			}
		}

		agent := aicore.NewAgent(agentDeps, agentConfig, c.ZapLog)
		c.NewAgent = agent
		c.AgentDeps = agentDeps
		// Travel is wired here (not in funding_wiring) because AgentDeps is
		// constructed during initializeAIServices, after the travel service.
		c.AgentDeps.Travel = buildTravelProvider(c)

		// Mirror the travel provider into the streaming AgentAdapter so the
		// staged book_flight confirmation card can resolve the exact charge.
		if c.AIOrchestrator != nil {
			c.AIOrchestrator.SetTravel(c.AgentDeps.Travel)
		}

		// Construct CoreChatEngineAdapter — a drop-in ChatEngine backed by core.Agent.
		// This is the new agent path that replaces the old AgentAdapter orchestrator.
		c.NewChatEngine = aiservice.NewCoreChatEngineAdapter(agent)

		if c.AIOrchestrator != nil {
			c.AIOrchestrator.SetAgent(agent)
			// Wire receipt splitter so stream confirm path can run equal P2P splits.
			if adapter, ok := agentDeps.Receipt.(*receiptP2PSplitAdapter); ok {
				c.AIOrchestrator.SetReceiptSplitter(adapter)
			}
			// Wire the passcode service as the StepUpVerifier so
			// ConfirmAction enforces passcode/Face ID for fund-moving
			// actions in the core, not in the HTTP handler.
			if passcodeSvc := c.GetPasscodeService(); passcodeSvc != nil {
				c.AIOrchestrator.SetStepUpVerifier(&passcodeStepUpAdapter{svc: passcodeSvc})
			} else {
				c.ZapLog.Warn("passcode service not available — AI fund-moving actions will be refused (fail-closed)")
			}
		}
	}

	return nil
}

// AI service adapters

type portfolioValueAdapter struct {
	repo *repositories.PortfolioRepository
}

func (a *portfolioValueAdapter) GetPortfolioValue(ctx context.Context, userID uuid.UUID, date time.Time) (decimal.Decimal, error) {
	return a.repo.GetPortfolioValue(ctx, userID, date)
}

type contributionRepoAdapter struct {
	repo *repositories.UserContributionsRepository
}

func (a *contributionRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID, contributionType *entities.ContributionType, startDate, endDate *time.Time, limit, offset int) ([]*entities.UserContribution, error) {
	return a.repo.GetByUserID(ctx, userID, contributionType, startDate, endDate, limit, offset)
}

func (a *contributionRepoAdapter) GetTotalByType(ctx context.Context, userID uuid.UUID, startDate, endDate time.Time) (map[entities.ContributionType]string, error) {
	return a.repo.GetTotalByType(ctx, userID, startDate, endDate)
}

type streakRepoAdapter struct {
	repo *repositories.InvestmentStreakRepository
}

func (a *streakRepoAdapter) GetByUserID(ctx context.Context, userID uuid.UUID) (*entities.InvestmentStreak, error) {
	return a.repo.GetByUserID(ctx, userID)
}

type newsProviderAdapter struct {
	svc *newsservice.Service
}

func (a *newsProviderAdapter) GetWeeklyNews(ctx context.Context, userID uuid.UUID) ([]*entities.UserNews, error) {
	return a.svc.GetWeeklyNews(ctx, userID)
}

type basketRepoAdapter struct {
	repo *repositories.BasketRepository
}

func (a *basketRepoAdapter) GetCuratedBaskets(ctx context.Context) ([]*entities.Basket, error) {
	return a.repo.GetAll(ctx)
}

func (a *basketRepoAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.Basket, error) {
	return a.repo.GetByID(ctx, id)
}

type alpacaNewsAdapter struct {
	client *alpaca.Client
}

func (a *alpacaNewsAdapter) GetNews(ctx context.Context, req *entities.AlpacaNewsRequest) (*entities.AlpacaNewsResponse, error) {
	return a.client.GetNews(ctx, req)
}

// GetAIOrchestrator returns the ChatEngine interface (backed by core.Agent via CoreChatEngineAdapter).
func (c *Container) GetAIOrchestrator() aiservice.ChatEngine {
	return c.NewChatEngine
}

// GetNewChatEngine returns the new ChatEngine backed by core.Agent.
// This is the replacement for the old AgentAdapter orchestrator.
func (c *Container) GetNewChatEngine() aiservice.ChatEngine {
	return c.NewChatEngine
}

// GetAIRecommender returns the AI recommender
func (c *Container) GetAIRecommender() *aiservice.Recommender {
	return c.AIRecommender
}

// GetConversationService returns the conversation service
func (c *Container) GetConversationService() *conversationsvc.Service {
	return c.ConversationService
}

// GetUsageService returns the usage service
func (c *Container) GetUsageService() *usagesvc.Service {
	return c.UsageService
}

// GetKnowledgeService returns the knowledge service
func (c *Container) GetKnowledgeService() *knowledgesvc.Service {
	return c.KnowledgeService
}

// GetNewsService returns the news service
func (c *Container) GetNewsService() *newsservice.Service {
	return c.NewsService
}

// GetPortfolioDataProvider returns the portfolio data provider
func (c *Container) GetPortfolioDataProvider() *aiservice.PortfolioDataProviderImpl {
	return c.PortfolioDataProvider
}

// GetActivityDataProvider returns the activity data provider
func (c *Container) GetActivityDataProvider() *aiservice.ActivityDataProviderImpl {
	return c.ActivityDataProvider
}

// GetStreakRepository returns the investment streak repository adapter
func (c *Container) GetStreakRepository() handlers.InvestmentStreakRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &streakRepoAdapter{repo: repositories.NewInvestmentStreakRepository(c.DB, c.ZapLog)}
}

// GetContributionsRepository returns the user contributions repository adapter
func (c *Container) GetContributionsRepository() handlers.UserContributionsRepository {
	if c.ActivityDataProvider == nil {
		return nil
	}
	return &contributionRepoAdapter{repo: repositories.NewUserContributionsRepository(c.DB, c.ZapLog)}
}

type prefsResolverAdapter struct {
	svc *miriamservice.PreferencesService
}

func (a *prefsResolverAdapter) ProactivePrefs(ctx context.Context, userID uuid.UUID) platform.ProactivePrefs {
	p, err := a.svc.Get(ctx, userID)
	if err != nil {
		return platform.ProactivePrefs{
			QuietEnabled:   true,
			QuietStart:     22,
			QuietEnd:       7,
			DailyCap:       6,
			AllowBriefings: true,
			AllowRisk:      true,
			AllowNudges:    true,
			AllowFollowups: true,
		}
	}
	var tz string
	if p.Timezone != nil {
		tz = *p.Timezone
	}
	return platform.ProactivePrefs{
		QuietEnabled:   p.QuietEnabled,
		QuietStart:     p.QuietStart,
		QuietEnd:       p.QuietEnd,
		DailyCap:       p.DailyCap,
		Timezone:       tz,
		AllowBriefings: p.AllowBriefings,
		AllowRisk:      p.AllowRisk,
		AllowNudges:    p.AllowNudges,
		AllowFollowups: p.AllowFollowups,
	}
}

// cloudflareGateway returns the Cloudflare AI Gateway base URL and
// gateway-scoped API key when the gateway is configured. When disabled it
// returns the origin key unchanged, so callers can treat the result as "no
// override".
func (c *Container) cloudflareGateway(originKey string) (baseURL, apiKey string) {
	gw := c.Config.AI.Cloudflare.Gateway
	if !gw.Enabled || strings.TrimSpace(gw.BaseURL) == "" || strings.TrimSpace(gw.APIKey) == "" {
		return "", originKey
	}
	return strings.TrimSuffix(strings.TrimSpace(gw.BaseURL), "/"), strings.TrimSpace(gw.APIKey)
}

// intentClassifier builds the cheap Cloudflare Workers AI intent classifier when
// configured. Returns nil otherwise, in which case the agent keeps its
// deterministic keyword routing.
func (c *Container) intentClassifier() aicore.IntentClassifier {
	cf := c.Config.AI.Cloudflare
	workers := cf.WorkersAI
	if !workers.ClassifierEnabled || strings.TrimSpace(cf.AccountID) == "" || strings.TrimSpace(cf.APIToken) == "" {
		return nil
	}

	timeout := time.Duration(workers.ClassifierTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	client, err := ai.NewWorkersAIClient(&ai.WorkersAIConfig{
		AccountID: strings.TrimSpace(cf.AccountID),
		APIToken:  strings.TrimSpace(cf.APIToken),
		Model:     strings.TrimSpace(workers.Model),
	}, c.ZapLog)
	if err != nil {
		c.ZapLog.Warn("Workers AI intent classifier not initialized", zap.Error(err))
		return nil
	}
	inner := ai.NewWorkersAIIntentClassifier(&ai.IntentClassifierConfig{
		Client:  client,
		Model:   strings.TrimSpace(workers.Model),
		Timeout: timeout,
	}, c.ZapLog)
	return &coreIntentClassifier{inner: inner}
}

// aiCategoryToCoreCategory maps the ai package's intent categories to core's
// ToolCategory values (identical strings, kept in one place here so the domain
// layer never depends on the infrastructure classifier).
var aiCategoryToCoreCategory = map[ai.IntentCategory]aicore.ToolCategory{
	ai.IntentOverview:   aicore.CategoryOverview,
	ai.IntentSpending:   aicore.CategorySpending,
	ai.IntentAction:     aicore.CategoryAction,
	ai.IntentPlanning:   aicore.CategoryPlanning,
	ai.IntentHistory:    aicore.CategoryHistory,
	ai.IntentAutomation: aicore.CategoryAutomation,
	ai.IntentBudget:     aicore.CategoryBudget,
	ai.IntentMemory:     aicore.CategoryMemory,
	ai.IntentVoice:      aicore.CategoryVoice,
	ai.IntentInvestment: aicore.CategoryInvestment,
	ai.IntentKnowledge:  aicore.CategoryKnowledge,
}

// coreIntentClassifier adapts the infrastructure intent classifier to the
// domain-owned port, translating provider categories into core ToolCategory
// values. Unmappable categories are treated as untrustworthy so the agent falls
// back to deterministic keyword routing.
type coreIntentClassifier struct {
	inner ai.IntentClassifier
}

func (c *coreIntentClassifier) Classify(ctx context.Context, message string) (aicore.ToolCategory, float64, bool) {
	category, confidence, ok := c.inner.Classify(ctx, message)
	if !ok {
		return "", confidence, false
	}
	mapped, known := aiCategoryToCoreCategory[category]
	if !known {
		return "", confidence, false
	}
	return mapped, confidence, true
}
