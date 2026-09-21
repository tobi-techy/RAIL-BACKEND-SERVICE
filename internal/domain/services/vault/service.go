// Package vault (service) — the Premium Global Dollar Retirement Vault's domain
// service. It owns the lock policy, the cost-basis lots, the penalty gate and
// the ledger settlement. See engines.go for the pure decision logic.
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Vault event names for the audit trail.
const (
	EventVaultCreated        = "retirement_vault_created"
	EventVaultUpdated        = "retirement_vault_updated"
	EventVaultContribution   = "retirement_vault_contribution"
	EventVaultWithdrawPlan   = "retirement_vault_withdrawal_planned"
	EventVaultWithdrawSubmit = "retirement_vault_withdrawal_submitted"
	EventVaultWithdrawSettle = "retirement_vault_withdrawal_settled"
	EventVaultPenalty        = "retirement_vault_early_penalty"
	EventVaultUnlocked       = "retirement_vault_unlocked"
	EventVaultBlocked        = "retirement_vault_blocked"
)

// Deps are the vault service's collaborators.
type Deps struct {
	Config     Config
	Logger     *logger.Logger
	Repository Repository
	Engine     InvestmentEngine
	Ledger     Ledger
	Users      UserReader
	Notifier   Notifier // optional
	Onramp     Onramp   // optional
	Clock      func() time.Time
}

// Service is the retirement vault.
type Service struct {
	cfg      Config
	log      *logger.Logger
	repo     Repository
	engine   InvestmentEngine
	ledger   Ledger
	users    UserReader
	notifier Notifier
	onramp   Onramp
	clock    func() time.Time
}

// NewService builds the vault service.
func NewService(d Deps) *Service {
	log := d.Logger
	if log == nil {
		log = logger.NewLogger(zap.NewNop())
	}
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	cfg := d.Config
	if cfg.AuthorizationTTL <= 0 {
		cfg.AuthorizationTTL = 10 * time.Minute
	}
	if cfg.MinLockYears <= 0 {
		cfg.MinLockYears = 5
	}
	if cfg.DefaultRetirementAge <= 0 {
		cfg.DefaultRetirementAge = 50
	}
	if cfg.PenaltyRate.IsZero() {
		cfg.PenaltyRate = decimal.NewFromFloat(0.10)
	}
	if cfg.MaxAutoContributionPct.IsZero() {
		cfg.MaxAutoContributionPct = decimal.NewFromInt(1)
	}
	if strings.TrimSpace(cfg.ContributionSource) == "" {
		cfg.ContributionSource = "spending"
	}
	return &Service{
		cfg:      cfg,
		log:      log,
		repo:     d.Repository,
		engine:   d.Engine,
		ledger:   d.Ledger,
		users:    d.Users,
		notifier: d.Notifier,
		onramp:   d.Onramp,
		clock:    clock,
	}
}

// Enabled reports whether the vault is configured on.
func (s *Service) Enabled() bool { return s.cfg.Enabled }

// ready reports whether the vault can safely hold money.
//
// It requires a Rail-controlled settlement account. Without one, a user could
// pay into a plan they could never take money out of — so a misconfigured vault
// refuses new money (fail closed) instead of trapping it.
func (s *Service) ready() bool {
	return s.cfg.Enabled && strings.TrimSpace(s.cfg.SettlementAccount) != ""
}

func (s *Service) now() time.Time { return s.clock().UTC() }

// ---------------------------------------------------------------------------
// Read model
// ---------------------------------------------------------------------------

// GetView returns the single number that matters: what the plan is worth, how
// much of it is growth, and when the growth unlocks. It reads only Rail-owned
// tables, so it answers while the provider is unreachable.
func (s *Service) GetView(ctx context.Context, userID uuid.UUID) (*entities.VaultView, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, ErrNotFound
	}
	return s.buildView(ctx, vault)
}

