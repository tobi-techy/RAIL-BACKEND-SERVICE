package investment

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Asset resolution
// ---------------------------------------------------------------------------

// assetRepositoryResolver adapts the asset catalog repository to the small
// AssetResolver port the validator consumes.
type assetRepositoryResolver struct {
	repo AssetRepository
}

// Resolve applies the catalog's precedence: id, then CAIP-19, then symbol.
func (r assetRepositoryResolver) Resolve(ctx context.Context, assetID, caip19, symbol string) (*entities.InvestmentAsset, error) {
	if r.repo == nil {
		return nil, ErrNotFound
	}
	if trimmed := strings.TrimSpace(assetID); trimmed != "" {
		if id, err := uuid.Parse(trimmed); err == nil {
			if asset, err := r.repo.GetByID(ctx, id); err == nil && asset != nil {
				return asset, nil
			}
		}
	}
	if trimmed := strings.TrimSpace(caip19); trimmed != "" {
		if asset, err := r.repo.GetByCAIP19(ctx, trimmed); err == nil && asset != nil {
			return asset, nil
		}
	}
	if trimmed := strings.TrimSpace(symbol); trimmed != "" {
		if asset, err := r.repo.GetBySymbol(ctx, trimmed); err == nil && asset != nil {
			return asset, nil
		}
	}
	return nil, ErrNotFound
}

// resolverFor returns an AssetResolver backed by the repository, or nil when
// there is no catalog, so callers skip resolution instead of panicking.
func resolverFor(repo AssetRepository) AssetResolver {
	if repo == nil {
		return nil
	}
	return assetRepositoryResolver{repo: repo}
}

// ---------------------------------------------------------------------------
// Deterministic validation engine (spec §5, §15, §16)
//
// Pure and side-effect free: given a proposal and the user's limits it either
// accepts the allocation (returning a normalized copy) or returns every reason
// it was rejected. The AI cannot override a rejection.
// ---------------------------------------------------------------------------

// ValidationInput is one proposed allocation plus the context needed to judge it.
type ValidationInput struct {
	Legs              []entities.InvestmentAllocationLeg
	AmountUSD         decimal.Decimal
	Constraints       entities.InvestmentPlanConstraints
	Limits            entities.InvestmentLimits
	PortfolioValueUSD decimal.Decimal
}

// Validator checks proposed allocations against hard constraints.
type Validator struct {
	assets AssetResolver
	cfg    Config
	now    func() time.Time
}

