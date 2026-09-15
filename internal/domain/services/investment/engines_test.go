package investment

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeResolver struct {
	byID     map[uuid.UUID]*entities.InvestmentAsset
	byCAIP19 map[string]*entities.InvestmentAsset
	bySymbol map[string]*entities.InvestmentAsset
}

func newFakeResolver(assets ...*entities.InvestmentAsset) *fakeResolver {
	r := &fakeResolver{
		byID:     map[uuid.UUID]*entities.InvestmentAsset{},
		byCAIP19: map[string]*entities.InvestmentAsset{},
		bySymbol: map[string]*entities.InvestmentAsset{},
	}
	for _, asset := range assets {
		r.byID[asset.ID] = asset
		r.byCAIP19[asset.CAIP19] = asset
		r.bySymbol[asset.Symbol] = asset
	}
	return r
}

func (f *fakeResolver) Resolve(_ context.Context, assetID, caip19, symbol string) (*entities.InvestmentAsset, error) {
	if assetID != "" {
		if id, err := uuid.Parse(assetID); err == nil {
			if asset, ok := f.byID[id]; ok {
				return asset, nil
			}
		}
	}
	if caip19 != "" {
		if asset, ok := f.byCAIP19[caip19]; ok {
			return asset, nil
		}
	}
	if symbol != "" {
		if asset, ok := f.bySymbol[symbol]; ok {
			return asset, nil
		}
	}
	return nil, ErrNotFound
}

type fakeUserProfiles struct {
	profile *UserProfile
	err     error
}

