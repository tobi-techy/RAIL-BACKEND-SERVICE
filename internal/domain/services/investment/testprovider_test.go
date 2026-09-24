package investment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// simProvider is a TEST-ONLY stand-in for the Glider Provider port. It exists
// so the domain tests can exercise success and failure paths deterministically
// without any network access. Production never sees it: the real provider is
// always the HTTP client in internal/infrastructure/adapters/glider.
type simProvider struct {
	mu sync.Mutex

	strategies    map[string]*entities.GliderStrategy
	portfolios    map[string]*simPortfolio
	operations    map[string]*simOperation
	enrollFlows   map[string]*entities.GliderEnrollAuthorization
	enrollResults map[string]*entities.GliderPortfolio
	// enrollFlowStrategy remembers which strategy a flowId was prepared for,
	// because stage 2 only round-trips the flow id.
	enrollFlowStrategy map[string]string

	// failure injection: method name -> error to return until cleared
	failures map[string]error

	// depositAccount -> portfolioID
	depositIndex map[string]string

	prices map[string]decimal.Decimal

	rebalanceCooldown      time.Duration
	lastRebalance          map[string]time.Time
	operationCompleteAfter time.Duration

	now func() time.Time
	seq int
}

type simPortfolio struct {
	id               string
	strategyID       string
	strategyVersion  int
	scheduleStatus   string
	chain            string
	ownerAccountID   string
	agentAccountID   string
	depositAccountID string
	valueUSD         decimal.Decimal
	allocation       []entities.InvestmentAllocationLeg
	schedule         entities.GliderSchedule
	createdAt        time.Time
	lastRebalanceAt  *time.Time
}

type simOperation struct {
	id          string
	portfolioID string
	kind        string
	state       string
	err         *string
	createdAt   time.Time
	updatedAt   time.Time
	finishedAt  *time.Time
	completeAt  time.Time
}

func newSimProvider() *simProvider {
	return &simProvider{
		strategies:             map[string]*entities.GliderStrategy{},
		portfolios:             map[string]*simPortfolio{},
		operations:             map[string]*simOperation{},
		enrollFlows:            map[string]*entities.GliderEnrollAuthorization{},
		enrollResults:          map[string]*entities.GliderPortfolio{},
		enrollFlowStrategy:     map[string]string{},
		failures:               map[string]error{},
		depositIndex:           map[string]string{},
		prices:                 map[string]decimal.Decimal{},
		lastRebalance:          map[string]time.Time{},
		operationCompleteAfter: time.Millisecond,
		now:                    func() time.Time { return time.Now().UTC() },
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// SetNow overrides the clock.
func (s *simProvider) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetRebalanceCooldown sets the per-portfolio manual rebalance cooldown.
func (s *simProvider) SetRebalanceCooldown(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebalanceCooldown = d
}

// SetPrice pins a symbol price.
func (s *simProvider) SetPrice(symbol string, price decimal.Decimal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices[strings.ToUpper(symbol)] = price
}

// FailNext makes every call to a method fail with err until ClearFailure is
// called. Method names match the Provider interface: "CreateStrategy",
// "TriggerRebalance", "SubmitEnrollment", "GetPositions", ...
func (s *simProvider) FailNext(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[method] = err
}

// ClearFailure removes an injected failure.
func (s *simProvider) ClearFailure(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, method)
}

// SimulateDeposit mirrors a confirmed on-chain deposit into a portfolio's
// deposit account.
func (s *simProvider) SimulateDeposit(portfolioID string, amountUSD decimal.Decimal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &simAPIError{code: "not_found"}
	}
	p.valueUSD = p.valueUSD.Add(amountUSD)
	return nil
}

// PortfolioExists reports whether the provider double knows a portfolio.
func (s *simProvider) PortfolioExists(portfolioID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.portfolios[portfolioID]
	return ok
}

func (s *simProvider) fail(method string) error {
	if err, ok := s.failures[method]; ok {
		return err
	}
	return nil
}

// simAPIError is the structural shape the domain's providerError interface
// detects (IsConflict/IsCooldown).
type simAPIError struct {
	code string
}

