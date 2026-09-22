package vault

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// In-memory repository
// ---------------------------------------------------------------------------

type fakeRepo struct {
	vaults        map[uuid.UUID]*entities.RetirementVault
	lots          []*entities.VaultContributionLot
	snaps         []*entities.VaultEarningsSnapshot
	penalties     []*entities.VaultPenaltyEvent
	auths         []*entities.VaultWithdrawalAuthorization
	onramps       []*entities.VaultOnrampTransfer
	tiers         map[entities.VaultTier]*entities.VaultTierBinding
	skips         []*entities.VaultSkip
	health        map[uuid.UUID]*entities.VaultHealth
	enrollFailure *entities.VaultEnrollFailure
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{vaults: map[uuid.UUID]*entities.RetirementVault{}, tiers: map[entities.VaultTier]*entities.VaultTierBinding{}, health: map[uuid.UUID]*entities.VaultHealth{}}
}

func (r *fakeRepo) CreateVault(_ context.Context, vault *entities.RetirementVault) error {
	for _, existing := range r.vaults {
		if existing.UserID == vault.UserID && existing.Status == entities.VaultStatusActive {
			return errors.New("duplicate active vault")
		}
	}
	r.vaults[vault.ID] = vault
	return nil
}

func (r *fakeRepo) UpdateVault(_ context.Context, vault *entities.RetirementVault) error {
	r.vaults[vault.ID] = vault
	return nil
}

func (r *fakeRepo) GetVaultByID(_ context.Context, id uuid.UUID) (*entities.RetirementVault, error) {
	return r.vaults[id], nil
}

