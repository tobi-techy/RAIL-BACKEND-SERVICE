package di

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	premiumhandlers "github.com/rail-service/rail_service/internal/api/handlers/premium"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	alpacaservice "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	marketservice "github.com/rail-service/rail_service/internal/domain/services/market"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/publictrades"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

func (c *Container) initializeAlpacaInvestmentServices(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.AlpacaAccountRepo = repositories.NewAlpacaAccountRepository(sqlxDB)
	c.InvestmentOrderRepo = repositories.NewInvestmentOrderRepository(sqlxDB)
	c.InvestmentPositionRepo = repositories.NewInvestmentPositionRepository(sqlxDB)
	c.AlpacaEventRepo = repositories.NewAlpacaEventRepository(sqlxDB)
	c.AlpacaInstantFundingRepo = repositories.NewAlpacaInstantFundingRepository(sqlxDB)

	// User profile adapter for account service
	userProfileAdapter := repositories.NewUserProfileAdapter(c.UserRepo)

	// Initialize Account Service
	c.AlpacaAccountService = alpacaservice.NewAccountService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		userProfileAdapter,
		c.ZapLog,
	)

	// Initialize Funding Bridge
	c.AlpacaFundingBridge = alpacaservice.NewFundingBridge(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.AlpacaInstantFundingRepo,
		c.BalanceRepo,
		c.Config.Alpaca.FirmAccountNo,
		c.ZapLog,
	)

	// Initialize Event Processor
	c.AlpacaEventProcessor = alpacaservice.NewEventProcessor(
		c.AlpacaAccountRepo,
		c.InvestmentOrderRepo,
		c.InvestmentPositionRepo,
		c.AlpacaEventRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	// Initialize Portfolio Sync Service
	c.AlpacaPortfolioSync = alpacaservice.NewPortfolioSyncService(
		c.AlpacaClient,
		c.AlpacaAccountRepo,
		c.InvestmentPositionRepo,
		c.BalanceRepo,
		c.ZapLog,
	)

	c.ZapLog.Info("Alpaca investment services initialized")
	return nil
}

