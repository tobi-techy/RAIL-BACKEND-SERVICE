package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	activityhandlers "github.com/rail-service/rail_service/internal/api/handlers/activity"
	fundinghandlers "github.com/rail-service/rail_service/internal/api/handlers/funding"
	p2phandlers "github.com/rail-service/rail_service/internal/api/handlers/p2p"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/entities"
	activitysvc "github.com/rail-service/rail_service/internal/domain/services/activity"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/domain/services/automation"
	"github.com/rail-service/rail_service/internal/domain/services/billpay"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/integration"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/limits"
	"github.com/rail-service/rail_service/internal/domain/services/pajfunding"
	rampsvc "github.com/rail-service/rail_service/internal/domain/services/ramp"
	"github.com/rail-service/rail_service/internal/domain/services/travel"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/airbills"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/brij"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/chainrails"
	graphadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/graph"
	pajadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/paj"
	ramphubadapter "github.com/rail-service/rail_service/internal/infrastructure/adapters/ramphub"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func (c *Container) initializeBridgeServices() {
	if c.BridgeClient == nil {
		c.ZapLog.Warn("Bridge client not configured, skipping Bridge services initialization")
		return
	}

	// Bridge virtual account service will be initialized after allocation service
	// For now, just set up the webhook handler with a placeholder service
	webhookSecret := c.Config.Bridge.WebhookSecret
	if webhookSecret == "" {
		c.ZapLog.Warn("Bridge webhook secret not configured")
	}

	// Determine if webhook verification should be skipped (only in development)
	skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""

	// Create a minimal webhook service for now
	// Full service will be wired after domain services are initialized
	c.BridgeWebhookHandler = handlers.NewBridgeWebhookHandler(
		nil, // Service will be set later
		&walletWebhookAdapter{walletService: c.WalletService},
		c.ZapLog,
		webhookSecret,
		skipWebhookVerification,
		c.Config.Environment,
	)

	c.ZapLog.Info("Bridge webhook handler initialized")
}

// GetInstantFundingHandlers returns the instant funding handlers
func (c *Container) GetInstantFundingHandlers() *fundinghandlers.InstantFundingHandlers {
	return c.InstantFundingHandlers
}

// GetP2PHandlers returns the P2P transfer handlers
func (c *Container) GetP2PHandlers() *p2phandlers.Handlers {
	return c.P2PHandlers
}

// GetWithdrawalSecurityStore returns the withdrawal security store
func (c *Container) GetWithdrawalSecurityStore() *repositories.WithdrawalSecurityStore {
	return c.WithdrawalSecurityStore
}

// GetDepositSecurityStore returns the deposit security store
func (c *Container) GetDepositSecurityStore() *repositories.DepositSecurityStore {
	return c.DepositSecurityStore
}

// GetUnifiedFundingWebhookHandler returns the unified funding webhook handler
func (c *Container) GetUnifiedFundingWebhookHandler() *webhooks.UnifiedFundingWebhookHandler {
	return c.UnifiedFundingWebhookHandler
}