func (e *simAPIError) Error() string    { return "simulated provider error: " + e.code }
func (e *simAPIError) IsConflict() bool { return e.code == "conflict" }
func (e *simAPIError) IsCooldown() bool { return e.code == "cooldown" }

// ---------------------------------------------------------------------------
// Provider surface
// ---------------------------------------------------------------------------

func (s *simProvider) Whoami(_ context.Context) (*entities.GliderIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("Whoami"); err != nil {
		return nil, err
	}
	return &entities.GliderIdentity{
		APIKeyID:    "test_key",
		TenantName:  "Rail (test)",
		TenantEmail: "ops@example.invalid",
		Scopes: []string{
			"strategies:read", "strategies:write", "portfolios:read",
			"portfolios:write", "portfolios:withdraw", "enroll:write",
		},
	}, nil
}

func (s *simProvider) CreateStrategy(_ context.Context, in entities.GliderStrategyInput) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CreateStrategy"); err != nil {
		return nil, err
	}
	if err := validateSimAllocation(in.Allocation.Assets); err != nil {
		return nil, err
	}
	s.seq++
	now := s.now()
	strategy := &entities.GliderStrategy{
		StrategyID:  fmt.Sprintf("sim_str_%03d", s.seq),
		Name:        in.Name,
		Description: derefString(in.Description),
		Allocation:  in.Allocation,
		IsPublic:    in.IsPublic != nil && *in.IsPublic,
		Version:     1,
		CreatedAt:   &now,
	}
	if in.Schedule != nil {
		strategy.Schedule = *in.Schedule
	}
	s.strategies[strategy.StrategyID] = strategy
	return strategy, nil
}

func (s *simProvider) PublishStrategyVersion(_ context.Context, strategyID string, in entities.GliderPublishVersionInput) (*entities.GliderPublishedVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PublishStrategyVersion"); err != nil {
		return nil, err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	if err := validateSimAllocation(in.Allocation.Assets); err != nil {
		return nil, err
	}
	strategy.Allocation = in.Allocation
	strategy.Version++
	// Enrolled portfolios re-target on their next rebalance: update the
	// mirrored allocation so live positions reflect the new target.
	for _, p := range s.portfolios {
		if p.strategyID == strategyID {
			p.allocation = in.Allocation.Assets
			p.strategyVersion = strategy.Version
		}
	}
	now := s.now()
	return &entities.GliderPublishedVersion{Version: strategy.Version, IsHead: true, CreatedAt: &now}, nil
}

func (s *simProvider) GetStrategy(_ context.Context, strategyID string) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetStrategy"); err != nil {
		return nil, err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	return strategy, nil
}

func (s *simProvider) SetStrategySchedule(_ context.Context, strategyID, frequency string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SetStrategySchedule"); err != nil {
		return err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return &simAPIError{code: "not_found"}
	}
	strategy.Schedule = entities.GliderSchedule{Type: "interval", Frequency: frequency}
	for _, p := range s.portfolios {
		if p.strategyID == strategyID {
			p.schedule.Frequency = frequency
			next := s.now().Add(24 * time.Hour)
			p.schedule.NextDueAt = &next
		}
	}
	return nil
}

func (s *simProvider) DiscoverStrategies(_ context.Context, _ string, _ string, _ int) ([]entities.GliderDiscoveredStrategy, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("DiscoverStrategies"); err != nil {
		return nil, "", err
	}
	created := s.now().Add(-90 * 24 * time.Hour)
	tvl := decimal.NewFromInt(1250342)
	count := 421
	maxAPY := decimal.NewFromFloat(10)
	perf := &entities.GliderDiscoveredMetrics{
		TVLUSD:         &tvl,
		PortfolioCount: &count,
		Performance: &struct {
			Summary struct {
				Windows []struct {
					Window        string          `json:"window"`
					PercentChange decimal.Decimal `json:"percentChange"`
					Since         string          `json:"since"`
				} `json:"windows"`
			} `json:"summary"`
		}{},
	}
	perf.Performance.Summary.Windows = append(perf.Performance.Summary.Windows, struct {
		Window        string          `json:"window"`
		PercentChange decimal.Decimal `json:"percentChange"`
		Since         string          `json:"since"`
	}{Window: "3m", PercentChange: decimal.NewFromFloat(4.12), Since: s.now().Add(-90 * 24 * time.Hour).Format("2006-01-02")})

	usdc := "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/token:EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	description := "Test public strategy: a stable, provider-published allocation."
	out := []entities.GliderDiscoveredStrategy{
		{
			StrategyID:  "sim_pub_balanced",
			Name:        "Balanced Growth",
			Description: &description,
			CanMirror:   true,
			MaxAPY:      &maxAPY,
			Allocation: entities.GliderAllocation{Assets: []entities.InvestmentAllocationLeg{
				{CAIP19: usdc, Symbol: "USDC", Weight: decimal.NewFromInt(60)},
				{CAIP19: "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/slip44:501", Symbol: "SOL", Weight: decimal.NewFromInt(40)},
			}},
			Metrics:   *perf,
			CreatedAt: &created,
		},
	}
	return out, "", nil
}

