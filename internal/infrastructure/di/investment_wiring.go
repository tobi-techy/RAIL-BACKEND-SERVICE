package di

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/rail-service/rail_service/internal/api/handlers"
	premiumhandlers "github.com/rail-service/rail_service/internal/api/handlers/premium"
	"github.com/rail-service/rail_service/internal/api/handlers/webhooks"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	analyticsservice "github.com/rail-service/rail_service/internal/domain/services/analytics"
	"github.com/rail-service/rail_service/internal/domain/services/card"
	"github.com/rail-service/rail_service/internal/domain/services/copytrading"
	"github.com/rail-service/rail_service/internal/domain/services/funding"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	moneyguardservice "github.com/rail-service/rail_service/internal/domain/services/moneyguard"
	"github.com/rail-service/rail_service/internal/domain/services/roundup"
	spendingsvc "github.com/rail-service/rail_service/internal/domain/services/spending"
	"github.com/rail-service/rail_service/internal/domain/services/station"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/publictrades"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/shopspring/decimal"
)

func (c *Container) initializeAdvancedFeatures(sqlxDB *sqlx.DB) error {
	// Initialize Round-up Service. The brokerage order placer that used to
	// execute round-ups was removed with the Alpaca stack; roundup.Service
	// tolerates a nil order placer (auto-invest execution is skipped).
	c.RoundupRepo = repositories.NewRoundupRepository(sqlxDB)
	c.RoundupService = roundup.NewService(
		c.RoundupRepo,
		c.LedgerService,
		nil, // OrderPlacer — brokerage execution removed
		nil, // ContributionRecorder - can be added later
		c.ZapLog,
		sqlxDB,
	)

	// Initialize Copy Trading Service. Order execution against the removed
	// brokerage adapter is wired as nil; signal ingestion (FMP congressional
	// disclosures) still powers conductor/draft listings.
	c.CopyTradingRepo = repositories.NewCopyTradingRepository(sqlxDB)
	c.CopyTradingService = copytrading.NewService(
		c.CopyTradingRepo,
		&copyTradingBalanceAdapter{ledgerService: c.LedgerService, userID: uuid.Nil},
		nil, // TradingAdapter — brokerage execution removed
		c.ZapLog,
	)
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

// GetFinancialSnapshotHandler returns the ledger-backed financial-snapshot
// handler used by the delegated Python agent's financial intelligence engine.
// It degrades field-by-field (zeros / unset) when a provider is missing, so it
// is safe to construct whenever the container is.
func (c *Container) GetFinancialSnapshotHandler() *handlers.FinancialSnapshotHandler {
	if c.LedgerSpendingRepo == nil {
		return handlers.NewFinancialSnapshotHandler(
			nil, c.LedgerService, c.BudgetRepo, c.FinancialProfileRepo, c.Logger,
		)
	}
	return handlers.NewFinancialSnapshotHandler(
		spendingsvc.NewService(c.LedgerSpendingRepo),
		c.LedgerService,
		c.BudgetRepo,
		c.FinancialProfileRepo,
		c.Logger,
	)
}

// GetPortfolioAnalyticsService returns the portfolio analytics service.
// Removed with the Alpaca brokerage (it read Alpaca positions/cash); returns
// nil so the portfolio-snapshot worker stays idle until a Glider-backed
// position source is wired.
func (c *Container) GetPortfolioAnalyticsService() *analyticsservice.PortfolioAnalyticsService {
	return nil
}

// GetAnalyticsHandlers returns portfolio analytics handlers.
// The Alpaca-backed PortfolioAnalyticsService was removed with the brokerage
// provider; until analytics is re-sourced from the Glider/Solana sleeve this
// returns nil and the advanced-features routes stay unregistered.
func (c *Container) GetAnalyticsHandlers() *handlers.AnalyticsHandlers {
	return nil
}

// GetScheduledInvestmentHandlers returns scheduled-investment handlers.
// Unwired post-Alpaca (execution was brokered through Alpaca orders).
func (c *Container) GetScheduledInvestmentHandlers() *handlers.ScheduledInvestmentHandlers {
	return nil
}

// GetRebalancingHandlers returns rebalancing handlers.
// Unwired post-Alpaca (rebalancing traded Alpaca positions against market data).
func (c *Container) GetRebalancingHandlers() *handlers.RebalancingHandlers {
	return nil
}

// GetRoundupService returns the round-up service
func (c *Container) GetRoundupService() *roundup.Service {
	return c.RoundupService
}

// GetInvestmentStashHandlers returns the investment-stash dashboard handlers.
// Position, order and performance providers were removed with the Alpaca
// brokerage; the handler degrades those sections (explicit "unavailable")
// while allocation- and ledger-backed figures keep working.
func (c *Container) GetInvestmentStashHandlers() *handlers.InvestmentStashHandlers {
	return handlers.NewInvestmentStashHandlers(
		c.AllocationService,
		nil, // positions provider — pending Glider-backed source
		nil, // orders provider — pending Glider-backed source
		nil, // portfolio analytics provider — pending Glider-backed source
		c.ZapLog,
	)
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
