package unit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/domain/services/spendingcommitment"
)

// ── Fakes ─────────────────────────────────────────────────────────

type fakeCommitmentRepo struct {
	commitment *entities.SpendingCommitment
	usedCents  int64
	resetAt    time.Time
}

func newFakeCommitmentRepo() *fakeCommitmentRepo {
	return &fakeCommitmentRepo{resetAt: time.Now().UTC().Add(12 * time.Hour)}
}

func (f *fakeCommitmentRepo) GetCommitment(ctx context.Context, userID uuid.UUID) (*entities.SpendingCommitment, error) {
	return f.commitment, nil
}

func (f *fakeCommitmentRepo) UpsertCommitment(ctx context.Context, c *entities.SpendingCommitment) error {
	cp := *c
	f.commitment = &cp
	return nil
}

func (f *fakeCommitmentRepo) DeactivateCommitment(ctx context.Context, userID uuid.UUID) error {
	if f.commitment != nil {
		f.commitment.IsActive = false
	}
	return nil
}

func (f *fakeCommitmentRepo) GetOrCreateUsage(ctx context.Context, userID uuid.UUID, resetAt time.Time) (*entities.SpendingCommitmentUsage, error) {
	return &entities.SpendingCommitmentUsage{UserID: userID, UsedCents: f.usedCents, ResetAt: f.resetAt}, nil
}

func (f *fakeCommitmentRepo) ResetExpiredUsage(ctx context.Context, userID uuid.UUID, now, nextReset time.Time) error {
	if !f.resetAt.After(now) {
		f.usedCents = 0
		f.resetAt = nextReset
	}
	return nil
}

func (f *fakeCommitmentRepo) AtomicIncrementUsage(ctx context.Context, userID uuid.UUID, cents, limitCents int64) (bool, error) {
	if f.usedCents+cents > limitCents {
		return false, nil
	}
	f.usedCents += cents
	return true, nil
}

func (f *fakeCommitmentRepo) DecrementUsage(ctx context.Context, userID uuid.UUID, cents int64) error {
	f.usedCents -= cents
	if f.usedCents < 0 {
		f.usedCents = 0
	}
	return nil
}

type fakeFeeCharger struct {
	charges int
	fail    bool
}

func (f *fakeFeeCharger) ChargeLimitIncreaseFee(ctx context.Context, userID uuid.UUID, fee decimal.Decimal, idem string) error {
	if f.fail {
		return assertErr
	}
	f.charges++
	return nil
}

var assertErr = &chargeError{}

type chargeError struct{}

func (e *chargeError) Error() string { return "charge failed" }

type fakeBalanceReader struct {
	balance decimal.Decimal
}

func (f *fakeBalanceReader) GetAccountBalance(ctx context.Context, userID uuid.UUID, at entities.AccountType) (decimal.Decimal, error) {
	return f.balance, nil
}

// ── Tests ─────────────────────────────────────────────────────────

func newCommitmentService(repo spendingcommitment.Repository, fee *fakeFeeCharger, bal *fakeBalanceReader) *spendingcommitment.Service {
	return spendingcommitment.NewService(repo, fee, bal, decimal.NewFromFloat(1.00), zap.NewNop())
}

func TestSetCommitment_NewIsFree(t *testing.T) {
	repo := newFakeCommitmentRepo()
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	status, err := svc.SetCommitment(context.Background(), uuid.New(), entities.SetCommitmentRequest{DailyLimitCents: 50000})
	require.NoError(t, err)
	assert.True(t, status.Active)
	assert.Equal(t, int64(50000), status.DailyLimitCents)
	assert.Equal(t, 0, fee.charges, "creating a commitment must not charge a fee")
}

