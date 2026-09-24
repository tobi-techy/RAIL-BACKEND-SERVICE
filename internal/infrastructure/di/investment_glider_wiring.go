package di

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	investmenthandlers "github.com/rail-service/rail_service/internal/api/handlers/investment"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services"
	investmentsvc "github.com/rail-service/rail_service/internal/domain/services/investment"
	"github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/domain/services/wallet"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/glider"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/investmentowner"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	investmentsync "github.com/rail-service/rail_service/internal/workers/investment_sync"
)

// initializeInvestmentGliderServices wires the Glider-backed investment engine:
// the real provider client, the portfolio-owner signer, the funding leg, the
// deterministic domain service, the HTTP handlers and the sync worker.
//
// Everything policy-relevant is derived from config here, so the AI agent can
// never influence limits, allowlists or the signer.
func (c *Container) initializeInvestmentGliderServices(sqlxDB *sqlx.DB) error {
	cfg := c.Config.InvestmentGlider
	if !cfg.Enabled {
		c.ZapLog.Info("investment (Glider) feature disabled")
		return nil
	}

	// Repositories (the read model Rail owns).
	c.InvestmentAssetRepo = repositories.NewInvestmentAssetRepository(sqlxDB)
	c.InvestmentStrategyRepo = repositories.NewInvestmentStrategyRepository(sqlxDB)
	c.InvestmentEnrollmentRepo = repositories.NewInvestmentEnrollmentRepository(sqlxDB)
	c.InvestmentHoldingRepo = repositories.NewInvestmentHoldingRepository(sqlxDB)
	c.InvestmentExecutionRepo = repositories.NewInvestmentExecutionRepository(sqlxDB)
	c.InvestmentFundingRepo = repositories.NewInvestmentFundingTransferRepository(sqlxDB)
	c.InvestmentSignatureRepo = repositories.NewInvestmentSignatureRequestRepository(sqlxDB)
	c.InvestmentConfirmationRepo = repositories.NewInvestmentConfirmationRepository(sqlxDB)
	c.InvestmentOperationRepo = repositories.NewInvestmentOperationRepository(sqlxDB)
	c.InvestmentAuditRepo = repositories.NewInvestmentAuditRepository(sqlxDB)
	c.InvestmentLimitsRepo = repositories.NewInvestmentLimitsRepository(sqlxDB)
	c.InvestmentUserProfileReader = repositories.NewInvestmentUserProfileReader(sqlxDB)

	provider, err := c.buildInvestmentProvider(cfg)
	if err != nil {
		return err
	}
	if err := c.verifyGliderCredentials(provider); err != nil {
		return err
	}
	signer, err := c.buildInvestmentOwnerSigner(cfg)
	if err != nil {
		return err
	}
	funding, err := c.buildInvestmentFundingAdapter(cfg)
	if err != nil {
		return err
	}

	c.InvestmentGliderService = investmentsvc.NewService(investmentsvc.Deps{
		Config:        investmentServiceConfig(cfg),
		Logger:        c.Logger,
		Provider:      provider,
		Signer:        signer,
		Funding:       funding,
		Assets:        c.InvestmentAssetRepo,
		Strategies:    c.InvestmentStrategyRepo,
		Enrollments:   c.InvestmentEnrollmentRepo,
		Holdings:      c.InvestmentHoldingRepo,
		Executions:    c.InvestmentExecutionRepo,
		Transfers:     c.InvestmentFundingRepo,
		Signatures:    c.InvestmentSignatureRepo,
		Confirmations: c.InvestmentConfirmationRepo,
		Operations:    c.InvestmentOperationRepo,
		Audit:         c.InvestmentAuditRepo,
		Limits:        c.InvestmentLimitsRepo,
		Users:         c.InvestmentUserProfileReader,
	})

	interval := time.Duration(cfg.SyncIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	c.InvestmentSyncWorker = investmentsync.NewWorker(
		c.InvestmentGliderService,
		c.Config.InvestmentGlider.AutoRebalance,
		interval,
		c.Config.InvestmentGlider.SyncBatchSize,
		c.ZapLog,
	)

	c.ZapLog.Info("investment (Glider) services initialized",
		zap.String("owner_signer", cfg.OwnerSigner),
		zap.Int("sync_interval_minutes", cfg.SyncIntervalMinutes))
	return nil
}

// buildInvestmentProvider builds the real Glider HTTP client. There is no
// simulated fallback: a missing API key fails closed at call time (401) and a
// missing key in a non-dev environment is rejected by config validation.
func (c *Container) buildInvestmentProvider(cfg config.InvestmentGliderConfig) (investmentsvc.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("investment: glider api key is required when investment_glider is enabled")
	}
	return glider.NewClient(glider.Config{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Timeout: time.Duration(cfg.Timeout) * time.Second,
	}, c.ZapLog), nil
}