func (s *simProvider) PrepareEnrollment(_ context.Context, in entities.GliderEnrollSignatureInput) (*entities.GliderEnrollAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PrepareEnrollment"); err != nil {
		return nil, err
	}
	if _, ok := s.strategies[in.StrategyID]; !ok {
		return nil, &simAPIError{code: "unknown_strategy"}
	}
	if !strings.HasPrefix(in.OwnerAccountID, "solana:") {
		return nil, &simAPIError{code: "bad_owner"}
	}
	auth := &entities.GliderEnrollAuthorization{
		FlowID:            "sim_flow_" + uuid.NewString()[:8],
		AccountIndex:      "0",
		AgentAccountID:    "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:Agent1111111111111111111111111111111111",
		AccountType:       in.AccountType,
		Message:           &entities.GliderEnrollMessage{Kind: "solana-message", Text: "Authorize portfolio automation for " + in.StrategyID},
		SolanaTransaction: "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB",
	}
	s.enrollFlows[auth.FlowID] = auth
	s.enrollFlowStrategy[auth.FlowID] = in.StrategyID
	return auth, nil
}

func (s *simProvider) SubmitEnrollment(_ context.Context, in entities.GliderEnrollSubmitInput) (*entities.GliderPortfolio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SubmitEnrollment"); err != nil {
		return nil, err
	}
	auth, ok := s.enrollFlows[in.FlowID]
	if !ok {
		return nil, &simAPIError{code: "unknown_flow"}
	}
	if cached, ok := s.enrollResults[in.FlowID]; ok {
		clone := *cached
		return &clone, nil
	}
	if in.SignedSolanaTransaction == "" && in.Signature == "" {
		return nil, &simAPIError{code: "missing_signature"}
	}

	s.seq++
	now := s.now()
	portfolioID := fmt.Sprintf("sim_pf_%03d", s.seq)
	depositAccount := fmt.Sprintf("solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:Dep0sit%036d", s.seq)
	strategyID := s.enrollFlowStrategy[in.FlowID]
	strategy := s.strategies[strategyID]
	allocation := []entities.InvestmentAllocationLeg{}
	if strategy != nil {
		allocation = strategy.Allocation.Assets
	}
	next := now.Add(24 * time.Hour)
	s.portfolios[portfolioID] = &simPortfolio{
		id:               portfolioID,
		strategyID:       strategyID,
		strategyVersion:  1,
		scheduleStatus:   "active",
		chain:            "solana",
		ownerAccountID:   in.OwnerAccountID,
		agentAccountID:   auth.AgentAccountID,
		depositAccountID: depositAccount,
		valueUSD:         decimal.Zero,
		allocation:       allocation,
		schedule:         entities.GliderSchedule{Type: "interval", Frequency: "daily", Status: "active", NextDueAt: &next},
		createdAt:        now,
	}
	s.depositIndex[depositAccount] = portfolioID

	role := 1
	result := &entities.GliderPortfolio{
		PortfolioID:     portfolioID,
		StrategyID:      strategyID,
		StrategyVersion: 1,
		SmartAccounts: []entities.GliderSmartAccount{
			{AccountID: depositAccount, DepositAccountID: depositAccount, SwigRoleID: &role},
		},
		Schedule:  s.portfolios[portfolioID].schedule,
		CreatedAt: &now,
	}
	s.enrollResults[in.FlowID] = result
	clone := *result
	return &clone, nil
}