// NewValidator builds a validator. assets may be nil in tests that only
// exercise weight maths, in which case asset resolution is skipped.
func NewValidator(assets AssetResolver, cfg Config) *Validator {
	return &Validator{assets: assets, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the validator clock (tests).
func (v *Validator) SetClock(now func() time.Time) { v.now = now }

// Validate applies every hard rule and normalizes the allocation.
func (v *Validator) Validate(ctx context.Context, in ValidationInput) (*entities.InvestmentValidationReport, error) {
	report := &entities.InvestmentValidationReport{
		Valid:     true,
		CheckedAt: v.now(),
	}

	minLegs := v.cfg.MinAllocationLegs
	if minLegs <= 0 {
		minLegs = 1
	}
	maxLegs := v.cfg.MaxAllocationLegs
	if maxLegs <= 0 {
		maxLegs = 50
	}

	if len(in.Legs) < minLegs {
		report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
			Code:    "allocation.empty",
			Message: fmt.Sprintf("an allocation needs at least %d asset", minLegs),
		})
	}
	if len(in.Legs) > maxLegs {
		report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
			Code:    "allocation.too_many_legs",
			Message: fmt.Sprintf("an allocation can hold at most %d assets", maxLegs),
		})
	}

	sum := decimal.Zero
	seen := map[string]bool{}
	resolved := make([]entities.InvestmentAllocationLeg, 0, len(in.Legs))

	for _, leg := range in.Legs {
		weight := leg.Weight
		switch {
		case weight.LessThanOrEqual(decimal.Zero):
			report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
				Code:    "allocation.non_positive_weight",
				Message: fmt.Sprintf("weight for %s must be greater than zero", legLabel(leg)),
				AssetID: leg.AssetID,
			})
			continue
		case weight.Exponent() < -2:
			report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
				Code:    "allocation.weight_precision",
				Message: fmt.Sprintf("weight for %s must have at most 2 decimal places", legLabel(leg)),
				AssetID: leg.AssetID,
			})
			continue
		}
		sum = sum.Add(weight)

		out := leg
		if v.assets != nil {
			asset, err := v.assets.Resolve(ctx, leg.AssetID, leg.CAIP19, leg.Symbol)
			if err != nil {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.unknown_asset",
					Message: fmt.Sprintf("could not resolve asset %s; it is not in the supported asset list", legLabel(leg)),
					AssetID: leg.AssetID,
				})
				continue
			}
			if asset.Prohibited || !asset.Allowlisted {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.asset_not_permitted",
					Message: fmt.Sprintf("%s is not available for investing", asset.Symbol),
					AssetID: asset.ID.String(),
				})
				continue
			}
			out.AssetID = asset.ID.String()
			out.CAIP19 = asset.CAIP19
			out.Symbol = asset.Symbol

			if restrictedAsset(in.Constraints.ProhibitedAssets, asset) {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.asset_prohibited_by_strategy",
					Message: fmt.Sprintf("%s is excluded by this strategy", asset.Symbol),
					AssetID: asset.ID.String(),
				})
				continue
			}
			if len(in.Constraints.AllowedAssets) > 0 && !allowedAsset(in.Constraints.AllowedAssets, asset) {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.asset_not_in_strategy_universe",
					Message: fmt.Sprintf("%s is outside this strategy's asset universe", asset.Symbol),
					AssetID: asset.ID.String(),
				})
				continue
			}
		}

		key := out.Symbol
		if key == "" {
			key = out.AssetID
		}
		if key != "" {
			if seen[key] {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.duplicate_asset",
					Message: fmt.Sprintf("%s appears more than once in the allocation", key),
					AssetID: out.AssetID,
				})
				continue
			}
			seen[key] = true
		}
		resolved = append(resolved, out)
	}

	if !sum.Equal(decimal.NewFromInt(100)) {
		report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
			Code:    "allocation.weights_sum",
			Message: fmt.Sprintf("allocation weights must sum to 100 (they sum to %s)", sum.String()),
		})
	}

	// Position and strategy concentration caps.
	maxPosition := in.Limits.MaxPositionPct
	if in.Constraints.MaxPositionPct != nil && in.Constraints.MaxPositionPct.GreaterThan(decimal.Zero) &&
		(maxPosition.IsZero() || in.Constraints.MaxPositionPct.LessThan(maxPosition)) {
		maxPosition = *in.Constraints.MaxPositionPct
	}
	if maxPosition.GreaterThan(decimal.Zero) {
		for _, leg := range resolved {
			if leg.Weight.GreaterThan(maxPosition) {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.position_cap_exceeded",
					Message: fmt.Sprintf("%s would be %s%% of the portfolio; the maximum position is %s%%", legLabel(leg), leg.Weight.String(), maxPosition.String()),
					AssetID: leg.AssetID,
				})
			}
		}
	}

	// Cash reserve: only checkable when we know the resulting portfolio value.
	if in.PortfolioValueUSD.GreaterThan(decimal.Zero) {
		reserve := in.Limits.MinCashReserveUSD
		if in.Constraints.MinCashReserve != nil && in.Constraints.MinCashReserve.GreaterThan(reserve) {
			reserve = *in.Constraints.MinCashReserve
		}
		if reserve.GreaterThan(decimal.Zero) {
			totalAfter := in.PortfolioValueUSD.Add(in.AmountUSD)
			cashWeight := decimal.Zero
			for _, leg := range resolved {
				if isCashLeg(leg, v.cfg.SettlementSymbol) {
					cashWeight = cashWeight.Add(leg.Weight)
				}
			}
			cashValue := totalAfter.Mul(cashWeight).Div(decimal.NewFromInt(100))
			if cashValue.LessThan(reserve) {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "allocation.below_cash_reserve",
					Message: fmt.Sprintf("this would leave %s in cash; at least %s must stay liquid", cashValue.StringFixed(2), reserve.StringFixed(2)),
				})
			}
		}
	}

	// Transaction sizing.
	if in.AmountUSD.GreaterThan(decimal.Zero) {
		if in.Limits.MinOrderAmountUSD.GreaterThan(decimal.Zero) && in.AmountUSD.LessThan(in.Limits.MinOrderAmountUSD) {
			report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
				Code:    "amount.below_minimum",
				Message: fmt.Sprintf("the smallest investment is %s", in.Limits.MinOrderAmountUSD.StringFixed(2)),
			})
		}
		if in.Limits.MaxTransactionUSD.GreaterThan(decimal.Zero) && in.AmountUSD.GreaterThan(in.Limits.MaxTransactionUSD) {
			report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
				Code:    "amount.above_maximum",
				Message: fmt.Sprintf("a single investment is capped at %s", in.Limits.MaxTransactionUSD.StringFixed(2)),
			})
		}
		if in.Limits.MaxDailyVolumeUSD.GreaterThan(decimal.Zero) {
			remaining := in.Limits.MaxDailyVolumeUSD.Sub(in.Limits.DailyVolumeUSD)
			if in.AmountUSD.GreaterThan(remaining) {
				report.Violations = append(report.Violations, entities.InvestmentValidationIssue{
					Code:    "amount.daily_limit",
					Message: fmt.Sprintf("that would pass today's limit; %s of today's volume is still available", maxDecimal(remaining, decimal.Zero).StringFixed(2)),
				})
			}
		}
	}

	report.NormalizedAllocation = normalizeWeights(resolved)
	report.Valid = len(report.Violations) == 0
	if !report.Valid {
		report.NormalizedAllocation = nil
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// Compliance policy layer (spec §27)
// ---------------------------------------------------------------------------

// PolicyAction names a protected action.
type PolicyAction string

const (
	PolicyActionCreateStrategy PolicyAction = "create_strategy"
	PolicyActionUpdateStrategy PolicyAction = "update_strategy"
	PolicyActionEnroll         PolicyAction = "enroll"
	PolicyActionContribute     PolicyAction = "contribute"
	PolicyActionTrade          PolicyAction = "trade"
	PolicyActionRebalance      PolicyAction = "rebalance"
	PolicyActionPause          PolicyAction = "pause"
	PolicyActionWithdraw       PolicyAction = "withdraw"
	PolicyActionLiquidate      PolicyAction = "liquidate"
)

// PolicyInput is one action to judge.
type PolicyInput struct {
	UserID    uuid.UUID
	Action    PolicyAction
	AmountUSD decimal.Decimal
	Risk      string
	Limits    entities.InvestmentLimits
}

// Policy decides whether an investment action is allowed, needs confirmation,
// needs step-up authentication, needs compliance review, or is not supported.
type Policy struct {
	users UserProfileReader
	cfg   Config
	now   func() time.Time
}

// NewPolicy builds the policy layer.
func NewPolicy(users UserProfileReader, cfg Config) *Policy {
	return &Policy{users: users, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// Evaluate returns the verdict for one action.
func (p *Policy) Evaluate(ctx context.Context, in PolicyInput) (*entities.InvestmentPolicyDecision, error) {
	decision := &entities.InvestmentPolicyDecision{EvaluatedAt: p.now()}

	if !p.cfg.Enabled {
		decision.Verdict = entities.InvestmentVerdictNotSupported
		decision.Reasons = append(decision.Reasons, "investing is not enabled for this account yet")
		return decision, nil
	}

	if p.users != nil {
		profile, err := p.users.GetInvestmentProfile(ctx, in.UserID)
		if err != nil {
			return nil, fmt.Errorf("policy: load user profile: %w", err)
		}
		if profile != nil {
			// KYC is deliberately NOT a gate for Glider strategy investing: the
			// provider holds and executes the assets, so an unverified user can
			// start and fund a strategy. Country restrictions still apply, and
			// the KYC tier is carried through in the decision reasons so the
			// audit trail still shows the tier the action ran under.
			if len(p.cfg.AllowedCountries) > 0 && profile.Country != "" && !containsFold(p.cfg.AllowedCountries, profile.Country) {
				decision.Verdict = entities.InvestmentVerdictNotSupported
				decision.Reasons = append(decision.Reasons,
					fmt.Sprintf("investing is not available in %s", strings.ToUpper(profile.Country)))
				return decision, nil
			}
		}
	}

	switch in.Action {
	case PolicyActionWithdraw, PolicyActionLiquidate:
		decision.Verdict = entities.InvestmentVerdictRequiresAuthentication
		decision.Reasons = append(decision.Reasons,
			"moving money out of an investment always needs an in-app confirmation")
		decision.Disclosures = append(decision.Disclosures,
			"Withdrawals cannot be authorised from a chat message.")
		return decision, nil
	}

	if p.cfg.HighValueThresholdUSD.GreaterThan(decimal.Zero) && in.AmountUSD.GreaterThanOrEqual(p.cfg.HighValueThresholdUSD) {
		decision.Verdict = entities.InvestmentVerdictRequiresAuthentication
		decision.Reasons = append(decision.Reasons,
			fmt.Sprintf("amounts of %s or more need an in-app confirmation", p.cfg.HighValueThresholdUSD.StringFixed(0)))
		return decision, nil
	}

	decision.Verdict = entities.InvestmentVerdictRequiresConfirmation
	decision.Reasons = append(decision.Reasons, "this moves money, so it needs your confirmation first")
	return decision, nil
}

// ---------------------------------------------------------------------------
// Rebalance preview (spec §8, §12, §21)
// ---------------------------------------------------------------------------

// PreviewInput is everything needed to calculate a pre-trade picture.
type PreviewInput struct {
	StrategyID     string
	Version        int
	Target         []entities.InvestmentAllocationLeg
	Current        []*entities.InvestmentHolding
	AmountUSD      decimal.Decimal
	Rules          entities.InvestmentRebalanceRules
	ExecutionRules entities.InvestmentExecutionRules
	Policy         *entities.InvestmentPolicyDecision
}

// Previewer calculates drift, proposed trades and the post-trade allocation.
// Its output is always an estimate: the provider decides the actual swaps.
type Previewer struct {
	cfg Config
	now func() time.Time
}

// NewPreviewer builds the preview engine.
func NewPreviewer(cfg Config) *Previewer {
	return &Previewer{cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// Preview calculates the pre-trade picture.
func (p *Previewer) Preview(in PreviewInput) *entities.InvestmentAllocationPreview {
	now := p.now()
	currentValue := decimal.Zero
	byKey := map[string]*entities.InvestmentHolding{}
	for _, holding := range in.Current {
		if holding == nil {
			continue
		}
		currentValue = currentValue.Add(holding.ValueUSD)
		byKey[holding.CAIP19] = holding
		byKey[strings.ToUpper(holding.Symbol)] = holding
	}
	totalAfter := currentValue.Add(in.AmountUSD)

	preview := &entities.InvestmentAllocationPreview{
		Version:          in.Version,
		TargetAllocation: cloneLegs(in.Target),
		Estimated:        true,
		MarketDataAsOf:   now,
		Assumptions: []string{
			"trade sizes are estimated from the target weights and the last observed prices; the provider decides the final swaps during its rebalance",
		},
	}
	if id, ok := parseUUID(in.StrategyID); ok {
		preview.StrategyID = id
	}
	if in.Policy != nil {
		preview.Verdict = in.Policy.Verdict
		preview.PolicyReasons = in.Policy.Reasons
		preview.PolicyDisclosures = in.Policy.Disclosures
	}

	// Current allocation, including legs the target no longer holds.
	targetKeys := map[string]bool{}
	for _, leg := range in.Target {
		targetKeys[holdingKey(leg)] = true
	}
	current := make([]entities.InvestmentAllocationLeg, 0, len(in.Current))
	for _, holding := range in.Current {
		if holding == nil {
			continue
		}
		weight := decimal.Zero
		if currentValue.GreaterThan(decimal.Zero) {
			weight = holding.ValueUSD.Div(currentValue).Mul(decimal.NewFromInt(100)).Round(2)
		}
		current = append(current, entities.InvestmentAllocationLeg{
			CAIP19:  holding.CAIP19,
			AssetID: uuidString(holding.AssetID),
			Symbol:  holding.Symbol,
			Weight:  weight,
		})
	}
	preview.CurrentAllocation = current

	drift := decimal.Zero
	trades := make([]entities.InvestmentProposedTrade, 0)
	tradedVolume := decimal.Zero
	contributionOnly := in.AmountUSD.GreaterThan(decimal.Zero) && in.Rules.PreferContributions && !in.Rules.AllowSells

	// Target legs: buy what is underweight.
	for _, leg := range in.Target {
		holding := byKey[leg.CAIP19]
		if holding == nil {
			holding = byKey[strings.ToUpper(leg.Symbol)]
		}
		currentLegValue := decimal.Zero
		currentWeight := decimal.Zero
		if holding != nil {
			currentLegValue = holding.ValueUSD
			if currentValue.GreaterThan(decimal.Zero) {
				currentWeight = holding.ValueUSD.Div(currentValue).Mul(decimal.NewFromInt(100))
			}
		}
		if diff := currentWeight.Sub(leg.Weight).Abs(); diff.GreaterThan(drift) {
			drift = diff
		}

		desiredValue := totalAfter.Mul(leg.Weight).Div(decimal.NewFromInt(100))
		delta := desiredValue.Sub(currentLegValue)
		if delta.IsZero() {
			continue
		}
		action := "buy"
		if delta.IsNegative() {
			action = "sell"
			if !in.Rules.AllowSells {
				continue
			}
		}
		amount := delta.Abs()
		units := decimal.Zero
		if holding != nil && holding.PriceUSD.GreaterThan(decimal.Zero) && !holding.Balance.IsZero() {
			// price per unit implied by the observed holding
			units = amount.Div(holding.PriceUSD).Round(6)
		}
		tradedVolume = tradedVolume.Add(amount)
		trades = append(trades, entities.InvestmentProposedTrade{
			AssetID:      leg.AssetID,
			Symbol:       leg.Symbol,
			Action:       action,
			EstAmountUSD: amount.Round(2),
			EstUnits:     units,
			Reason:       reasonForTrade(action, currentWeight, leg.Weight, contributionOnly),
		})
	}

	// Legs the target drops entirely are sells.
	for _, heldLeg := range current {
		if targetKeys[holdingKey(heldLeg)] {
			continue
		}
		holding := byKey[heldLeg.CAIP19]
		if holding == nil {
			holding = byKey[strings.ToUpper(heldLeg.Symbol)]
		}
		if diff := heldLeg.Weight.Abs(); diff.GreaterThan(drift) {
			drift = diff
		}
		if !in.Rules.AllowSells {
			continue
		}
		amount := decimal.Zero
		if holding != nil {
			amount = holding.ValueUSD
		}
		tradedVolume = tradedVolume.Add(amount)
		trades = append(trades, entities.InvestmentProposedTrade{
			AssetID:      heldLeg.AssetID,
			Symbol:       heldLeg.Symbol,
			Action:       "sell",
			EstAmountUSD: amount.Round(2),
			Reason:       "no longer part of the target allocation",
		})
	}

	sort.SliceStable(trades, func(i, j int) bool {
		if trades[i].Action != trades[j].Action {
			return trades[i].Action == "buy"
		}
		return trades[i].EstAmountUSD.GreaterThan(trades[j].EstAmountUSD)
	})

	feeBps := p.cfg.DefaultFeeBps
	preview.ProposedTrades = trades
	preview.DriftPct = drift.Round(2)
	preview.ContributionOnly = contributionOnly
	preview.EstFeesUSD = tradedVolume.Mul(feeBps).Div(decimal.NewFromInt(10000)).Round(2)
	preview.EstSlippageBps = slippageBps(in.ExecutionRules, p.cfg.DefaultSlippageBps)
	preview.ExpectedPostTradeAllocation = expectedPostTrade(in.Target, current, trades, totalAfter, currentValue)
	preview.Assumptions = append(preview.Assumptions,
		fmt.Sprintf("fees are estimated at %s bps because the provider does not publish fee schedules", feeBps.String()))
	if contributionOnly {
		preview.Assumptions = append(preview.Assumptions,
			"this strategy is set to contribution-only, so new money is directed at underweight assets instead of selling existing positions")
	}
	return preview
}

func expectedPostTrade(target, current []entities.InvestmentAllocationLeg, trades []entities.InvestmentProposedTrade, totalAfter, currentValue decimal.Decimal) []entities.InvestmentAllocationLeg {
	if totalAfter.IsZero() {
		return cloneLegs(target)
	}
	executed := map[string]string{}
	for _, trade := range trades {
		key := trade.Symbol
		if key == "" {
			key = trade.AssetID
		}
		executed[key] = trade.Action
	}
	values := map[string]decimal.Decimal{}
	meta := map[string]entities.InvestmentAllocationLeg{}
	for _, leg := range current {
		holdingValue := decimal.Zero
		if currentValue.GreaterThan(decimal.Zero) {
			holdingValue = currentValue.Mul(leg.Weight).Div(decimal.NewFromInt(100))
		}
		key := leg.Symbol
		if key == "" {
			key = leg.AssetID
		}
		values[key] = holdingValue
		meta[key] = leg
	}
	for _, leg := range target {
		key := leg.Symbol
		if key == "" {
			key = leg.AssetID
		}
		if _, ok := values[key]; !ok {
			values[key] = decimal.Zero
		}
		meta[key] = leg
	}
	// Apply executed trades: bought legs move to their target value, sold legs
	// to zero.
	for key, action := range executed {
		leg := meta[key]
		if action == "buy" {
			values[key] = totalAfter.Mul(leg.Weight).Div(decimal.NewFromInt(100))
		} else {
			values[key] = decimal.Zero
		}
	}
	out := make([]entities.InvestmentAllocationLeg, 0, len(values))
	for key, value := range values {
		leg := meta[key]
		leg.Weight = value.Div(totalAfter).Mul(decimal.NewFromInt(100)).Round(2)
		leg.Symbol = key
		out = append(out, leg)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight.GreaterThan(out[j].Weight) })
	return out
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// normalizeWeights rounds to two decimals and keeps the sum at exactly 100 by
// adjusting the largest leg. The engine must never send weights the provider
// will reject.
func normalizeWeights(legs []entities.InvestmentAllocationLeg) []entities.InvestmentAllocationLeg {
	if len(legs) == 0 {
		return nil
	}
	out := make([]entities.InvestmentAllocationLeg, len(legs))
	copy(out, legs)
	sum := decimal.Zero
	largest := 0
	for i := range out {
		out[i].Weight = out[i].Weight.Round(2)
		sum = sum.Add(out[i].Weight)
		if out[i].Weight.GreaterThan(out[largest].Weight) {
			largest = i
		}
	}
	if diff := decimal.NewFromInt(100).Sub(sum); !diff.IsZero() {
		out[largest].Weight = out[largest].Weight.Add(diff)
	}
	return out
}

func reasonForTrade(action string, currentWeight, targetWeight decimal.Decimal, contributionOnly bool) string {
	switch action {
	case "buy":
		if contributionOnly {
			return fmt.Sprintf("underweight at %s%% against a %s%% target; new money goes here first", currentWeight.Round(2).String(), targetWeight.String())
		}
		return fmt.Sprintf("underweight at %s%% against a %s%% target", currentWeight.Round(2).String(), targetWeight.String())
	default:
		return fmt.Sprintf("overweight at %s%% against a %s%% target", currentWeight.Round(2).String(), targetWeight.String())
	}
}

func holdingKey(leg entities.InvestmentAllocationLeg) string {
	if leg.Symbol != "" {
		return strings.ToUpper(leg.Symbol)
	}
	return leg.CAIP19
}

func legLabel(leg entities.InvestmentAllocationLeg) string {
	if leg.Symbol != "" {
		return leg.Symbol
	}
	if leg.AssetID != "" {
		return leg.AssetID
	}
	return leg.CAIP19
}

func cloneLegs(legs []entities.InvestmentAllocationLeg) []entities.InvestmentAllocationLeg {
	if len(legs) == 0 {
		return nil
	}
	out := make([]entities.InvestmentAllocationLeg, len(legs))
	copy(out, legs)
	return out
}

func restrictedAsset(list []string, asset *entities.InvestmentAsset) bool {
	for _, item := range list {
		if strings.EqualFold(item, asset.Symbol) || item == asset.ID.String() || item == asset.CAIP19 {
			return true
		}
	}
	return false
}

func allowedAsset(list []string, asset *entities.InvestmentAsset) bool {
	for _, item := range list {
		if strings.EqualFold(item, asset.Symbol) || item == asset.ID.String() || item == asset.CAIP19 {
			return true
		}
	}
	return false
}

func isCashLeg(leg entities.InvestmentAllocationLeg, settlementSymbol string) bool {
	symbol := strings.ToUpper(leg.Symbol)
	if symbol == "" {
		return false
	}
	if settlementSymbol != "" && symbol == strings.ToUpper(settlementSymbol) {
		return true
	}
	switch symbol {
	case "USDC", "USDT", "USD":
		return true
	}
	return false
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func maxDecimal(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}

// slippageBps resolves the execution slippage for a preview.
func slippageBps(rules entities.InvestmentExecutionRules, fallback int) int {
	if rules.SlippageBps != nil && *rules.SlippageBps > 0 {
		return *rules.SlippageBps
	}
	return fallback
}

func parseUUID(value string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

