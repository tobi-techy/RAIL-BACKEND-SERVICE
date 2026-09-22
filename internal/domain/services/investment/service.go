package investment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Investment event names (spec §22).
const (
	EventAssetViewed          = "investment_asset_viewed"
	EventStrategyViewed       = "investment_strategy_viewed"
	EventStrategyCreated      = "strategy_created"
	EventStrategyProposed     = "strategy_proposed"
	EventStrategyAccepted     = "strategy_accepted"
	EventStrategyRejected     = "strategy_rejected"
	EventStrategyVersioned    = "strategy_version_published"
	EventStrategyEnrolled     = "strategy_enrolled"
	EventStrategyPaused       = "strategy_paused"
	EventStrategyResumed      = "strategy_resumed"
	EventRebalancePreviewed   = "rebalance_previewed"
	EventRebalanceExecuted    = "rebalance_executed"
	EventOrderCreated         = "investment_order_created"
	EventOrderSubmitted       = "investment_order_submitted"
	EventOrderFilled          = "investment_order_filled"
	EventOrderFailed          = "investment_order_failed"
	EventOrderCancelled       = "investment_order_cancelled"
	EventCopyStrategyViewed   = "copy_strategy_viewed"
	EventPortfolioViewed      = "portfolio_viewed"
	EventPortfolioExplanation = "portfolio_explanation_requested"
	EventWithdrawalRequested  = "investment_withdrawal_requested"
	EventFundingInitiated     = "investment_funding_initiated"
	EventFundingSettled       = "investment_funding_settled"
	EventPolicyBlocked        = "investment_policy_blocked"
	EventConfirmationRequest  = "investment_confirmation_requested"
)

// Deps are the collaborators the investment service needs.
type Deps struct {
	Config        Config
	Logger        *logger.Logger
	Provider      Provider
	Signer        OwnerSigner
	Funding       FundingPort
	Assets        AssetRepository
	Strategies    StrategyRepository
	Enrollments   EnrollmentRepository
	Holdings      HoldingRepository
	Executions    ExecutionRepository
	Transfers     FundingTransferRepository
	Signatures    SignatureRequestRepository
	Confirmations ConfirmationRepository
	Operations    OperationRepository
	Audit         AuditRepository
	Limits        LimitsRepository
	Users         UserProfileReader

	// VaultObserver is optional. It is set when the retirement vault is enabled,
	// and it is the only thing that can authorise a withdrawal from a
	// vault-linked portfolio.
	VaultObserver VaultObserver
}

// Service is the deterministic investment infrastructure. Handlers, the sync
// worker and (indirectly) the AI agent all go through it.
type Service struct {
	cfg      Config
	log      *logger.Logger
	provider Provider
	signer   OwnerSigner
	funding  FundingPort

	assets        AssetRepository
	strategies    StrategyRepository
	enrollments   EnrollmentRepository
	holdings      HoldingRepository
	executions    ExecutionRepository
	transfers     FundingTransferRepository
	signatures    SignatureRequestRepository
	confirmations ConfirmationRepository
	operations    OperationRepository
	audit         AuditRepository
	limits        LimitsRepository
	users         UserProfileReader
	vaultObserver VaultObserver

	validator *Validator
	policy    *Policy
	previewer *Previewer
	resolver  AssetResolver

	clock func() time.Time
}

// NewService builds the investment service.
func NewService(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = logger.NewLogger(zap.NewNop())
	}
	s := &Service{
		cfg:           d.Config,
		log:           d.Logger,
		provider:      d.Provider,
		signer:        d.Signer,
		funding:       d.Funding,
		assets:        d.Assets,
		strategies:    d.Strategies,
		enrollments:   d.Enrollments,
		holdings:      d.Holdings,
		executions:    d.Executions,
		transfers:     d.Transfers,
		signatures:    d.Signatures,
		confirmations: d.Confirmations,
		operations:    d.Operations,
		audit:         d.Audit,
		limits:        d.Limits,
		users:         d.Users,
		vaultObserver: d.VaultObserver,
		resolver:      resolverFor(d.Assets),
		clock:         func() time.Time { return time.Now().UTC() },
	}
	s.validator = NewValidator(s.resolver, d.Config)
	s.policy = NewPolicy(d.Users, d.Config)
	s.previewer = NewPreviewer(d.Config)
	return s
}

