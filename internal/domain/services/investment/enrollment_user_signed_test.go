package investment

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// User-signed (Solana Model B) enrollment: prepare -> complete
// ---------------------------------------------------------------------------

const testOwnerAccount = "solana:testnet:UserWallet11111111111111111111111111111111"

// confirmUserPrepare runs stage 1 through confirmation and returns the sign
// payload response the wallet must sign.
func (h *harness) confirmUserPrepare(t *testing.T, req *UserEnrollPrepareRequest) *UserEnrollPrepareResponse {
	t.Helper()
	staged, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)
	require.NotNil(t, staged.Confirmation)
	assert.Equal(t, "enroll_user_signed_prepare", staged.Confirmation.Action)

	req.ConfirmationToken = staged.Confirmation.Token
	ready, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, req, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, ready.Status)
	return ready
}

func TestUserEnrollPrepareThenComplete(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
		Source:         "spending",
	}
	staged, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)
	require.NotNil(t, staged.Preview, "the allocate card needs a preview before anything is signed")
	assert.True(t, staged.Live)
	assert.False(t, staged.Simulated)
	assert.Empty(t, staged.SignPayload, "no sign payload may be issued before confirmation")

	ready := h.confirmUserPrepare(t, prep)
	require.NotEmpty(t, ready.FlowID)
	require.NotEmpty(t, ready.AccountIndex)
	require.NotEmpty(t, ready.AgentAccount)
	require.NotEmpty(t, ready.SignPayload)
	assert.Equal(t, testOwnerAccount, ready.OwnerAccount)

	// Stage 1 is recorded for replay protection.
	require.Len(t, h.store.signatures, 1)
	for _, signature := range h.store.signatures {
		assert.Equal(t, "enroll_user_signed", signature.Flow)
		assert.Equal(t, "prepared", signature.Status)
		assert.Equal(t, ready.FlowID, signature.ProviderFlowID)
	}

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		ChainIDs:                ready.ChainIDs,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(500),
		Source:                  "spending",
	}
	stagedComplete, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, stagedComplete.Status)
	require.NotNil(t, stagedComplete.Confirmation)
	assert.Equal(t, "enroll_user_signed_complete", stagedComplete.Confirmation.Action)
	assert.Empty(t, h.store.enrollments, "no portfolio may exist before the complete confirmation")

	comp.ConfirmationToken = stagedComplete.Confirmation.Token
	done, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionCompleted, done.Status)
	require.NotNil(t, done.Enrollment)
	require.NotNil(t, done.Funding)

	assert.Equal(t, entities.InvestmentEnrollmentActive, done.Enrollment.Status)
	assert.Equal(t, "solana", done.Enrollment.Chain)
	assert.Equal(t, testOwnerAccount, done.Enrollment.OwnerAccountID)
	assert.NotEmpty(t, done.Enrollment.DepositAccountID)
	assert.True(t, h.provider.PortfolioExists(done.Enrollment.GliderPortfolioID))

	// The funding leg really moved money.
	assert.Equal(t, 1, h.funding.transfers)
	assert.True(t, h.funding.lastAmount.Equal(decimal.NewFromInt(500)))

	// The signature request is closed out against the enrollment.
	require.Len(t, h.store.signatures, 1)
	for _, signature := range h.store.signatures {
		assert.Equal(t, "submitted", signature.Status)
		require.NotNil(t, signature.EnrollmentID)
	}
}

func TestUserEnrollCompleteRejectsMutatedAmount(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
		Source:         "spending",
	}
	ready := h.confirmUserPrepare(t, prep)

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		ChainIDs:                ready.ChainIDs,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(500),
		Source:                  "spending",
	}
	staged, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	// Mutate the bound amount after the token was issued.
	tampered := *comp
	tampered.AmountUSD = decimal.NewFromInt(999)
	tampered.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, &tampered, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a changed amount must invalidate the complete confirmation")
	assert.Empty(t, h.store.enrollments, "a mutated payload must never reach the provider")
	assert.Equal(t, 0, h.funding.transfers, "a mutated payload must never move money")
}

func TestUserEnrollCompleteRejectsMutatedOwner(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
	}
	ready := h.confirmUserPrepare(t, prep)

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(500),
	}
	staged, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	tampered := *comp
	tampered.OwnerAccountID = "solana:testnet:AttackerWallet11111111111111111111111111111"
	tampered.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, &tampered, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a changed owner must invalidate the complete confirmation")
	assert.Empty(t, h.store.enrollments)
}

func TestUserEnrollPrepareRejectsHighValueWithoutStepUp(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(5000),
		Source:         "spending",
	}
	staged, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)

	prep.ConfirmationToken = staged.Confirmation.Token
	rejected, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrPolicyBlocked, "high-value enrollments need in-app step-up, not a chat confirmation")
	require.NotNil(t, rejected)
	assert.Equal(t, entities.InvestmentActionRejected, rejected.Status)
	assert.Empty(t, rejected.SignPayload, "a blocked enrollment must never yield a sign payload")
	assert.Empty(t, h.store.signatures)
}

func TestUserEnrollRespectsMaxEnrollments(t *testing.T) {
	h := newHarness(t)
	limits := testLimits()
	limits.MaxEnrollments = 1
	require.NoError(t, h.store.UpsertLimits(context.Background(), h.userID, &limits))

	first := h.confirmCreate(t, h.createRequest("USDC", "SOL"))
	second := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	// One active enrollment fills the cap.
	require.NoError(t, h.store.CreateEnrollment(context.Background(), &entities.InvestmentEnrollment{
		ID:         uuid.New(),
		UserID:     h.userID,
		StrategyID: first.Strategy.ID,
		Status:     entities.InvestmentEnrollmentActive,
	}))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     second.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(100),
	}
	_, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrPolicyBlocked, "prepare must enforce the enrollment cap")

	comp := &UserEnrollCompleteRequest{
		StrategyID:              second.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  "flow_at_cap",
		AccountIndex:            "0",
		SignedSolanaTransaction: "signed:whatever",
		AmountUSD:               decimal.NewFromInt(100),
	}
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrPolicyBlocked, "complete must enforce the enrollment cap before staging")
}

func TestUserEnrollRefusesSimulation(t *testing.T) {
	h := newHarness(t)
	h.service.cfg.Simulation = true
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(100),
	}
	staged, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)

	prep.ConfirmationToken = staged.Confirmation.Token
	rejected, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrUnsupported, "simulation must never issue a sign payload")
	require.NotNil(t, rejected)
	assert.Equal(t, entities.InvestmentActionRejected, rejected.Status)
	assert.Empty(t, rejected.SignPayload)

	// Stage 2 fails closed even with a token: the funding leg is real while
	// the provider is fake, so complete must never run in simulation mode.
	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  "sim_flow_invented",
		AccountIndex:            "0",
		SignedSolanaTransaction: "signed:whatever",
		AmountUSD:               decimal.NewFromInt(100),
	}
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrUnsupported)
	assert.Empty(t, h.store.enrollments)
	assert.Equal(t, 0, h.funding.transfers, "simulation must never move real money")
}

func TestUserEnrollCompleteRejectsInsufficientFunds(t *testing.T) {
	h := newHarness(t)
	h.funding.available = decimal.NewFromInt(50)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
	}
	ready := h.confirmUserPrepare(t, prep)

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(500),
	}
	_, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrPolicyBlocked, "complete must check funding before submitting to the provider")
	assert.Empty(t, h.store.enrollments, "no provider portfolio may be opened without funds")
	assert.Equal(t, 0, h.funding.transfers)
}