func (s *simProvider) GetPortfolio(_ context.Context, portfolioID string) (*entities.GliderPortfolio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetPortfolio"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	role := 1
	return &entities.GliderPortfolio{
		PortfolioID:     p.id,
		StrategyID:      p.strategyID,
		StrategyVersion: p.strategyVersion,
		SmartAccounts: []entities.GliderSmartAccount{
			{AccountID: p.depositAccountID, DepositAccountID: p.depositAccountID, SwigRoleID: &role},
		},
		Schedule:  p.schedule,
		CreatedAt: &p.createdAt,
	}, nil
}

func (s *simProvider) ListPortfolios(_ context.Context) ([]entities.GliderPortfolio, error) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.portfolios))
	for id := range s.portfolios {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sortStrings(ids)

	out := make([]entities.GliderPortfolio, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetPortfolio(context.Background(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

func (s *simProvider) GetPositions(_ context.Context, portfolioID string) (*entities.GliderPositions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetPositions"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	positions := make([]entities.GliderPosition, 0, len(p.allocation))
	for _, leg := range p.allocation {
		value := p.valueUSD.Mul(leg.Weight).Div(decimal.NewFromInt(100))
		price := s.priceFor(leg.Symbol)
		units := decimal.Zero
		if !price.IsZero() {
			units = value.Div(price)
		}
		positions = append(positions, entities.GliderPosition{
			AssetID:    leg.CAIP19,
			Symbol:     leg.Symbol,
			Decimals:   6,
			Balance:    units,
			BalanceRaw: units.Shift(6).Truncate(0).String(),
			PriceUSD:   price,
			ValueUSD:   value,
		})
	}
	return &entities.GliderPositions{
		PortfolioID:   portfolioID,
		TotalValueUSD: p.valueUSD,
		Assets:        positions,
		AsOf:          s.now(),
	}, nil
}

func (s *simProvider) priceFor(symbol string) decimal.Decimal {
	if price, ok := s.prices[strings.ToUpper(symbol)]; ok {
		return price
	}
	return decimal.NewFromInt(100)
}

func (s *simProvider) StartPortfolio(_ context.Context, portfolioID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("StartPortfolio"); err != nil {
		return err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &simAPIError{code: "not_found"}
	}
	p.scheduleStatus = "active"
	p.schedule.Status = "active"
	return nil
}

func (s *simProvider) StopPortfolio(_ context.Context, portfolioID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("StopPortfolio"); err != nil {
		return err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &simAPIError{code: "not_found"}
	}
	p.scheduleStatus = "paused"
	p.schedule.Status = "paused"
	return nil
}

func (s *simProvider) TriggerRebalance(_ context.Context, portfolioID string) (*entities.GliderOperationHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("TriggerRebalance"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	now := s.now()
	if s.rebalanceCooldown > 0 {
		if last, ok := s.lastRebalance[portfolioID]; ok {
			if elapsed := now.Sub(last); elapsed < s.rebalanceCooldown {
				return nil, &simCooldownError{retryAfter: s.rebalanceCooldown - elapsed}
			}
		}
	}
	s.lastRebalance[portfolioID] = now
	s.seq++
	opID := fmt.Sprintf("sim_op_%03d", s.seq)
	s.operations[opID] = &simOperation{
		id:          opID,
		portfolioID: portfolioID,
		kind:        "rebalance",
		state:       "accepted",
		createdAt:   now,
		updatedAt:   now,
		completeAt:  now.Add(s.operationCompleteAfter),
	}
	p.lastRebalanceAt = &now
	return &entities.GliderOperationHandle{OperationID: opID, SubmittedAt: &now}, nil
}

func (s *simProvider) GetOperation(_ context.Context, portfolioID, operationID string) (*entities.GliderOperationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetOperation"); err != nil {
		return nil, err
	}
	op, ok := s.operations[operationID]
	if !ok || op.portfolioID != portfolioID {
		return nil, &simAPIError{code: "not_found"}
	}
	now := s.now()
	if op.state != "completed" && !now.Before(op.completeAt) {
		op.state = "completed"
		op.updatedAt = now
		op.finishedAt = &now
	}
	return &entities.GliderOperationState{
		OperationID: op.id,
		PortfolioID: op.portfolioID,
		Kind:        op.kind,
		State:       op.state,
		Error:       op.err,
		CreatedAt:   &op.createdAt,
		UpdatedAt:   &op.updatedAt,
		FinishedAt:  op.finishedAt,
	}, nil
}

func (s *simProvider) PrepareWithdrawal(_ context.Context, portfolioID string, in entities.GliderWithdrawSignatureInput) (*entities.GliderWithdrawAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PrepareWithdrawal"); err != nil {
		return nil, err
	}
	if _, ok := s.portfolios[portfolioID]; !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	if !strings.HasPrefix(in.RecipientAccountID, "solana:") {
		return nil, &simAPIError{code: "bad_recipient"}
	}
	now := s.now()
	expires := now.Add(10 * time.Minute)
	// Mirror the real stage-1 shape: a solana-message authorization whose
	// Message is the structured object stage 2 must echo verbatim.
	return &entities.GliderWithdrawAuthorization{
		AuthorizationID: "sim_auth_" + uuid.NewString()[:8],
		ExpiresAt:       &expires,
		Authorization: &entities.GliderAuthorization{
			Kind:    "solana-message",
			Text:    "Withdraw from " + portfolioID,
			Message: []byte(`{"portfolioId":"` + portfolioID + `","recipientAccountId":` + quoteJSON(in.RecipientAccountID) + `}`),
		},
	}, nil
}

func (s *simProvider) SubmitWithdrawal(_ context.Context, portfolioID string, in entities.GliderWithdrawSubmitInput) (*entities.GliderOperationHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SubmitWithdrawal"); err != nil {
		return nil, err
	}
	if _, ok := s.portfolios[portfolioID]; !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	if len(in.Message) == 0 {
		return nil, &simAPIError{code: "missing_message"}
	}
	if in.Signature == "" {
		return nil, &simAPIError{code: "missing_signature"}
	}
	now := s.now()
	s.seq++
	opID := fmt.Sprintf("sim_op_%03d", s.seq)
	s.operations[opID] = &simOperation{
		id:          opID,
		portfolioID: portfolioID,
		kind:        "withdraw",
		state:       "accepted",
		createdAt:   now,
		updatedAt:   now,
		completeAt:  now.Add(s.operationCompleteAfter),
	}
	return &entities.GliderOperationHandle{OperationID: opID, SubmittedAt: &now}, nil
}

// --- Extended Provider surface (mirrors real Glider endpoints) ---

func (s *simProvider) ListScopes(_ context.Context) ([]entities.GliderScope, error) {
	return []entities.GliderScope{
		{Name: "strategies:read", Description: "List and view strategies", Tier: "default"},
		{Name: "strategies:write", Description: "Create and update strategies", Tier: "standard"},
		{Name: "portfolios:read", Description: "View portfolios", Tier: "default"},
		{Name: "portfolios:write", Description: "Manage portfolios", Tier: "standard"},
		{Name: "portfolios:withdraw", Description: "Withdraw", Tier: "restricted"},
		{Name: "enroll:write", Description: "Enroll users", Tier: "standard"},
		{Name: "tenant:read", Description: "Read tenant prefs", Tier: "default"},
		{Name: "tenant:write", Description: "Write tenant prefs", Tier: "standard"},
		{Name: "fees:read", Description: "Read fees", Tier: "default"},
		{Name: "fees:write", Description: "Write fees", Tier: "restricted"},
	}, nil
}

func (s *simProvider) ValidateStrategy(_ context.Context, in entities.GliderStrategyInput) error {
	return validateSimAllocation(in.Allocation.Assets)
}

func (s *simProvider) ListStrategies(_ context.Context, _ entities.GliderStrategyListFilter) ([]entities.GliderStrategy, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]entities.GliderStrategy, 0, len(s.strategies))
	for _, st := range s.strategies {
		clone := *st
		out = append(out, clone)
	}
	return out, "", nil
}