func (s *Service) buildView(ctx context.Context, vault *entities.RetirementVault) (*entities.VaultView, error) {
	position, asOf, source, stale, err := s.position(ctx, vault)
	if err != nil {
		return nil, err
	}
	now := s.now()
	view := &entities.VaultView{
		VaultID:                  vault.ID,
		Name:                     vault.Name,
		Tier:                     vault.Tier,
		TierLabel:                vault.Tier.Label(),
		Status:                   vault.Status,
		PrincipalUSD:             position.PrincipalUSD,
		MarketValueUSD:           position.MarketValueUSD,
		EarningsUSD:              position.Earnings(),
		UnlockDate:               vault.UnlockDate,
		Locked:                   vault.Locked(now),
		PenaltyIfWithdrawnNowUSD: PenaltyIfWithdrawnNow(position, now, s.cfg.PenaltyRate),
		PenaltyRate:              s.cfg.PenaltyRate,
		AutoContributionPct:      vault.AutoContributionPct,
		RetirementAge:            vault.RetirementAge,
		MinLockYears:             vault.MinLockYears,
		AsOf:                     asOf,
		Source:                   source,
		Stale:                    stale,
	}
	return view, nil
}

// position assembles the cost basis and last-known market value.
func (s *Service) position(ctx context.Context, vault *entities.RetirementVault) (entities.VaultPosition, time.Time, string, bool, error) {
	lots, err := s.repo.ListOpenLots(ctx, vault.ID)
	if err != nil {
		return entities.VaultPosition{}, time.Time{}, "", false, err
	}
	principal := SumPrincipal(lots)

	position := entities.VaultPosition{
		PrincipalUSD:   principal,
		MarketValueUSD: principal,
	}
	asOf := vault.CreatedAt
	source := "ledger_fallback"
	stale := false

	snapshot, err := s.repo.LatestSnapshot(ctx, vault.ID)
	if err != nil {
		return entities.VaultPosition{}, time.Time{}, "", false, err
	}
	if snapshot != nil {
		position.MarketValueUSD = snapshot.MarketValueUSD
		asOf = snapshot.AsOf
		source = snapshot.Source
		if s.cfg.StaleAfter > 0 && s.now().Sub(snapshot.AsOf) > s.cfg.StaleAfter {
			stale = true
		}
	}
	if vault.UnlockDate != nil {
		position.UnlockDate = *vault.UnlockDate
	}
	return position, asOf, source, stale, nil
}

// ListStrategyOptions returns the tiers the app offers, with human labels only.
func (s *Service) ListStrategyOptions() []entities.VaultStrategyOption {
	options := make([]entities.VaultStrategyOption, 0, len(entities.AllVaultTiers()))
	for _, tier := range entities.AllVaultTiers() {
		options = append(options, entities.VaultStrategyOption{
			Tier:        tier,
			Label:       tier.Label(),
			Description: tierDescription(tier),
		})
	}
	return options
}

// Activity returns a plain-language contribution/withdrawal history.
func (s *Service) Activity(ctx context.Context, userID uuid.UUID, limit int) ([]entities.VaultActivityEntry, error) {
	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	lots, err := s.repo.ListLots(ctx, vault.ID, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]entities.VaultActivityEntry, 0, len(lots))
	for _, lot := range lots {
		if lot == nil {
			continue
		}
		entries = append(entries, entities.VaultActivityEntry{
			Kind:      "contribution",
			At:        lot.AcquiredAt,
			AmountUSD: lot.AmountUSD,
		})
	}
	return entries, nil
}

// ---------------------------------------------------------------------------
// Create / update
// ---------------------------------------------------------------------------