// Enabled reports whether the investment feature is configured on.
func (s *Service) Enabled() bool { return s.cfg.Enabled }

// SetVaultObserver wires the retirement vault's control surface. Called once at
// startup; nil keeps vault behaviour entirely off.
func (s *Service) SetVaultObserver(observer VaultObserver) { s.vaultObserver = observer }

// GetAssetByCAIP19 resolves one catalog row by provider id. The vault tier
// bootstrap uses it to prove every tier-file leg names a real catalog asset.
func (s *Service) GetAssetByCAIP19(ctx context.Context, caip19 string) (*entities.InvestmentAsset, error) {
	if s.assets == nil {
		return nil, nil
	}
	return s.assets.GetByCAIP19(ctx, caip19)
}

// SetClock overrides the service clock (tests).
func (s *Service) SetClock(now func() time.Time) {
	s.clock = now
	s.validator.SetClock(now)
}

// ---------------------------------------------------------------------------
// Read models
// ---------------------------------------------------------------------------

// GetPortfolio returns the whole-portfolio view. Miriam reads this instead of
// reconstructing portfolio state from memory.
func (s *Service) GetPortfolio(ctx context.Context, userID uuid.UUID) (*entities.InvestmentPortfolioSummary, error) {
	enrollments, err := s.enrollments.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list enrollments: %w", err)
	}
	holdings, err := s.holdings.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}

	positions := make([]entities.InvestmentHolding, 0, len(holdings))
	for _, holding := range holdings {
		if holding != nil {
			positions = append(positions, *holding)
		}
	}

	summary := &entities.InvestmentPortfolioSummary{
		Enrollments: make([]entities.InvestmentEnrollmentSummary, 0, len(enrollments)),
		Positions:   positions,
		Source:      "glider",
		AsOf:        s.nowOr(),
	}

	var total, cash decimal.Decimal
	stale := false
	for _, holding := range holdings {
		if holding == nil {
			continue
		}
		total = total.Add(holding.ValueUSD)
		if isCashLeg(entities.InvestmentAllocationLeg{Symbol: holding.Symbol}, s.cfg.SettlementSymbol) {
			cash = cash.Add(holding.ValueUSD)
		}
		if s.cfg.StaleMarketDataAfter > 0 && s.nowOr().Sub(holding.AsOf) > s.cfg.StaleMarketDataAfter {
			stale = true
		}
	}
	summary.TotalValueUSD = total
	summary.CashValueUSD = cash
	summary.InvestedValueUSD = total.Sub(cash)
	summary.Stale = stale

	// P&L basis: net contributions from our own funding records. This is a
	// money-weighted approximation, not tax cost basis.
	netContributions := decimal.Zero
	for _, enrollment := range enrollments {
		summary.Enrollments = append(summary.Enrollments, entities.InvestmentEnrollmentSummary{
			EnrollmentID:    enrollment.ID,
			StrategyID:      enrollment.StrategyID,
			Status:          enrollment.Status,
			TotalValueUSD:   enrollment.TotalValueUSD,
			Chain:           enrollment.Chain,
			NextDueAt:       enrollment.NextDueAt,
			LastRebalanceAt: enrollment.LastRebalanceAt,
		})
		if enrollment.StrategyID != uuid.Nil {
			if strategy, err := s.strategies.GetByID(ctx, enrollment.StrategyID); err == nil && strategy != nil {
				summary.Enrollments[len(summary.Enrollments)-1].StrategyName = strategy.Name
			}
		}
		if s.transfers != nil {
			transfers, err := s.transfers.ListByEnrollment(ctx, enrollment.ID)
			if err == nil {
				for _, transfer := range transfers {
					switch transfer.Direction {
					case "deposit":
						netContributions = netContributions.Add(transfer.AmountUSD)
					case "withdrawal":
						netContributions = netContributions.Sub(transfer.AmountUSD)
					}
				}
			}
		}
	}
	summary.UnrealizedPnLUSD = total.Sub(netContributions)
	return summary, nil
}