func (c *Container) initializeInstantFundingServices(sqlxDB *sqlx.DB) {
	// Initialize repositories
	c.InstantFundingRepo = repositories.NewInstantFundingRepository(sqlxDB)
	c.UserAccountRepo = repositories.NewUserAccountRepository(sqlxDB)
	c.WithdrawalSecurityStore = repositories.NewWithdrawalSecurityStore(sqlxDB)
	c.DepositSecurityStore = repositories.NewDepositSecurityStore(sqlxDB)

	// Initialize virtual account repo for instant funding
	virtualAccountRepo := repositories.NewVirtualAccountRepository(sqlxDB)

	// Create Alpaca adapter for instant funding
	alpacaAdapter := &InstantFundingAlpacaAdapterImpl{
		service: c.AlpacaService,
	}

	// Initialize instant funding service
	c.InstantFundingService = funding.NewInstantFundingService(
		alpacaAdapter,
		virtualAccountRepo,
		c.InstantFundingRepo,
		c.UserAccountRepo,
		c.ZapLog,
		c.Config.Alpaca.FirmAccountNo,
	)

	// Initialize handlers
	c.InstantFundingHandlers = fundinghandlers.NewInstantFundingHandlers(
		c.InstantFundingService,
		c.ZapLog,
	)

	// Wire deposit security store to validation service
	if c.FundingService != nil && c.FundingService.GetValidationService() != nil {
		c.FundingService.GetValidationService().SetDepositSecurityStore(c.DepositSecurityStore)
	}

	c.ZapLog.Info("Instant funding services initialized")

	// --- ChainRails (cross-chain deposit funnel) ---
	c.ZapLog.Info("ChainRails config",
		zap.Bool("api_key_present", c.Config.ChainRails.APIKey != ""),
		zap.Bool("webhook_secret_present", c.Config.ChainRails.WebhookSecret != ""),
		zap.String("destination_chain", c.Config.ChainRails.DestinationChain),
		zap.String("settlement_token", c.Config.ChainRails.SettlementToken))

	if c.Config.ChainRails.APIKey != "" {
		crClient := chainrails.NewClient(chainrails.Config{
			APIKey:           c.Config.ChainRails.APIKey,
			WebhookSecret:    c.Config.ChainRails.WebhookSecret,
			BaseURL:          c.Config.ChainRails.BaseURL,
			DestinationChain: c.Config.ChainRails.DestinationChain,
			SettlementToken:  c.Config.ChainRails.SettlementToken,
		}, c.ZapLog)
		c.ChainRailsClient = crClient
		c.ChainRailsHandlers = fundinghandlers.NewChainRailsHandlers(
			c.ChainRailsClient, c.FundingService, c.Config.ChainRails.WebhookSecret, c.Config.ChainRails.DestinationChain, c.Logger,
		)
		// Wire ChainRails into withdrawal service for cross-chain withdrawals
		if c.WithdrawalService != nil {
			c.WithdrawalService.SetChainRailsAdapter(c.ChainRailsClient)
			c.ChainRailsHandlers.SetWithdrawalService(c.WithdrawalService)
		}
		if c.WalletService != nil {
			c.ChainRailsHandlers.SetWalletLookup(c.WalletService)
		}
		// Wire deposit sweep repository for auto-sweep and webhook handling
		c.DepositSweepRepo = repositories.NewDepositSweepRepository(sqlxDB)
		if c.FundingService != nil {
			c.FundingService.SetDepositSweepRepo(c.DepositSweepRepo)
		}
		c.ChainRailsHandlers.SetSweepService(c.DepositSweepRepo)

		if c.BlendDepositRouter != nil {
			// Empty destination => router derives BASE_MAINNET/BASE_TESTNET from chain_id.
			c.BlendDepositRouter.SetChainRailsBridge(c.ChainRailsClient, "")
			c.ZapLog.Info("ChainRails wired into Blend deposit router (bridging stash USDC to Base)")
		}
		c.ZapLog.Info("ChainRails deposit funnel initialized")
	} else {
		c.ZapLog.Warn("ChainRails API key is empty, skipping initialization")
	}

	// --- Paj Cash (NGN on/off ramp) ---
	// pajService is hoisted so RampHub can use it as a fallback even though the
	// two initialize independently (RampHub must work with Paj unconfigured).
	var pajService *pajfunding.Service
	if c.Config.Paj.APIKey != "" {
		pajClient := pajadapter.NewClient(pajadapter.Config{
			APIKey:        c.Config.Paj.APIKey,
			BaseURL:       c.Config.Paj.BaseURL,
			WebhookURL:    c.Config.Paj.WebhookURL,
			WalletAddress: c.Config.Paj.WalletAddress,
			TokenMint:     c.Config.Paj.TokenMint,
			Chain:         c.Config.Paj.Chain,
		}, c.ZapLog)
		pajService = pajfunding.NewService(sqlxDB, pajClient, &WithdrawalLedgerAdapter{ledgerService: c.LedgerService}, c.AllocationService, &PajDepositLedgerAdapter{ledgerService: c.LedgerService}, c.RedisClient, c.Config.Security.EncryptionKey, c.ZapLog)
		pajService.SetDepositRepository(c.DepositRepo)
		if c.NotificationService != nil {
			pajService.SetNotificationService(c.NotificationService)
		}
		if c.WalletService != nil {
			pajService.SetWalletProvider(c.WalletService)
		}
		if c.CircleAdapter != nil {
			pajService.SetCircleTransfer(c.CircleAdapter)
		}
		if c.ChainRailsClient != nil {
			pajService.SetChainRailsAdapter(c.ChainRailsClient)
		}
		if c.GameplayHooks != nil {
			pajService.SetGameplayHooks(c.GameplayHooks)
		}
		if c.LimitsService != nil {
			pajService.SetLimitsChecker(&PajLimitsAdapter{limitsService: c.LimitsService})
			pajService.SetDepositLimits(c.LimitsService)
		}
		c.PajHandlers = fundinghandlers.NewPajHandlers(pajService, c.ZapLog)
		c.ZapLog.Info("Paj Cash NGN ramp initialized")
	} else {
		c.ZapLog.Warn("Paj API key is empty, skipping initialization")
	}

	// --- RampHub (best-rate on/off ramp; primary, Paj fallback) ---
	// Initialized independently of Paj. pajService is passed as a fallback and
	// may be nil when Paj is unconfigured; the ramp service handles a nil fallback.
	if c.Config.RampHub.APIKey != "" && c.Config.RampHub.WebhookSecret != "" {
		ramphubClient, err := ramphubadapter.NewClient(ramphubadapter.Config{
			APIKey:        c.Config.RampHub.APIKey,
			BaseURL:       c.Config.RampHub.BaseURL,
			WebhookSecret: c.Config.RampHub.WebhookSecret,
			Sandbox:       c.Config.RampHub.Sandbox,
		}, c.ZapLog)
		if err != nil {
			c.ZapLog.Fatal("failed to initialize RampHub client", zap.Error(err))
		}
		rampService := rampsvc.NewService(sqlxDB, ramphubClient, pajService, c.RedisClient, c.ZapLog)
		rampService.SetDeveloperFeePercent(c.Config.RampHub.DeveloperFeePercent)
		rampService.SetLedger(&WithdrawalLedgerAdapter{ledgerService: c.LedgerService})
		rampService.SetDepositLedger(&PajDepositLedgerAdapter{ledgerService: c.LedgerService})
		if c.AllocationService != nil {
			rampService.SetAllocationService(c.AllocationService)
		}
		rampService.SetDepositRepository(c.DepositRepo)
		if c.NotificationService != nil {
			rampService.SetNotificationService(c.NotificationService)
		}
		if c.WalletService != nil {
			rampService.SetWalletProvider(c.WalletService)
		}
		if c.CircleAdapter != nil {
			rampService.SetCircleTransfer(c.CircleAdapter)
		}
		if c.ChainRailsClient != nil {
			rampService.SetChainRailsAdapter(c.ChainRailsClient)
		}
		if c.GameplayHooks != nil {
			rampService.SetGameplayHooks(c.GameplayHooks)
		}
		if c.LimitsService != nil {
			rampService.SetLimitsChecker(&PajLimitsAdapter{limitsService: c.LimitsService})
			rampService.SetDepositLimits(c.LimitsService)
		}
		c.RampHandlers = fundinghandlers.NewRampHandlers(rampService, c.ZapLog)
		c.ZapLog.Info("RampHub on/off ramp initialized (primary, Paj fallback)")

		// Wire live FX rate from RampHub instead of the empty DB-backed repo.
		// This ensures Miriam always quotes the current interbank rate.
		getQuote := func(ctx context.Context, side string, fiatAmount, tokenAmount float64, currency string) (float64, error) {
			q, err := rampService.GetBestQuote(ctx, side, fiatAmount, tokenAmount, currency)
			if err != nil {
				return 0, err
			}
			return q.Rate, nil
		}
		if c.AIOrchestrator != nil {
			c.AIOrchestrator.SetCurrencyRateProvider(aiservice.NewRampHubRateProvider(getQuote))
		}
	} else if c.Config.RampHub.APIKey != "" {
		c.ZapLog.Fatal("SECURITY: RampHub webhook_secret is required when RampHub API key is configured — refusing to start with unauthenticated webhooks")
	} else {
		c.ZapLog.Warn("RampHub API key is empty, skipping initialization")
	}

	// --- Graph (useoval.com) NGN named virtual accounts ---
	// Provides Nigerian Naira bank accounts. Each inbound NGN deposit is
	// auto-converted to USDC and runs the standard 70/30 spend/stash split.
	if c.Config.Graph.Enabled && c.Config.Graph.APIKey != "" {
		if c.Config.Graph.WebhookSecret == "" {
			c.ZapLog.Fatal("SECURITY: Graph webhook_secret is required when Graph is enabled — refusing to start with unauthenticated webhooks")
		}
		graphClient, err := graphadapter.NewClient(graphadapter.Config{
			APIKey:        c.Config.Graph.APIKey,
			BaseURL:       c.Config.Graph.BaseURL,
			WebhookSecret: c.Config.Graph.WebhookSecret,
		}, c.ZapLog)
		if err != nil {
			c.ZapLog.Fatal("failed to initialize Graph client", zap.Error(err))
		}

		graphVARepo := repositories.NewVirtualAccountRepository(sqlxDB)
		graphLedger := integration.NewLedgerIntegration(c.LedgerService, c.BalanceRepo, c.Logger, false, false)
		graphLedgerAdapter := &LedgerIntegrationAdapter{integration: graphLedger}

		graphVAService := funding.NewGraphVirtualAccountService(
			graphClient,
			graphVARepo,
			c.DepositRepo,
			c.UserRepo,
			c.AllocationService,
			graphLedgerAdapter,
			c.Config.Graph.DeveloperFeePercent,
			c.Logger,
		)
		if c.ComplianceService != nil {
			graphVAService.SetComplianceScreener(c.ComplianceService)
		}
		if c.NotificationService != nil {
			graphVAService.SetNotificationService(&FundingNotificationAdapter{svc: c.NotificationService})
		}
		if c.GameplayHooks != nil {
			graphVAService.SetGameplayHooks(c.GameplayHooks)
		}
		if c.ExchangeRateRepo != nil {
			graphVAService.SetCurrencyRates(c.ExchangeRateRepo)
		}

		c.GraphVirtualAccountService = graphVAService
		c.NGNHandlers = fundinghandlers.NewNGNHandlers(graphVAService, c.Logger)
		c.GraphWebhookHandler = webhooks.NewGraphWebhookHandler(
			graphVAService,
			c.Config.Graph.WebhookSecret,
			c.ZapLog,
		)
		c.ZapLog.Info("Graph NGN virtual accounts initialized")
	} else {
		c.ZapLog.Warn("Graph disabled or API key empty, skipping NGN virtual accounts")
	}

	// --- Airbills (Nigerian bill payments: airtime, data, electricity, cable,
	// betting, transport). Settlement mirrors the RampHub off-ramp path. ---
	if c.Config.Airbills.SecretKey != "" && c.Config.Airbills.WebhookSecret != "" {
		airbillsClient, err := airbills.NewClient(airbills.Config{
			SecretKey:     c.Config.Airbills.SecretKey,
			BaseURL:       c.Config.Airbills.BaseURL,
			CallbackURL:   c.Config.Airbills.CallbackURL,
			WebhookSecret: c.Config.Airbills.WebhookSecret,
		}, c.ZapLog)
		if err != nil {
			c.ZapLog.Fatal("failed to initialize Airbills client", zap.Error(err))
		}
		billPayService := billpay.NewService(sqlxDB, airbillsClient, billpay.Config{
			DeveloperFeePercent: c.Config.Airbills.DeveloperFeePercent,
			DefaultToken:        c.Config.Airbills.DefaultToken,
			MaxAmountNGN:        c.Config.Airbills.MaxAmountNGN,
		}, c.ZapLog)
		if c.LedgerService != nil {
			billPayService.SetLedger(&WithdrawalLedgerAdapter{ledgerService: c.LedgerService})
		}
		if c.CircleAdapter != nil {
			billPayService.SetCircleTransfer(c.CircleAdapter)
		}
		if c.ChainRailsClient != nil {
			billPayService.SetChainRails(c.ChainRailsClient)
		}
		if c.ExchangeRateRepo != nil {
			billPayService.SetCurrencyRates(c.ExchangeRateRepo)
		}
		if c.NotificationService != nil {
			billPayService.SetNotifier(&automationNotificationAdapter{svc: c.NotificationService, logger: c.ZapLog})
		}
		c.BillPayService = billPayService
		c.BillPayHandlers = fundinghandlers.NewBillPayHandlers(billPayService, c.ZapLog)
		if c.AutomationService != nil {
			c.AutomationService.SetUtilityBillPayer(billPayService)
		}
		if c.AgentDeps != nil {
			c.AgentDeps.Bills = buildBillsProvider(c)
		}
		c.ZapLog.Info("Airbills bill payments initialized")
	} else if c.Config.Airbills.SecretKey != "" {
		c.ZapLog.Fatal("SECURITY: Airbills webhook_secret is required when Airbills secret key is configured — refusing to start with unauthenticated callbacks")
	} else {
		c.ZapLog.Warn("Airbills secret key is empty, skipping bill payments initialization")
	}

	// --- BRIJ Travel (flight bookings via travel.brij.fi). Settlement is per
	// call via x402 micropayments from Rail's funding wallet; the user is
	// charged through the ledger with a spend-balance hold. Messenger is wired
	// later in initializePlatformMessaging once the bridge dispatcher exists.
	if c.Config.Brij.FundingPrivateKey != "" {
		brijClient, err := brij.NewClient(brij.Config{
			BaseURL:           c.Config.Brij.BaseURL,
			SolanaRPC:         c.Config.Brij.SolanaRPC,
			FundingPrivateKey: c.Config.Brij.FundingPrivateKey,
			HTTPTimeout:       time.Duration(c.Config.Brij.HTTPTimeout) * time.Second,
			MaxRetries:        c.Config.Brij.MaxRetries,
		}, c.ZapLog)
		if err != nil {
			c.ZapLog.Fatal("failed to initialize BRIJ client", zap.Error(err))
		}
		travelService := travel.NewService(sqlxDB, brijClient, travel.Config{
			DeveloperFeePercent: c.Config.Brij.DeveloperFeePercent,
			MaxEscrowUSD:        c.Config.Brij.MaxEscrowUSD,
		}, c.ZapLog)
		if c.LedgerService != nil {
			travelService.SetLedger(&WithdrawalLedgerAdapter{ledgerService: c.LedgerService})
		}
		c.TravelService = travelService
		c.ZapLog.Info("BRIJ travel initialized")
	} else {
		c.ZapLog.Warn("BRIJ funding private key is empty, skipping travel initialization")
	}

	// Initialize unified activity feed service
	activityService := activitysvc.NewService(sqlxDB, c.ZapLog)
	c.ActivityHandlers = activityhandlers.NewHandlers(activityService, c.ZapLog)
	c.ZapLog.Info("Activity feed service initialized")
}