func (s *simProvider) PatchStrategy(_ context.Context, strategyID string, patch entities.GliderStrategyPatch) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.strategies[strategyID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	if patch.Name != nil {
		st.Name = *patch.Name
	}
	if patch.Description != nil {
		st.Description = *patch.Description
	}
	if patch.IsPublic != nil {
		st.IsPublic = *patch.IsPublic
	}
	clone := *st
	return &clone, nil
}

func (s *simProvider) ListStrategyVersions(_ context.Context, strategyID, _ string, _ int) ([]entities.GliderStrategyVersion, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.strategies[strategyID]
	if !ok {
		return nil, "", &simAPIError{code: "not_found"}
	}
	now := s.now()
	changeLog := "initial version"
	return []entities.GliderStrategyVersion{
		{Version: st.Version, Allocation: st.Allocation, ChangeLog: &changeLog, IsHead: true, CreatedAt: &now},
	}, "", nil
}

func (s *simProvider) GetStrategyPerformance(_ context.Context, strategyID string) (*entities.GliderStrategyPerformance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.strategies[strategyID]; !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	now := s.now()
	pct := decimal.NewFromFloat(4.12)
	return &entities.GliderStrategyPerformance{
		StrategyID: strategyID,
		Schedule:   entities.GliderSchedule{Type: "interval", Frequency: "daily"},
		Meta:       entities.GliderPerformanceMeta{Method: "TWR", Currency: "USD", Resolution: "1d", AsOf: &now},
		Points:     []entities.GliderPerformancePoint{{Date: now.Format("2006-01-02"), PercentChange: &pct}},
		Summary:    &entities.GliderPerformanceSummary{Windows: []entities.GliderPerformanceWindow{{Window: "all", PercentChange: pct, Since: now.Add(-30 * 24 * time.Hour).Format("2006-01-02")}}},
	}, nil
}