func (f *fakeUserProfiles) GetInvestmentProfile(_ context.Context, userID uuid.UUID) (*UserProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.profile == nil {
		return nil, nil
	}
	profile := *f.profile
	profile.UserID = userID
	return &profile, nil
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

var (
	testUSDC = &entities.InvestmentAsset{
		ID: uuid.New(), CAIP19: "solana:testnet/token:usdc", Symbol: "USDC",
		Name: "USD Coin", AssetClass: "cash", Chain: "solana", Decimals: 6, Allowlisted: true,
	}
	testSOL = &entities.InvestmentAsset{
		ID: uuid.New(), CAIP19: "solana:testnet/slip44:501", Symbol: "SOL",
		Name: "Solana", AssetClass: "crypto", Chain: "solana", Decimals: 9, Allowlisted: true,
	}
	testSPY = &entities.InvestmentAsset{
		ID: uuid.New(), CAIP19: "eip155:1/erc20:spy", Symbol: "SPY",
		Name: "S&P 500 tracker", AssetClass: "equity", Chain: "eip155:1", Decimals: 18, Allowlisted: true,
	}
	testBlocked = &entities.InvestmentAsset{
		ID: uuid.New(), CAIP19: "solana:testnet/token:blocked", Symbol: "BLOCKED",
		Name: "Not permitted", AssetClass: "crypto", Chain: "solana", Decimals: 6,
		Allowlisted: false, Prohibited: true,
	}
)

func testConfig() Config {
	return Config{
		Enabled:               true,
		DefaultChain:          "solana",
		SettlementSymbol:      "USDC",
		DefaultSlippageBps:    50,
		DefaultFeeBps:         decimal.NewFromInt(10),
		HighValueThresholdUSD: decimal.NewFromInt(1000),
		ConfirmationTTL:       15 * time.Minute,
		AllowedCountries:      []string{"NG", "GB"},
		MinAllocationLegs:     1,
		MaxAllocationLegs:     5,
		StaleMarketDataAfter:  time.Hour,
		DefaultLimits: Limits{
			MaxPositionPct:    decimal.NewFromInt(60),
			MaxTransactionUSD: decimal.NewFromInt(5000),
			MaxDailyVolumeUSD: decimal.NewFromInt(10000),
			MinOrderAmountUSD: decimal.NewFromInt(10),
			MaxEnrollments:    3,
		},
	}
}

func testLimits() entities.InvestmentLimits {
	d := testConfig().DefaultLimits
	return entities.InvestmentLimits{
		MaxPositionPct:    d.MaxPositionPct,
		MaxTransactionUSD: d.MaxTransactionUSD,
		MaxDailyVolumeUSD: d.MaxDailyVolumeUSD,
		MinOrderAmountUSD: d.MinOrderAmountUSD,
		MaxEnrollments:    d.MaxEnrollments,
	}
}

func leg(asset *entities.InvestmentAsset, weight int64) entities.InvestmentAllocationLeg {
	return entities.InvestmentAllocationLeg{
		AssetID: asset.ID.String(),
		CAIP19:  asset.CAIP19,
		Symbol:  asset.Symbol,
		Weight:  decimal.NewFromInt(weight),
	}
}

// ---------------------------------------------------------------------------
// Validator
// ---------------------------------------------------------------------------

func TestValidatorRejectsInvalidAllocations(t *testing.T) {
	resolver := newFakeResolver(testUSDC, testSOL, testSPY, testBlocked)
	validator := NewValidator(resolver, testConfig())

	cases := []struct {
		name        string
		legs        []entities.InvestmentAllocationLeg
		amount      decimal.Decimal
		value       decimal.Decimal
		constraints entities.InvestmentPlanConstraints
		limits      func(*entities.InvestmentLimits)
		wantCode    string
	}{
		{
			name:     "weights must sum to 100",
			legs:     []entities.InvestmentAllocationLeg{leg(testUSDC, 60), leg(testSOL, 30)},
			wantCode: "allocation.weights_sum",
		},
		{
			name: "weights may not exceed two decimals",
			legs: []entities.InvestmentAllocationLeg{
				{AssetID: testUSDC.ID.String(), Symbol: "USDC", Weight: decimal.RequireFromString("33.333")},
				{Symbol: "SOL", Weight: decimal.RequireFromString("66.667")},
			},
			wantCode: "allocation.weight_precision",
		},
		{
			name:     "zero weight is not a leg",
			legs:     []entities.InvestmentAllocationLeg{leg(testUSDC, 100), {Symbol: "SOL", Weight: decimal.Zero}},
			wantCode: "allocation.non_positive_weight",
		},
		{
			name:     "unknown asset",
			legs:     []entities.InvestmentAllocationLeg{{Symbol: "DOGE", Weight: decimal.NewFromInt(100)}},
			wantCode: "allocation.unknown_asset",
		},
		{
			name:     "prohibited asset",
			legs:     []entities.InvestmentAllocationLeg{leg(testBlocked, 100)},
			wantCode: "allocation.asset_not_permitted",
		},
		{
			name:     "duplicate asset",
			legs:     []entities.InvestmentAllocationLeg{leg(testUSDC, 50), leg(testUSDC, 50)},
			wantCode: "allocation.duplicate_asset",
		},
		{
			name:     "position cap exceeded",
			legs:     []entities.InvestmentAllocationLeg{leg(testSOL, 90), leg(testUSDC, 10)},
			wantCode: "allocation.position_cap_exceeded",
		},
		{
			name: "asset outside the strategy universe",
			legs: []entities.InvestmentAllocationLeg{leg(testSPY, 100)},
			constraints: entities.InvestmentPlanConstraints{
				AllowedAssets: []string{"USDC", "SOL"},
			},
			wantCode: "allocation.asset_not_in_strategy_universe",
		},
		{
			name: "asset excluded by the strategy",
			legs: []entities.InvestmentAllocationLeg{leg(testSOL, 100)},
			constraints: entities.InvestmentPlanConstraints{
				ProhibitedAssets: []string{"SOL"},
			},
			wantCode: "allocation.asset_prohibited_by_strategy",
		},
		{
			name:     "amount below the minimum",
			legs:     []entities.InvestmentAllocationLeg{leg(testUSDC, 100)},
			amount:   decimal.NewFromInt(5),
			limits:   func(l *entities.InvestmentLimits) { l.MaxPositionPct = decimal.NewFromInt(100) },
			wantCode: "amount.below_minimum",
		},
		{
			name:     "amount above the per-transaction cap",
			legs:     []entities.InvestmentAllocationLeg{leg(testUSDC, 100)},
			amount:   decimal.NewFromInt(9000),
			limits:   func(l *entities.InvestmentLimits) { l.MaxPositionPct = decimal.NewFromInt(100) },
			wantCode: "amount.above_maximum",
		},
		{
			name:   "daily volume exhausted",
			legs:   []entities.InvestmentAllocationLeg{leg(testUSDC, 100)},
			amount: decimal.NewFromInt(500),
			limits: func(l *entities.InvestmentLimits) {
				l.MaxPositionPct = decimal.NewFromInt(100)
				l.DailyVolumeUSD = decimal.NewFromInt(9800)
			},
			wantCode: "amount.daily_limit",
		},
		{
			name:  "cash reserve breached",
			legs:  []entities.InvestmentAllocationLeg{leg(testSOL, 100)},
			value: decimal.NewFromInt(1000),
			limits: func(l *entities.InvestmentLimits) {
				l.MaxPositionPct = decimal.NewFromInt(100)
				l.MinCashReserveUSD = decimal.NewFromInt(100)
			},
			wantCode: "allocation.below_cash_reserve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limits := testLimits()
			if tc.limits != nil {
				tc.limits(&limits)
			}
			report, err := validator.Validate(context.Background(), ValidationInput{
				Legs:              tc.legs,
				AmountUSD:         tc.amount,
				Limits:            limits,
				Constraints:       tc.constraints,
				PortfolioValueUSD: tc.value,
			})
			require.NoError(t, err)
			assert.False(t, report.Valid, "expected the allocation to be rejected")
			require.NotEmpty(t, report.Violations)
			assert.Equal(t, tc.wantCode, report.Violations[0].Code)
			assert.Nil(t, report.NormalizedAllocation, "a rejected allocation must not carry a normalized copy")
		})
	}
}