// GetPositions returns the normalized holdings for a user.
func (s *Service) GetPositions(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	return s.holdings.ListByUser(ctx, userID)
}

// GetLimitsResponse returns the effective limits plus the capability verdicts,
// so the agent can answer "can I invest?" without attempting anything.
func (s *Service) GetLimitsResponse(ctx context.Context, userID uuid.UUID) (*entities.InvestmentLimitsResponse, error) {
	effective, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	resp := &entities.InvestmentLimitsResponse{
		Limits: *effective,
		Source: "rail_policy",
	}
	if s.users != nil {
		if profile, err := s.users.GetInvestmentProfile(ctx, userID); err == nil && profile != nil {
			resp.KYCTier = profile.KYCTier
		}
	}
	if s.assets != nil {
		if count, err := s.assets.CountAllowed(ctx); err == nil {
			resp.AllowedAssetsCount = count
		}
	}
	for _, probe := range []struct {
		action PolicyAction
		assign func(*entities.InvestmentLimitVerdicts, entities.InvestmentVerdict)
	}{
		{PolicyActionCreateStrategy, func(v *entities.InvestmentLimitVerdicts, verdict entities.InvestmentVerdict) {
			v.CreateStrategy = verdict
		}},
		{PolicyActionEnroll, func(v *entities.InvestmentLimitVerdicts, verdict entities.InvestmentVerdict) { v.Enroll = verdict }},
		{PolicyActionWithdraw, func(v *entities.InvestmentLimitVerdicts, verdict entities.InvestmentVerdict) { v.Withdraw = verdict }},
	} {
		decision, err := s.policy.Evaluate(ctx, PolicyInput{UserID: userID, Action: probe.action, Limits: *effective})
		if err != nil {
			return nil, err
		}
		probe.assign(&resp.Verdicts, decision.Verdict)
	}
	resp.Verdicts.CanCreateStrategy = resp.Verdicts.CreateStrategy != entities.InvestmentVerdictNotSupported &&
		resp.Verdicts.CreateStrategy != entities.InvestmentVerdictRequiresComplianceReview
	resp.Verdicts.CanEnroll = resp.Verdicts.Enroll != entities.InvestmentVerdictNotSupported &&
		resp.Verdicts.Enroll != entities.InvestmentVerdictRequiresComplianceReview
	resp.Verdicts.CanWithdraw = resp.Verdicts.Withdraw != entities.InvestmentVerdictNotSupported &&
		resp.Verdicts.Withdraw != entities.InvestmentVerdictRequiresComplianceReview
	return resp, nil
}

// EffectiveLimits resolves a user's limits, falling back to configured defaults.
func (s *Service) EffectiveLimits(ctx context.Context, userID uuid.UUID) (*entities.InvestmentLimits, error) {
	limits := s.defaultLimits()
	if s.limits != nil {
		stored, err := s.limits.Get(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load limits: %w", err)
		}
		if stored != nil {
			limits = *stored
		}
	}
	if s.transfers != nil {
		since := s.nowOr().Truncate(24 * time.Hour)
		if volume, err := s.transfers.SumDepositsSince(ctx, userID, since); err == nil {
			limits.DailyVolumeUSD = volume
		}
	}
	return &limits, nil
}

