package investment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/glider"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// In-memory repositories
// ---------------------------------------------------------------------------

type memoryStore struct {
	assets        map[uuid.UUID]*entities.InvestmentAsset
	strategies    map[uuid.UUID]*entities.InvestmentStrategy
	versions      map[uuid.UUID][]*entities.InvestmentStrategyVersion
	enrollments   map[uuid.UUID]*entities.InvestmentEnrollment
	holdings      map[uuid.UUID][]*entities.InvestmentHolding
	executions    map[uuid.UUID]*entities.InvestmentExecution
	transfers     map[uuid.UUID]*entities.InvestmentFundingTransfer
	signatures    map[uuid.UUID]*entities.InvestmentSignatureRequest
	confirmations map[string]*entities.InvestmentConfirmation
	operations    map[string]*entities.GliderOperation
	events        []*entities.InvestmentAuditEvent
	limits        map[uuid.UUID]*entities.InvestmentLimits
}

func newMemoryStore(assets ...*entities.InvestmentAsset) *memoryStore {
	store := &memoryStore{
		assets:        map[uuid.UUID]*entities.InvestmentAsset{},
		strategies:    map[uuid.UUID]*entities.InvestmentStrategy{},
		versions:      map[uuid.UUID][]*entities.InvestmentStrategyVersion{},
		enrollments:   map[uuid.UUID]*entities.InvestmentEnrollment{},
		holdings:      map[uuid.UUID][]*entities.InvestmentHolding{},
		executions:    map[uuid.UUID]*entities.InvestmentExecution{},
		transfers:     map[uuid.UUID]*entities.InvestmentFundingTransfer{},
		signatures:    map[uuid.UUID]*entities.InvestmentSignatureRequest{},
		confirmations: map[string]*entities.InvestmentConfirmation{},
		operations:    map[string]*entities.GliderOperation{},
		limits:        map[uuid.UUID]*entities.InvestmentLimits{},
	}
	for _, asset := range assets {
		store.assets[asset.ID] = asset
	}
	return store
}

// --- assets ---

func (s *memoryStore) Upsert(_ context.Context, asset *entities.InvestmentAsset) error {
	s.assets[asset.ID] = asset
	return nil
}

func (s *memoryStore) GetByID(_ context.Context, id uuid.UUID) (*entities.InvestmentAsset, error) {
	return s.assets[id], nil
}