func TestValidatorAcceptsAndNormalizesAllocation(t *testing.T) {
	resolver := newFakeResolver(testUSDC, testSOL, testSPY)
	validator := NewValidator(resolver, testConfig())

	report, err := validator.Validate(context.Background(), ValidationInput{
		Legs: []entities.InvestmentAllocationLeg{
			leg(testUSDC, 40),
			leg(testSOL, 35),
			leg(testSPY, 25),
		},
		AmountUSD: decimal.NewFromInt(500),
		Limits:    testLimits(),
	})
	require.NoError(t, err)
	require.True(t, report.Valid)
	require.Len(t, report.NormalizedAllocation, 3)

	sum := decimal.Zero
	for _, normalized := range report.NormalizedAllocation {
		sum = sum.Add(normalized.Weight)
		// Resolution must attach the catalog identity, never trust the caller.
		assert.NotEmpty(t, normalized.CAIP19)
		assert.NotEmpty(t, normalized.AssetID)
	}
	assert.True(t, sum.Equal(decimal.NewFromInt(100)), "normalized weights must sum to exactly 100, got %s", sum)
}

func TestValidatorNormalizationFixesRoundingDrift(t *testing.T) {
	resolver := newFakeResolver(testUSDC, testSOL, testSPY)
	validator := NewValidator(resolver, testConfig())

	report, err := validator.Validate(context.Background(), ValidationInput{
		Legs: []entities.InvestmentAllocationLeg{
			{Symbol: "USDC", Weight: decimal.RequireFromString("33.33")},
			{Symbol: "SOL", Weight: decimal.RequireFromString("33.33")},
			{Symbol: "SPY", Weight: decimal.RequireFromString("33.34")},
		},
		Limits: testLimits(),
	})
	require.NoError(t, err)
	require.True(t, report.Valid)
	sum := decimal.Zero
	for _, normalized := range report.NormalizedAllocation {
		sum = sum.Add(normalized.Weight)
	}
	assert.True(t, sum.Equal(decimal.NewFromInt(100)))
}

func TestValidatorRejectsTooManyLegs(t *testing.T) {
	resolver := newFakeResolver(testUSDC)
	cfg := testConfig()
	cfg.MaxAllocationLegs = 2
	validator := NewValidator(resolver, cfg)

	report, err := validator.Validate(context.Background(), ValidationInput{
		Legs: []entities.InvestmentAllocationLeg{
			leg(testUSDC, 40), leg(testUSDC, 30), leg(testUSDC, 30),
		},
		Limits: testLimits(),
	})
	require.NoError(t, err)
	assert.False(t, report.Valid)
	assert.Equal(t, "allocation.too_many_legs", report.Violations[0].Code)
}

// ---------------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------------

func TestPolicyVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		cfg     func(*Config)
		profile *UserProfile
		action  PolicyAction
		amount  decimal.Decimal
		want    entities.InvestmentVerdict
	}{
		{
			name: "disabled feature is not supported",
			cfg:  func(c *Config) { c.Enabled = false },
			want: entities.InvestmentVerdictNotSupported,
		},
		{
			name:    "tier below advanced needs compliance review",
			profile: &UserProfile{KYCTier: "basic", KYCStatus: "approved"},
			action:  PolicyActionCreateStrategy,
			want:    entities.InvestmentVerdictRequiresComplianceReview,
		},
		{
			name:    "unverified user needs compliance review",
			profile: &UserProfile{KYCTier: "non_kyc", KYCStatus: "pending"},
			action:  PolicyActionEnroll,
			want:    entities.InvestmentVerdictRequiresComplianceReview,
		},
		{
			name:    "unsupported jurisdiction",
			profile: &UserProfile{KYCTier: "advanced", KYCStatus: "approved", Country: "US"},
			action:  PolicyActionEnroll,
			want:    entities.InvestmentVerdictNotSupported,
		},
		{
			name:    "withdrawal always needs authentication",
			profile: &UserProfile{KYCTier: "advanced", KYCStatus: "approved", Country: "NG"},
			action:  PolicyActionWithdraw,
			amount:  decimal.NewFromInt(50),
			want:    entities.InvestmentVerdictRequiresAuthentication,
		},
		{
			name:    "liquidation always needs authentication",
			profile: &UserProfile{KYCTier: "advanced", KYCStatus: "approved", Country: "NG"},
			action:  PolicyActionLiquidate,
			want:    entities.InvestmentVerdictRequiresAuthentication,
		},
		{
			name:    "high value needs authentication",
			profile: &UserProfile{KYCTier: "advanced", KYCStatus: "approved", Country: "NG"},
			action:  PolicyActionTrade,
			amount:  decimal.NewFromInt(1000),
			want:    entities.InvestmentVerdictRequiresAuthentication,
		},
		{
			name:    "ordinary trade needs confirmation",
			profile: &UserProfile{KYCTier: "advanced", KYCStatus: "approved", Country: "NG"},
			action:  PolicyActionTrade,
			amount:  decimal.NewFromInt(250),
			want:    entities.InvestmentVerdictRequiresConfirmation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			policy := NewPolicy(&fakeUserProfiles{profile: tc.profile}, cfg)
			decision, err := policy.Evaluate(context.Background(), PolicyInput{
				UserID:    uuid.New(),
				Action:    tc.action,
				AmountUSD: tc.amount,
				Limits:    testLimits(),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, decision.Verdict)
			assert.NotEmpty(t, decision.Reasons, "every verdict must be explainable")
		})
	}
}