func (c *Container) initializeAdvancedFeatures(sqlxDB *sqlx.DB) error {
	// Initialize repositories
	c.PortfolioSnapshotRepo = repositories.NewPortfolioSnapshotRepository(sqlxDB)
	c.ScheduledInvestmentRepo = repositories.NewScheduledInvestmentRepository(sqlxDB)
	c.RebalancingConfigRepo = repositories.NewRebalancingConfigRepository(sqlxDB)
	c.MarketAlertRepo = repositories.NewMarketAlertRepository(sqlxDB)

	// Initialize Portfolio Analytics Service
	c.PortfolioAnalyticsService = analyticsservice.NewPortfolioAnalyticsService(
		c.PortfolioSnapshotRepo,
		c.InvestmentPositionRepo,
		c.AlpacaAccountRepo,
		c.ZapLog,
	)

	// Initialize Market Data Service
	c.MarketDataService = marketservice.NewMarketDataService(
		c.AlpacaClient,
		c.MarketAlertRepo,
		&marketNotificationAdapter{svc: c.NotificationService},
		c.ZapLog,
		c.Config.Alpaca.TaxonomyFile,
	)

	// Initialize Order Placer adapter for scheduled investments
	orderPlacer := &orderPlacerAdapter{
		investingService: c.InvestingService,
		accountService:   c.AlpacaAccountService,
		alpacaClient:     c.AlpacaClient,
		orderRepo:        c.InvestmentOrderRepo,
		logger:           c.ZapLog,
	}

	// Initialize Scheduled Investment Service
	c.ScheduledInvestmentService = investing.NewScheduledInvestmentService(
		c.ScheduledInvestmentRepo,
		orderPlacer,
		c.BrokerageAdapter, // BasketOrderPlacer
		c.ZapLog,
	)

	// Initialize Rebalancing Service
	c.RebalancingService = investing.NewRebalancingService(
		c.RebalancingConfigRepo,
		c.InvestmentPositionRepo,
		c.MarketDataService,
		orderPlacer,
		c.ZapLog,
	)

	// Initialize Round-up Service
	c.RoundupRepo = repositories.NewRoundupRepository(sqlxDB)
	c.RoundupService = roundup.NewService(
		c.RoundupRepo,
		c.LedgerService,
		orderPlacer,
		nil, // ContributionRecorder - can be added later
		c.ZapLog,
		sqlxDB,
	)

	// Initialize Copy Trading Service
	c.CopyTradingRepo = repositories.NewCopyTradingRepository(sqlxDB)
	c.CopyTradingService = copytrading.NewService(
		c.CopyTradingRepo,
		&copyTradingBalanceAdapter{ledgerService: c.LedgerService, userID: uuid.Nil},
		&copyTradingTradingAdapter{alpacaClient: c.AlpacaClient, accountRepo: c.AlpacaAccountRepo},
		c.ZapLog,
	)
	// Conductor order fills become copy-trading signals; the copy trading
	// worker then replicates them into drafter accounts.
	if c.AlpacaEventProcessor != nil {
		c.AlpacaEventProcessor.SetSignalGenerator(c.CopyTradingService)
	}
	// Public-figure copy trading: FMP congressional disclosure feeds power the
	// same signal pipeline. Without FMP_API_KEY the tools report the data
	// source as unavailable instead of failing silently.
	c.PublicTradesClient = publictrades.NewClient(publictrades.Config{APIKey: os.Getenv("FMP_API_KEY")}, c.ZapLog)
	if !c.PublicTradesClient.Configured() {
		c.ZapLog.Warn("FMP_API_KEY not set — public-figure copy trading data unavailable")
	}
	c.CopyTradingService.SetPublicTradesSource(&publicTradesSourceAdapter{client: c.PublicTradesClient})

	// Initialize Card Service
	c.CardRepo = repositories.NewCardRepository(sqlxDB)
	c.CardService = card.NewService(
		c.CardRepo,
		c.BridgeAdapter,
		&cardUserProfileAdapter{userRepo: c.UserRepo},
		&cardWalletAdapter{walletService: c.WalletService},
		&cardBalanceAdapter{ledgerService: c.LedgerService},
		c.ZapLog,
	)
	// Wire ledger service to card service for transaction ledger entries
	c.CardService.SetLedgerService(c.LedgerService)
	if c.NotificationService != nil {
		c.CardService.SetNotificationService(c.NotificationService)
	}
	if c.AutomationService != nil {
		c.AutomationService.SetCardController(&automationCardControllerAdapter{card: c.CardService})
	}
	if c.MoneyGuardService != nil && c.CardService != nil {
		c.CardService.SetMoneyGuard(&cardMoneyGuardAdapter{service: c.MoneyGuardService})
	}
	if c.SpendingCommitmentService != nil && c.CardService != nil {
		c.CardService.SetSpendingCommitment(c.SpendingCommitmentService)
	}

	// Rewire Bridge webhook service now that card service is available.
	if c.BridgeWebhookHandler != nil && c.BridgeVirtualAccountService != nil {
		bridgeWebhookService := webhooks.NewBridgeWebhookService(
			&BridgeVirtualAccountWebhookAdapter{service: c.BridgeVirtualAccountService},
			c.BridgeCustomerStatusProcessor, // preserve KYC processor — do NOT pass nil
			&BridgeCardWebhookAdapter{service: c.CardService},
			c.WithdrawalService,
			&bridgeWebhookNotifierAdapter{svc: c.NotificationService},
			c.UserRepo,
			c.ZapLog,
			c.DB,
		)
		c.BridgeWebhookHandler.SetService(bridgeWebhookService)
	}

	c.ZapLog.Info("Advanced features initialized")
	return nil
}

// marketNotificationAdapter adapts NotificationService for market alerts
type marketNotificationAdapter struct {
	svc *services.NotificationService
}