func (s *Service) defaultLimits() entities.InvestmentLimits {
	d := s.cfg.DefaultLimits
	return entities.InvestmentLimits{
		MaxPositionPct:    d.MaxPositionPct,
		MaxStrategyPct:    d.MaxStrategyPct,
		MaxTransactionUSD: d.MaxTransactionUSD,
		MaxDailyVolumeUSD: d.MaxDailyVolumeUSD,
		MinCashReserveUSD: d.MinCashReserveUSD,
		MinOrderAmountUSD: d.MinOrderAmountUSD,
		MaxEnrollments:    d.MaxEnrollments,
	}
}

// ListAssets searches the supported asset catalog.
func (s *Service) ListAssets(ctx context.Context, query string, limit int) ([]*entities.InvestmentAsset, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	assets, err := s.assets.List(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	return assets, nil
}

// GetAsset resolves one asset by id, CAIP-19 or symbol.
func (s *Service) GetAsset(ctx context.Context, assetID, caip19, symbol string) (*entities.InvestmentAsset, error) {
	return s.resolveAsset(ctx, assetID, caip19, symbol)
}

// resolveAsset is the single asset-resolution path used by every caller.
func (s *Service) resolveAsset(ctx context.Context, assetID, caip19, symbol string) (*entities.InvestmentAsset, error) {
	if s.resolver == nil {
		return nil, ErrNotFound
	}
	return s.resolver.Resolve(ctx, assetID, caip19, symbol)
}

// ListStrategies returns a user's strategies.
func (s *Service) ListStrategies(ctx context.Context, userID uuid.UUID, status string) ([]*entities.InvestmentStrategy, error) {
	strategies, err := s.strategies.ListByUser(ctx, userID, status)
	if err != nil {
		return nil, fmt.Errorf("list strategies: %w", err)
	}
	for _, strategy := range strategies {
		_ = s.recordStrategyEvent(ctx, userID, EventStrategyViewed, strategy, entities.InvestmentActorMiriam, nil)
	}
	return strategies, nil
}

// GetStrategy returns a strategy plus its version history.
func (s *Service) GetStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentStrategy, []*entities.InvestmentStrategyVersion, error) {
	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil {
		return nil, nil, ErrNotFound
	}
	if !s.canAccess(strategy, userID) {
		return nil, nil, ErrNotFound
	}
	versions, err := s.strategies.ListVersions(ctx, strategyID)
	if err != nil {
		return nil, nil, fmt.Errorf("list versions: %w", err)
	}
	_ = s.recordStrategyEvent(ctx, userID, EventStrategyViewed, strategy, entities.InvestmentActorMiriam, nil)
	return strategy, versions, nil
}

// GetExecution returns one auditable action.
func (s *Service) GetExecution(ctx context.Context, userID, executionID uuid.UUID) (*entities.InvestmentExecution, error) {
	execution, err := s.executions.GetByID(ctx, executionID)
	if err != nil {
		return nil, fmt.Errorf("get execution: %w", err)
	}
	if execution == nil || execution.UserID != userID {
		return nil, ErrNotFound
	}
	return execution, nil
}

// ListExecutions returns a user's auditable actions.
func (s *Service) ListExecutions(ctx context.Context, userID uuid.UUID, status string, limit int) ([]*entities.InvestmentExecution, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	return s.executions.ListByUser(ctx, userID, status, limit)
}

// canAccess reports whether a strategy is visible to a user: their own, or a
// Rail/shared strategy.
func (s *Service) canAccess(strategy *entities.InvestmentStrategy, userID uuid.UUID) bool {
	if strategy.OwnerType != entities.InvestmentOwnerUser && strategy.OwnerType != entities.InvestmentOwnerUserPortfolio {
		return true
	}
	return strategy.UserID != nil && *strategy.UserID == userID
}

// canMutate reports whether a user may change a strategy.
func (s *Service) canMutate(strategy *entities.InvestmentStrategy, userID uuid.UUID) bool {
	if strategy.OwnerType == entities.InvestmentOwnerGliderPublic || strategy.OwnerType == entities.InvestmentOwnerRail {
		return false
	}
	return strategy.UserID != nil && *strategy.UserID == userID
}

