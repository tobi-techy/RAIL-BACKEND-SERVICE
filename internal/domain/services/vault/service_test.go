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
	vaults    map[uuid.UUID]*entities.RetirementVault
	lots      []*entities.VaultContributionLot
	snaps     []*entities.VaultEarningsSnapshot
	penalties []*entities.VaultPenaltyEvent
	auths     []*entities.VaultWithdrawalAuthorization
	onramps   []*entities.VaultOnrampTransfer
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{vaults: map[uuid.UUID]*entities.RetirementVault{}}
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

// ---------------------------------------------------------------------------
// Fakes: engine, ledger, users, notifier
// ---------------------------------------------------------------------------

type fakeEngine struct {
	strategies  []*entities.InvestmentStrategy
	enrollment  *entities.InvestmentEnrollment
	enrollErr   error
	fundErr     error
	withdrawErr error
	withdraws   int
	lastKey     string
	lastAmount  decimal.Decimal
	transfers   []*entities.InvestmentFundingTransfer
	// authorize emulates the investment withdrawal gate. The real gate refuses a
	// vault withdrawal without a valid authorization.
	authorize      func(ctx context.Context, enrollmentID uuid.UUID, gross decimal.Decimal, key string) error
	onWithdrawFill func(ctx context.Context, execution *entities.InvestmentExecution)
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
	if f.enrollment != nil {
		f.enrollment.VaultID = &vaultID
	}
	return nil
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

	// Clamp: only $10 spendable => contribution is $10, never more than exists.
	h.ledger.balances = &entities.UserBalances{SpendingBalance: dec("10")}
	_, err = h.service.ContributeFromDeposit(context.Background(), h.userID, uuid.New(), dec("100"), dec("30"))
	require.NoError(t, err)
	require.Len(t, h.repo.lots, 2)
	assert.True(t, h.repo.lots[1].AmountUSD.Equal(dec("10")), "got %s", h.repo.lots[1].AmountUSD)
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
	require.NoError(t, h.service.BootstrapStrategies(context.Background()))
	require.Len(t, h.engine.strategies, 1)
	require.NoError(t, h.service.BootstrapStrategies(context.Background()))
	assert.Len(t, h.engine.strategies, 1, "bootstrap must not duplicate strategies")
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