func (a *marketNotificationAdapter) SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.SendGenericNotification(ctx, userID, title, message)
}

type automationCardControllerAdapter struct {
	card *card.Service
}

func (a *automationCardControllerAdapter) FreezeCard(ctx context.Context, userID, cardID uuid.UUID) error {
	_, err := a.card.FreezeCard(ctx, userID, cardID)
	return err
}

func (a *automationCardControllerAdapter) UnfreezeCard(ctx context.Context, userID, cardID uuid.UUID) error {
	_, err := a.card.UnfreezeCard(ctx, userID, cardID)
	return err
}

func (a *automationCardControllerAdapter) GetCardsByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	cards, err := a.card.GetUserCards(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	return ids, nil
}

type cardMoneyGuardAdapter struct {
	service *moneyguardservice.Service
}

func (a *cardMoneyGuardAdapter) EvaluateCardAuthorization(ctx context.Context, userID uuid.UUID, input card.MoneyGuardTransactionInput) (*entities.MoneyGuardDecision, error) {
	return a.service.EvaluateCardAuthorization(ctx, userID, moneyguardservice.TransactionInput{
		Amount: input.Amount, Currency: input.Currency, Merchant: input.Merchant,
		Category: input.Category, Reference: input.Reference,
	})
}

func (a *cardMoneyGuardAdapter) EvaluateCardTransaction(ctx context.Context, userID uuid.UUID, input card.MoneyGuardTransactionInput) (*entities.MoneyGuardDecision, error) {
	return a.service.EvaluateCardTransaction(ctx, userID, moneyguardservice.TransactionInput{
		Amount: input.Amount, Currency: input.Currency, Merchant: input.Merchant,
		Category: input.Category, Reference: input.Reference,
	})
}

// walletWebhookAdapter adapts wallet.Service to WalletWebhookService interface
type walletWebhookAdapter struct {
	walletService *wallet.Service
}

func (a *walletWebhookAdapter) SyncWalletStatus(ctx context.Context, bridgeWalletID string, status string) error {
	if a.walletService == nil {
		return fmt.Errorf("wallet service not available")
	}
	return a.walletService.SyncWalletStatus(ctx, bridgeWalletID, status)
}

// bridgeWebhookNotifierAdapter adapts NotificationService to BridgeWebhookNotifier
type bridgeWebhookNotifierAdapter struct {
	svc *services.NotificationService
}

func (a *bridgeWebhookNotifierAdapter) NotifyDepositReceived(ctx *gin.Context, userID uuid.UUID, amount, currency string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.NotifyDepositConfirmed(ctx.Request.Context(), userID, amount+" "+currency, "", "")
}

func (a *bridgeWebhookNotifierAdapter) NotifyKYCStatusChanged(ctx *gin.Context, userID uuid.UUID, status string) error {
	if a.svc == nil {
		return nil
	}
	switch status {
	case "active":
		return a.svc.NotifyKYCApproved(ctx.Request.Context(), userID)
	case "rejected":
		return a.svc.NotifyKYCRejected(ctx.Request.Context(), userID)
	}
	return nil
}

// copyTradingBalanceAdapter adapts LedgerService for copy trading balance operations
type copyTradingBalanceAdapter struct {
	ledgerService *ledger.Service
	userID        uuid.UUID
}

func (a *copyTradingBalanceAdapter) GetAvailableBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	balances, err := a.ledgerService.GetUserBalances(ctx, userID)
	if err != nil {
		return decimal.Zero, err
	}
	return balances.USDCBalance, nil
}

func (a *copyTradingBalanceAdapter) DeductBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Reserve funds for copy trading allocation
	return a.ledgerService.ReserveForInvestment(ctx, userID, amount)
}

func (a *copyTradingBalanceAdapter) AddBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Release reserved funds back to user
	return a.ledgerService.ReleaseReservation(ctx, userID, amount)
}

// copyTradingTradingAdapter adapts Alpaca client for copy trading order execution
type copyTradingTradingAdapter struct {
	alpacaClient *alpaca.Client
	accountRepo  *repositories.AlpacaAccountRepository
}