func TestSetCommitment_DecreaseIsFree(t *testing.T) {
	repo := newFakeCommitmentRepo()
	repo.commitment = &entities.SpendingCommitment{UserID: uuid.New(), DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	status, err := svc.SetCommitment(context.Background(), repo.commitment.UserID, entities.SetCommitmentRequest{DailyLimitCents: 30000})
	require.NoError(t, err)
	assert.Equal(t, int64(30000), status.DailyLimitCents)
	assert.Equal(t, 0, fee.charges, "lowering the cap must be free")
}

func TestSetCommitment_IncreaseRequiresConfirmation(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	_, err := svc.SetCommitment(context.Background(), userID, entities.SetCommitmentRequest{DailyLimitCents: 80000})
	require.ErrorIs(t, err, entities.ErrIncreaseFeeUnconfirmed)
	assert.Equal(t, 0, fee.charges)
	assert.Equal(t, int64(50000), repo.commitment.DailyLimitCents, "limit must not change until confirmed")
}

func TestSetCommitment_IncreaseChargesFeeOnConfirm(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	status, err := svc.SetCommitment(context.Background(), userID, entities.SetCommitmentRequest{DailyLimitCents: 80000, ConfirmFee: true})
	require.NoError(t, err)
	assert.Equal(t, int64(80000), status.DailyLimitCents)
	assert.Equal(t, 1, fee.charges, "raising the cap must charge exactly one fee")
	assert.Equal(t, 1, repo.commitment.IncreaseCount)
}

func TestSetCommitment_IncreaseInsufficientBalance(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromFloat(0.50)})

	_, err := svc.SetCommitment(context.Background(), userID, entities.SetCommitmentRequest{DailyLimitCents: 80000, ConfirmFee: true})
	require.ErrorIs(t, err, entities.ErrInsufficientForFee)
	assert.Equal(t, 0, fee.charges)
}

func TestCheckOutflow_WithinAndOverCap(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	repo.usedCents = 40000
	svc := newCommitmentService(repo, &fakeFeeCharger{}, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	// $50 more would exceed the $500 cap (used $400) → allowed since 400+50=450
	require.NoError(t, svc.CheckOutflow(context.Background(), userID, decimal.NewFromInt(50), "USD"))

	// $150 more would push to $550 > $500 cap
	err := svc.CheckOutflow(context.Background(), userID, decimal.NewFromInt(150), "USD")
	require.ErrorIs(t, err, entities.ErrCommitmentExceeded)
}

func TestCheckOutflow_NoCommitmentIsNoop(t *testing.T) {
	repo := newFakeCommitmentRepo()
	svc := newCommitmentService(repo, &fakeFeeCharger{}, &fakeBalanceReader{balance: decimal.NewFromInt(100)})
	require.NoError(t, svc.CheckOutflow(context.Background(), uuid.New(), decimal.NewFromInt(100000), "USD"))
}

func TestRecordAndReleaseOutflow(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	svc := newCommitmentService(repo, &fakeFeeCharger{}, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	require.NoError(t, svc.RecordOutflow(context.Background(), userID, decimal.NewFromInt(100), "USD"))
	assert.Equal(t, int64(10000), repo.usedCents)

	require.NoError(t, svc.ReleaseOutflow(context.Background(), userID, decimal.NewFromInt(40), "USD"))
	assert.Equal(t, int64(6000), repo.usedCents)
}

func TestUsageResetsWhenExpired(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	repo.usedCents = 49000
	repo.resetAt = time.Now().UTC().Add(-1 * time.Hour) // already expired

	svc := newCommitmentService(repo, &fakeFeeCharger{}, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	// After reset, a large outflow should be allowed again.
	require.NoError(t, svc.CheckOutflow(context.Background(), userID, decimal.NewFromInt(400), "USD"))
	assert.Equal(t, int64(0), repo.usedCents)
}

func TestDeactivateChargesFee(t *testing.T) {
	repo := newFakeCommitmentRepo()
	userID := uuid.New()
	repo.commitment = &entities.SpendingCommitment{UserID: userID, DailyLimitCents: 50000, Currency: "USD", IsActive: true}
	fee := &fakeFeeCharger{}
	svc := newCommitmentService(repo, fee, &fakeBalanceReader{balance: decimal.NewFromInt(100)})

	// Without confirmation → rejected
	_, err := svc.Deactivate(context.Background(), userID, false)
	require.ErrorIs(t, err, entities.ErrIncreaseFeeUnconfirmed)

	// With confirmation → fee charged and deactivated
	status, err := svc.Deactivate(context.Background(), userID, true)
	require.NoError(t, err)
	assert.False(t, status.Active)
	assert.Equal(t, 1, fee.charges)
}