// ---------------------------------------------------------------------------
// Confirmations (spec §13, §24)
// ---------------------------------------------------------------------------

// confirmationOutcome says whether the caller may proceed, or must first ask
// the user to confirm the exact payload.
type confirmationOutcome struct {
	Pending *entities.InvestmentPendingAction
	Proceed bool
}

// confirmMutation enforces explicit confirmation for a mutation. When token is
// empty the payload is staged and a token is returned; when token is supplied it
// must match the same payload hash, be unexpired and unused.
func (s *Service) confirmMutation(
	ctx context.Context,
	userID uuid.UUID,
	action string,
	payload any,
	decision *entities.InvestmentPolicyDecision,
	preview any,
	token string,
) (confirmationOutcome, error) {
	hash, err := hashPayload(payload)
	if err != nil {
		return confirmationOutcome{}, err
	}

	decisionVerdict := entities.InvestmentVerdictRequiresConfirmation
	if decision != nil {
		decisionVerdict = decision.Verdict
	}

	if strings.TrimSpace(token) == "" {
		ttl := s.cfg.ConfirmationTTL
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		// Idempotent staging: an identical proposal that is already awaiting
		// confirmation reuses its token. Without this, a retried (or
		// duplicated) request mints a second live token and confirming both
		// would execute the action twice.
		if existing, err := s.confirmations.FindPending(ctx, userID, action, hash); err == nil && existing != nil {
			return confirmationOutcome{
				Proceed: false,
				Pending: &entities.InvestmentPendingAction{
					Token:       existing.Token,
					Action:      action,
					PayloadHash: hash,
					ExpiresAt:   existing.ExpiresAt,
					Instruction: "ask the user to confirm this exact proposal, then repeat the call with confirmation_token set",
				},
			}, nil
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return confirmationOutcome{}, fmt.Errorf("encode confirmation payload: %w", err)
		}
		var previewJSON []byte
		if preview != nil {
			previewJSON, _ = json.Marshal(preview)
		}
		confirmation := &entities.InvestmentConfirmation{
			ID:         uuid.New(),
			UserID:     userID,
			Token:      uuid.NewString(),
			Action:     action,
			ActionHash: hash,
			Payload:    payloadJSON,
			Preview:    previewJSON,
			Verdict:    decisionVerdict,
			ExpiresAt:  s.nowOr().Add(ttl),
			CreatedAt:  s.nowOr(),
		}
		if err := s.confirmations.Create(ctx, confirmation); err != nil {
			return confirmationOutcome{}, fmt.Errorf("store confirmation: %w", err)
		}
		_ = s.recordEvent(ctx, userID, EventConfirmationRequest, entities.InvestmentActorSystem, map[string]any{
			"action": action,
			"hash":   hash,
		})
		return confirmationOutcome{
			Proceed: false,
			Pending: &entities.InvestmentPendingAction{
				Token:       confirmation.Token,
				Action:      action,
				PayloadHash: hash,
				ExpiresAt:   confirmation.ExpiresAt,
				Instruction: "ask the user to confirm this exact proposal, then repeat the call with confirmation_token set",
			},
		}, nil
	}

	stored, err := s.confirmations.GetByToken(ctx, token)
	if err != nil {
		return confirmationOutcome{}, fmt.Errorf("%w: %v", ErrConfirmationInvalid, err)
	}
	if stored == nil || stored.UserID != userID {
		return confirmationOutcome{}, fmt.Errorf("%w: unknown token", ErrConfirmationInvalid)
	}
	if stored.ConsumedAt != nil {
		return confirmationOutcome{}, fmt.Errorf("%w: already used", ErrConfirmationInvalid)
	}
	if s.nowOr().After(stored.ExpiresAt) {
		return confirmationOutcome{}, fmt.Errorf("%w: expired", ErrConfirmationInvalid)
	}
	if stored.Action != action || stored.ActionHash != hash {
		return confirmationOutcome{}, fmt.Errorf("%w: it was issued for a different proposal", ErrConfirmationInvalid)
	}
	if err := s.confirmations.Consume(ctx, token, s.nowOr()); err != nil {
		return confirmationOutcome{}, fmt.Errorf("consume confirmation: %w", err)
	}
	return confirmationOutcome{Proceed: true}, nil
}