func (a *copyTradingTradingAdapter) PlaceOrder(ctx context.Context, userID uuid.UUID, symbol string, side string, quantity decimal.Decimal) (string, decimal.Decimal, error) {
	if a.alpacaClient == nil || a.accountRepo == nil {
		return "", decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	// Get user's Alpaca account
	account, err := a.accountRepo.GetByUserID(ctx, userID)
	if err != nil || account == nil {
		return "", decimal.Zero, fmt.Errorf("user has no brokerage account")
	}

	// Place order via Alpaca
	orderSide := entities.AlpacaOrderSideBuy
	if side == "sell" {
		orderSide = entities.AlpacaOrderSideSell
	}

	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Qty:         &quantity,
		Side:        orderSide,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	resp, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return "", decimal.Zero, fmt.Errorf("failed to place order: %w", err)
	}

	// Get executed price (for market orders, use filled_avg_price or current price)
	executedPrice := decimal.Zero
	if resp.FilledAvgPrice != nil && !resp.FilledAvgPrice.IsZero() {
		executedPrice = *resp.FilledAvgPrice
	}

	return resp.ID, executedPrice, nil
}

func (a *copyTradingTradingAdapter) GetCurrentPrice(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if a.alpacaClient == nil {
		return decimal.Zero, fmt.Errorf("trading adapter not configured")
	}

	quote, err := a.alpacaClient.GetLatestQuote(ctx, symbol)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to get quote: %w", err)
	}

	return quote.Ask, nil
}

// autoInvestOrderPlacerAdapter implements autoinvest.OrderPlacer interface
type autoInvestOrderPlacerAdapter struct {
	accountService *alpacaservice.AccountService
	alpacaClient   *alpaca.Client
	orderRepo      *repositories.InvestmentOrderRepository
	logger         *zap.Logger
}

func (a *autoInvestOrderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, amount decimal.Decimal, clientOrderID string) (*entities.AlpacaOrderResponse, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Guard: account must be tradeable
	if account.AccountBlocked {
		return nil, fmt.Errorf("alpaca account is blocked for user %s", userID)
	}
	if account.TradingBlocked {
		return nil, fmt.Errorf("trading is blocked on alpaca account for user %s", userID)
	}

	// Guard: only place orders during market hours (Mon–Fri 09:30–16:00 ET)
	// DAY orders are rejected by Alpaca outside these hours; queue for next open instead.
	if !isMarketOpen() {
		return nil, fmt.Errorf("market is closed: order for %s queued for next market open", symbol)
	}

	// Create market order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:        symbol,
		Notional:      &amount,
		Side:          entities.AlpacaOrderSideBuy,
		Type:          entities.AlpacaOrderTypeMarket,
		TimeInForce:   entities.AlpacaTimeInForceDay,
		ClientOrderID: clientOrderID,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database for tracking
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            entities.AlpacaOrderSideBuy,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &amount,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store auto-invest order", zap.Error(err))
	}

	return alpacaOrder, nil
}

// isMarketOpen returns true when the US equity market is currently open (Mon–Fri 09:30–16:00 ET).
func isMarketOpen() bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		// If timezone data is unavailable, fail open so orders aren't silently dropped.
		return true
	}
	et := time.Now().In(loc)
	wd := et.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	open := time.Date(et.Year(), et.Month(), et.Day(), 9, 30, 0, 0, loc)
	close := time.Date(et.Year(), et.Month(), et.Day(), 16, 0, 0, 0, loc)
	return et.After(open) && et.Before(close)
}

// strategyUserProfileAdapter adapts UserRepository for strategy engine
type strategyUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *strategyUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// orderPlacerAdapter implements OrderPlacer interface for scheduled investments
type orderPlacerAdapter struct {
	investingService *investing.Service
	accountService   *alpacaservice.AccountService
	alpacaClient     *alpaca.Client
	orderRepo        *repositories.InvestmentOrderRepository
	logger           *zap.Logger
}