// requiredGliderScopes are the API-key scopes the investment engine needs for
// its flows. A key missing one of these will fail at the first real user
// action, so the scope set is verified at boot instead.
var requiredGliderScopes = []string{
	"strategies:read", "strategies:write",
	"portfolios:read", "portfolios:write", "portfolios:withdraw",
	"enroll:write",
}

// verifyGliderCredentials proves the configured API key is valid and reports
// missing scopes. It NEVER aborts boot: Glider is one provider behind a
// multi-product API, and a rotated key, tier mismatch, or Glider outage must
// degrade investments (provider errors on investment calls, visible in
// health/readiness) — never brick health checks, funding, chat, and
// everything else sharing this binary. Missing scopes are logged loudly but
// do not block boot, since scope tiers are provisioned out-of-band and a
// tier upgrade should not require a redeploy.
func (c *Container) verifyGliderCredentials(provider investmentsvc.Provider) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	identity, err := provider.Whoami(ctx)
	if err != nil {
		var apiErr *glider.APIError
		if errors.As(err, &apiErr) && (apiErr.IsUnauthorized() || apiErr.IsForbidden()) {
			c.ZapLog.Error("investment: glider api key rejected at boot — investment calls will fail until the key is rotated (server keeps running)",
				zap.Error(err))
			return nil
		}
		c.ZapLog.Warn("investment: could not verify glider credentials at boot",
			zap.Error(err))
		return nil
	}

	granted := map[string]bool{}
	for _, scope := range identity.Scopes {
		granted[scope] = true
	}
	var missing []string
	for _, scope := range requiredGliderScopes {
		if !granted[scope] {
			missing = append(missing, scope)
		}
	}
	if len(missing) > 0 {
		c.ZapLog.Warn("investment: glider api key is missing required scopes; affected flows will fail at call time",
			zap.Strings("missing_scopes", missing),
			zap.String("tenant", identity.TenantName))
	}
	c.ZapLog.Info("investment: glider credentials verified",
		zap.String("tenant", identity.TenantName),
		zap.Int("scopes_granted", len(identity.Scopes)))
	return nil
}

// buildInvestmentOwnerSigner selects who signs the two-stage owner
// authorizations. See investmentowner for the trade-offs behind each choice.
func (c *Container) buildInvestmentOwnerSigner(cfg config.InvestmentGliderConfig) (investmentsvc.OwnerSigner, error) {
	chain := entities.WalletChainSolana
	switch strings.ToLower(strings.TrimSpace(cfg.OwnerSigner)) {
	case "derived":
		return investmentowner.NewDerivedSigner(cfg.OwnerKeySeed, cfg.OwnerAccountPrefix, chain)
	case "circle":
		if c.WalletService == nil {
			return nil, fmt.Errorf("investment: the circle owner signer needs the wallet service")
		}
		return investmentowner.NewCircleSigner(
			&investmentWalletLookupAdapter{walletService: c.WalletService},
			c.CircleAdapter,
			cfg.OwnerAccountPrefix,
			chain,
		)
	default:
		return nil, fmt.Errorf("investment: unknown owner signer %q", cfg.OwnerSigner)
	}
}

// buildInvestmentFundingAdapter builds the ledger + on-chain funding leg.
func (c *Container) buildInvestmentFundingAdapter(cfg config.InvestmentGliderConfig) (investmentsvc.FundingPort, error) {
	if c.LedgerService == nil {
		return nil, fmt.Errorf("investment: the funding leg needs the ledger service")
	}
	adapter := &investmentFundingAdapter{
		ledger:      c.LedgerService,
		withdrawals: c.WithdrawalService,
		wallets:     c.WalletService,
		accountPrefix: func() string {
			prefix := strings.TrimRight(strings.TrimSpace(cfg.OwnerAccountPrefix), ":")
			if prefix == "" {
				prefix = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
			}
			return prefix
		}(),
		log: c.ZapLog,
	}
	return adapter, nil
}