// CreateVault opens a retirement vault and enrolls the user into a Rail-owned
// strategy. Enrollment is confirmed (two-stage), so this returns an
// AWAITING_CONFIRMATION response on the first call and completes on replay.
func (s *Service) CreateVault(ctx context.Context, userID uuid.UUID, req *entities.VaultCreateRequest) (*entities.VaultCreateResponse, error) {
	if !s.ready() {
		return nil, ErrDisabled
	}
	if req == nil || !req.Tier.Valid() {
		return nil, fmt.Errorf("%w: choose one of the retirement plans", ErrValidation)
	}
	retirementAge := req.RetirementAge
	if retirementAge == 0 {
		retirementAge = s.cfg.DefaultRetirementAge
	}
	if retirementAge < 50 || retirementAge > 90 {
		return nil, fmt.Errorf("%w: retirement age must be between 50 and 90", ErrValidation)
	}
	pct := req.AutoContributionPct
	if pct.IsZero() {
		pct = s.cfg.DefaultAutoContributionPct
	}
	if pct.IsNegative() || pct.GreaterThan(s.cfg.MaxAutoContributionPct) {
		return nil, fmt.Errorf("%w: automatic saving percentage is out of range", ErrValidation)
	}

	existing, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrAlreadyExists
	}

	strategy, err := s.strategyForTier(ctx, req.Tier)
	if err != nil {
		return nil, err
	}

	enrollResp, err := s.engine.Enroll(ctx, userID, &entities.InvestmentEnrollRequest{
		StrategyID:        strategy.ID.String(),
		AmountUSD:         decimal.Zero,
		ConfirmationToken: req.ConfirmationToken,
	}, entities.InvestmentActorUser)
	if err != nil {
		return nil, err
	}
	if enrollResp == nil {
		return nil, fmt.Errorf("%w: enrollment returned no result", ErrValidation)
	}
	if enrollResp.Status == entities.InvestmentActionAwaitingConfirmation || enrollResp.Enrollment == nil {
		return &entities.VaultCreateResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Confirmation: enrollResp.Confirmation,
		}, nil
	}

	enrollment := enrollResp.Enrollment
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "USD Retirement Plan"
	}
	now := s.now()
	vault := &entities.RetirementVault{
		ID:                  uuid.New(),
		UserID:              userID,
		Name:                name,
		Tier:                req.Tier,
		Status:              entities.VaultStatusActive,
		RetirementAge:       retirementAge,
		MinLockYears:        s.cfg.MinLockYears,
		AutoContributionPct: pct,
		GliderEnrollmentID:  &enrollment.ID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.repo.CreateVault(ctx, vault); err != nil {
		return nil, err
	}
	if err := s.engine.LinkVaultEnrollment(ctx, userID, enrollment.ID, vault.ID); err != nil {
		// The vault exists but the portfolio is not yet locked to it. That is a
		// dangerous half-state, so it is surfaced rather than swallowed.
		return nil, fmt.Errorf("link vault portfolio: %w", err)
	}

	view, err := s.buildView(ctx, vault)
	if err != nil {
		return nil, err
	}
	return &entities.VaultCreateResponse{Status: entities.InvestmentActionCompleted, View: view}, nil
}

// UpdateVault changes the automatic-savings rule or the retirement age.
func (s *Service) UpdateVault(ctx context.Context, userID uuid.UUID, req *entities.VaultUpdateRequest) (*entities.VaultView, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nothing to update", ErrValidation)
	}
	vault, err := s.repo.GetActiveVaultByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if vault == nil {
		return nil, ErrNotFound
	}

	if req.AutoContributionPct != nil {
		pct := *req.AutoContributionPct
		if pct.IsNegative() || pct.GreaterThan(s.cfg.MaxAutoContributionPct) {
			return nil, fmt.Errorf("%w: automatic saving percentage is out of range", ErrValidation)
		}
		vault.AutoContributionPct = pct
	}
	if req.RetirementAge != nil {
		if *req.RetirementAge < 50 || *req.RetirementAge > 90 {
			return nil, fmt.Errorf("%w: retirement age must be between 50 and 90", ErrValidation)
		}
		vault.RetirementAge = *req.RetirementAge
		if err := s.recomputeUnlock(ctx, vault); err != nil {
			return nil, err
		}
	}

	if err := s.repo.UpdateVault(ctx, vault); err != nil {
		return nil, err
	}
	return s.buildView(ctx, vault)
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