// PajLimitsAdapter adapts limits.Service to pajfunding.WithdrawalLimitsChecker.
type PajLimitsAdapter struct {
	limitsService *limits.Service
}

func (a *PajLimitsAdapter) ValidateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal) error {
	result, err := a.limitsService.ValidateWithdrawal(ctx, userID, amount)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%s", result.Reason)
	}
	return nil
}

func (a *PajLimitsAdapter) ValidateWithdrawalWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	result, err := a.limitsService.ValidateWithdrawalWithCurrency(ctx, userID, amount, currency)
	if err != nil {
		return err
	}
	if !result.Allowed {
		return fmt.Errorf("%s", result.Reason)
	}
	return nil
}

func (a *PajLimitsAdapter) RecordWithdrawalWithCurrency(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, currency string) error {
	return a.limitsService.RecordWithdrawalWithCurrency(ctx, userID, amount, currency)
}

// PajRateProvider implements limits.ExchangeRateProvider using the cached PAJ rate from Redis.
type PajRateProvider struct {
	redis cache.RedisClient
}

func NewPajRateProvider(redis cache.RedisClient) *PajRateProvider {
	return &PajRateProvider{redis: redis}
}

func (p *PajRateProvider) GetNGNRate(ctx context.Context) (decimal.Decimal, error) {
	var cached struct {
		OffRampRate struct {
			Rate float64 `json:"rate"`
		} `json:"offRampRate"`
	}
	if err := p.redis.Get(ctx, "paj:rates", &cached); err != nil {
		return decimal.Zero, err
	}
	if cached.OffRampRate.Rate <= 0 {
		return decimal.Zero, fmt.Errorf("cached rate is zero")
	}
	return decimal.NewFromFloat(cached.OffRampRate.Rate), nil
}