func (s *simProvider) GetStrategySchedule(_ context.Context, strategyID string) (*entities.GliderSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.strategies[strategyID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	sched := st.Schedule
	return &sched, nil
}

func (s *simProvider) GetStrategyPreferences(_ context.Context, _ string) (*entities.GliderPreferences, error) {
	slip, impact := 300, 300
	threshold := decimal.NewFromFloat(5)
	return &entities.GliderPreferences{Swap: entities.GliderSwapPreferenceView{SlippageBps: &slip, PriceImpactBps: &impact, ThresholdUSD: &threshold}}, nil
}

func (s *simProvider) PatchStrategyPreferences(_ context.Context, _ string, _ map[string]any) (*entities.GliderPreferences, error) {
	return s.GetStrategyPreferences(context.Background(), "")
}

func (s *simProvider) GetStrategyFees(_ context.Context, _ string) (*entities.GliderFeeView, error) {
	bps := 50
	return &entities.GliderFeeView{SwapBps: &bps}, nil
}

func (s *simProvider) PatchStrategyFees(_ context.Context, _ string, patch map[string]any) (*entities.GliderFeeView, error) {
	if v, ok := patch["swapBps"]; ok && v == nil {
		return &entities.GliderFeeView{SwapBps: nil}, nil
	}
	return s.GetStrategyFees(context.Background(), "")
}

func (s *simProvider) GetTenantPreferences(_ context.Context) (*entities.GliderPreferences, error) {
	return s.GetStrategyPreferences(context.Background(), "")
}

func (s *simProvider) PatchTenantPreferences(_ context.Context, _ map[string]any) (*entities.GliderPreferences, error) {
	return s.GetStrategyPreferences(context.Background(), "")
}

func (s *simProvider) GetTenantFees(_ context.Context) (*entities.GliderFeeView, error) {
	return s.GetStrategyFees(context.Background(), "")
}

func (s *simProvider) PatchTenantFees(_ context.Context, _ map[string]any) (*entities.GliderFeeView, error) {
	return s.GetStrategyFees(context.Background(), "")
}

func (s *simProvider) ListPortfoliosFiltered(_ context.Context, _ entities.GliderPortfolioListFilter) ([]entities.GliderPortfolio, string, error) {
	portfolios, err := s.ListPortfolios(context.Background())
	return portfolios, "", err
}

func (s *simProvider) PatchPortfolio(_ context.Context, portfolioID string, _ entities.GliderPortfolioPatch) (*entities.GliderPortfolio, error) {
	s.mu.Lock()
	p, ok := s.portfolios[portfolioID]
	s.mu.Unlock()
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	return s.GetPortfolio(context.Background(), p.id)
}

func (s *simProvider) GetPortfolioPerformance(_ context.Context, portfolioID, _ string) (*entities.GliderPortfolioPerformance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	now := s.now()
	pct := decimal.NewFromFloat(1.5)
	return &entities.GliderPortfolioPerformance{
		PortfolioID: portfolioID,
		StrategyID:  p.strategyID,
		Meta:        entities.GliderPerformanceMeta{Method: "MWR", Currency: "USD", Resolution: "1d", AsOf: &now},
		Points:      []entities.GliderPerformancePoint{{Date: now.Format("2006-01-02"), PercentChange: &pct, ValueUSD: &p.valueUSD}},
	}, nil
}

func (s *simProvider) GetSectorExposure(_ context.Context, portfolioID string) (*entities.GliderSectorExposure, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &simAPIError{code: "not_found"}
	}
	return &entities.GliderSectorExposure{
		PortfolioID: portfolioID, Taxonomy: "internal_display_sector_v1",
		TotalMarketValueUSD: p.valueUSD, ClassifiedMarketValueUSD: p.valueUSD,
		Rows: []entities.GliderSectorRow{{DisplaySector: "Technology", MarketValueUSD: p.valueUSD, Weight: decimal.NewFromInt(100)}},
	}, nil
}

