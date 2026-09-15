package glider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Simulated is an in-process Glider V2 stand-in. It exists because the rail
// tenant has no provider sandbox: failure paths (cooldowns, conflicts, expired
// authorizations, provider timeouts, replay) must still be exercised
// deterministically in tests and in local development.
//
// It is selected by configuration (investment.glider.simulation) and must
// never be reachable in production traffic.
type Simulated struct {
	mu sync.Mutex

	log *zap.Logger

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

	// depositAccount -> portfolioID, mirrors the provider's sub-account lookup
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
	status           string
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

// SimulatedConfig configures the simulated provider.
type SimulatedConfig struct {
	RebalanceCooldown      time.Duration
	OperationCompleteAfter time.Duration
	Logger                 *zap.Logger
}

// NewSimulated builds a simulated provider. Defaults: 10s rebalance cooldown
// (mirrors a provider cooldown without slowing tests down) and operations that
// complete on first poll.
func NewSimulated(cfg SimulatedConfig) *Simulated {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.OperationCompleteAfter <= 0 {
		cfg.OperationCompleteAfter = time.Millisecond
	}
	return &Simulated{
		log:                    cfg.Logger,
		strategies:             map[string]*entities.GliderStrategy{},
		portfolios:             map[string]*simPortfolio{},
		operations:             map[string]*simOperation{},
		enrollFlows:            map[string]*entities.GliderEnrollAuthorization{},
		enrollResults:          map[string]*entities.GliderPortfolio{},
		enrollFlowStrategy:     map[string]string{},
		failures:               map[string]error{},
		depositIndex:           map[string]string{},
		prices:                 defaultSimulatedPrices(),
		rebalanceCooldown:      cfg.RebalanceCooldown,
		lastRebalance:          map[string]time.Time{},
		operationCompleteAfter: cfg.OperationCompleteAfter,
		now:                    func() time.Time { return time.Now().UTC() },
	}
}

func defaultSimulatedPrices() map[string]decimal.Decimal {
	prices := map[string]decimal.Decimal{
		"USDC": decimal.NewFromInt(1),
		"USDT": decimal.NewFromInt(1),
		"SOL":  decimal.NewFromInt(150),
		"BTC":  decimal.NewFromInt(60000),
		"ETH":  decimal.NewFromInt(3000),
		"AAPL": decimal.NewFromInt(200),
		"NVDA": decimal.NewFromInt(120),
		"SPY":  decimal.NewFromInt(500),
		"QQQ":  decimal.NewFromInt(450),
		"BND":  decimal.NewFromInt(75),
		"VOO":  decimal.NewFromInt(480),
	}
	return prices
}

// ---------------------------------------------------------------------------
// Test and development helpers
// ---------------------------------------------------------------------------

// SetNow overrides the clock (tests only).
func (s *Simulated) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetRebalanceCooldown sets the per-portfolio manual rebalance cooldown.
func (s *Simulated) SetRebalanceCooldown(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebalanceCooldown = d
}

// SetPrice pins a symbol price (tests only).
func (s *Simulated) SetPrice(symbol string, price decimal.Decimal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prices[strings.ToUpper(symbol)] = price
}

// FailNext makes every call to a method fail with err until ClearFailure is
// called. Method names match the client: "CreateStrategy", "TriggerRebalance",
// "SubmitEnrollment", "GetPositions", ...
func (s *Simulated) FailNext(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[method] = err
}

// ClearFailure removes an injected failure.
func (s *Simulated) ClearFailure(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, method)
}

// SimulateDeposit mirrors a confirmed on-chain deposit into a portfolio's
// deposit account. In production this is the on-chain transfer the funding
// service makes; nothing else in the system needs it.
func (s *Simulated) SimulateDeposit(portfolioID string, amountUSD decimal.Decimal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	p.valueUSD = p.valueUSD.Add(amountUSD)
	return nil
}

// PortfolioExists reports whether the simulated provider knows a portfolio.
func (s *Simulated) PortfolioExists(portfolioID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.portfolios[portfolioID]
	return ok
}

func (s *Simulated) fail(method string) error {
	if err, ok := s.failures[method]; ok {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Provider surface
// ---------------------------------------------------------------------------

// Whoami returns a simulated tenant identity.
func (s *Simulated) Whoami(_ context.Context) (*entities.GliderIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("Whoami"); err != nil {
		return nil, err
	}
	return &entities.GliderIdentity{
		APIKeyID:    "sim_key",
		TenantName:  "Rail (simulated)",
		TenantEmail: "ops@example.invalid",
		Scopes: []string{
			"strategies:read", "strategies:write", "portfolios:read",
			"portfolios:write", "portfolios:withdraw", "enroll:write",
		},
	}, nil
}

// CreateStrategy stores a simulated strategy.
func (s *Simulated) CreateStrategy(_ context.Context, in entities.GliderStrategyInput) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CreateStrategy"); err != nil {
		return nil, err
	}
	if err := validateSimulatedAllocation(in.Allocation.Assets); err != nil {
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

// PublishStrategyVersion appends a version to a simulated strategy.
func (s *Simulated) PublishStrategyVersion(_ context.Context, strategyID string, in entities.GliderStrategyInput) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PublishStrategyVersion"); err != nil {
		return nil, err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "strategy not found"}
	}
	if err := validateSimulatedAllocation(in.Allocation.Assets); err != nil {
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
	return strategy, nil
}