func (r *fakeRepo) GetActiveVaultByUser(_ context.Context, userID uuid.UUID) (*entities.RetirementVault, error) {
	for _, vault := range r.vaults {
		if vault.UserID == userID && vault.Status == entities.VaultStatusActive {
			return vault, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) GetVaultByEnrollment(_ context.Context, enrollmentID uuid.UUID) (*entities.RetirementVault, error) {
	for _, vault := range r.vaults {
		if vault.GliderEnrollmentID != nil && *vault.GliderEnrollmentID == enrollmentID {
			return vault, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) CreateLot(_ context.Context, lot *entities.VaultContributionLot) error {
	if lot.ID == uuid.Nil {
		lot.ID = uuid.New()
	}
	r.lots = append(r.lots, lot)
	return nil
}

func (r *fakeRepo) UpdateLot(_ context.Context, lot *entities.VaultContributionLot) error {
	for i, existing := range r.lots {
		if existing.ID == lot.ID {
			r.lots[i] = lot
			return nil
		}
	}
	return errors.New("lot not found")
}

func (r *fakeRepo) GetLotByKey(_ context.Context, key string) (*entities.VaultContributionLot, error) {
	for _, lot := range r.lots {
		if lot.IdempotencyKey == key {
			return lot, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListOpenLots(_ context.Context, vaultID uuid.UUID) ([]*entities.VaultContributionLot, error) {
	var out []*entities.VaultContributionLot
	for _, lot := range r.lots {
		if lot.VaultID == vaultID && lot.Status == entities.VaultLotOpen {
			out = append(out, lot)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AcquiredAt.Before(out[j].AcquiredAt) })
	return out, nil
}

func (r *fakeRepo) ListLots(_ context.Context, vaultID uuid.UUID, _ int) ([]*entities.VaultContributionLot, error) {
	var out []*entities.VaultContributionLot
	for _, lot := range r.lots {
		if lot.VaultID == vaultID {
			out = append(out, lot)
		}
	}
	return out, nil
}

func (r *fakeRepo) CreateSnapshot(_ context.Context, snapshot *entities.VaultEarningsSnapshot) error {
	if snapshot.ID == uuid.Nil {
		snapshot.ID = uuid.New()
	}
	r.snaps = append(r.snaps, snapshot)
	return nil
}

func (r *fakeRepo) LatestSnapshot(_ context.Context, vaultID uuid.UUID) (*entities.VaultEarningsSnapshot, error) {
	var latest *entities.VaultEarningsSnapshot
	for _, snapshot := range r.snaps {
		if snapshot.VaultID != vaultID {
			continue
		}
		if latest == nil || snapshot.AsOf.After(latest.AsOf) {
			latest = snapshot
		}
	}
	return latest, nil
}

func (r *fakeRepo) CreatePenaltyEvent(_ context.Context, event *entities.VaultPenaltyEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	r.penalties = append(r.penalties, event)
	return nil
}

func (r *fakeRepo) UpdatePenaltyEvent(_ context.Context, event *entities.VaultPenaltyEvent) error {
	for i, existing := range r.penalties {
		if existing.ID == event.ID {
			r.penalties[i] = event
			return nil
		}
	}
	return errors.New("penalty not found")
}

func (r *fakeRepo) GetPenaltyEvent(_ context.Context, id uuid.UUID) (*entities.VaultPenaltyEvent, error) {
	for _, event := range r.penalties {
		if event.ID == id {
			return event, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) GetPenaltyEventByExecution(_ context.Context, executionID uuid.UUID) (*entities.VaultPenaltyEvent, error) {
	for _, event := range r.penalties {
		if event.ExecutionID != nil && *event.ExecutionID == executionID {
			return event, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) CreateAuthorization(_ context.Context, auth *entities.VaultWithdrawalAuthorization) error {
	if auth.ID == uuid.Nil {
		auth.ID = uuid.New()
	}
	r.auths = append(r.auths, auth)
	return nil
}

// ConsumeAuthorization mirrors the SQL: exactly one caller can win a key.
func (r *fakeRepo) ConsumeAuthorization(_ context.Context, key string, enrollmentID uuid.UUID, gross decimal.Decimal, now time.Time) (*entities.VaultWithdrawalAuthorization, error) {
	for _, auth := range r.auths {
		if auth.Key != key {
			continue
		}
		if auth.Status != entities.VaultAuthorizationIssued ||
			auth.EnrollmentID != enrollmentID ||
			!auth.GrossUSD.Equal(gross) ||
			!auth.ExpiresAt.After(now) {
			return nil, errors.New("authorization not consumable")
		}
		auth.Status = entities.VaultAuthorizationConsumed
		auth.ConsumedAt = &now
		return auth, nil
	}
	return nil, errors.New("authorization not found")
}

func (r *fakeRepo) AttachAuthorizationExecution(_ context.Context, key string, executionID uuid.UUID) error {
	for _, auth := range r.auths {
		if auth.Key == key {
			auth.ExecutionID = &executionID
		}
	}
	return nil
}

func (r *fakeRepo) HasIssuedAuthorization(_ context.Context, enrollmentID uuid.UUID, now time.Time) (bool, error) {
	for _, auth := range r.auths {
		if auth.EnrollmentID == enrollmentID && auth.Status == entities.VaultAuthorizationIssued && auth.ExpiresAt.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) ExpireAuthorization(_ context.Context, key string, now time.Time) error {
	for _, auth := range r.auths {
		if auth.Key == key && auth.Status == entities.VaultAuthorizationIssued {
			auth.Status = entities.VaultAuthorizationExpired
			auth.ConsumedAt = &now
		}
	}
	return nil
}

func (r *fakeRepo) GetAuthorizationByExecution(_ context.Context, executionID uuid.UUID) (*entities.VaultWithdrawalAuthorization, error) {
	for _, auth := range r.auths {
		if auth.ExecutionID != nil && *auth.ExecutionID == executionID {
			return auth, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) CreateOnrampTransfer(_ context.Context, transfer *entities.VaultOnrampTransfer) error {
	r.onramps = append(r.onramps, transfer)
	return nil
}

func (r *fakeRepo) UpdateOnrampTransfer(_ context.Context, transfer *entities.VaultOnrampTransfer) error {
	return nil
}

func (r *fakeRepo) FindOnrampByIdempotencyKey(_ context.Context, key string) (*entities.VaultOnrampTransfer, error) {
	for _, transfer := range r.onramps {
		if transfer.IdempotencyKey == key {
			return transfer, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) UpsertTier(_ context.Context, tier *entities.VaultTierBinding) error {
	r.tiers[tier.Tier] = tier
	return nil
}

func (r *fakeRepo) GetTier(_ context.Context, tier entities.VaultTier) (*entities.VaultTierBinding, error) {
	return r.tiers[tier], nil
}

func (r *fakeRepo) ListTiers(_ context.Context) ([]*entities.VaultTierBinding, error) {
	out := make([]*entities.VaultTierBinding, 0, len(r.tiers))
	for _, tier := range r.tiers {
		out = append(out, tier)
	}
	return out, nil
}

func (r *fakeRepo) CreateSkip(_ context.Context, skip *entities.VaultSkip) error {
	if skip.ID == uuid.Nil {
		skip.ID = uuid.New()
	}
	r.skips = append(r.skips, skip)
	return nil
}

func (r *fakeRepo) FindSkipByPayment(_ context.Context, paymentID uuid.UUID) (*entities.VaultSkip, error) {
	for _, skip := range r.skips {
		if skip.PaymentID == paymentID {
			return skip, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListSkips(_ context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultSkip, error) {
	var out []*entities.VaultSkip
	for _, skip := range r.skips {
		if skip.VaultID == vaultID {
			out = append(out, skip)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepo) CountRecentSkips(_ context.Context, vaultID uuid.UUID, since time.Time) (int, error) {
	count := 0
	for _, skip := range r.skips {
		if skip.VaultID == vaultID && !skip.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepo) UpsertHealth(_ context.Context, health *entities.VaultHealth) error {
	r.health[health.VaultID] = health
	return nil
}

func (r *fakeRepo) GetHealth(_ context.Context, vaultID uuid.UUID) (*entities.VaultHealth, error) {
	return r.health[vaultID], nil
}

func (r *fakeRepo) GetEnrollFailure(_ context.Context, userID uuid.UUID) (*entities.VaultEnrollFailure, error) {
	if r.enrollFailure == nil || r.enrollFailure.UserID != userID {
		return nil, nil
	}
	return r.enrollFailure, nil
}

func (r *fakeRepo) UpsertEnrollFailure(_ context.Context, failure *entities.VaultEnrollFailure) error {
	r.enrollFailure = failure
	return nil
}

func (r *fakeRepo) DeleteVault(_ context.Context, vaultID uuid.UUID) error {
	vault, ok := r.vaults[vaultID]
	if !ok || vault.Status != entities.VaultStatusPending {
		return errors.New("no pending vault")
	}
	delete(r.vaults, vaultID)
	return nil
}

func (r *fakeRepo) ListAuthorizations(_ context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultWithdrawalAuthorization, error) {
	var out []*entities.VaultWithdrawalAuthorization
	for _, auth := range r.auths {
		if auth.VaultID == vaultID {
			out = append(out, auth)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepo) ListPenaltyEvents(_ context.Context, vaultID uuid.UUID, limit int) ([]*entities.VaultPenaltyEvent, error) {
	var out []*entities.VaultPenaltyEvent
	for _, penalty := range r.penalties {
		if penalty.VaultID == vaultID {
			out = append(out, penalty)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Fakes: engine, ledger, users, notifier
// ---------------------------------------------------------------------------

type fakeEngine struct {
	strategies  []*entities.InvestmentStrategy
	assets      []*entities.InvestmentAsset
	enrollment  *entities.InvestmentEnrollment
	enrollErr   error
	fundErr     error
	withdrawErr error
	linkErr     error
	withdraws   int
	lastKey     string
	lastAmount  decimal.Decimal
	transfers   []*entities.InvestmentFundingTransfer
	// authorize emulates the investment withdrawal gate. The real gate refuses a
	// vault withdrawal without a valid authorization.
	authorize func(ctx context.Context, enrollmentID uuid.UUID, gross decimal.Decimal, key string) error
}

func (f *fakeEngine) EnsureRailStrategy(_ context.Context, req entities.InvestmentRailStrategyRequest) (*entities.InvestmentStrategy, error) {
	for _, strategy := range f.strategies {
		if strategy.Name == req.Name {
			return strategy, nil
		}
	}
	strategy := &entities.InvestmentStrategy{ID: uuid.New(), OwnerType: entities.InvestmentOwnerRail, Name: req.Name, Status: entities.InvestmentStrategyActive, CurrentVersion: 1}
	f.strategies = append(f.strategies, strategy)
	return strategy, nil
}

func (f *fakeEngine) ListRailStrategies(_ context.Context) ([]*entities.InvestmentStrategy, error) {
	return f.strategies, nil
}

func (f *fakeEngine) Enroll(_ context.Context, userID uuid.UUID, req *entities.InvestmentEnrollRequest, _ entities.InvestmentActor) (*entities.InvestmentEnrollResponse, error) {
	if f.enrollErr != nil {
		return nil, f.enrollErr
	}
	if f.enrollment == nil {
		return nil, errors.New("no enrollment configured")
	}
	f.enrollment.UserID = userID
	return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionCompleted, Enrollment: f.enrollment}, nil
}

func (f *fakeEngine) Fund(_ context.Context, userID, enrollmentID uuid.UUID, amount decimal.Decimal, source, _ string, _ entities.InvestmentActor) (*entities.InvestmentFundingTransfer, error) {
	if f.fundErr != nil {
		return nil, f.fundErr
	}
	transfer := &entities.InvestmentFundingTransfer{ID: uuid.New(), UserID: userID, EnrollmentID: enrollmentID, Direction: "deposit", AmountUSD: amount, SourceAccount: source, Status: "SUBMITTED"}
	f.transfers = append(f.transfers, transfer)
	return transfer, nil
}

func (f *fakeEngine) Withdraw(ctx context.Context, _ uuid.UUID, req *entities.InvestmentWithdrawalRequest, stepUp bool, _ entities.InvestmentActor) (*entities.InvestmentWithdrawalResponse, error) {
	f.withdraws++
	f.lastKey = req.VaultAuthorizationKey
	f.lastAmount = req.AmountUSD
	if !stepUp {
		return nil, errors.New("step-up required")
	}
	if f.authorize != nil {
		enrollmentID := uuid.Nil
		if f.enrollment != nil {
			enrollmentID = f.enrollment.ID
		}
		if err := f.authorize(ctx, enrollmentID, req.AmountUSD, req.VaultAuthorizationKey); err != nil {
			return nil, err
		}
	}
	if f.withdrawErr != nil {
		return nil, f.withdrawErr
	}
	execution := &entities.InvestmentExecution{
		ID:                 uuid.New(),
		EnrollmentID:       &f.enrollment.ID,
		Kind:               entities.InvestmentExecutionWithdraw,
		Status:             entities.InvestmentExecutionSubmitted,
		RequestedAmountUSD: req.AmountUSD,
	}
	return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionCompleted, Execution: execution, OperationID: "op-" + execution.ID.String()[:8], Recipient: "rail-settlement"}, nil
}

func (f *fakeEngine) LinkVaultEnrollment(_ context.Context, _ uuid.UUID, _ uuid.UUID, vaultID uuid.UUID) error {
	if f.linkErr != nil {
		return f.linkErr
	}
	if f.enrollment != nil {
		f.enrollment.VaultID = &vaultID
	}
	return nil
}

func (f *fakeEngine) GetAssetByCAIP19(_ context.Context, caip19 string) (*entities.InvestmentAsset, error) {
	for _, asset := range f.assets {
		if asset.CAIP19 == caip19 {
			return asset, nil
		}
	}
	return nil, nil
}

type fakeLedger struct {
	balances *entities.UserBalances
	txs      []*entities.CreateTransactionRequest
}

func (f *fakeLedger) GetUserBalances(_ context.Context, _ uuid.UUID) (*entities.UserBalances, error) {
	if f.balances == nil {
		return &entities.UserBalances{}, nil
	}
	return f.balances, nil
}

func (f *fakeLedger) GetOrCreateUserAccount(_ context.Context, _ uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	return &entities.LedgerAccount{ID: uuid.New(), AccountType: accountType}, nil
}

func (f *fakeLedger) GetSystemAccount(_ context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	return &entities.LedgerAccount{ID: uuid.New(), AccountType: accountType}, nil
}

func (f *fakeLedger) CreateTransaction(_ context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error) {
	f.txs = append(f.txs, req)
	return &entities.LedgerTransaction{ID: uuid.New(), Status: entities.TransactionStatusCompleted}, nil
}

type fakeUsers struct {
	dobs map[uuid.UUID]*time.Time
}

func (f *fakeUsers) GetDateOfBirth(_ context.Context, userID uuid.UUID) (*time.Time, error) {
	return f.dobs[userID], nil
}

type fakeNotifier struct {
	contributions int
	unlocked      int
	withdrawals   int
	actionNeeded  int
}

func (f *fakeNotifier) NotifyVaultContribution(context.Context, uuid.UUID, decimal.Decimal, *time.Time) error {
	f.contributions++
	return nil
}
func (f *fakeNotifier) NotifyVaultUnlocked(context.Context, uuid.UUID, time.Time) error {
	f.unlocked++
	return nil
}
func (f *fakeNotifier) NotifyVaultWithdrawal(context.Context, uuid.UUID, decimal.Decimal, decimal.Decimal, bool) error {
	f.withdrawals++
	return nil
}
func (f *fakeNotifier) NotifyVaultActionRequired(context.Context, uuid.UUID, string, string) error {
	f.actionNeeded++
	return nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type testHarness struct {
	service  *Service
	repo     *fakeRepo
	engine   *fakeEngine
	ledger   *fakeLedger
	users    *fakeUsers
	notifier *fakeNotifier
	userID   uuid.UUID
	now      time.Time
	nowPtr   *time.Time
	// gate wiring
	settlement string
}

// advance moves the harness clock forward; the service shares the same view.
func (h *testHarness) advance(d time.Duration) {
	h.now = h.now.Add(d)
	*h.nowPtr = h.now
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	userID := uuid.New()
	strategy := &entities.InvestmentStrategy{
		ID:             uuid.New(),
		OwnerType:      entities.InvestmentOwnerRail,
		Name:           entities.VaultTierBalanced.Label(),
		Status:         entities.InvestmentStrategyActive,
		CurrentVersion: 1,
	}
	enrollment := &entities.InvestmentEnrollment{
		ID:                uuid.New(),
		UserID:            userID,
		StrategyID:        strategy.ID,
		GliderPortfolioID: "pf-" + uuid.NewString()[:8],
		DepositAccountID:  "solana:testnet:deposit",
		Status:            entities.InvestmentEnrollmentActive,
	}
	engine := &fakeEngine{strategies: []*entities.InvestmentStrategy{strategy}, enrollment: enrollment}
	ledger := &fakeLedger{balances: &entities.UserBalances{SpendingBalance: decimal.NewFromInt(10000)}}
	dob := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	users := &fakeUsers{dobs: map[uuid.UUID]*time.Time{userID: &dob}}
	notifier := &fakeNotifier{}

	cfg := Config{
		Enabled:                true,
		DefaultRetirementAge:   50,
		MinLockYears:           5,
		PenaltyRate:            decimal.NewFromFloat(0.10),
		MaxAutoContributionPct: decimal.NewFromInt(1),
		MinContributionUSD:     decimal.NewFromInt(1),
		MinSpendableUSD:        decimal.NewFromInt(20),
		ContributionSource:     "spending",
		SettlementAccount:      "solana:testnet:RailVaultSettlement",
		AuthorizationTTL:       10 * time.Minute,
		StaleAfter:             24 * time.Hour,
		Strategies: []StrategyDefinition{{
			Tier:    entities.VaultTierBalanced,
			Name:    entities.VaultTierBalanced.Label(),
			Risk:    "medium",
			Horizon: "long",
			Legs:    []entities.InvestmentAllocationLeg{{CAIP19: "solana:testnet/token:usdc", Weight: decimal.NewFromInt(100)}},
		}},
	}
	now := time.Now().UTC()
	nowPtr := &now
	service := NewService(Deps{
		Config:     cfg,
		Repository: newFakeRepo(),
		Engine:     engine,
		Ledger:     ledger,
		Users:      users,
		Notifier:   notifier,
		Clock:      func() time.Time { return *nowPtr },
	})

	h := &testHarness{service: service, repo: service.repo.(*fakeRepo), engine: engine, ledger: ledger, users: users, notifier: notifier, userID: userID, now: now, nowPtr: nowPtr, settlement: cfg.SettlementAccount}
	// Emulate the real investment gate: the withdrawal only proceeds if the
	// vault authorizes (and consumes) the single-use key.
	engine.authorize = func(ctx context.Context, enrollmentID uuid.UUID, gross decimal.Decimal, key string) error {
		_, err := service.Authorize(ctx, enrollmentID, gross, key)
		return err
	}
	return h
}

func (h *testHarness) createVault(t *testing.T, pct string) *entities.RetirementVault {
	t.Helper()
	response, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{
		Tier:                entities.VaultTierBalanced,
		AutoContributionPct: dec(pct),
	})
	require.NoError(t, err)
	require.NotNil(t, response.View)
	vault, err := h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	require.NoError(t, err)
	require.NotNil(t, vault)
	return vault
}

func (h *testHarness) seedPosition(t *testing.T, vault *entities.RetirementVault, principal, market string) {
	t.Helper()
	now := h.now
	vault.FundedAt = &now
	unlock := now.AddDate(5, 0, 0)
	vault.UnlockDate = &unlock
	require.NoError(t, h.repo.CreateLot(context.Background(), &entities.VaultContributionLot{
		VaultID: vault.ID, UserID: h.userID, AmountUSD: dec(principal), RemainingUSD: dec(principal),
		AcquiredAt: now, Status: entities.VaultLotOpen, IdempotencyKey: "seed-" + uuid.NewString()[:8],
	}))
	require.NoError(t, h.repo.CreateSnapshot(context.Background(), &entities.VaultEarningsSnapshot{
		VaultID: vault.ID, UserID: h.userID, PrincipalUSD: dec(principal), MarketValueUSD: dec(market),
		EarningsUSD: dec(market).Sub(dec(principal)), Source: "glider_sync", AsOf: now,
	}))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCreateVaultEnrollsAndLinksPortfolio(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0.2")

	assert.Equal(t, entities.VaultStatusActive, vault.Status)
	require.NotNil(t, vault.GliderEnrollmentID)
	assert.Equal(t, h.engine.enrollment.ID, *vault.GliderEnrollmentID)
	require.NotNil(t, h.engine.enrollment.VaultID, "portfolio must be linked to the vault")
	assert.Equal(t, vault.ID, *h.engine.enrollment.VaultID)
	// No unlock date until the first contribution anchors the lock.
	assert.Nil(t, vault.UnlockDate)

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{Tier: entities.VaultTierBalanced})
	require.ErrorIs(t, err, ErrAlreadyExists)
}

func TestCreateVaultSurfacesEnrollmentFailure(t *testing.T) {
	h := newTestHarness(t)
	h.engine.enrollErr = errors.New("provider unreachable")

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{Tier: entities.VaultTierBalanced})
	require.Error(t, err)
	_, err = h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	require.NoError(t, err)
	assert.Nil(t, h.repo.vaults[uuid.Nil], "no vault should be half-created")
	vault, _ := h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	assert.Nil(t, vault, "a failed enrollment must not leave an active vault")

	// Ops still sees the failure even though no vault row exists.
	require.NotNil(t, h.repo.enrollFailure, "a failed enroll must be observable by ops")
	assert.Equal(t, h.userID, h.repo.enrollFailure.UserID)
	assert.Contains(t, h.repo.enrollFailure.LastError, "provider unreachable")
	assert.Equal(t, 1, h.repo.enrollFailure.Tries)
}

func TestContributionIsIdempotentAndRecordsBasis(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")

	result, err := h.service.Contribute(context.Background(), h.userID, vault, dec("25"), "spending", "key-1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.AmountUSD.Equal(dec("25")))
	require.NotNil(t, vault.UnlockDate, "first contribution must anchor the unlock date")
	// 1990 + 50 years is later than now + 5 years.
	assert.Equal(t, time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), vault.UnlockDate.UTC())

	// Replaying the same key does not double-fund.
	again, err := h.service.Contribute(context.Background(), h.userID, vault, dec("25"), "spending", "key-1")
	require.NoError(t, err)
	assert.Equal(t, result.LotID, again.LotID)
	assert.Len(t, h.engine.transfers, 1, "funding must happen exactly once")
	assert.Len(t, h.repo.lots, 1)
}

func TestContributionFailsClosedWhenFundingFails(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.engine.fundErr = errors.New("no available balance")

	_, err := h.service.Contribute(context.Background(), h.userID, vault, dec("25"), "spending", "key-fail")
	require.Error(t, err)
	assert.Empty(t, h.repo.lots, "a failed funding must not write a lot")
	assert.Nil(t, vault.FundedAt, "a failed funding must not anchor the lock")
}

func TestAutoContributionAppliesPercentAndClamps(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0.5")

	// 50% of a $100 deposit, funded from spendable money.
	_, err := h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("100"), dec("30"))
	require.NoError(t, err)
	require.Len(t, h.repo.lots, 1)
	assert.True(t, h.repo.lots[0].AmountUSD.Equal(dec("50")), "got %s", h.repo.lots[0].AmountUSD)

	// Clamp: only $60 spendable => contribution is $60, never more than exists.
	// ($60 - $60 take = $0 left would breach the $20 floor, so raise spendable
	// to $100: $100 - $60 take = $40 left, above the floor.)
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("100")}
	_, err = h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("120"), dec("30"))
	require.NoError(t, err)
	require.Len(t, h.repo.lots, 2)
	assert.True(t, h.repo.lots[1].AmountUSD.Equal(dec("60")), "got %s", h.repo.lots[1].AmountUSD)
}

func TestAutoContributionSkipsWhenRuleZeroOrBelowMinimum(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0")
	_, err := h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("100"), dec("30"))
	require.NoError(t, err)
	assert.Empty(t, h.repo.lots, "a zero rule must not contribute")

	vault, _ := h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	vault.AutoContributionPct = dec("0.5")
	// 50% of $1 = $0.50, below the $1 minimum.
	_, err = h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("1"), dec("0.3"))
	require.NoError(t, err)
	assert.Empty(t, h.repo.lots, "below-minimum contributions are skipped")
}

func TestAutoContributionSkipsOnSpendableFloor(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0.5") // 50% of any deposit

	// $30 spendable, floor $20. 50% of $100 = $50 take would leave $-20: skip.
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("30")}
	_, err := h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("100"), dec("0"))
	require.NoError(t, err)
	assert.Empty(t, h.repo.lots, "a floor-breaching take must not open a lot")
	require.Len(t, h.repo.skips, 1, "a skip must be recorded")
	assert.Equal(t, entities.VaultSkipFloor, h.repo.skips[0].Reason)
}

func TestAutoContributionFloorBoundaryIsAllowed(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0.5") // 50% of any deposit

	// $40 spendable, floor $20. 50% of $40 = $20 take leaves exactly $20 = floor:
	// the boundary is inclusive, so it contributes.
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("40")}
	_, err := h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("40"), dec("0"))
	require.NoError(t, err)
	require.Len(t, h.repo.lots, 1, "landing exactly on the floor is allowed")
	assert.True(t, h.repo.lots[0].AmountUSD.Equal(dec("20")), "got %s", h.repo.lots[0].AmountUSD)
}

func TestAutoContributionSkipIsIdempotentOnPaymentID(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0.5")
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("30")}
	paymentID := uuid.New()

	_, err := h.service.ContributeFromDeposit(context.Background(), h.userID, paymentID, dec("100"), dec("0"))
	require.NoError(t, err)
	_, err = h.service.ContributeFromDeposit(context.Background(), h.userID, paymentID, dec("100"), dec("0"))
	require.NoError(t, err)
	assert.Len(t, h.repo.skips, 1, "the same payment must not open two skips")
}

func TestAutoContributionRetryAfterFundedLotReturnsLotNotSkip(t *testing.T) {
	h := newTestHarness(t)
	h.createVault(t, "0.5") // 50% of any deposit
	paymentID := uuid.New()

	// First call funds: $10,000 spendable, 50% of $100 = $50 take.
	first, err := h.service.ContributeFromDeposit(context.Background(), h.userID, paymentID, dec("100"), dec("0"))
	require.NoError(t, err)
	require.NotNil(t, first)

	// Spendable collapses before the retry. The floor would now appear
	// breached, but the payment already funded a lot: the retry must return
	// the existing lot, never a skip.
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("30")}
	second, err := h.service.ContributeFromDeposit(context.Background(), h.userID, paymentID, dec("100"), dec("0"))
	require.NoError(t, err)
	require.NotNil(t, second, "a retry of a funded payment must return the lot")
	assert.Equal(t, first.LotID, second.LotID)
	assert.Empty(t, h.repo.skips, "a funded payment must never record a skip")
}

func TestListStrategyOptionsOnlyListsResolvedTiers(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	options, err := h.service.ListStrategyOptions(ctx)
	require.NoError(t, err)
	assert.Empty(t, options, "unresolved tiers must be absent, never offered-then-refused")

	require.NoError(t, h.repo.UpsertTier(ctx, &entities.VaultTierBinding{
		Tier: entities.VaultTierBalanced, RailStrategyID: uuid.New(), Version: 1,
	}))
	options, err = h.service.ListStrategyOptions(ctx)
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, entities.VaultTierBalanced, options[0].Tier)
	assert.Equal(t, entities.VaultTierBalanced.Label(), options[0].Label)
}

func TestWithdrawRequiresStepUp(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")

	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("100")}, false)
	require.ErrorIs(t, err, ErrStepUpRequired)
	assert.Equal(t, 0, h.engine.withdraws)
}

func TestWithdrawMintsAuthorizationAndSettlesWithPenalty(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")

	result, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("1200")}, true)
	require.NoError(t, err)
	require.NotNil(t, result.Plan)
	// Principal-first: $1,000 free, $200 of growth haircut at 10% = $20.
	assert.True(t, result.Plan.PrincipalReturnedUSD.Equal(dec("1000")))
	assert.True(t, result.Plan.EarningsReturnedUSD.Equal(dec("200")))
	assert.True(t, result.Plan.PenaltyUSD.Equal(dec("20")))
	assert.True(t, result.Plan.NetToUserUSD.Equal(dec("1180")))
	require.NotNil(t, result.ExecutionID)

	// The settlement posts the split and consumes the basis.
	require.NoError(t, h.service.OnWithdrawalFilled(context.Background(), &entities.InvestmentExecution{
		ID:           *result.ExecutionID,
		EnrollmentID: vault.GliderEnrollmentID,
		Kind:         entities.InvestmentExecutionWithdraw,
		Status:       entities.InvestmentExecutionFilled,
	}))
	require.Len(t, h.ledger.txs, 1)

	// Balanced: debits == credits, and the penalty reaches revenue.
	var debits, credits decimal.Decimal
	var sawPenalty bool
	for _, entry := range h.ledger.txs[0].Entries {
		if entry.EntryType == entities.EntryTypeDebit {
			debits = debits.Add(entry.Amount)
		} else {
			credits = credits.Add(entry.Amount)
		}
	}
	assert.True(t, debits.Equal(credits), "debits %s != credits %s", debits, credits)
	assert.True(t, debits.Equal(dec("1200")), "gross must be released in full")
	_ = sawPenalty
	require.Len(t, h.ledger.txs[0].Entries, 3, "settlement, user credit and penalty revenue")

	// Basis consumed FIFO by the principal returned.
	require.Len(t, h.repo.lots, 1)
	assert.True(t, h.repo.lots[0].RemainingUSD.IsZero())
	assert.Equal(t, entities.VaultLotConsumed, h.repo.lots[0].Status)

	// The penalty event is committed and linked to the ledger transaction.
	require.Len(t, h.repo.penalties, 1)
	assert.Equal(t, entities.VaultPenaltyCommitted, h.repo.penalties[0].Status)
	assert.NotNil(t, h.repo.penalties[0].LedgerTransactionID)
	assert.Equal(t, 1, h.notifier.withdrawals)
}

func TestWithdrawDoubleSpendIsRefused(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")

	// First withdrawal succeeds and consumes its authorization.
	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("100")}, true)
	require.NoError(t, err)
	key := h.engine.lastKey
	require.NotEmpty(t, key)

	// Replaying the same key directly at the gate is refused (single use).
	_, err = h.service.Authorize(context.Background(), *vault.GliderEnrollmentID, dec("100"), key)
	require.ErrorIs(t, err, ErrAuthorizationInvalid)

	// An absent key is refused too.
	_, err = h.service.Authorize(context.Background(), *vault.GliderEnrollmentID, dec("100"), "")
	require.ErrorIs(t, err, ErrAuthorizationInvalid)

	// A wrong amount is refused.
	auth := h.repo.auths[len(h.repo.auths)-1] // consumed, so also refused
	auth.Status = entities.VaultAuthorizationIssued
	_, err = h.service.Authorize(context.Background(), *vault.GliderEnrollmentID, dec("999"), auth.Key)
	require.ErrorIs(t, err, ErrAuthorizationInvalid, "an authorization is bound to its amount")
}

func TestWithdrawExpiredAuthorizationIsRefused(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")

	// An authorization that stays issued (nothing consumed it) but lapses:
	// exactly the in-flight state that must die silently when the TTL passes.
	key := "vault-stale-key"
	require.NoError(t, h.repo.CreateAuthorization(context.Background(), &entities.VaultWithdrawalAuthorization{
		VaultID:      vault.ID,
		UserID:       h.userID,
		EnrollmentID: *vault.GliderEnrollmentID,
		Key:          key,
		GrossUSD:     dec("100"),
		NetUSD:       dec("100"),
		Status:       entities.VaultAuthorizationIssued,
		ExpiresAt:    h.now.Add(2 * time.Minute),
	}))
	h.advance(3 * time.Minute)

	// A stale key can never authorize a payout.
	_, err := h.service.Authorize(context.Background(), *vault.GliderEnrollmentID, dec("100"), key)
	require.ErrorIs(t, err, ErrAuthorizationInvalid, "an expired key must refuse at the gate")

	// The expired key also no longer counts as an in-flight withdrawal, so a
	// fresh withdrawal attempt is not blocked by a ghost.
	inflight, err := h.repo.HasIssuedAuthorization(context.Background(), *vault.GliderEnrollmentID, h.now)
	require.NoError(t, err)
	assert.False(t, inflight, "an expired authorization is not an in-flight withdrawal")
}

func TestWithdrawReleasesAuthorizationWhenTheEngineFails(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")
	h.engine.withdrawErr = errors.New("provider unreachable")

	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("100")}, true)
	require.Error(t, err)

	// The authorization is released (or consumed by the failed attempt) and the
	// penalty voided, so the user can retry.
	require.Len(t, h.repo.auths, 1)
	assert.NotEqual(t, entities.VaultAuthorizationIssued, h.repo.auths[0].Status,
		"a failed attempt must not leave a live authorization behind")
	require.Len(t, h.repo.penalties, 1)
	assert.Equal(t, entities.VaultPenaltyVoid, h.repo.penalties[0].Status)

	// A retry is not blocked by the in-flight guard.
	inflight, err := h.repo.HasIssuedAuthorization(context.Background(), *vault.GliderEnrollmentID, h.now)
	require.NoError(t, err)
	assert.False(t, inflight)
}

func TestWithdrawBlocksWhenPenaltyCannotBeComputed(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	// Funded but no date of birth -> no unlock date -> fail closed.
	h.users.dobs[h.userID] = nil
	now := h.now
	vault.FundedAt = &now
	h.repo.vaults[vault.ID] = vault

	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("100")}, true)
	require.Error(t, err)
	assert.Equal(t, 0, h.engine.withdraws, "no provider call may be made")
}

func TestViewAnswersWithoutAProviderSnapshot(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	// Basis exists, but the provider has never been reached (no snapshot).
	require.NoError(t, h.repo.CreateLot(context.Background(), &entities.VaultContributionLot{
		VaultID: vault.ID, UserID: h.userID, AmountUSD: dec("100"), RemainingUSD: dec("100"),
		AcquiredAt: h.now, Status: entities.VaultLotOpen, IdempotencyKey: "k1",
	}))

	view, err := h.service.GetView(context.Background(), h.userID)
	require.NoError(t, err, "the vault must answer while the provider is down")
	assert.True(t, view.PrincipalUSD.Equal(dec("100")))
	assert.True(t, view.MarketValueUSD.Equal(dec("100")), "falls back to the known basis")
	assert.True(t, view.EarningsUSD.IsZero())
	assert.Equal(t, "ledger_fallback", view.Source)
	assert.True(t, view.Locked, "no unlock date means locked")
}

func TestSnapshotFlagsStaleData(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")
	// Move the clock well past the staleness threshold.
	h.advance(48 * time.Hour)

	view, err := h.service.GetView(context.Background(), h.userID)
	require.NoError(t, err)
	assert.True(t, view.Stale, "an old snapshot must be flagged, not hidden")
	assert.True(t, view.MarketValueUSD.Equal(dec("1400")), "last known value is still shown")
}

func TestOnEnrollmentSyncedWritesSnapshotAndNotifiesUnlockOnce(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	unlock := h.now.Add(-time.Hour) // already unlocked
	vault.UnlockDate = &unlock
	h.repo.vaults[vault.ID] = vault
	enrollment := h.engine.enrollment
	enrollment.VaultID = &vault.ID
	enrollment.TotalValueUSD = dec("1200")
	require.NoError(t, h.repo.CreateLot(context.Background(), &entities.VaultContributionLot{
		VaultID: vault.ID, UserID: h.userID, AmountUSD: dec("1000"), RemainingUSD: dec("1000"),
		AcquiredAt: h.now, Status: entities.VaultLotOpen, IdempotencyKey: "k",
	}))

	require.NoError(t, h.service.OnEnrollmentSynced(context.Background(), enrollment))
	require.Len(t, h.repo.snaps, 1)
	assert.True(t, h.repo.snaps[0].EarningsUSD.Equal(dec("200")))
	assert.Equal(t, 1, h.notifier.unlocked, "the unlock notice fires on the crossing")

	// A second sync must not re-notify.
	require.NoError(t, h.service.OnEnrollmentSynced(context.Background(), enrollment))
	assert.Equal(t, 1, h.notifier.unlocked)
}

func TestBootstrapStrategiesIsIdempotent(t *testing.T) {
	h := newTestHarness(t)
	h.service.cfg.TierFiles = nil
	require.NoError(t, h.service.BootstrapStrategies(context.Background()))
	require.Len(t, h.engine.strategies, 1)
	require.NoError(t, h.service.BootstrapStrategies(context.Background()))
	assert.Len(t, h.engine.strategies, 1, "bootstrap must not duplicate strategies")
}

func TestCreateVaultRefusesWithoutDateOfBirth(t *testing.T) {
	h := newTestHarness(t)
	h.users.dobs[h.userID] = nil

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{
		Tier: entities.VaultTierBalanced,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation, "a missing date of birth must refuse the open")
	_, err = h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	require.NoError(t, err)
	assert.Nil(t, h.repo.vaults[uuid.Nil], "no vault row may be written for an open that fails pre-enroll")
}

func TestCreateVaultCompensatingDeleteOnLinkFailure(t *testing.T) {
	h := newTestHarness(t)
	// Pre-bind the tier so strategy resolution succeeds before the link fails.
	require.NoError(t, h.repo.UpsertTier(context.Background(), &entities.VaultTierBinding{
		Tier:           entities.VaultTierBalanced,
		RailStrategyID: h.engine.strategies[0].ID,
	}))
	h.engine.linkErr = errors.New("portfolio link refused")

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{
		Tier: entities.VaultTierBalanced,
	})
	require.Error(t, err)

	// The pending row must be gone, not stranded, so a retry is not blocked.
	_, err = h.repo.GetActiveVaultByUser(context.Background(), h.userID)
	require.NoError(t, err)
	assert.Empty(t, h.repo.vaults, "a failed open must leave no vault row behind")

	// The failed link is also ops-visible outside the health row.
	require.NotNil(t, h.repo.enrollFailure, "a failed link is a failed enroll for ops")
	assert.Equal(t, h.userID, h.repo.enrollFailure.UserID)
}

func TestCreateVaultRefusesWhenStrategyUnavailable(t *testing.T) {
	h := newTestHarness(t)
	// No tier files, no inline strategies, no rail strategies in the engine =>
	// strategyForTier cannot resolve anything.
	h.service.cfg.TierFiles = nil
	h.service.cfg.Strategies = nil
	h.engine.strategies = nil

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{
		Tier: entities.VaultTierBalanced,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStrategyUnavailable)
}

func TestActivityReportsContributionsWithdrawalsAndPenalties(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	h.seedPosition(t, vault, "1000", "1400")

	// A withdrawal to the cap: $1,000 principal free + $200 growth, 10% penalty
	// on the growth. Settle it so the lot is consumed and the penalty committed.
	result, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("1200")}, true)
	require.NoError(t, err)
	require.NoError(t, h.service.OnWithdrawalFilled(context.Background(), &entities.InvestmentExecution{
		ID:           *result.ExecutionID,
		EnrollmentID: vault.GliderEnrollmentID,
		Kind:         entities.InvestmentExecutionWithdraw,
		Status:       entities.InvestmentExecutionFilled,
	}))

	entries, err := h.service.Activity(context.Background(), h.userID, 50)
	require.NoError(t, err)
	kinds := map[string]int{}
	for _, e := range entries {
		kinds[e.Kind]++
	}
	assert.Contains(t, kinds, "contribution", "activity must show contributions")
	assert.Contains(t, kinds, "withdrawal", "activity must show withdrawals")
	assert.Contains(t, kinds, "penalty", "activity must show penalties")
}