// PajDepositLedgerAdapter credits USDC balance for PAJ onramp deposits using the
// correct double-entry direction (Debit = increase user balance).
type PajDepositLedgerAdapter struct {
	ledgerService *ledger.Service
}

func (a *PajDepositLedgerAdapter) CreditUSDCBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, idempotencyKey string, metadata map[string]interface{}) error {
	userAccount, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeUSDCBalance)
	if err != nil {
		return fmt.Errorf("get user USDC account: %w", err)
	}
	systemAccount, err := a.ledgerService.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		return fmt.Errorf("get system buffer account: %w", err)
	}
	desc := "PAJ onramp USDC deposit"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeDeposit,
		IdempotencyKey:  idempotencyKey,
		Description:     &desc,
		Metadata:        metadata,
		Entries: []entities.CreateEntryRequest{
			{AccountID: userAccount.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USDC", Description: &desc},
			{AccountID: systemAccount.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USDC", Description: &desc},
		},
	}
	_, err = a.ledgerService.CreateTransaction(ctx, req)
	return err
}

// InstantFundingAlpacaAdapterImpl adapts alpaca.Service to funding.InstantFundingAlpacaAdapter
type InstantFundingAlpacaAdapterImpl struct {
	service *alpaca.Service
}

func (a *InstantFundingAlpacaAdapterImpl) CreateJournal(ctx context.Context, req *entities.AlpacaJournalRequest) (*entities.AlpacaJournalResponse, error) {
	return a.service.CreateJournal(ctx, req)
}