// BootstrapStrategies ensures the Rail-owned strategies for every configured
// tier exist. Idempotent, so it is safe to run on every startup.
func (s *Service) BootstrapStrategies(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}
	var firstErr error
	for _, def := range s.cfg.Strategies {
		if _, err := s.engine.EnsureRailStrategy(ctx, entities.InvestmentRailStrategyRequest{
			Name:             def.Name,
			Risk:             def.Risk,
			Horizon:          def.Horizon,
			TargetAllocation: def.Legs,
		}); err != nil {
			s.log.Error("failed to ensure rail retirement strategy",
				"tier", string(def.Tier), "name", def.Name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ---------------------------------------------------------------------------
// VaultObserver implementation (the withdrawal gate)
// ---------------------------------------------------------------------------

// IsVaultEnrollment reports whether a portfolio belongs to a retirement vault.
func (s *Service) IsVaultEnrollment(ctx context.Context, enrollmentID uuid.UUID) (bool, error) {
	vault, err := s.repo.GetVaultByEnrollment(ctx, enrollmentID)
	if err != nil {
		return false, err
	}
	return vault != nil, nil
}

// Authorize validates and consumes a single-use withdrawal authorization. Any
// failure — unknown, expired, replayed, wrong amount — refuses the withdrawal.
func (s *Service) Authorize(ctx context.Context, enrollmentID uuid.UUID, grossUSD decimal.Decimal, key string) (*entities.VaultWithdrawalPlan, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%w: no authorization supplied", ErrAuthorizationInvalid)
	}
	if strings.TrimSpace(s.cfg.SettlementAccount) == "" {
		// Without a Rail-controlled settlement account we cannot compute or
		// retain the penalty, so we do not allow the money to move.
		return nil, fmt.Errorf("%w: no settlement account is configured", ErrAuthorizationInvalid)
	}
	auth, err := s.repo.ConsumeAuthorization(ctx, key, enrollmentID, grossUSD, s.now())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthorizationInvalid, err)
	}

	var plan entities.VaultWithdrawalPlan
	if len(auth.Plan) > 0 {
		if err := json.Unmarshal(auth.Plan, &plan); err != nil {
			return nil, fmt.Errorf("%w: unreadable plan", ErrAuthorizationInvalid)
		}
	}
	plan.VaultID = auth.VaultID
	plan.EnrollmentID = auth.EnrollmentID
	plan.GrossUSD = auth.GrossUSD
	plan.PenaltyUSD = auth.PenaltyUSD
	plan.NetToUserUSD = auth.NetUSD
	plan.SettlementAccount = s.cfg.SettlementAccount
	plan.AuthorizationKey = key
	if plan.DestinationAccount == "" {
		plan.DestinationAccount = "stash"
	}
	return &plan, nil
}

// OnEnrollmentSynced records a valuation snapshot after a successful provider
// sync, and notifies the user the first time their earnings unlock.
func (s *Service) OnEnrollmentSynced(ctx context.Context, enrollment *entities.InvestmentEnrollment) error {
	if enrollment == nil || enrollment.VaultID == nil {
		return nil
	}
	vault, err := s.repo.GetVaultByID(ctx, *enrollment.VaultID)
	if err != nil {
		return err
	}
	if vault == nil {
		return nil
	}
	lots, err := s.repo.ListOpenLots(ctx, vault.ID)
	if err != nil {
		return err
	}
	principal := SumPrincipal(lots)
	market := enrollment.TotalValueUSD
	earnings := market.Sub(principal)
	if earnings.IsNegative() {
		earnings = decimal.Zero
	}

	previous, err := s.repo.LatestSnapshot(ctx, vault.ID)
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.repo.CreateSnapshot(ctx, &entities.VaultEarningsSnapshot{
		VaultID:        vault.ID,
		UserID:         vault.UserID,
		PrincipalUSD:   principal,
		EarningsUSD:    earnings,
		MarketValueUSD: market,
		Source:         "glider_sync",
		AsOf:           now,
	}); err != nil {
		return err
	}

	if s.notifier != nil && vault.UnlockDate != nil && !now.Before(*vault.UnlockDate) {
		if previous == nil || previous.AsOf.Before(*vault.UnlockDate) {
			if err := s.notifier.NotifyVaultUnlocked(ctx, vault.UserID, *vault.UnlockDate); err != nil {
				s.log.Warn("vault unlock notification failed", "vault_id", vault.ID.String(), "error", err)
			}
		}
	}
	return nil
}

