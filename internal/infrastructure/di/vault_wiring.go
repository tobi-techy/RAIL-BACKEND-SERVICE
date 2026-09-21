package di

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	vaulthandlers "github.com/rail-service/rail_service/internal/api/handlers/vault"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/notification"
	vaultsvc "github.com/rail-service/rail_service/internal/domain/services/vault"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

// initializeVaultServices wires the Premium Global Dollar Retirement Vault and
// connects it to the investment engine (as the withdrawal gate) and the
// allocation service (as the automatic-savings hook).
//
// A failure here disables the vault only: every vault endpoint reports
// unavailable rather than the whole API failing.
func (c *Container) initializeVaultServices(sqlxDB *sqlx.DB) error {
	cfg := c.Config.Vault
	if !cfg.Enabled {
		c.ZapLog.Info("retirement vault feature disabled")
		return nil
	}
	if c.InvestmentGliderService == nil {
		// The investment (Glider) engine is the vault's investment surface. Without
		// it the vault cannot enroll or fund a portfolio. Rather than brick the whole
		// API when the engine is off, we log and skip: the vault simply stays
		// unavailable until investment_glider.enabled is turned on (requires a
		// restart).
		c.ZapLog.Warn("retirement vault enabled but investment engine is not initialized: " +
			"vault stays unavailable until the investment feature is enabled")
		return nil
	}

	c.VaultRepo = repositories.NewVaultRepository(sqlxDB)

	var notifier vaultsvc.Notifier
	if c.NotificationService != nil {
		notifier = &vaultNotifierAdapter{svc: c.NotificationService}
	}
	var users vaultsvc.UserReader
	if c.UserRepo != nil {
		users = &vaultUserReaderAdapter{repo: c.UserRepo}
	}

	c.VaultService = vaultsvc.NewService(vaultsvc.Deps{
		Config:     vaultServiceConfig(cfg),
		Logger:     c.Logger,
		Repository: c.VaultRepo,
		Engine:     c.InvestmentGliderService,
		Ledger:     c.LedgerService,
		Users:      users,
		Notifier:   notifier,
	})

	// The vault becomes the investment engine's withdrawal gate and the
	// allocation service's automatic-savings hook.
	c.InvestmentGliderService.SetVaultObserver(c.VaultService)
	if c.AllocationService != nil {
		c.AllocationService.SetVaultContributor(c.VaultService)
	}

	// Ensure the Rail-owned retirement strategies exist. Idempotent by name.
	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := c.VaultService.BootstrapStrategies(bootstrapCtx); err != nil {
		c.ZapLog.Warn("retirement strategy bootstrap incomplete", zap.Error(err))
	}

	c.ZapLog.Info("retirement vault services initialized",
		zap.Int("strategies", len(cfg.Strategies)),
		zap.String("contribution_source", cfg.ContributionSource))
	if strings.TrimSpace(cfg.SettlementAccount) == "" {
		// The vault is enabled but not deployable: it will refuse to open a plan
		// or take a contribution until this is set. Loud, not silent.
		c.ZapLog.Warn("vault is enabled but VAULT_SETTLEMENT_ACCOUNT is not set: " +
			"retirement plans will refuse to open or accept money until it is configured")
	}
	if len(cfg.Strategies) == 0 {
		c.ZapLog.Warn("vault is enabled but VAULT_STRATEGIES is empty: no retirement plan can be offered yet")
	}
	return nil
}