// GetInvestmentRulesRepo returns the investment rules repository.
func (c *Container) GetInvestmentRulesRepo() *repositories.InvestmentRulesRepository {
	return c.InvestmentRulesRepo
}

// rebalancingStrategyAdapter implements rebalancing_worker.StrategyProvider.
// It returns the target allocations from the user's first active RebalancingConfig.
type rebalancingStrategyAdapter struct {
	configRepo *repositories.RebalancingConfigRepository
}

func (a *rebalancingStrategyAdapter) GetTargetAllocations(ctx context.Context, userID uuid.UUID) (map[string]decimal.Decimal, error) {
	configs, err := a.configRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, cfg := range configs {
		if cfg.Status == entities.ScheduleStatusActive && len(cfg.TargetAllocations) > 0 {
			return cfg.TargetAllocations, nil
		}
	}
	return nil, fmt.Errorf("no active rebalancing config for user %s", userID)
}

// rebalancingOrderAdapter adapts orderPlacerAdapter to rebalancing_worker.OrderPlacer.
type rebalancingOrderAdapter struct {
	inner *orderPlacerAdapter
}

func (a *rebalancingOrderAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal) (*entities.AlpacaOrderResponse, error) {
	order, err := a.inner.PlaceMarketOrder(ctx, userID, symbol, amount)
	if err != nil {
		return nil, err
	}
	if order.AlpacaOrderID == nil {
		return nil, fmt.Errorf("order placed but no Alpaca order ID returned")
	}
	return &entities.AlpacaOrderResponse{ID: *order.AlpacaOrderID}, nil
}