// GetStrategy reads a simulated strategy.
func (s *Simulated) GetStrategy(_ context.Context, strategyID string) (*entities.GliderStrategy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetStrategy"); err != nil {
		return nil, err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "strategy not found"}
	}
	return strategy, nil
}

// SetStrategySchedule updates a simulated schedule.
func (s *Simulated) SetStrategySchedule(_ context.Context, strategyID, frequency string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SetStrategySchedule"); err != nil {
		return err
	}
	strategy, ok := s.strategies[strategyID]
	if !ok {
		return &APIError{StatusCode: 404, Code: "API_004", Message: "strategy not found"}
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

// DiscoverStrategies returns a fixed set of public strategies.
func (s *Simulated) DiscoverStrategies(_ context.Context, collection, _ string, _ int) ([]entities.GliderDiscoveredStrategy, string, error) {
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
	description := "Simulated public strategy: a stable, provider-published allocation."
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
	if strings.EqualFold(collection, "top_performing") {
		out[0].Name = "Top Performing (simulated)"
	}
	return out, "", nil
}

// PrepareEnrollment returns a simulated signable authorization.
func (s *Simulated) PrepareEnrollment(_ context.Context, in entities.GliderEnrollSignatureInput) (*entities.GliderEnrollAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PrepareEnrollment"); err != nil {
		return nil, err
	}
	if _, ok := s.strategies[in.StrategyID]; !ok {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "unknown strategy", Details: []string{"strategyId"}}
	}
	if !strings.HasPrefix(in.OwnerAccountID, "solana:") {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "ownerAccountId must be a Solana CAIP-10 account"}
	}
	now := s.now()
	auth := &entities.GliderEnrollAuthorization{
		FlowID:                    "sim_flow_" + uuid.NewString()[:8],
		AccountIndex:              "0",
		AgentAccountID:            "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:Agent1111111111111111111111111111111111",
		ChainIDs:                  in.ChainIDs,
		AccountType:               in.AccountType,
		Authorization:             &entities.GliderAuthorization{Kind: "solana-message", Text: "Authorize portfolio automation for " + in.StrategyID},
		SolanaTransaction: "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB",
		ExpiresAt:                 ptrTime(now.Add(10 * time.Minute)),
	}
	s.enrollFlows[auth.FlowID] = auth
	s.enrollFlowStrategy[auth.FlowID] = in.StrategyID
	return auth, nil
}

