package di

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	alpacaservice "github.com/rail-service/rail_service/internal/domain/services/alpaca"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/autoinvest"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/investing"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	marketservice "github.com/rail-service/rail_service/internal/domain/services/market"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// GetAutoInvestService returns the auto-invest service
func (c *Container) GetAutoInvestService() *autoinvest.Service {
	return c.AutoInvestService
}

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

// GetCopyTradingRepository returns the copy trading repository
func (c *Container) GetCopyTradingRepository() *repositories.CopyTradingRepository {
	return c.CopyTradingRepo
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

// GetInvestmentRulesRepo returns the investment rules repository.
func (c *Container) GetInvestmentRulesRepo() *repositories.InvestmentRulesRepository {
	return c.InvestmentRulesRepo
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
	return a.ledgerService.ReserveForInvestment(ctx, userID, amount)
}

func (a *copyTradingBalanceAdapter) AddBalance(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description string) error {
	if a.ledgerService == nil {
		return fmt.Errorf("ledger service not available")
	}
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

	account, err := a.accountRepo.GetByUserID(ctx, userID)
	if err != nil || account == nil {
		return "", decimal.Zero, fmt.Errorf("user has no brokerage account")
	}

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
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	if account.AccountBlocked {
		return nil, fmt.Errorf("alpaca account is blocked for user %s", userID)
	}
	if account.TradingBlocked {
		return nil, fmt.Errorf("trading is blocked on alpaca account for user %s", userID)
	}

	if !isMarketOpen() {
		return nil, fmt.Errorf("market is closed: order for %s queued for next market open", symbol)
	}

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
	account, err := a.accountService.GetUserAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("user has no Alpaca account")
	}

	side := entities.AlpacaOrderSideBuy
	if notional.LessThan(decimal.Zero) {
		side = entities.AlpacaOrderSideSell
		notional = notional.Abs()
	}

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

// rebalancingStrategyAdapter implements rebalancing_worker.StrategyProvider.
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