func (a *orderPlacerAdapter) PlaceMarketOrder(ctx context.Context, userID uuid.UUID, symbol string, notional decimal.Decimal) (*entities.InvestmentOrder, error) {
	// Get user's Alpaca account
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	// Determine side based on notional sign
	side := entities.AlpacaOrderSideBuy
	if notional.LessThan(decimal.Zero) {
		side = entities.AlpacaOrderSideSell
		notional = notional.Abs()
	}

	// Create order via Alpaca
	orderReq := &entities.AlpacaCreateOrderRequest{
		Symbol:      symbol,
		Notional:    &notional,
		Side:        side,
		Type:        entities.AlpacaOrderTypeMarket,
		TimeInForce: entities.AlpacaTimeInForceDay,
	}

	alpacaOrder, err := a.alpacaClient.CreateOrder(ctx, account.AlpacaAccountID, orderReq)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Store order in database
	now := time.Now()
	order := &entities.InvestmentOrder{
		ID:              uuid.New(),
		UserID:          userID,
		AlpacaAccountID: &account.ID,
		AlpacaOrderID:   &alpacaOrder.ID,
		ClientOrderID:   alpacaOrder.ClientOrderID,
		Symbol:          symbol,
		Side:            side,
		OrderType:       entities.AlpacaOrderTypeMarket,
		TimeInForce:     entities.AlpacaTimeInForceDay,
		Notional:        &notional,
		Status:          alpacaOrder.Status,
		SubmittedAt:     &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := a.orderRepo.Create(ctx, order); err != nil {
		a.logger.Error("Failed to store order", zap.Error(err))
	}

	return order, nil
}

// Card service adapters

// cardUserProfileAdapter adapts UserRepository for card service
type cardUserProfileAdapter struct {
	userRepo *repositories.UserRepository
}

func (a *cardUserProfileAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if a.userRepo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	return a.userRepo.GetByID(ctx, id)
}

// cardWalletAdapter adapts WalletService for card service
type cardWalletAdapter struct {
	walletService *wallet.Service
}

func (a *cardWalletAdapter) GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error) {
	if a.walletService == nil {
		return nil, fmt.Errorf("wallet service not available")
	}
	walletChain := entities.WalletChain(strings.ToUpper(chain))
	return a.walletService.GetWalletByUserAndChain(ctx, userID, walletChain)
}

// cardBalanceAdapter adapts LedgerService for card balance operations
type cardBalanceAdapter struct {
	ledgerService *ledger.Service
}

func (a *cardBalanceAdapter) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if a.ledgerService == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	// Get spending balance account directly
	account, err := a.ledgerService.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	if err != nil {
		return decimal.Zero, err
	}
	return account.Balance, nil
}

func (a *cardBalanceAdapter) DeductSpendBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, reference string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
	// Create a debit entry for card transaction
	return a.ledgerService.RecordCardTransaction(ctx, userID, amount, reference)
}

// Getters for new services

// GetAlpacaAccountService returns the Alpaca account service
func (c *Container) GetAlpacaAccountService() *alpacaservice.AccountService {
	return c.AlpacaAccountService
}

// GetAlpacaFundingBridge returns the Alpaca funding bridge
func (c *Container) GetAlpacaFundingBridge() *alpacaservice.FundingBridge {
	return c.AlpacaFundingBridge
}

// GetAlpacaEventProcessor returns the Alpaca event processor
func (c *Container) GetAlpacaEventProcessor() *alpacaservice.EventProcessor {
	return c.AlpacaEventProcessor
}

// GetAlpacaPortfolioSync returns the Alpaca portfolio sync service
func (c *Container) GetAlpacaPortfolioSync() *alpacaservice.PortfolioSyncService {
	return c.AlpacaPortfolioSync
}

// GetPortfolioAnalyticsService returns the portfolio analytics service
func (c *Container) GetPortfolioAnalyticsService() *analyticsservice.PortfolioAnalyticsService {
	return c.PortfolioAnalyticsService
}