func (s *memoryStore) GetByCAIP19(_ context.Context, caip19 string) (*entities.InvestmentAsset, error) {
	for _, asset := range s.assets {
		if asset.CAIP19 == caip19 {
			return asset, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) GetBySymbol(_ context.Context, symbol string) (*entities.InvestmentAsset, error) {
	for _, asset := range s.assets {
		if asset.Symbol == symbol {
			return asset, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) List(_ context.Context, query string, limit int) ([]*entities.InvestmentAsset, error) {
	out := make([]*entities.InvestmentAsset, 0, len(s.assets))
	for _, asset := range s.assets {
		if query != "" && !strings.Contains(strings.ToUpper(asset.Symbol), strings.ToUpper(query)) {
			continue
		}
		out = append(out, asset)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *memoryStore) CountAllowed(_ context.Context) (int, error) {
	count := 0
	for _, asset := range s.assets {
		if asset.Allowlisted && !asset.Prohibited {
			count++
		}
	}
	return count, nil
}

// --- strategies ---

func (s *memoryStore) Create(_ context.Context, strategy *entities.InvestmentStrategy) error {
	s.strategies[strategy.ID] = strategy
	return nil
}

func (s *memoryStore) Update(_ context.Context, strategy *entities.InvestmentStrategy) error {
	s.strategies[strategy.ID] = strategy
	return nil
}

func (s *memoryStore) GetStrategyByID(_ context.Context, id uuid.UUID) (*entities.InvestmentStrategy, error) {
	return s.strategies[id], nil
}

func (s *memoryStore) ListByUser(_ context.Context, userID uuid.UUID, status string) ([]*entities.InvestmentStrategy, error) {
	out := []*entities.InvestmentStrategy{}
	for _, strategy := range s.strategies {
		if strategy.UserID == nil || *strategy.UserID != userID {
			continue
		}
		if status != "" && string(strategy.Status) != status {
			continue
		}
		out = append(out, strategy)
	}
	return out, nil
}

func (s *memoryStore) ListByOwnerType(_ context.Context, ownerType entities.InvestmentStrategyOwnerType, limit int) ([]*entities.InvestmentStrategy, error) {
	out := []*entities.InvestmentStrategy{}
	for _, strategy := range s.strategies {
		if strategy.OwnerType != ownerType {
			continue
		}
		out = append(out, strategy)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *memoryStore) FindByGliderID(_ context.Context, gliderStrategyID string) (*entities.InvestmentStrategy, error) {
	for _, strategy := range s.strategies {
		if strategy.GliderStrategyID != nil && *strategy.GliderStrategyID == gliderStrategyID {
			return strategy, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) CreateVersion(_ context.Context, version *entities.InvestmentStrategyVersion) error {
	s.versions[version.StrategyID] = append(s.versions[version.StrategyID], version)
	return nil
}

func (s *memoryStore) GetVersion(_ context.Context, strategyID uuid.UUID, version int) (*entities.InvestmentStrategyVersion, error) {
	for _, candidate := range s.versions[strategyID] {
		if candidate.Version == version {
			return candidate, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) ListVersions(_ context.Context, strategyID uuid.UUID) ([]*entities.InvestmentStrategyVersion, error) {
	return s.versions[strategyID], nil
}

// --- enrollments ---

func (s *memoryStore) CreateEnrollment(_ context.Context, enrollment *entities.InvestmentEnrollment) error {
	s.enrollments[enrollment.ID] = enrollment
	return nil
}

func (s *memoryStore) UpdateEnrollment(_ context.Context, enrollment *entities.InvestmentEnrollment) error {
	s.enrollments[enrollment.ID] = enrollment
	return nil
}

func (s *memoryStore) GetEnrollmentByID(_ context.Context, id uuid.UUID) (*entities.InvestmentEnrollment, error) {
	return s.enrollments[id], nil
}

func (s *memoryStore) GetByUserAndStrategy(_ context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	for _, enrollment := range s.enrollments {
		if enrollment.UserID == userID && enrollment.StrategyID == strategyID {
			return enrollment, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) GetByPortfolioID(_ context.Context, portfolioID string) (*entities.InvestmentEnrollment, error) {
	for _, enrollment := range s.enrollments {
		if enrollment.GliderPortfolioID == portfolioID {
			return enrollment, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) ListEnrollmentsByUser(_ context.Context, userID uuid.UUID) ([]*entities.InvestmentEnrollment, error) {
	out := []*entities.InvestmentEnrollment{}
	for _, enrollment := range s.enrollments {
		if enrollment.UserID == userID {
			out = append(out, enrollment)
		}
	}
	return out, nil
}

func (s *memoryStore) ListActive(_ context.Context, limit int) ([]*entities.InvestmentEnrollment, error) {
	out := []*entities.InvestmentEnrollment{}
	for _, enrollment := range s.enrollments {
		if enrollment.Status != entities.InvestmentEnrollmentActive {
			continue
		}
		out = append(out, enrollment)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- holdings ---

func (s *memoryStore) ReplaceForEnrollment(_ context.Context, enrollmentID uuid.UUID, holdings []*entities.InvestmentHolding) error {
	s.holdings[enrollmentID] = holdings
	return nil
}

func (s *memoryStore) ListHoldingsByUser(_ context.Context, userID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	out := []*entities.InvestmentHolding{}
	for _, holdings := range s.holdings {
		for _, holding := range holdings {
			if holding.UserID == userID {
				out = append(out, holding)
			}
		}
	}
	return out, nil
}

func (s *memoryStore) ListByEnrollment(_ context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	return s.holdings[enrollmentID], nil
}

// --- executions ---

func (s *memoryStore) CreateExecution(_ context.Context, execution *entities.InvestmentExecution) error {
	s.executions[execution.ID] = execution
	return nil
}

func (s *memoryStore) UpdateExecution(_ context.Context, execution *entities.InvestmentExecution) error {
	s.executions[execution.ID] = execution
	return nil
}

func (s *memoryStore) GetExecutionByID(_ context.Context, id uuid.UUID) (*entities.InvestmentExecution, error) {
	return s.executions[id], nil
}

func (s *memoryStore) FindByIdempotencyKey(_ context.Context, key string) (*entities.InvestmentExecution, error) {
	for _, execution := range s.executions {
		if execution.IdempotencyKey == key {
			return execution, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) ListExecutionsByUser(_ context.Context, userID uuid.UUID, status string, limit int) ([]*entities.InvestmentExecution, error) {
	out := []*entities.InvestmentExecution{}
	for _, execution := range s.executions {
		if execution.UserID != userID {
			continue
		}
		if status != "" && string(execution.Status) != status {
			continue
		}
		out = append(out, execution)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- funding transfers ---

func (s *memoryStore) CreateTransfer(_ context.Context, transfer *entities.InvestmentFundingTransfer) error {
	s.transfers[transfer.ID] = transfer
	return nil
}

func (s *memoryStore) UpdateTransfer(_ context.Context, transfer *entities.InvestmentFundingTransfer) error {
	s.transfers[transfer.ID] = transfer
	return nil
}

func (s *memoryStore) FindTransferByIdempotencyKey(_ context.Context, key string) (*entities.InvestmentFundingTransfer, error) {
	for _, transfer := range s.transfers {
		if transfer.IdempotencyKey == key {
			return transfer, nil
		}
	}
	return nil, nil
}

func (s *memoryStore) ListByEnrollmentTransfer(_ context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentFundingTransfer, error) {
	out := []*entities.InvestmentFundingTransfer{}
	for _, transfer := range s.transfers {
		if transfer.EnrollmentID == enrollmentID {
			out = append(out, transfer)
		}
	}
	return out, nil
}

func (s *memoryStore) SumDepositsSince(_ context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	total := decimal.Zero
	for _, transfer := range s.transfers {
		if transfer.UserID == userID && transfer.Direction == "deposit" && !transfer.CreatedAt.Before(since) {
			total = total.Add(transfer.AmountUSD)
		}
	}
	return total, nil
}

// --- signature requests ---

func (s *memoryStore) CreateSignatureRequest(_ context.Context, request *entities.InvestmentSignatureRequest) error {
	s.signatures[request.ID] = request
	return nil
}

func (s *memoryStore) UpdateSignatureRequest(_ context.Context, request *entities.InvestmentSignatureRequest) error {
	s.signatures[request.ID] = request
	return nil
}

func (s *memoryStore) FindSignatureByFlow(_ context.Context, flow, providerFlowID string) (*entities.InvestmentSignatureRequest, error) {
	for _, request := range s.signatures {
		if request.Flow == flow && request.ProviderFlowID == providerFlowID {
			return request, nil
		}
	}
	return nil, nil
}

// --- confirmations ---

func (s *memoryStore) CreateConfirmation(_ context.Context, confirmation *entities.InvestmentConfirmation) error {
	s.confirmations[confirmation.Token] = confirmation
	return nil
}

func (s *memoryStore) GetByToken(_ context.Context, token string) (*entities.InvestmentConfirmation, error) {
	return s.confirmations[token], nil
}

func (s *memoryStore) Consume(_ context.Context, token string, now time.Time) error {
	stored, ok := s.confirmations[token]
	if !ok {
		return ErrConfirmationInvalid
	}
	if stored.ConsumedAt != nil {
		return ErrConfirmationInvalid
	}
	stored.ConsumedAt = &now
	return nil
}

func (s *memoryStore) FindPendingConfirmation(_ context.Context, userID uuid.UUID, action, actionHash string) (*entities.InvestmentConfirmation, error) {
	for _, stored := range s.confirmations {
		if stored.UserID == userID && stored.Action == action && stored.ActionHash == actionHash && stored.ConsumedAt == nil {
			return stored, nil
		}
	}
	return nil, nil
}

// --- operations ---

func (s *memoryStore) UpsertOperation(_ context.Context, operation *entities.GliderOperation) error {
	if existing, ok := s.operations[operation.ProviderOperationID]; ok {
		existing.State = operation.State
		existing.Error = operation.Error
		existing.FinishedAt = operation.FinishedAt
		existing.UpdatedAt = operation.UpdatedAt
		if operation.ExecutionID != nil {
			existing.ExecutionID = operation.ExecutionID
		}
		if operation.EnrollmentID != nil {
			existing.EnrollmentID = operation.EnrollmentID
		}
		return nil
	}
	s.operations[operation.ProviderOperationID] = operation
	return nil
}

func (s *memoryStore) ListOpenOperations(_ context.Context, limit int) ([]*entities.GliderOperation, error) {
	out := []*entities.GliderOperation{}
	for _, operation := range s.operations {
		switch operation.State {
		case "completed", "failed", "cancelled":
			continue
		}
		out = append(out, operation)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *memoryStore) GetOperationByProviderID(_ context.Context, providerOperationID string) (*entities.GliderOperation, error) {
	return s.operations[providerOperationID], nil
}

// --- audit ---

func (s *memoryStore) RecordAuditEvent(_ context.Context, event *entities.InvestmentAuditEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *memoryStore) ListAuditByUser(_ context.Context, userID uuid.UUID, limit int) ([]*entities.InvestmentAuditEvent, error) {
	out := []*entities.InvestmentAuditEvent{}
	for _, event := range s.events {
		if event.UserID != userID {
			continue
		}
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- limits ---

func (s *memoryStore) GetLimits(_ context.Context, userID uuid.UUID) (*entities.InvestmentLimits, error) {
	return s.limits[userID], nil
}

func (s *memoryStore) UpsertLimits(_ context.Context, userID uuid.UUID, limits *entities.InvestmentLimits) error {
	s.limits[userID] = limits
	return nil
}

// --- repository adapters (the interfaces take pointers to concrete types) ---

type enrollmentRepo struct{ store *memoryStore }

func (r enrollmentRepo) Create(ctx context.Context, enrollment *entities.InvestmentEnrollment) error {
	return r.store.CreateEnrollment(ctx, enrollment)
}
func (r enrollmentRepo) Update(ctx context.Context, enrollment *entities.InvestmentEnrollment) error {
	return r.store.UpdateEnrollment(ctx, enrollment)
}
func (r enrollmentRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentEnrollment, error) {
	return r.store.GetEnrollmentByID(ctx, id)
}
func (r enrollmentRepo) GetByUserAndStrategy(ctx context.Context, userID, strategyID uuid.UUID) (*entities.InvestmentEnrollment, error) {
	return r.store.GetByUserAndStrategy(ctx, userID, strategyID)
}
func (r enrollmentRepo) GetByPortfolioID(ctx context.Context, portfolioID string) (*entities.InvestmentEnrollment, error) {
	return r.store.GetByPortfolioID(ctx, portfolioID)
}
func (r enrollmentRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentEnrollment, error) {
	return r.store.ListEnrollmentsByUser(ctx, userID)
}
func (r enrollmentRepo) ListActive(ctx context.Context, limit int) ([]*entities.InvestmentEnrollment, error) {
	return r.store.ListActive(ctx, limit)
}

type holdingRepo struct{ store *memoryStore }

func (r holdingRepo) ReplaceForEnrollment(ctx context.Context, enrollmentID uuid.UUID, holdings []*entities.InvestmentHolding) error {
	return r.store.ReplaceForEnrollment(ctx, enrollmentID, holdings)
}
func (r holdingRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	return r.store.ListHoldingsByUser(ctx, userID)
}
func (r holdingRepo) ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentHolding, error) {
	return r.store.ListByEnrollment(ctx, enrollmentID)
}

type executionRepo struct{ store *memoryStore }

func (r executionRepo) Create(ctx context.Context, execution *entities.InvestmentExecution) error {
	return r.store.CreateExecution(ctx, execution)
}
func (r executionRepo) Update(ctx context.Context, execution *entities.InvestmentExecution) error {
	return r.store.UpdateExecution(ctx, execution)
}
func (r executionRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentExecution, error) {
	return r.store.GetExecutionByID(ctx, id)
}
func (r executionRepo) FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentExecution, error) {
	return r.store.FindByIdempotencyKey(ctx, key)
}
func (r executionRepo) ListByUser(ctx context.Context, userID uuid.UUID, status string, limit int) ([]*entities.InvestmentExecution, error) {
	return r.store.ListExecutionsByUser(ctx, userID, status, limit)
}

type transferRepo struct{ store *memoryStore }

func (r transferRepo) Create(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error {
	return r.store.CreateTransfer(ctx, transfer)
}
func (r transferRepo) Update(ctx context.Context, transfer *entities.InvestmentFundingTransfer) error {
	return r.store.UpdateTransfer(ctx, transfer)
}
func (r transferRepo) FindByIdempotencyKey(ctx context.Context, key string) (*entities.InvestmentFundingTransfer, error) {
	return r.store.FindTransferByIdempotencyKey(ctx, key)
}
func (r transferRepo) ListByEnrollment(ctx context.Context, enrollmentID uuid.UUID) ([]*entities.InvestmentFundingTransfer, error) {
	return r.store.ListByEnrollmentTransfer(ctx, enrollmentID)
}
func (r transferRepo) SumDepositsSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	return r.store.SumDepositsSince(ctx, userID, since)
}

type signatureRepo struct{ store *memoryStore }

func (r signatureRepo) Create(ctx context.Context, request *entities.InvestmentSignatureRequest) error {
	return r.store.CreateSignatureRequest(ctx, request)
}
func (r signatureRepo) Update(ctx context.Context, request *entities.InvestmentSignatureRequest) error {
	return r.store.UpdateSignatureRequest(ctx, request)
}
func (r signatureRepo) FindByFlow(ctx context.Context, flow, providerFlowID string) (*entities.InvestmentSignatureRequest, error) {
	return r.store.FindSignatureByFlow(ctx, flow, providerFlowID)
}

type confirmationRepo struct{ store *memoryStore }

func (r confirmationRepo) Create(ctx context.Context, confirmation *entities.InvestmentConfirmation) error {
	return r.store.CreateConfirmation(ctx, confirmation)
}
func (r confirmationRepo) GetByToken(ctx context.Context, token string) (*entities.InvestmentConfirmation, error) {
	return r.store.GetByToken(ctx, token)
}
func (r confirmationRepo) Consume(ctx context.Context, token string, now time.Time) error {
	return r.store.Consume(ctx, token, now)
}
func (r confirmationRepo) FindPending(ctx context.Context, userID uuid.UUID, action, actionHash string) (*entities.InvestmentConfirmation, error) {
	return r.store.FindPendingConfirmation(ctx, userID, action, actionHash)
}

type operationRepo struct{ store *memoryStore }

func (r operationRepo) Upsert(ctx context.Context, operation *entities.GliderOperation) error {
	return r.store.UpsertOperation(ctx, operation)
}
func (r operationRepo) ListOpen(ctx context.Context, limit int) ([]*entities.GliderOperation, error) {
	return r.store.ListOpenOperations(ctx, limit)
}
func (r operationRepo) GetByProviderID(ctx context.Context, providerOperationID string) (*entities.GliderOperation, error) {
	return r.store.GetOperationByProviderID(ctx, providerOperationID)
}

type auditRepo struct{ store *memoryStore }

func (r auditRepo) Record(ctx context.Context, event *entities.InvestmentAuditEvent) error {
	return r.store.RecordAuditEvent(ctx, event)
}
func (r auditRepo) ListByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.InvestmentAuditEvent, error) {
	return r.store.ListAuditByUser(ctx, userID, limit)
}

type limitsRepo struct{ store *memoryStore }

func (r limitsRepo) Get(ctx context.Context, userID uuid.UUID) (*entities.InvestmentLimits, error) {
	return r.store.GetLimits(ctx, userID)
}
func (r limitsRepo) Upsert(ctx context.Context, userID uuid.UUID, limits *entities.InvestmentLimits) error {
	return r.store.UpsertLimits(ctx, userID, limits)
}

// ---------------------------------------------------------------------------
// Fake owner signer + funding port
// ---------------------------------------------------------------------------

// fakeSigner stands in for the portfolio-owner authority. The real cryptographic
// signing is covered by the investmentowner package tests.
type fakeSigner struct {
	accountID string
	signCalls int
	failMsg   error
}

func (f *fakeSigner) OwnerAccount(_ context.Context, _ uuid.UUID) (string, error) {
	if f.accountID == "" {
		return "solana:testnet:Owner0000000000000000000000000000000000000000", nil
	}
	return f.accountID, nil
}

func (f *fakeSigner) SignSolanaMessage(_ context.Context, _ uuid.UUID, message string) (string, error) {
	if f.failMsg != nil {
		return "", f.failMsg
	}
	f.signCalls++
	return "sig:" + message, nil
}

func (f *fakeSigner) SignSolanaTransaction(_ context.Context, _ uuid.UUID, tx string) (string, error) {
	f.signCalls++
	return "signed:" + tx, nil
}

type fakeFunding struct {
	store         *memoryStore
	provider      *glider.Simulated
	available     decimal.Decimal
	recipient     string
	transfers     int
	failAvailable error
	failTransfer  error
	lastAmount    decimal.Decimal
}

func (f *fakeFunding) Available(_ context.Context, _ uuid.UUID, _ string) (decimal.Decimal, error) {
	if f.failAvailable != nil {
		return decimal.Zero, f.failAvailable
	}
	return f.available, nil
}

func (f *fakeFunding) TransferToPortfolio(_ context.Context, in FundingRequest) (*FundingResult, error) {
	if f.failTransfer != nil {
		return nil, f.failTransfer
	}
	f.transfers++
	f.lastAmount = in.AmountUSD
	// The provider credits the portfolio's deposit account when USDC lands.
	for _, enrollment := range f.store.enrollments {
		if enrollment.DepositAccountID == in.Destination && f.provider != nil {
			if err := f.provider.SimulateDeposit(enrollment.GliderPortfolioID, in.AmountUSD); err != nil {
				return nil, err
			}
		}
	}
	transferID := uuid.New()
	return &FundingResult{
		TransferID:   transferID,
		OnchainTxRef: "sim_tx_" + transferID.String()[:8],
		Status:       "PROCESSING",
	}, nil
}

func (f *fakeFunding) RecipientAccount(_ context.Context, _ uuid.UUID) (string, error) {
	if f.recipient == "" {
		return "", errors.New("no settlement account")
	}
	return f.recipient, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type harness struct {
	service  *Service
	store    *memoryStore
	provider *glider.Simulated
	signer   *fakeSigner
	funding  *fakeFunding
	userID   uuid.UUID
	profile  *UserProfile
	now      time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := newMemoryStore(
		&entities.InvestmentAsset{
			ID: uuid.New(), CAIP19: "solana:testnet/token:usdc", Symbol: "USDC", Name: "USD Coin",
			AssetClass: "cash", Chain: "solana", Decimals: 6, Allowlisted: true,
		},
		&entities.InvestmentAsset{
			ID: uuid.New(), CAIP19: "solana:testnet/slip44:501", Symbol: "SOL", Name: "Solana",
			AssetClass: "crypto", Chain: "solana", Decimals: 9, Allowlisted: true,
		},
	)

	provider := glider.NewSimulated(glider.SimulatedConfig{})
	signer := &fakeSigner{}
	funding := &fakeFunding{store: store, provider: provider, available: decimal.NewFromInt(100000), recipient: "solana:testnet:RailSettlementAddress"}

	cfg := testConfig()
	cfg.DefaultLimits.MaxPositionPct = decimal.NewFromInt(100)
	cfg.DefaultLimits.MaxEnrollments = 3

	service := NewService(Deps{
		Config:        cfg,
		Provider:      provider,
		Signer:        signer,
		Funding:       funding,
		Assets:        &assetRepoAdapter{store: store},
		Strategies:    &strategyRepoAdapter{store: store},
		Enrollments:   enrollmentRepo{store: store},
		Holdings:      holdingRepo{store: store},
		Executions:    executionRepo{store: store},
		Transfers:     transferRepo{store: store},
		Signatures:    signatureRepo{store: store},
		Confirmations: confirmationRepo{store: store},
		Operations:    operationRepo{store: store},
		Audit:         auditRepo{store: store},
		Limits:        limitsRepo{store: store},
		Users:         &fakeUserProfiles{},
	})
	now := time.Now().UTC()
	h := &harness{service: service, store: store, provider: provider, signer: signer, funding: funding, userID: uuid.New(), now: now}
	service.SetClock(func() time.Time { return h.now })
	h.setTier("advanced")
	return h
}

// advance moves the harness clock forward; the service shares the same view.
func (h *harness) advance(d time.Duration) {
	h.now = h.now.Add(d)
}

func (h *harness) setTier(tier string) {
	h.profile = &UserProfile{KYCTier: tier, KYCStatus: "approved", Country: "NG"}
	// Rebinding the profile reader keeps the policy layer authoritative.
	h.service.users = &fakeUserProfiles{profile: h.profile}
	h.service.policy = NewPolicy(h.service.users, h.service.cfg)
}

// assetRepoAdapter satisfies AssetRepository while the store methods use
// different names, keeping the test store readable.
type assetRepoAdapter struct{ store *memoryStore }

func (a *assetRepoAdapter) Upsert(ctx context.Context, asset *entities.InvestmentAsset) error {
	return a.store.Upsert(ctx, asset)
}
func (a *assetRepoAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentAsset, error) {
	return a.store.GetByID(ctx, id)
}
func (a *assetRepoAdapter) GetByCAIP19(ctx context.Context, caip19 string) (*entities.InvestmentAsset, error) {
	return a.store.GetByCAIP19(ctx, caip19)
}
func (a *assetRepoAdapter) GetBySymbol(ctx context.Context, symbol string) (*entities.InvestmentAsset, error) {
	return a.store.GetBySymbol(ctx, symbol)
}
func (a *assetRepoAdapter) List(ctx context.Context, query string, limit int) ([]*entities.InvestmentAsset, error) {
	return a.store.List(ctx, query, limit)
}
func (a *assetRepoAdapter) CountAllowed(ctx context.Context) (int, error) {
	return a.store.CountAllowed(ctx)
}

type strategyRepoAdapter struct{ store *memoryStore }

func (a *strategyRepoAdapter) Create(ctx context.Context, strategy *entities.InvestmentStrategy) error {
	return a.store.Create(ctx, strategy)
}
func (a *strategyRepoAdapter) Update(ctx context.Context, strategy *entities.InvestmentStrategy) error {
	return a.store.Update(ctx, strategy)
}
func (a *strategyRepoAdapter) GetByID(ctx context.Context, id uuid.UUID) (*entities.InvestmentStrategy, error) {
	return a.store.GetStrategyByID(ctx, id)
}
func (a *strategyRepoAdapter) ListByUser(ctx context.Context, userID uuid.UUID, status string) ([]*entities.InvestmentStrategy, error) {
	return a.store.ListByUser(ctx, userID, status)
}
func (a *strategyRepoAdapter) ListByOwnerType(ctx context.Context, ownerType entities.InvestmentStrategyOwnerType, limit int) ([]*entities.InvestmentStrategy, error) {
	return a.store.ListByOwnerType(ctx, ownerType, limit)
}
func (a *strategyRepoAdapter) FindByGliderID(ctx context.Context, gliderStrategyID string) (*entities.InvestmentStrategy, error) {
	return a.store.FindByGliderID(ctx, gliderStrategyID)
}
func (a *strategyRepoAdapter) CreateVersion(ctx context.Context, version *entities.InvestmentStrategyVersion) error {
	return a.store.CreateVersion(ctx, version)
}
func (a *strategyRepoAdapter) GetVersion(ctx context.Context, strategyID uuid.UUID, version int) (*entities.InvestmentStrategyVersion, error) {
	return a.store.GetVersion(ctx, strategyID, version)
}
func (a *strategyRepoAdapter) ListVersions(ctx context.Context, strategyID uuid.UUID) ([]*entities.InvestmentStrategyVersion, error) {
	return a.store.ListVersions(ctx, strategyID)
}

// assetID resolves a test asset id by symbol.
func (h *harness) assetID(t *testing.T, symbol string) uuid.UUID {
	t.Helper()
	for _, asset := range h.store.assets {
		if asset.Symbol == symbol {
			return asset.ID
		}
	}
	t.Fatalf("asset %s not found", symbol)
	return uuid.Nil
}

func (h *harness) createRequest(symbols ...string) *entities.InvestmentCreateStrategyRequest {
	weight := decimal.NewFromInt(int64(100 / len(symbols)))
	legs := make([]entities.InvestmentAllocationLeg, 0, len(symbols))
	for index, symbol := range symbols {
		leg := entities.InvestmentAllocationLeg{Symbol: symbol, Weight: weight}
		if index == 0 {
			// Absorb the rounding remainder so the weights sum to exactly 100.
			leg.Weight = decimal.NewFromInt(100).Sub(weight.Mul(decimal.NewFromInt(int64(len(symbols) - 1))))
		}
		legs = append(legs, leg)
	}
	return &entities.InvestmentCreateStrategyRequest{
		Name:             "Test strategy",
		Objective:        "grow steadily",
		Risk:             "medium",
		Horizon:          "long",
		TargetAllocation: legs,
		Rationale:        "test",
	}
}

// confirmCreate stages then confirms a strategy creation, returning the response.
func (h *harness) confirmCreate(t *testing.T, req *entities.InvestmentCreateStrategyRequest) *entities.InvestmentCreateStrategyResponse {
	t.Helper()
	staged, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)
	require.NotNil(t, staged.Confirmation)

	req.ConfirmationToken = staged.Confirmation.Token
	confirmed, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, confirmed.Status)
	return confirmed
}

// ---------------------------------------------------------------------------
// Strategy lifecycle
// ---------------------------------------------------------------------------

func TestCreateStrategyStagesThenExecutes(t *testing.T) {
	h := newHarness(t)
	req := h.createRequest("USDC", "SOL")

	staged, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)
	require.NotNil(t, staged.Confirmation)
	assert.NotEmpty(t, staged.Confirmation.Token)
	assert.Equal(t, "create_strategy", staged.Confirmation.Action)
	assert.Nil(t, staged.Strategy, "nothing may be created before the user confirms")

	req.ConfirmationToken = staged.Confirmation.Token
	confirmed, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, confirmed.Status)
	require.NotNil(t, confirmed.Strategy)
	require.NotNil(t, confirmed.Version)

	assert.Equal(t, entities.InvestmentStrategyUserReview, confirmed.Strategy.Status)
	assert.Equal(t, 1, confirmed.Strategy.CurrentVersion)
	require.NotNil(t, confirmed.Strategy.GliderStrategyID)
	require.NotNil(t, confirmed.Version.GliderStrategyVersion)

	// The provider really holds a strategy with the normalized allocation.
	providerStrategy, err := h.provider.GetStrategy(context.Background(), *confirmed.Strategy.GliderStrategyID)
	require.NoError(t, err)
	sum := decimal.Zero
	for _, leg := range providerStrategy.Allocation.Assets {
		sum = sum.Add(leg.Weight)
	}
	assert.True(t, sum.Equal(decimal.NewFromInt(100)))

	// And the action is auditable.
	events, err := h.store.ListAuditByUser(context.Background(), h.userID, 50)
	require.NoError(t, err)
	assert.True(t, hasEvent(events, EventStrategyCreated))
	assert.True(t, hasEvent(events, EventConfirmationRequest))
}

// TestDuplicateStageAndConfirmIsIdempotent: an identical retried proposal
// must reuse the same confirmation token, and replaying a consumed token
// must fail without executing again.
func TestDuplicateStageAndConfirmIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	req := h.createRequest("USDC", "SOL")

	first, err := h.service.CreateStrategy(ctx, h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, first.Status)
	require.NotNil(t, first.Confirmation)

	second, err := h.service.CreateStrategy(ctx, h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, second.Status)
	require.NotNil(t, second.Confirmation)
	assert.Equal(t, first.Confirmation.Token, second.Confirmation.Token,
		"an identical retried proposal must reuse the same confirmation token")

	req.ConfirmationToken = first.Confirmation.Token
	confirmed, err := h.service.CreateStrategy(ctx, h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, confirmed.Status)

	replay, err := h.service.CreateStrategy(ctx, h.userID, req, entities.InvestmentActorMiriam)
	require.Error(t, err, "replaying an already consumed token must fail")
	assert.Nil(t, replay, "nothing may be returned from a consumed token replay")
}

func TestCreateStrategyRejectsAllocationThatDoesNotSum(t *testing.T) {
	h := newHarness(t)
	req := &entities.InvestmentCreateStrategyRequest{
		Name: "Broken",
		TargetAllocation: []entities.InvestmentAllocationLeg{
			{Symbol: "USDC", Weight: decimal.NewFromInt(70)},
			{Symbol: "SOL", Weight: decimal.NewFromInt(20)},
		},
	}
	_, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Empty(t, h.store.strategies, "an invalid proposal must never reach the provider")
}

func TestConfirmationTokenIsSingleUse(t *testing.T) {
	h := newHarness(t)
	req := h.createRequest("USDC", "SOL")
	staged, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	req.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	_, err = h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a confirmation must not be replayable")
}

func TestConfirmationBindsToTheExactPayload(t *testing.T) {
	h := newHarness(t)
	req := h.createRequest("USDC", "SOL")
	staged, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	tampered := h.createRequest("USDC", "SOL")
	tampered.Name = "Something else entirely"
	tampered.ConfirmationToken = staged.Confirmation.Token

	_, err = h.service.CreateStrategy(context.Background(), h.userID, tampered, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a changed payload must invalidate the confirmation")
}

func TestExpiredConfirmationIsRejected(t *testing.T) {
	h := newHarness(t)
	req := h.createRequest("USDC")
	staged, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	// Advance the clock past the confirmation TTL.
	h.advance(h.service.cfg.ConfirmationTTL + time.Minute)
	req.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid)
}

func TestPolicyAllowsUnverifiedUserToStartStrategy(t *testing.T) {
	h := newHarness(t)
	h.setTier("basic")

	req := h.createRequest("USDC")
	response, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err, "strategy investing must not be KYC gated")
	require.NotNil(t, response.Policy)
	assert.Equal(t, entities.InvestmentVerdictRequiresConfirmation, response.Policy.Verdict)
}

func TestPolicyAllowsUnverifiedNonKYCUserToStartStrategy(t *testing.T) {
	h := newHarness(t)
	h.setTier("non_kyc")

	req := h.createRequest("USDC")
	response, err := h.service.CreateStrategy(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err, "a brand-new account can start a Glider strategy")
	require.NotNil(t, response.Policy)
	assert.Equal(t, entities.InvestmentVerdictRequiresConfirmation, response.Policy.Verdict)
}

// ---------------------------------------------------------------------------
// Enrollment and funding
// ---------------------------------------------------------------------------

func TestEnrollStagesThenDeploysAndFunds(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	enroll := &entities.InvestmentEnrollRequest{
		StrategyID: strategy.Strategy.ID.String(),
		AmountUSD:  decimal.NewFromInt(500),
		Source:     "spending",
	}

	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)
	require.NotNil(t, staged.Preview)
	assert.Equal(t, "enroll_strategy", staged.Confirmation.Action)
	assert.Empty(t, h.store.enrollments, "no portfolio may exist before confirmation")

	enroll.ConfirmationToken = staged.Confirmation.Token
	enrolled, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, enrolled.Status)
	require.NotNil(t, enrolled.Enrollment)
	require.NotNil(t, enrolled.Funding)

	assert.Equal(t, entities.InvestmentEnrollmentActive, enrolled.Enrollment.Status)
	assert.Equal(t, "solana", enrolled.Enrollment.Chain)
	assert.NotEmpty(t, enrolled.Enrollment.DepositAccountID)
	assert.True(t, h.provider.PortfolioExists(enrolled.Enrollment.GliderPortfolioID))

	// The funding leg really moved money.
	assert.Equal(t, 1, h.funding.transfers)
	assert.True(t, h.funding.lastAmount.Equal(decimal.NewFromInt(500)))
	assert.Equal(t, "PROCESSING", enrolled.Funding.Status)

	// Positions were synced from the provider, not invented.
	positions, err := h.service.GetPositions(context.Background(), h.userID)
	require.NoError(t, err)
	require.NotEmpty(t, positions)

	summary, err := h.service.GetPortfolio(context.Background(), h.userID)
	require.NoError(t, err)
	assert.True(t, summary.TotalValueUSD.Equal(decimal.NewFromInt(500)), "portfolio value %s", summary.TotalValueUSD)
	assert.True(t, summary.UnrealizedPnLUSD.Equal(decimal.Zero))

	// The owner authorization was recorded for replay protection.
	require.Len(t, h.store.signatures, 1)
	for _, signature := range h.store.signatures {
		assert.Equal(t, "enroll", signature.Flow)
		assert.Equal(t, "submitted", signature.Status)
		require.NotNil(t, signature.EnrollmentID)
	}
}

func TestEnrollIsIdempotent(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(250)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	first, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	// A repeated request (new idempotency key, no confirmation) returns the same
	// portfolio instead of creating a second one.
	second, err := h.service.Enroll(context.Background(), h.userID, &entities.InvestmentEnrollRequest{
		StrategyID: strategy.Strategy.ID.String(),
		AmountUSD:  decimal.NewFromInt(250),
	}, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, second.Status)
	assert.Equal(t, first.Enrollment.ID, second.Enrollment.ID)
	assert.Len(t, h.store.enrollments, 1)
}

func TestEnrollRejectsInsufficientFunds(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	h.funding.available = decimal.NewFromInt(50)

	_, err := h.service.Enroll(context.Background(), h.userID, &entities.InvestmentEnrollRequest{
		StrategyID: strategy.Strategy.ID.String(),
		AmountUSD:  decimal.NewFromInt(500),
	}, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrPolicyBlocked)
	assert.Contains(t, err.Error(), "available")
	assert.Empty(t, h.store.enrollments)
}

func TestEnrollReportsFundingFailureHonestly(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(100)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token

	h.funding.failTransfer = errors.New("circle transfer rejected")
	response, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err, "a portfolio that exists is reported, not hidden")
	require.NotNil(t, response.Funding)
	assert.Equal(t, "FAILED", response.Funding.Status)
	assert.Contains(t, response.Funding.FailureReason, "circle transfer rejected")
	assert.Len(t, h.store.enrollments, 1)
}

// ---------------------------------------------------------------------------
// Allocation changes, rebalancing, withdrawals
// ---------------------------------------------------------------------------

func TestPlaceOrderPublishesANewVersionAndFundsTheBuy(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	// Keep the seed under the high-value threshold so the order, not the seed,
	// is what the test exercises.
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(500)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	order := &entities.InvestmentOrderRequest{
		StrategyID: strategy.Strategy.ID.String(),
		Symbol:     "SOL",
		Side:       "buy",
		AmountUSD:  decimal.NewFromInt(200),
	}
	stagedOrder, err := h.service.PlaceOrder(context.Background(), h.userID, order, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, stagedOrder.Status)

	order.ConfirmationToken = stagedOrder.Confirmation.Token
	placed, err := h.service.PlaceOrder(context.Background(), h.userID, order, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, placed.Status)
	require.NotNil(t, placed.Execution)
	assert.Equal(t, entities.InvestmentExecutionSubmitted, placed.Execution.Status)
	assert.Equal(t, "buy", placed.Execution.Side)

	// A new immutable version exists and the strategy points at it.
	updated, versions, err := h.service.GetStrategy(context.Background(), h.userID, strategy.Strategy.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)
	assert.Len(t, versions, 2)

	// The buy was funded and the portfolio now holds the extra money.
	assert.Equal(t, 2, h.funding.transfers)
	summary, err := h.service.GetPortfolio(context.Background(), h.userID)
	require.NoError(t, err)
	assert.True(t, summary.TotalValueUSD.Equal(decimal.NewFromInt(700)), "value %s", summary.TotalValueUSD)
}

func TestManualRebalanceSurfacesProviderCooldown(t *testing.T) {
	h := newHarness(t)
	h.provider.SetRebalanceCooldown(time.Hour)
	strategy := h.confirmCreate(t, h.createRequest("USDC"))

	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(100)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	// The first rebalance is accepted; the second falls inside the provider's
	// cooldown and must come back as a typed, explainable error.
	_, err = h.service.TriggerRebalance(context.Background(), h.userID, strategy.Strategy.ID, "manual", entities.InvestmentActorUser)
	require.NoError(t, err)
	_, err = h.service.TriggerRebalance(context.Background(), h.userID, strategy.Strategy.ID, "manual", entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrProviderCooldown, "a provider 429 must surface as a typed cooldown")
}

func TestRebalanceTracksTheProviderOperation(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String()}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	enrolled, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.NotEmpty(t, enrolled.Enrollment.GliderPortfolioID)

	execution, err := h.service.TriggerRebalance(context.Background(), h.userID, strategy.Strategy.ID, "drift", entities.InvestmentActorWorker)
	require.NoError(t, err)
	require.NotNil(t, execution)
	assert.NotEmpty(t, execution.ProviderOperationID)

	open, err := h.store.ListOpenOperations(context.Background(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, open)

	// The provider completes operations asynchronously.
	time.Sleep(5 * time.Millisecond)
	polled, err := h.service.PollOperations(context.Background(), 10)
	require.NoError(t, err)
	assert.Equal(t, 1, polled)

	settled, err := h.service.GetExecution(context.Background(), h.userID, execution.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.InvestmentExecutionFilled, settled.Status)
	require.NotNil(t, settled.CompletedAt)
}

func TestWithdrawRequiresStepUpAndOwnerSignature(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(400)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	withdraw := &entities.InvestmentWithdrawalRequest{
		StrategyID: strategy.Strategy.ID.String(),
		AssetID:    h.assetID(t, "USDC").String(),
		AmountUSD:  decimal.NewFromInt(100),
	}

	// Without a verified step-up the request is refused outright.
	refused, err := h.service.Withdraw(context.Background(), h.userID, withdraw, false, entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrStepUpRequired)
	require.NotNil(t, refused.Policy)
	assert.Equal(t, entities.InvestmentVerdictRequiresAuthentication, refused.Policy.Verdict)

	// With it, the owner's key signs the authorization and the operation is tracked.
	accepted, err := h.service.Withdraw(context.Background(), h.userID, withdraw, true, entities.InvestmentActorUser)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, accepted.Status)
	assert.NotEmpty(t, accepted.OperationID)
	assert.NotEmpty(t, accepted.Recipient)
	assert.Positive(t, h.signer.signCalls)

	all, err := h.store.ListByEnrollmentTransfer(context.Background(), *accepted.Execution.EnrollmentID)
	require.NoError(t, err)
	withdrawals := make([]*entities.InvestmentFundingTransfer, 0, 1)
	for _, transfer := range all {
		if transfer.Direction == "withdrawal" {
			withdrawals = append(withdrawals, transfer)
		}
	}
	require.Len(t, withdrawals, 1, "exactly one withdrawal transfer for this enrollment")
	assert.Equal(t, "SUBMITTED", withdrawals[0].Status)
	assert.Equal(t, accepted.Recipient, withdrawals[0].DestinationAccountID)
}

func hasEvent(events []*entities.InvestmentAuditEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