// OnWithdrawalFilled settles a filled vault withdrawal: it consumes the cost
// basis, commits the penalty and moves the money in the ledger, net of the fee.
func (s *Service) OnWithdrawalFilled(ctx context.Context, execution *entities.InvestmentExecution) error {
	if execution == nil {
		return nil
	}
	penalty, err := s.repo.GetPenaltyEventByExecution(ctx, execution.ID)
	if err != nil {
		return err
	}
	if penalty == nil || penalty.Status != entities.VaultPenaltyPending {
		return nil
	}
	auth, err := s.repo.GetAuthorizationByExecution(ctx, execution.ID)
	if err != nil {
		return err
	}
	if auth == nil {
		return fmt.Errorf("%w: no authorization for execution %s", ErrAuthorizationInvalid, execution.ID)
	}
	var plan entities.VaultWithdrawalPlan
	if len(auth.Plan) > 0 {
		if err := json.Unmarshal(auth.Plan, &plan); err != nil {
			return fmt.Errorf("decode vault withdrawal plan: %w", err)
		}
	}
	return s.settle(ctx, execution, penalty, auth, &plan)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// strategyForTier resolves the Rail-owned strategy a tier maps to.
func (s *Service) strategyForTier(ctx context.Context, tier entities.VaultTier) (*entities.InvestmentStrategy, error) {
	name := ""
	for _, def := range s.cfg.Strategies {
		if def.Tier == tier {
			name = strings.ToLower(strings.TrimSpace(def.Name))
			break
		}
	}
	strategies, err := s.engine.ListRailStrategies(ctx)
	if err != nil {
		return nil, err
	}
	if name == "" {
		// Fall back to matching the human label, which is how bootstrap names
		// configured strategies when no explicit mapping is present.
		name = strings.ToLower(tier.Label())
	}
	for _, strategy := range strategies {
		if strategy != nil && strings.EqualFold(strings.TrimSpace(strategy.Name), name) {
			return strategy, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrStrategyUnavailable, tier.Label())
}

// recomputeUnlock derives the unlock date from the funding date and the user's
// date of birth. A missing date of birth leaves the vault locked.
func (s *Service) recomputeUnlock(ctx context.Context, vault *entities.RetirementVault) error {
	if vault == nil || vault.FundedAt == nil {
		return nil
	}
	dob := s.dateOfBirth(ctx, vault.UserID)
	unlock, err := ComputeUnlockDate(dob, vault.RetirementAge, *vault.FundedAt, vault.MinLockYears)
	if err != nil {
		s.log.Warn("vault unlock date unresolved",
			"vault_id", vault.ID.String(), "user_id", vault.UserID.String(), "error", err)
		return nil
	}
	vault.UnlockDate = &unlock
	return nil
}

func (s *Service) dateOfBirth(ctx context.Context, userID uuid.UUID) *time.Time {
	if s.users == nil {
		return nil
	}
	dob, err := s.users.GetDateOfBirth(ctx, userID)
	if err != nil || dob == nil {
		return nil
	}
	return dob
}

// destinationAccountType maps the caller's plain-language destination onto a
// ledger account. Anything unknown is rejected rather than guessed.
func destinationAccountType(destination string) (entities.AccountType, error) {
	switch strings.ToLower(strings.TrimSpace(destination)) {
	case "", "stash", "savings":
		return entities.AccountTypeStashBalance, nil
	case "spending", "spend":
		return entities.AccountTypeSpendingBalance, nil
	default:
		return "", fmt.Errorf("%w: unknown destination account", ErrValidation)
	}
}

func tierDescription(tier entities.VaultTier) string {
	switch tier {
	case entities.VaultTierConservative:
		return "Steady, income-focused dollars with less movement."
	case entities.VaultTierBalanced:
		return "A balance of global growth and stability."
	case entities.VaultTierBold:
		return "Long-horizon growth, with more ups and downs along the way."
	default:
		return "Automated global dollar wealth."
	}
}
