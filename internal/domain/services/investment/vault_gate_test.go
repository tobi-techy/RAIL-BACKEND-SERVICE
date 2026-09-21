package investment

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVaultObserver stands in for the retirement vault at the withdrawal gate.
type fakeVaultObserver struct {
	plan       *entities.VaultWithdrawalPlan
	err        error
	authorized int
	filled     []*entities.InvestmentExecution
}

func (f *fakeVaultObserver) IsVaultEnrollment(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func (f *fakeVaultObserver) Authorize(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) (*entities.VaultWithdrawalPlan, error) {
	f.authorized++
	if f.err != nil {
		return nil, f.err
	}
	if f.plan == nil {
		return nil, errors.New("no plan")
	}
	return f.plan, nil
}

func (f *fakeVaultObserver) OnEnrollmentSynced(context.Context, *entities.InvestmentEnrollment) error {
	return nil
}

func (f *fakeVaultObserver) OnWithdrawalFilled(_ context.Context, execution *entities.InvestmentExecution) error {
	f.filled = append(f.filled, execution)
	return nil
}

// vaultLinkedEnrollment enrolls a user and marks the portfolio as belonging to a
// retirement vault, forcing the strategy to be Rail-owned (so the mutability
// rule alone would refuse a withdrawal).
func vaultLinkedEnrollment(t *testing.T, h *harness) (*entities.InvestmentEnrollment, uuid.UUID) {
	t.Helper()
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(400)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorUser)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	enrolled, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorUser)
	require.NoError(t, err)
	require.NotNil(t, enrolled.Enrollment)

	// Make the strategy Rail-owned: a user cannot mutate or freely withdraw from
	// it. Only the vault path may.
	stored := h.store.strategies[strategy.Strategy.ID]
	require.NotNil(t, stored)
	stored.OwnerType = entities.InvestmentOwnerRail
	stored.UserID = nil

	vaultID := uuid.New()
	require.NoError(t, h.service.LinkVaultEnrollment(context.Background(), h.userID, enrolled.Enrollment.ID, vaultID))
	reloaded, err := h.store.GetEnrollmentByID(context.Background(), enrolled.Enrollment.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.VaultID, "the portfolio must be vault-linked")
	return reloaded, vaultID
}

func TestVaultWithdrawalIsBlockedWithoutAGate(t *testing.T) {
	h := newHarness(t)
	enrollment, _ := vaultLinkedEnrollment(t, h)

	// No vault wired at all -> fail closed, no provider call.
	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID: enrollment.StrategyID.String(),
		AmountUSD:  decimal.NewFromInt(100),
	}, true, entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrPolicyBlocked)
}

func TestVaultWithdrawalIsBlockedWithoutAValidKey(t *testing.T) {
	h := newHarness(t)
	enrollment, _ := vaultLinkedEnrollment(t, h)

	observer := &fakeVaultObserver{err: errors.New("authorization not found")}
	h.service.SetVaultObserver(observer)

	signCallsBefore := h.signer.signCalls
	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID:            enrollment.StrategyID.String(),
		AssetID:               h.assetID(t, "USDC").String(),
		AmountUSD:             decimal.NewFromInt(100),
		VaultAuthorizationKey: "made-up",
	}, true, entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrPolicyBlocked)
	assert.Equal(t, 1, observer.authorized, "the gate was consulted")
	assert.Equal(t, signCallsBefore, h.signer.signCalls, "no signing may happen when the gate refuses")
}

func TestVaultWithdrawalProceedsWithAnAuthorization(t *testing.T) {
	h := newHarness(t)
	enrollment, _ := vaultLinkedEnrollment(t, h)

	observer := &fakeVaultObserver{plan: &entities.VaultWithdrawalPlan{
		SettlementAccount: "solana:testnet:RailVaultSettlement",
		NetToUserUSD:      decimal.NewFromInt(100),
	}}
	h.service.SetVaultObserver(observer)

	response, err := h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID:            enrollment.StrategyID.String(),
		AssetID:               h.assetID(t, "USDC").String(),
		AmountUSD:             decimal.NewFromInt(100),
		VaultAuthorizationKey: "cfm-valid",
	}, true, entities.InvestmentActorUser)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, entities.InvestmentActionCompleted, response.Status)
	// The provider recipient is the Rail-controlled settlement account, never the
	// user's own address.
	assert.Equal(t, "solana:testnet:RailVaultSettlement", response.Recipient)
	assert.Equal(t, 1, observer.authorized)
}

func TestVaultWithdrawalWithNoSettlementAccountIsRefused(t *testing.T) {
	h := newHarness(t)
	enrollment, _ := vaultLinkedEnrollment(t, h)

	// A plan without a settlement account cannot be trusted: refuse it.
	observer := &fakeVaultObserver{plan: &entities.VaultWithdrawalPlan{}}
	h.service.SetVaultObserver(observer)

	_, err := h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID:            enrollment.StrategyID.String(),
		AmountUSD:             decimal.NewFromInt(100),
		VaultAuthorizationKey: "cfm-valid",
	}, true, entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrPolicyBlocked)
}

func TestSettledVaultWithdrawalNotifiesTheObserver(t *testing.T) {
	h := newHarness(t)
	enrollment, vaultID := vaultLinkedEnrollment(t, h)
	observer := &fakeVaultObserver{plan: &entities.VaultWithdrawalPlan{
		SettlementAccount: "solana:testnet:RailVaultSettlement",
		NetToUserUSD:      decimal.NewFromInt(100),
		VaultID:           vaultID,
	}}
	h.service.SetVaultObserver(observer)

	response, err := h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID:            enrollment.StrategyID.String(),
		AssetID:               h.assetID(t, "USDC").String(),
		AmountUSD:             decimal.NewFromInt(100),
		VaultAuthorizationKey: "cfm-valid",
	}, true, entities.InvestmentActorUser)
	require.NoError(t, err)
	require.NotNil(t, response.Execution)

	// Simulate the provider completing the withdrawal.
	state := &entities.GliderOperationState{State: "completed"}
	require.NoError(t, h.service.settleExecution(context.Background(), response.Execution.ID, state))
	require.Len(t, observer.filled, 1, "a filled vault withdrawal must be settled by the vault")
	assert.Equal(t, response.Execution.ID, observer.filled[0].ID)
}

func TestNonVaultWithdrawalStillNeedsOwnership(t *testing.T) {
	h := newHarness(t)
	// Enroll normally, then make the strategy Rail-owned: a Rail-owned strategy
	// that is NOT vault-linked cannot be withdrawn from by a user.
	strategy := h.confirmCreate(t, h.createRequest("USDC"))
	enroll := &entities.InvestmentEnrollRequest{StrategyID: strategy.Strategy.ID.String(), AmountUSD: decimal.NewFromInt(400)}
	staged, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorUser)
	require.NoError(t, err)
	enroll.ConfirmationToken = staged.Confirmation.Token
	enrolled, err := h.service.Enroll(context.Background(), h.userID, enroll, entities.InvestmentActorUser)
	require.NoError(t, err)

	stored := h.store.strategies[strategy.Strategy.ID]
	stored.OwnerType = entities.InvestmentOwnerRail
	stored.UserID = nil

	_, err = h.service.Withdraw(context.Background(), h.userID, &entities.InvestmentWithdrawalRequest{
		StrategyID: enrolled.Enrollment.StrategyID.String(),
		AssetID:    h.assetID(t, "USDC").String(),
		AmountUSD:  decimal.NewFromInt(100),
	}, true, entities.InvestmentActorUser)
	require.ErrorIs(t, err, ErrUnsupported)
}
