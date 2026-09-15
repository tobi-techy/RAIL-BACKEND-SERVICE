package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// CreateStrategy validates then (after confirmation) persists a new strategy
// with version 1 and a matching provider strategy.
//
// The AI proposes; this method decides whether the proposal is allowed.
func (s *Service) CreateStrategy(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.InvestmentCreateStrategyRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentCreateStrategyResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("%w: a strategy needs a name", ErrValidationFailed)
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}

	rules := req.RebalanceRules
	// A threshold or hybrid strategy has to be allowed to sell, otherwise the
	// portfolio can never converge back to target.
	if rules.Type == "" {
		rules.Type = "contribution"
	}
	if !rules.AllowSells && (rules.Type == "threshold" || rules.Type == "hybrid") {
		rules.AllowSells = true
	}
	if !rules.PreferContributions && rules.Type == "contribution" {
		rules.PreferContributions = true
	}

	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:        req.TargetAllocation,
		AmountUSD:   decimal.Zero,
		Limits:      *limits,
		Constraints: entities.InvestmentPlanConstraints{},
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		_ = s.recordEvent(ctx, userID, EventPolicyBlocked, actor, map[string]any{
			"action":     "create_strategy",
			"violations": report.Violations,
		})
		return &entities.InvestmentCreateStrategyResponse{
			Status:     entities.InvestmentActionRejected,
			Validation: report,
		}, fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}

	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID: userID,
		Action: PolicyActionCreateStrategy,
		Risk:   req.Risk,
		Limits: *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		if decision.Verdict == entities.InvestmentVerdictNotSupported {
			_ = s.recordEvent(ctx, userID, EventPolicyBlocked, actor, map[string]any{
				"action": "create_strategy",
				"reason": decision.Reasons,
			})
			return &entities.InvestmentCreateStrategyResponse{Status: entities.InvestmentActionRejected, Policy: decision},
				fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
		}
		return &entities.InvestmentCreateStrategyResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	// The confirmation binds to the proposal, never to the token itself, so the
	// token field is cleared before the payload is hashed.
	sanitized := *req
	sanitized.ConfirmationToken = ""
	outcome, err := s.confirmMutation(ctx, userID, "create_strategy", sanitized, decision, report, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentCreateStrategyResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Validation:   report,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	now := s.nowOr()
	strategy := &entities.InvestmentStrategy{
		ID:             uuid.New(),
		UserID:         &userID,
		OwnerType:      entities.InvestmentOwnerUser,
		Name:           strings.TrimSpace(req.Name),
		Description:    req.Description,
		Objective:      req.Objective,
		Risk:           req.Risk,
		Horizon:        req.Horizon,
		Status:         entities.InvestmentStrategyUserReview,
		CurrentVersion: 1,
		CreatedBy:      actor,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// The provider strategy is created with the normalized allocation so the
	// provider never receives weights it would reject.
	created, err := s.provider.CreateStrategy(ctx, entities.GliderStrategyInput{
		Name:        strategy.Name,
		Allocation:  entities.GliderAllocation{Assets: report.NormalizedAllocation},
		Schedule:    &entities.GliderSchedule{Type: "interval", Frequency: providerFrequency(rules)},
		Preferences: s.preferencesFor(entities.InvestmentExecutionRules{}),
	})
	if err != nil {
		return nil, fmt.Errorf("create provider strategy: %w", s.mapProviderError(err))
	}
	strategy.GliderStrategyID = &created.StrategyID

	if err := s.strategies.Create(ctx, strategy); err != nil {
		return nil, fmt.Errorf("store strategy: %w", err)
	}

	version := &entities.InvestmentStrategyVersion{
		ID:                uuid.New(),
		StrategyID:        strategy.ID,
		Version:           1,
		TargetAllocation:  report.NormalizedAllocation,
		Risk:              req.Risk,
		Horizon:           req.Horizon,
		RebalanceRules:    rules,
		ContributionRules: req.ContributionRules,
		ExecutionRules:    req.ExecutionRules,
		Rationale:         req.Rationale,
		ValidationReport:  report,
		CreatedBy:         actor,
		CreatedAt:         now,
	}
	gliderVersion := created.Version
	version.GliderStrategyVersion = &gliderVersion
	if err := s.strategies.CreateVersion(ctx, version); err != nil {
		return nil, fmt.Errorf("store strategy version: %w", err)
	}

	_ = s.recordStrategyEvent(ctx, userID, EventStrategyCreated, strategy, actor, map[string]any{
		"version":      1,
		"allocation":   report.NormalizedAllocation,
		"rationale":    req.Rationale,
		"objective":    req.Objective,
		"risk":         req.Risk,
		"horizon":      req.Horizon,
		"policy":       decision.Verdict,
		"confirmed_by": actor,
	})

	return &entities.InvestmentCreateStrategyResponse{
		Status:     entities.InvestmentActionCompleted,
		Strategy:   strategy,
		Version:    version,
		Validation: report,
		Policy:     decision,
	}, nil
}

// PublishStrategyVersion appends a new immutable version and re-targets the
// provider strategy. Active strategies are never mutated in place.
func (s *Service) PublishStrategyVersion(
	ctx context.Context,
	userID, strategyID uuid.UUID,
	req *entities.InvestmentUpdateStrategyRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentCreateStrategyResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canMutate(strategy, userID) {
		return nil, ErrNotFound
	}
	if strategy.Status == entities.InvestmentStrategyClosed {
		return nil, fmt.Errorf("%w: this strategy is closed", ErrValidationFailed)
	}
	if req == nil {
		return nil, fmt.Errorf("%w: missing allocation", ErrValidationFailed)
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	current, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, fmt.Errorf("get current version: %w", err)
	}
	if current == nil {
		return nil, ErrNotFound
	}

	rules := current.RebalanceRules
	if req.RebalanceRules != nil {
		rules = *req.RebalanceRules
	}
	contributions := current.ContributionRules
	if req.ContributionRules != nil {
		contributions = *req.ContributionRules
	}
	execution := current.ExecutionRules
	if req.ExecutionRules != nil {
		execution = *req.ExecutionRules
	}

	value, err := s.portfolioValue(ctx, userID)
	if err != nil {
		return nil, err
	}
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:              req.TargetAllocation,
		Limits:            *limits,
		Constraints:       current.Constraints,
		PortfolioValueUSD: value,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return &entities.InvestmentCreateStrategyResponse{
			Status:     entities.InvestmentActionRejected,
			Validation: report,
		}, fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}

	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID: userID,
		Action: PolicyActionUpdateStrategy,
		Risk:   strategy.Risk,
		Limits: *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &entities.InvestmentCreateStrategyResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	sanitized := *req
	sanitized.ConfirmationToken = ""
	outcome, err := s.confirmMutation(ctx, userID, "update_strategy", sanitized, decision, report, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentCreateStrategyResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Validation:   report,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	var publishedVersion *int
	if strategy.GliderStrategyID != nil {
		published, err := s.provider.PublishStrategyVersion(ctx, *strategy.GliderStrategyID, entities.GliderStrategyInput{
			Allocation:  entities.GliderAllocation{Assets: report.NormalizedAllocation},
			Schedule:    &entities.GliderSchedule{Type: "interval", Frequency: providerFrequency(rules)},
			Preferences: s.preferencesFor(execution),
		})
		if err != nil {
			return nil, fmt.Errorf("publish provider version: %w", s.mapProviderError(err))
		}
		if published.Version > 0 {
			value := published.Version
			publishedVersion = &value
		}
	}

	now := s.nowOr()
	version := &entities.InvestmentStrategyVersion{
		ID:                    uuid.New(),
		StrategyID:            strategy.ID,
		Version:               strategy.CurrentVersion + 1,
		TargetAllocation:      report.NormalizedAllocation,
		Risk:                  strategy.Risk,
		Horizon:               strategy.Horizon,
		RebalanceRules:        rules,
		ContributionRules:     contributions,
		ExecutionRules:        execution,
		Constraints:           current.Constraints,
		Rationale:             req.Rationale,
		ValidationReport:      report,
		GliderStrategyVersion: publishedVersion,
		CreatedBy:             actor,
		CreatedAt:             now,
	}
	if err := s.strategies.CreateVersion(ctx, version); err != nil {
		return nil, fmt.Errorf("store strategy version: %w", err)
	}
	strategy.CurrentVersion = version.Version
	strategy.Status = entities.InvestmentStrategyUpdated
	strategy.UpdatedAt = now
	if err := s.strategies.Update(ctx, strategy); err != nil {
		return nil, fmt.Errorf("update strategy: %w", err)
	}
	// The strategy is active again once the new version is published.
	_ = s.transitionStrategy(ctx, strategy, entities.InvestmentStrategyActive)

	_ = s.recordStrategyEvent(ctx, userID, EventStrategyVersioned, strategy, actor, map[string]any{
		"version":    version.Version,
		"allocation": report.NormalizedAllocation,
		"rationale":  req.Rationale,
	})

	return &entities.InvestmentCreateStrategyResponse{
		Status:     entities.InvestmentActionCompleted,
		Strategy:   strategy,
		Version:    version,
		Validation: report,
		Policy:     decision,
	}, nil
}

// transitionStrategy moves a strategy through its lifecycle.
func (s *Service) transitionStrategy(ctx context.Context, strategy *entities.InvestmentStrategy, status entities.InvestmentStrategyStatus) error {
	if strategy == nil {
		return nil
	}
	strategy.Status = status
	strategy.UpdatedAt = s.nowOr()
	return s.strategies.Update(ctx, strategy)
}

// ---------------------------------------------------------------------------
// Rebalance preview (spec §8, §12)
// ---------------------------------------------------------------------------

// PreviewRebalance calculates the pre-trade picture for a strategy without
// changing anything. It is safe to call as often as the agent likes.
func (s *Service) PreviewRebalance(ctx context.Context, userID, strategyID uuid.UUID, amountUSD *decimal.Decimal) (*entities.InvestmentAllocationPreview, error) {
	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canAccess(strategy, userID) {
		return nil, ErrNotFound
	}
	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return nil, ErrNotFound
	}

	holdings, err := s.holdings.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}
	scoped := make([]*entities.InvestmentHolding, 0, len(holdings))
	enrollment, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategy.ID)
	if err != nil {
		return nil, fmt.Errorf("get enrollment: %w", err)
	}
	for _, holding := range holdings {
		if enrollment != nil && holding.EnrollmentID != enrollment.ID {
			continue
		}
		scoped = append(scoped, holding)
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	amount := decimal.Zero
	if amountUSD != nil {
		amount = *amountUSD
	}
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionRebalance,
		AmountUSD: amount,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}

	preview := s.previewer.Preview(PreviewInput{
		StrategyID:     strategy.ID.String(),
		Version:        version.Version,
		Target:         version.TargetAllocation,
		Current:        scoped,
		AmountUSD:      amount,
		Rules:          version.RebalanceRules,
		ExecutionRules: version.ExecutionRules,
		Policy:         decision,
	})
	_ = s.recordStrategyEvent(ctx, userID, EventRebalancePreviewed, strategy, entities.InvestmentActorMiriam, map[string]any{
		"version":     version.Version,
		"drift":       preview.DriftPct.String(),
		"trade_count": len(preview.ProposedTrades),
	})
	return preview, nil
}