// SubmitEnrollment is idempotent on flowId, mirroring the provider contract.
func (s *Simulated) SubmitEnrollment(_ context.Context, in entities.GliderEnrollSubmitInput) (*entities.GliderPortfolio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SubmitEnrollment"); err != nil {
		return nil, err
	}
	auth, ok := s.enrollFlows[in.FlowID]
	if !ok {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "unknown flowId"}
	}
	if cached, ok := s.enrollResults[in.FlowID]; ok {
		// Idempotent replay: the provider replays the original response for a
		// repeated flowId, so a retry can never create a second portfolio.
		clone := *cached
		return &clone, nil
	}
	if in.SignedSolanaTransaction == "" && in.Signature == "" {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "signature is required"}
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
		status:           "active",
		chain:            "solana",
		ownerAccountID:   "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:Owner111111111111111111111111111111111",
		agentAccountID:   auth.AgentAccountID,
		depositAccountID: depositAccount,
		valueUSD:         decimal.Zero,
		allocation:       allocation,
		schedule:         entities.GliderSchedule{Type: "interval", Frequency: "daily", NextDueAt: &next},
		createdAt:        now,
	}
	s.depositIndex[depositAccount] = portfolioID

	role := 1
	result := &entities.GliderPortfolio{
		PortfolioID:     portfolioID,
		StrategyID:      strategyID,
		StrategyVersion: 1,
		Status:          "active",
		AccountType:     "ECDSA",
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

// GetPortfolio reads a simulated portfolio.
func (s *Simulated) GetPortfolio(_ context.Context, portfolioID string) (*entities.GliderPortfolio, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetPortfolio"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	total := p.valueUSD
	role := 1
	return &entities.GliderPortfolio{
		PortfolioID:     p.id,
		StrategyID:      p.strategyID,
		StrategyVersion: p.strategyVersion,
		Status:          p.status,
		SmartAccounts: []entities.GliderSmartAccount{
			{AccountID: p.depositAccountID, DepositAccountID: p.depositAccountID, SwigRoleID: &role},
		},
		Schedule:      p.schedule,
		TotalValueUSD: &total,
		CreatedAt:     &p.createdAt,
	}, nil
}

// ListPortfolios lists simulated portfolios.
func (s *Simulated) ListPortfolios(ctx context.Context) ([]entities.GliderPortfolio, error) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.portfolios))
	for id := range s.portfolios {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Strings(ids)

	out := make([]entities.GliderPortfolio, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetPortfolio(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// GetPositions derives simulated positions from target weights and portfolio
// value, which keeps the numbers explainable instead of random.
func (s *Simulated) GetPositions(_ context.Context, portfolioID string) (*entities.GliderPositions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetPositions"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
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

func (s *Simulated) priceFor(symbol string) decimal.Decimal {
	if price, ok := s.prices[strings.ToUpper(symbol)]; ok {
		return price
	}
	return decimal.NewFromInt(100)
}

// StartPortfolio resumes automation.
func (s *Simulated) StartPortfolio(_ context.Context, portfolioID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("StartPortfolio"); err != nil {
		return err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	p.status = "active"
	return nil
}

// StopPortfolio pauses automation.
func (s *Simulated) StopPortfolio(_ context.Context, portfolioID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("StopPortfolio"); err != nil {
		return err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	p.status = "stopped"
	return nil
}

// TriggerRebalance dispatches a simulated rebalance, enforcing the cooldown.
func (s *Simulated) TriggerRebalance(_ context.Context, portfolioID string) (*entities.GliderOperationHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("TriggerRebalance"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	now := s.now()
	if s.rebalanceCooldown > 0 {
		if last, ok := s.lastRebalance[portfolioID]; ok {
			if elapsed := now.Sub(last); elapsed < s.rebalanceCooldown {
				return nil, &APIError{
					StatusCode: 429,
					Code:       "API_429",
					Message:    "rebalance triggered too soon after the previous rebalance",
					RetryAfter: s.rebalanceCooldown - elapsed,
				}
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

// GetOperation polls a simulated operation, completing it once due.
func (s *Simulated) GetOperation(_ context.Context, portfolioID, operationID string) (*entities.GliderOperationState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("GetOperation"); err != nil {
		return nil, err
	}
	op, ok := s.operations[operationID]
	if !ok || op.portfolioID != portfolioID {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "operation not found"}
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

// PrepareWithdrawal returns a simulated signable withdrawal authorization.
func (s *Simulated) PrepareWithdrawal(_ context.Context, portfolioID string, in entities.GliderWithdrawSignatureInput) (*entities.GliderWithdrawAuthorization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PrepareWithdrawal"); err != nil {
		return nil, err
	}
	if _, ok := s.portfolios[portfolioID]; !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	if !strings.HasPrefix(in.RecipientAccountID, "solana:") {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "recipientAccountId must be chain-bound"}
	}
	now := s.now()
	return &entities.GliderWithdrawAuthorization{
		AuthorizationID: "sim_auth_" + uuid.NewString()[:8],
		ExpiresAt:       ptrTime(now.Add(10 * time.Minute)),
		Message:         "Withdrawal authorization for " + portfolioID,
		Authorization:   &entities.GliderAuthorization{Kind: "solana-message", Text: "Withdraw from " + portfolioID},
	}, nil
}

// SubmitWithdrawal applies a simulated withdrawal.
func (s *Simulated) SubmitWithdrawal(_ context.Context, portfolioID string, in entities.GliderWithdrawSubmitInput) (*entities.GliderOperationHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("SubmitWithdrawal"); err != nil {
		return nil, err
	}
	p, ok := s.portfolios[portfolioID]
	if !ok {
		return nil, &APIError{StatusCode: 404, Code: "API_004", Message: "portfolio not found"}
	}
	if in.Signature == "" {
		return nil, &APIError{StatusCode: 400, Code: "API_400", Message: "signature is required"}
	}
	now := s.now()
	if in.Liquidate {
		p.valueUSD = decimal.Zero
	}
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validateSimulatedAllocation(legs []entities.InvestmentAllocationLeg) error {
	if len(legs) == 0 {
		return &APIError{StatusCode: 400, Code: "API_400", Message: "allocation.assets is required"}
	}
	sum := decimal.Zero
	for _, leg := range legs {
		if leg.Weight.LessThanOrEqual(decimal.Zero) {
			return &APIError{StatusCode: 400, Code: "API_400", Message: "allocation weights must be > 0"}
		}
		sum = sum.Add(leg.Weight)
	}
	if !sum.Equal(decimal.NewFromInt(100)) {
		return &APIError{
			StatusCode: 400,
			Code:       "API_400",
			Message:    "allocation validation failed",
			Details:    []string{fmt.Sprintf("allocation.assets: weights must sum to 100 (got %s)", sum.String())},
		}
	}
	return nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func ptrTime(t time.Time) *time.Time { return &t }