func TestPolicyFailsClosedWhenProfileCannotBeLoaded(t *testing.T) {
	policy := NewPolicy(&fakeUserProfiles{err: assert.AnError}, testConfig())
	_, err := policy.Evaluate(context.Background(), PolicyInput{UserID: uuid.New(), Action: PolicyActionEnroll})
	require.Error(t, err, "an unverifiable profile must not be treated as verified")
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

func TestPreviewComputesDriftAndTrades(t *testing.T) {
	previewer := NewPreviewer(testConfig())
	holdings := []*entities.InvestmentHolding{
		hold("USDC", 1000),
		hold("SOL", 1000),
	}
	preview := previewer.Preview(PreviewInput{
		StrategyID: uuid.NewString(),
		Version:    3,
		Target:     []entities.InvestmentAllocationLeg{leg(testUSDC, 60), leg(testSOL, 40)},
		Current:    holdings,
		AmountUSD:  decimal.NewFromInt(500),
		Rules:      entities.InvestmentRebalanceRules{Type: "threshold", AllowSells: true},
	})

	require.NotEmpty(t, preview.ProposedTrades)
	assert.True(t, preview.Estimated, "a preview is never provider output")
	assert.False(t, preview.MarketDataAsOf.IsZero())
	assert.NotEmpty(t, preview.Assumptions)
	assert.True(t, preview.DriftPct.GreaterThan(decimal.Zero))
	assert.Equal(t, 3, preview.Version)

	// Current allocation must add up to 100 when there is value to divide.
	sum := decimal.Zero
	for _, current := range preview.CurrentAllocation {
		sum = sum.Add(current.Weight)
	}
	assert.True(t, sum.Equal(decimal.NewFromInt(100)), "current weights should sum to 100, got %s", sum)

	bought := map[string]bool{}
	for _, trade := range preview.ProposedTrades {
		bought[trade.Symbol] = true
		assert.NotEmpty(t, trade.Reason)
	}
	assert.True(t, bought["USDC"], "the underweight leg should be bought")
}

func TestPreviewContributionOnlyDoesNotSell(t *testing.T) {
	previewer := NewPreviewer(testConfig())
	preview := previewer.Preview(PreviewInput{
		Version:   1,
		Target:    []entities.InvestmentAllocationLeg{leg(testUSDC, 50), leg(testSOL, 50)},
		Current:   []*entities.InvestmentHolding{hold("SOL", 1000)},
		AmountUSD: decimal.NewFromInt(200),
		Rules: entities.InvestmentRebalanceRules{
			Type:                "contribution",
			PreferContributions: true,
			AllowSells:          false,
		},
	})
	assert.True(t, preview.ContributionOnly)
	for _, trade := range preview.ProposedTrades {
		assert.Equal(t, "buy", trade.Action, "contribution-only strategies never sell to rebalance")
	}
}

func hold(symbol string, value int64) *entities.InvestmentHolding {
	return &entities.InvestmentHolding{
		ID:        uuid.New(),
		CAIP19:    "solana:testnet/token:" + symbol,
		Symbol:    symbol,
		Balance:   decimal.NewFromInt(value),
		PriceUSD:  decimal.NewFromInt(1),
		ValueUSD:  decimal.NewFromInt(value),
		Source:    "glider",
		AsOf:      time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}