func (s *simProvider) GetAllocationBreakdown(_ context.Context, _ entities.GliderBreakdownInput) (json.RawMessage, error) {
	return json.RawMessage(`{"breakdowns":[],"diagnostics":{}}`), nil
}

func (s *simProvider) PrepareChainActivation(_ context.Context, _ string, _ entities.GliderChainActivationInput) (*entities.GliderChainActivationMessage, error) {
	// Sim portfolios are Solana; chain activation is EVM-only on real Glider.
	return nil, &simAPIError{code: "solana_unsupported"}
}

func (s *simProvider) ActivateChains(_ context.Context, _ string, _ entities.GliderChainActivationSubmit) (*entities.GliderPortfolio, error) {
	return nil, &simAPIError{code: "solana_unsupported"}
}

func (s *simProvider) PrepareLiquidateAll(_ context.Context, portfolioID string, in entities.GliderLiquidateSignatureInput) (*entities.GliderWithdrawAuthorization, error) {
	return s.PrepareWithdrawal(context.Background(), portfolioID, entities.GliderWithdrawSignatureInput{RecipientAccountID: in.RecipientAccountID})
}

func (s *simProvider) SubmitLiquidateAll(_ context.Context, portfolioID string, in entities.GliderWithdrawSubmitInput) (*entities.GliderOperationHandle, error) {
	return s.SubmitWithdrawal(context.Background(), portfolioID, in)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validateSimAllocation(legs []entities.InvestmentAllocationLeg) error {
	if len(legs) == 0 {
		return &simAPIError{code: "empty_allocation"}
	}
	sum := decimal.Zero
	for _, leg := range legs {
		if leg.Weight.LessThanOrEqual(decimal.Zero) {
			return &simAPIError{code: "non_positive_weight"}
		}
		sum = sum.Add(leg.Weight)
	}
	if !sum.Equal(decimal.NewFromInt(100)) {
		return &simAPIError{code: "weights_do_not_sum_to_100"}
	}
	return nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func quoteJSON(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// simCooldownError mirrors the real provider's 429 + Retry-After cooldown.
type simCooldownError struct {
	retryAfter time.Duration
}

func (e *simCooldownError) Error() string    { return "rebalance cooldown active" }
func (e *simCooldownError) IsCooldown() bool { return true }
func (e *simCooldownError) IsConflict() bool { return false }