// vaultServiceConfig maps the YAML/env config onto the domain config.
func vaultServiceConfig(cfg config.VaultConfig) vaultsvc.Config {
	out := vaultsvc.Config{
		Enabled:                    cfg.Enabled,
		DefaultRetirementAge:       cfg.DefaultRetirementAge,
		MinLockYears:               cfg.MinLockYears,
		PenaltyRate:                decimal.NewFromFloat(cfg.PenaltyRate),
		DefaultAutoContributionPct: decimal.NewFromFloat(cfg.DefaultAutoContributionPct),
		MaxAutoContributionPct:     decimal.NewFromFloat(cfg.MaxAutoContributionPct),
		MinContributionUSD:         decimal.NewFromFloat(cfg.MinContributionUSD),
		ContributionSource:         orDefault(cfg.ContributionSource, "spending"),
		SettlementAccount:          strings.TrimSpace(cfg.SettlementAccount),
		AuthorizationTTL:           time.Duration(cfg.AuthorizationTTLMinutes) * time.Minute,
		StaleAfter:                 time.Duration(cfg.StaleAfterHours) * time.Hour,
	}
	for _, strategy := range cfg.Strategies {
		legs := make([]entities.InvestmentAllocationLeg, 0, len(strategy.Legs))
		for _, leg := range strategy.Legs {
			legs = append(legs, entities.InvestmentAllocationLeg{
				CAIP19: leg.CAIP19,
				Symbol: leg.Symbol,
				Weight: decimal.NewFromFloat(leg.Weight),
			})
		}
		out.Strategies = append(out.Strategies, vaultsvc.StrategyDefinition{
			Tier:    entities.VaultTier(strings.TrimSpace(strategy.Tier)),
			Name:    strings.TrimSpace(strategy.Name),
			Risk:    strategy.Risk,
			Horizon: strategy.Horizon,
			Legs:    legs,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Adapters
// ---------------------------------------------------------------------------

// vaultUserReaderAdapter exposes the date of birth the unlock policy needs.
//
// It reads through GetProfileByUserID because that query selects date_of_birth;
// the lighter user lookup deliberately does not.
type vaultUserReaderAdapter struct {
	repo *repositories.UserRepository
}

func (a *vaultUserReaderAdapter) GetDateOfBirth(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	if a.repo == nil {
		return nil, fmt.Errorf("user repository not available")
	}
	profile, err := a.repo.GetProfileByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, nil
	}
	return profile.DateOfBirth, nil
}

// vaultNotifierAdapter delivers vault messages through the shared notification
// service. Copy here never mentions crypto, a chain, a token or the provider.
type vaultNotifierAdapter struct {
	svc *notification.NotificationService
}

func (a *vaultNotifierAdapter) send(ctx context.Context, userID uuid.UUID, title, body string) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.Send(ctx, &entities.Notification{
		UserID:    userID,
		Type:      entities.NotificationTypePortfolio,
		Channel:   entities.ChannelPush,
		Priority:  entities.PriorityMedium,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}, nil)
}

func (a *vaultNotifierAdapter) NotifyVaultContribution(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, unlockDate *time.Time) error {
	body := fmt.Sprintf("$%s was added to your Retirement Plan.", amount.StringFixed(2))
	if unlockDate != nil {
		body = fmt.Sprintf("$%s was added to your Retirement Plan. Your growth unlocks on %s.",
			amount.StringFixed(2), unlockDate.Format("2 Jan 2006"))
	}
	return a.send(ctx, userID, "Retirement Plan updated", body)
}

func (a *vaultNotifierAdapter) NotifyVaultUnlocked(ctx context.Context, userID uuid.UUID, unlockDate time.Time) error {
	return a.send(ctx, userID, "Your retirement growth is unlocked",
		fmt.Sprintf("The growth in your Retirement Plan is now available without an early-withdrawal fee (since %s).",
			unlockDate.Format("2 Jan 2006")))
}

func (a *vaultNotifierAdapter) NotifyVaultWithdrawal(ctx context.Context, userID uuid.UUID, net, penalty decimal.Decimal, early bool) error {
	if penalty.GreaterThan(decimal.Zero) && early {
		return a.send(ctx, userID, "Withdrawal from your Retirement Plan",
			fmt.Sprintf("$%s was sent to your balance. A $%s early-withdrawal fee was applied to the growth portion.",
				net.StringFixed(2), penalty.StringFixed(2)))
	}
	return a.send(ctx, userID, "Withdrawal from your Retirement Plan",
		fmt.Sprintf("$%s was sent to your balance.", net.StringFixed(2)))
}

// ---------------------------------------------------------------------------
// Accessors
// ---------------------------------------------------------------------------

// GetVaultService returns the retirement vault domain service, or nil.
func (c *Container) GetVaultService() *vaultsvc.Service { return c.VaultService }

// GetVaultHandlers returns the retirement vault HTTP handlers, or nil.
func (c *Container) GetVaultHandlers() *vaulthandlers.Handlers {
	if c.VaultService == nil {
		return nil
	}
	return vaulthandlers.NewHandlers(c.VaultService, c.Logger)
}