// ---------------------------------------------------------------------------
// Lifecycle actions
// ---------------------------------------------------------------------------

// PauseStrategy stops provider automation for a user's enrollment.
func (s *Service) PauseStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	enrollment, strategy, err := s.enrollmentFor(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	if err := s.provider.StopPortfolio(ctx, enrollment.GliderPortfolioID); err != nil {
		return nil, fmt.Errorf("stop portfolio: %w", s.mapProviderError(err))
	}
	enrollment.Status = entities.InvestmentEnrollmentPaused
	enrollment.AutomationStatus = "stopped"
	enrollment.UpdatedAt = s.nowOr()
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return nil, fmt.Errorf("update enrollment: %w", err)
	}
	_ = s.recordStrategyEvent(ctx, userID, EventStrategyPaused, strategy, entities.InvestmentActorUser, nil)
	return enrollment, nil
}

// ResumeStrategy restarts provider automation.
func (s *Service) ResumeStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	enrollment, strategy, err := s.enrollmentFor(ctx, userID, strategyID)
	if err != nil {
		return nil, err
	}
	if err := s.provider.StartPortfolio(ctx, enrollment.GliderPortfolioID); err != nil {
		return nil, fmt.Errorf("start portfolio: %w", s.mapProviderError(err))
	}
	enrollment.Status = entities.InvestmentEnrollmentActive
	enrollment.AutomationStatus = "active"
	enrollment.UpdatedAt = s.nowOr()
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return nil, fmt.Errorf("update enrollment: %w", err)
	}
	_ = s.recordStrategyEvent(ctx, userID, EventStrategyResumed, strategy, entities.InvestmentActorUser, nil)
	return enrollment, nil
}