// GetMarketDataService returns the market data service
func (c *Container) GetMarketDataService() *marketservice.MarketDataService {
	return c.MarketDataService
}

// GetScheduledInvestmentService returns the scheduled investment service
func (c *Container) GetScheduledInvestmentService() *investing.ScheduledInvestmentService {
	return c.ScheduledInvestmentService
}

// GetRebalancingService returns the rebalancing service
func (c *Container) GetRebalancingService() *investing.RebalancingService {
	return c.RebalancingService
}

// GetInvestmentHandlers returns investment handlers
func (c *Container) GetInvestmentHandlers() *handlers.InvestmentHandlers {
	if c.AlpacaAccountService == nil {
		return nil
	}
	return handlers.NewInvestmentHandlers(
		c.AlpacaAccountService,
		c.AlpacaFundingBridge,
		c.AlpacaPortfolioSync,
		c.Logger,
	)
}

// GetAlpacaWebhookHandlers returns Alpaca webhook handlers
func (c *Container) GetAlpacaWebhookHandlers() *handlers.AlpacaWebhookHandlers {
	if c.AlpacaEventProcessor == nil {
		return nil
	}
	// Get webhook secret from config
	webhookSecret := c.Config.Alpaca.WebhookSecret
	if webhookSecret == "" {
		c.ZapLog.Warn("Alpaca webhook secret not configured")
	}
	// Determine if webhook verification should be skipped (only in development)
	skipWebhookVerification := c.Config.Environment == "development" && webhookSecret == ""
	return handlers.NewAlpacaWebhookHandlers(c.AlpacaEventProcessor, c.Logger, webhookSecret, skipWebhookVerification, c.Config.Environment)
}

// GetAnalyticsHandlers returns analytics handlers
func (c *Container) GetAnalyticsHandlers() *handlers.AnalyticsHandlers {
	if c.PortfolioAnalyticsService == nil {
		return nil
	}
	return handlers.NewAnalyticsHandlers(c.PortfolioAnalyticsService, c.Logger)
}

// GetMarketHandlers returns market data handlers
func (c *Container) GetMarketHandlers() *handlers.MarketHandlers {
	if c.MarketDataService == nil {
		return nil
	}
	return handlers.NewMarketHandlers(c.MarketDataService, c.Logger)
}

// GetScheduledInvestmentHandlers returns scheduled investment handlers
func (c *Container) GetScheduledInvestmentHandlers() *handlers.ScheduledInvestmentHandlers {
	if c.ScheduledInvestmentService == nil {
		return nil
	}
	return handlers.NewScheduledInvestmentHandlers(c.ScheduledInvestmentService, c.Logger)
}

// GetRebalancingHandlers returns rebalancing handlers
func (c *Container) GetRebalancingHandlers() *handlers.RebalancingHandlers {
	if c.RebalancingService == nil {
		return nil
	}
	return handlers.NewRebalancingHandlers(c.RebalancingService, c.Logger)
}

// GetRoundupService returns the round-up service
func (c *Container) GetRoundupService() *roundup.Service {
	return c.RoundupService
}

// GetRoundupHandlers returns round-up handlers
func (c *Container) GetRoundupHandlers() *handlers.RoundupHandlers {
	if c.RoundupService == nil {
		return nil
	}
	return handlers.NewRoundupHandlers(c.RoundupService, c.ZapLog)
}

// GetCopyTradingService returns the copy trading service
func (c *Container) GetCopyTradingService() *copytrading.Service {
	return c.CopyTradingService
}

// GetCopyTradingHandlers returns copy trading handlers
func (c *Container) GetCopyTradingHandlers() *handlers.CopyTradingHandlers {
	if c.CopyTradingService == nil {
		return nil
	}
	return handlers.NewCopyTradingHandlers(c.CopyTradingService, c.Logger)
}

// GetCardService returns the card service
func (c *Container) GetCardService() *card.Service {
	return c.CardService
}