// investmentServiceConfig maps the YAML/env config onto the domain config. Every
// money-relevant value is converted explicitly so an unset field becomes a
// deliberate zero rather than a surprise default.
func investmentServiceConfig(cfg config.InvestmentGliderConfig) investmentsvc.Config {
	return investmentsvc.Config{
		Enabled:               cfg.Enabled,
		DefaultChain:          orDefault(cfg.DefaultChain, "solana"),
		SolanaChainIDs:        cfg.SolanaChainIDs,
		OwnerAccountPrefix:    cfg.OwnerAccountPrefix,
		SettlementSymbol:      orDefault(cfg.SettlementSymbol, "USDC"),
		DefaultSlippageBps:    cfg.DefaultSlippageBps,
		DefaultFeeBps:         decimal.NewFromFloat(cfg.FeeBps),
		HighValueThresholdUSD: decimal.NewFromFloat(cfg.HighValueThresholdUSD),
		ConfirmationTTL:       time.Duration(cfg.ConfirmationTTLMinutes) * time.Minute,
		AllowedCountries:      cfg.AllowedCountries,
		MinAllocationLegs:     cfg.MinAllocationLegs,
		MaxAllocationLegs:     cfg.MaxAllocationLegs,
		StaleMarketDataAfter:  time.Duration(cfg.StaleMarketDataMinutes) * time.Minute,
		DefaultLimits: investmentsvc.Limits{
			MaxPositionPct:    decimal.NewFromFloat(cfg.MaxPositionPct),
			MaxStrategyPct:    decimal.NewFromFloat(cfg.MaxStrategyPct),
			MaxTransactionUSD: decimal.NewFromFloat(cfg.MaxTransactionUSD),
			MaxDailyVolumeUSD: decimal.NewFromFloat(cfg.MaxDailyVolumeUSD),
			MinCashReserveUSD: decimal.NewFromFloat(cfg.MinCashReserveUSD),
			MinOrderAmountUSD: decimal.NewFromFloat(cfg.MinOrderAmountUSD),
			MaxEnrollments:    cfg.MaxEnrollments,
		},
	}
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// ---------------------------------------------------------------------------
// Adapters
// ---------------------------------------------------------------------------

// investmentWalletLookupAdapter exposes the wallet service to the owner signer
// without the signer importing a big service type.
type investmentWalletLookupAdapter struct {
	walletService *wallet.Service
}

func (a *investmentWalletLookupAdapter) GetWalletByUserAndChain(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	if a.walletService == nil {
		return nil, fmt.Errorf("wallet service not available")
	}
	return a.walletService.GetWalletByUserAndChain(ctx, userID, chain)
}

// investmentFundingAdapter moves USDC between the user's Rail balance and a
// portfolio's deposit account.
//
// The debit and the on-chain transfer reuse the withdrawal service, which
// already owns ledger integrity, idempotency and Circle reconciliation. The
// transfer is tagged so investment funding is distinguishable from a user
// withdrawal in reporting; a dedicated investment ledger leg is a follow-up, not
// a silent behaviour change.
type investmentFundingAdapter struct {
	ledger        *ledger.Service
	withdrawals   *services.WithdrawalService
	wallets       *wallet.Service
	accountPrefix string
	log           *zap.Logger
}

// Available reports how much the user can invest from a source right now.
func (a *investmentFundingAdapter) Available(ctx context.Context, userID uuid.UUID, source string) (decimal.Decimal, error) {
	if a.ledger == nil {
		return decimal.Zero, fmt.Errorf("ledger service not available")
	}
	balances, err := a.ledger.GetUserBalances(ctx, userID)
	if err != nil {
		return decimal.Zero, fmt.Errorf("load balances: %w", err)
	}
	if balances == nil {
		return decimal.Zero, nil
	}
	if strings.EqualFold(strings.TrimSpace(source), "spending") {
		return balances.SpendingBalance, nil
	}
	return balances.StashBalance, nil
}

// TransferToPortfolio debits the user and delivers USDC to the portfolio's
// deposit account. It is idempotent on the caller's key.
func (a *investmentFundingAdapter) TransferToPortfolio(ctx context.Context, in investmentsvc.FundingRequest) (*investmentsvc.FundingResult, error) {
	if a.withdrawals == nil {
		return nil, fmt.Errorf("withdrawal service not available: cannot move funds into the portfolio")
	}
	if a.wallets == nil {
		return nil, fmt.Errorf("wallet service not available: cannot resolve the funding wallet")
	}
	address := solanaAddress(in.Destination)
	if address == "" {
		return nil, fmt.Errorf("investment funding: the portfolio has no deposit address")
	}
	wallet, err := a.wallets.GetWalletByUserAndChain(ctx, in.UserID, entities.WalletChainSolana)
	if err != nil {
		return nil, fmt.Errorf("load funding wallet: %w", err)
	}
	if wallet == nil || strings.TrimSpace(wallet.CircleWalletID) == "" {
		return nil, fmt.Errorf("investment funding: this account has no USDC wallet on solana")
	}

	source := entities.WithdrawalSourceSpendingBalance
	if strings.EqualFold(strings.TrimSpace(in.Source), "stash") {
		source = entities.WithdrawalSourceStashBalance
	}

	response, err := a.withdrawals.InitiateCryptoWithdrawal(ctx, &entities.InitiateCryptoWithdrawalRequest{
		UserID:              in.UserID,
		Amount:              in.AmountUSD,
		Currency:            entities.WithdrawalCurrencyUSDC,
		DestinationAddress:  address,
		DestinationChain:    "SOL",
		SourceChain:         "SOL",
		SourceAccount:       source,
		CircleWalletID:      wallet.CircleWalletID,
		SourceWalletAddress: wallet.Address,
		Category:            "investment_funding",
		Narration:           fmt.Sprintf("Investment funding for portfolio %s", in.EnrollmentID.String()),
		IdempotencyKey:      in.IdempotencyKey,
	})
	if err != nil {
		return nil, fmt.Errorf("move funds into portfolio: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("investment funding: withdrawal service returned no result")
	}

	result := &investmentsvc.FundingResult{
		TransferID: response.WithdrawalID,
		Status:     string(response.Status),
	}
	if a.log != nil {
		a.log.Info("investment funding submitted",
			zap.String("user_id", in.UserID.String()),
			zap.String("enrollment_id", in.EnrollmentID.String()),
			zap.String("withdrawal_id", response.WithdrawalID.String()),
			zap.String("status", string(response.Status)))
	}
	return result, nil
}

// RecipientAccount returns the user's own Solana custody address as a CAIP-10
// id, which is where withdrawal proceeds land.
func (a *investmentFundingAdapter) RecipientAccount(ctx context.Context, userID uuid.UUID) (string, error) {
	if a.wallets == nil {
		return "", fmt.Errorf("wallet service not available")
	}
	wallet, err := a.wallets.GetWalletByUserAndChain(ctx, userID, entities.WalletChainSolana)
	if err != nil {
		return "", fmt.Errorf("load settlement wallet: %w", err)
	}
	if wallet == nil || strings.TrimSpace(wallet.Address) == "" {
		return "", fmt.Errorf("%w: this account has no solana settlement address", investmentsvc.ErrNoSettlementAccount)
	}
	return a.accountPrefix + ":" + wallet.Address, nil
}

// solanaAddress extracts the address from a provider deposit account id, which
// may be a bare address or a CAIP-10 id.
func solanaAddress(accountID string) string {
	value := strings.TrimSpace(accountID)
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, ":"); index >= 0 {
		return strings.TrimSpace(value[index+1:])
	}
	return value
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// GetInvestmentGliderService returns the investment domain service.
func (c *Container) GetInvestmentGliderService() *investmentsvc.Service {
	return c.InvestmentGliderService
}

// GetInvestmentGliderHandlers returns the investment Agent API handlers, or nil
// when the feature is disabled.
func (c *Container) GetInvestmentGliderHandlers() *investmenthandlers.Handlers {
	if c.InvestmentGliderService == nil {
		return nil
	}
	return investmenthandlers.NewHandlers(c.InvestmentGliderService, c.Logger)
}

// GetInvestmentSyncWorker returns the investment sync worker, or nil when the
// feature is disabled.
func (c *Container) GetInvestmentSyncWorker() *investmentsync.Worker {
	return c.InvestmentSyncWorker
}