// enrollmentFor loads an enrollment plus its strategy for a user.
func (s *Service) enrollmentFor(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, *entities.InvestmentStrategy, error) {
	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canAccess(strategy, userID) {
		return nil, nil, ErrNotFound
	}
	enrollment, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategyID)
	if err != nil {
		return nil, nil, fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil {
		return nil, nil, fmt.Errorf("%w: you are not enrolled in this strategy", ErrNotFound)
	}
	return enrollment, strategy, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (s *Service) portfolioValue(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	holdings, err := s.holdings.ListByUser(ctx, userID)
	if err != nil {
		return decimal.Zero, fmt.Errorf("list holdings: %w", err)
	}
	total := decimal.Zero
	for _, holding := range holdings {
		if holding == nil {
			continue
		}
		total = total.Add(holding.ValueUSD)
	}
	return total, nil
}

func (s *Service) preferencesFor(rules entities.InvestmentExecutionRules) *entities.GliderPreferencesWire {
	prefs := &entities.GliderSwapPreferences{
		SlippageBps:    rules.SlippageBps,
		PriceImpactBps: rules.PriceImpactBps,
		ThresholdUSD:   rules.ThresholdUSD,
	}
	if prefs.SlippageBps == nil && s.cfg.DefaultSlippageBps > 0 {
		slippage := s.cfg.DefaultSlippageBps
		prefs.SlippageBps = &slippage
	}
	// Omit everything when nothing meaningful is configured so the provider
	// applies its own defaults rather than our guesses.
	if prefs.SlippageBps == nil && prefs.PriceImpactBps == nil && prefs.ThresholdUSD == nil {
		return nil
	}
	return &entities.GliderPreferencesWire{Swap: prefs}
}

func providerFrequency(rules entities.InvestmentRebalanceRules) string {
	switch strings.ToLower(rules.Frequency) {
	case "hourly", "daily", "weekly", "monthly":
		return strings.ToLower(rules.Frequency)
	case "":
		return "daily"
	default:
		return "daily"
	}
}

func firstViolation(report *entities.InvestmentValidationReport) string {
	if report == nil || len(report.Violations) == 0 {
		return "the proposal was rejected"
	}
	return report.Violations[0].Message
}