// hashPayload produces a stable hash of a request payload. It deliberately
// hashes the canonical JSON of the validated struct so that any change to the
// amount, allocation or target after confirmation invalidates the token.
func hashPayload(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode payload: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// ---------------------------------------------------------------------------
// Audit trail (spec §22, §23)
// ---------------------------------------------------------------------------

func (s *Service) recordStrategyEvent(
	ctx context.Context,
	userID uuid.UUID,
	eventType string,
	strategy *entities.InvestmentStrategy,
	actor entities.InvestmentActor,
	payload map[string]any,
) error {
	event := &entities.InvestmentAuditEvent{
		ID:        uuid.New(),
		UserID:    userID,
		EventType: eventType,
		Actor:     actor,
		CreatedAt: s.nowOr(),
	}
	if strategy != nil {
		strategyID := strategy.ID
		version := strategy.CurrentVersion
		event.StrategyID = &strategyID
		event.StrategyVersion = &version
		if event.UserID == uuid.Nil && strategy.UserID != nil {
			event.UserID = *strategy.UserID
		}
	}
	if payload != nil {
		if encoded, err := json.Marshal(payload); err == nil {
			event.Payload = encoded
		}
	}
	return s.recordEventEntity(ctx, event)
}

func (s *Service) recordEvent(
	ctx context.Context,
	userID uuid.UUID,
	eventType string,
	actor entities.InvestmentActor,
	payload map[string]any,
) error {
	event := &entities.InvestmentAuditEvent{
		ID:        uuid.New(),
		UserID:    userID,
		EventType: eventType,
		Actor:     actor,
		CreatedAt: s.nowOr(),
	}
	if payload != nil {
		if encoded, err := json.Marshal(payload); err == nil {
			event.Payload = encoded
		}
	}
	return s.recordEventEntity(ctx, event)
}

func (s *Service) recordEventEntity(ctx context.Context, event *entities.InvestmentAuditEvent) error {
	if s.audit == nil {
		return nil
	}
	if event.UserID == uuid.Nil {
		return nil
	}
	if err := s.audit.Record(ctx, event); err != nil {
		// Audit must never break the money path, but it must never be silent.
		s.log.Error("failed to record investment audit event",
			"event_type", event.EventType,
			"user_id", event.UserID.String(),
			"error", err)
		return err
	}
	return nil
}

// ListAuditEvents returns the investment event trail for a user.
func (s *Service) ListAuditEvents(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.InvestmentAuditEvent, error) {
	if s.audit == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	return s.audit.ListByUser(ctx, userID, limit)
}

// providerError is the structural contract the provider adapter's error type
// satisfies. The domain deliberately does not import infrastructure, so it
// detects provider conditions by shape rather than by concrete type.
type providerError interface {
	IsConflict() bool
	IsCooldown() bool
}

// mapProviderError translates provider errors into domain errors, preserving
// the cooldown signal the worker needs.
func (s *Service) mapProviderError(err error) error {
	if err == nil {
		return nil
	}
	var pe providerError
	if errors.As(err, &pe) {
		switch {
		case pe.IsConflict():
			return fmt.Errorf("%w: %v", ErrProviderConflict, err)
		case pe.IsCooldown():
			return fmt.Errorf("%w: %v", ErrProviderCooldown, err)
		}
	}
	return err
}

// ErrUnsupported is returned for behaviour that is deliberately deferred.
var ErrUnsupported = errors.New("investment: not supported yet")