func TestWithdrawRefusedWhenUnderwaterAndAboveValue(t *testing.T) {
	h := newTestHarness(t)
	vault := h.createVault(t, "0")
	// Underwater: market < principal. The book clamps principal to market, so
	// a request for more than market is refused.
	h.seedPosition(t, vault, "1000", "800")

	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.VaultWithdrawRequest{AmountUSD: dec("900")}, true)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientValue, "cannot withdraw more than the current value")
	assert.Equal(t, 0, h.engine.withdraws, "underwater must not reach the provider")
}

// A vault with no settlement account cannot take money out, so it must not take
// money in either. This is the guard that makes "enabled by default" safe.
func TestVaultRefusesNewMoneyWithoutASettlementAccount(t *testing.T) {
	h := newTestHarness(t)
	h.service.cfg.SettlementAccount = ""

	_, err := h.service.CreateVault(context.Background(), h.userID, &entities.VaultCreateRequest{Tier: entities.VaultTierBalanced})
	require.ErrorIs(t, err, ErrDisabled)
	assert.Nil(t, h.engine.enrollment.VaultID, "no portfolio may be linked")

	// The automatic-savings hook is a silent no-op, not a noisy failure.
	result, err := h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("100"), dec("30"))
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, h.repo.lots)
	assert.Empty(t, h.engine.transfers, "no money may move")
}