// GetRebalancingWorkerDeps returns the dependencies needed to start the rebalancing worker.
func (c *Container) GetRebalancingWorkerDeps() (
	rulesRepo *repositories.InvestmentRulesRepository,
	positionRepo *repositories.InvestmentPositionRepository,
	strategyProvider *rebalancingStrategyAdapter,
	orderPlacer *rebalancingOrderAdapter,
) {
	rulesRepo = c.InvestmentRulesRepo
	positionRepo = c.InvestmentPositionRepo
	strategyProvider = &rebalancingStrategyAdapter{configRepo: c.RebalancingConfigRepo}
	orderPlacer = &rebalancingOrderAdapter{inner: &orderPlacerAdapter{
		investingService: c.InvestingService,
		accountService:   c.AlpacaAccountService,
		alpacaClient:     c.AlpacaClient,
		orderRepo:        c.InvestmentOrderRepo,
		logger:           c.ZapLog,
	}}
	return
}

// SubscriptionBridgeTransferAdapter transfers subscription fees from user Bridge wallet to company wallet.
type SubscriptionBridgeTransferAdapter struct {
	bridgeClient      *bridge.Client
	walletRepo        *repositories.WalletRepository
	userRepo          *repositories.UserRepository
	companyWalletAddr string
	logger            *zap.Logger
}