// GetCardHandlers returns card handlers
func (c *Container) GetCardHandlers() *handlers.CardHandlers {
	if c.CardService == nil {
		return nil
	}
	return handlers.NewCardHandlers(c.CardService, c.ZapLog)
}

// GetStationHandlers returns station handlers
func (c *Container) GetStationHandlers() *handlers.StationHandlers {
	if c.StationService == nil {
		return nil
	}
	if c.RedisClient != nil {
		cached := station.NewCachedService(c.StationService, c.RedisClient)
		return handlers.NewStationHandlers(cached, c.ZapLog)
	}
	return handlers.NewStationHandlers(c.StationService, c.ZapLog)
}

// GetSpendingStashHandlers returns spending stash handlers
func (c *Container) GetSpendingStashHandlers() *handlers.SpendingStashHandlers {
	h := handlers.NewSpendingStashHandlers(
		c.AllocationService,
		c.CardService,
		c.RoundupService,
		c.ZapLog,
	)
	if c.P2PRepo != nil {
		h.SetP2PRepo(c.P2PRepo)
	}
	if c.WithdrawalRepo != nil {
		h.SetWithdrawalRepo(c.WithdrawalRepo)
	}
	return h
}

// GetInvestmentStashHandlers returns investment stash handlers
func (c *Container) GetInvestmentStashHandlers() *handlers.InvestmentStashHandlers {
	if c.AllocationService == nil || c.InvestmentPositionRepo == nil || c.InvestmentOrderRepo == nil || c.PortfolioAnalyticsService == nil {
		return nil
	}

	h := handlers.NewInvestmentStashHandlers(
		c.AllocationService,
		c.InvestmentPositionRepo,
		c.InvestmentOrderRepo,
		c.PortfolioAnalyticsService,
		c.ZapLog,
	)
	h.SetAutoInvestRepository(repositories.NewAutoInvestRepository(sqlx.NewDb(c.DB, "postgres")))
	if c.StrategyEngine != nil {
		h.SetStrategyProvider(c.StrategyEngine)
	}
	if c.AlpacaPortfolioSync != nil {
		h.SetPortfolioSyncer(c.AlpacaPortfolioSync)
	}
	return h
}

// GetPremiumHandlers returns premium feature HTTP handlers
func (c *Container) GetPremiumHandlers() *premiumhandlers.Handlers {
	if c.PremiumHandlers == nil {
		c.PremiumHandlers = premiumhandlers.NewHandlers(
			c.NairaShieldService,
			c.BlackTaxService,
			c.ReceiptSplitService,
			c.ScamIntelligenceService,
			c.TaxResidencyService,
			c.IncomeSmoothingService,
			c.FinancialTraumaService,
			c.VisaProofService,
			c.PanicButtonService,
			c.ZapLog,
		)
	}
	return c.PremiumHandlers
}

// GetCopyTradingRepository returns the copy trading repository
func (c *Container) GetCopyTradingRepository() *repositories.CopyTradingRepository {
	return c.CopyTradingRepo
}

// ListAllActiveUserIDs returns all active user IDs (for portfolio snapshot worker)
func (c *Container) ListAllActiveUserIDs(ctx context.Context) ([]uuid.UUID, error) {
	query := `SELECT id FROM users WHERE is_active = true`
	rows, err := c.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		userIDs = append(userIDs, id)
	}
	return userIDs, rows.Err()
}

// GetBridgeWebhookHandler returns the Bridge webhook handler
func (c *Container) GetBridgeWebhookHandler() *handlers.BridgeWebhookHandler {
	return c.BridgeWebhookHandler
}

// GetCircleWebhookHandler returns the Circle webhook handler.
func (c *Container) GetCircleWebhookHandler() *webhooks.CircleWebhookHandler {
	return c.CircleWebhookHandler
}

// GetBridgeVirtualAccountService returns the Bridge virtual account service
func (c *Container) GetBridgeVirtualAccountService() *funding.BridgeVirtualAccountService {
	return c.BridgeVirtualAccountService
}