func (a *SubscriptionBridgeTransferAdapter) TransferToCompanyWallet(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	if a.companyWalletAddr == "" || a.bridgeClient == nil {
		return fmt.Errorf("bridge transfer not configured")
	}

	wallet, err := a.walletRepo.GetByUserAndChain(ctx, userID, entities.WalletChainSolana)
	if err != nil {
		return fmt.Errorf("failed to get user wallet: %w", err)
	}
	if wallet == nil || wallet.BridgeWalletID == "" {
		return fmt.Errorf("user has no Bridge Solana wallet")
	}

	user, err := a.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user profile: %w", err)
	}
	if user == nil || user.BridgeCustomerID == nil || *user.BridgeCustomerID == "" {
		return fmt.Errorf("user has no Bridge customer ID")
	}

	_, err = a.bridgeClient.CreateTransfer(ctx, &bridge.CreateTransferRequest{
		ClientReferenceID: reference,
		OnBehalfOf:        *user.BridgeCustomerID,
		Amount:            amount.StringFixed(2),
		Source: bridge.TransferSource{
			PaymentRail:    bridge.PaymentRail("bridge_wallet"),
			Currency:       bridge.CurrencyUSDC,
			BridgeWalletID: wallet.BridgeWalletID,
		},
		Destination: bridge.TransferDestination{
			PaymentRail: bridge.PaymentRailSolana,
			Currency:    bridge.CurrencyUSDC,
			ToAddress:   a.companyWalletAddr,
		},
	})
	return err
}

// automationBalanceAdapter adapts LedgerService for automation balance checks.
type automationBalanceAdapter struct {
	ledger *ledger.Service
}

func (a *automationBalanceAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, fmt.Errorf("ledger not available")
	}
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

func (a *automationBalanceAdapter) GetStashBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, fmt.Errorf("ledger not available")
	}
	acct, err := a.ledger.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return acct.Balance, nil
}

// automationTransferAdapter adapts LedgerService for automation transfers.
type automationTransferAdapter struct {
	ledger *ledger.Service
}

func (a *automationTransferAdapter) TransferBetweenStashes(ctx context.Context, userID uuid.UUID, from, to string, amount decimal.Decimal) error {
	if a.ledger == nil {
		return fmt.Errorf("ledger not available")
	}
	if from == "spend" && to == "stash" {
		return a.ledger.TransferSpendingToStash(ctx, userID, amount, uuid.New().String())
	}
	return a.ledger.TransferStashToSpending(ctx, userID, amount, uuid.New().String())
}

// automationProviderAdapter adapts automation.Service to the AI orchestrator's AutomationProvider interface.
type automationProviderAdapter struct {
	svc *automation.Service
}

func (a *automationProviderAdapter) Create(ctx context.Context, userID uuid.UUID, req *aiservice.AutomationRequest) (*entities.MiriamAutomation, error) {
	return a.svc.Create(ctx, userID, &automation.CreateAutomationRequest{
		Name:              req.Name,
		Description:       req.Description,
		TriggerType:       req.TriggerType,
		TriggerConfig:     req.TriggerConfig,
		ActionType:        req.ActionType,
		ActionConfig:      req.ActionConfig,
		MaxTriggersPerDay: req.MaxTriggersPerDay,
		CooldownMinutes:   req.CooldownMinutes,
		SavingsGoalID:     req.SavingsGoalID,
		ObligationID:      req.ObligationID,
	})
}

func (a *automationProviderAdapter) List(ctx context.Context, userID uuid.UUID) ([]entities.MiriamAutomation, error) {
	return a.svc.List(ctx, userID)
}
