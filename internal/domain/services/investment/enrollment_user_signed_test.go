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

func TestUserEnrollRefusesDisabledService(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	// A disabled investment engine must never stage or complete an enrollment,
	// and must never move money.
	h.service.cfg.Enabled = false

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(100),
	}
	_, err := h.service.PrepareUserEnrollment(context.Background(), h.userID, prep, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrDisabled)

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  "flow_invented",
		AccountIndex:            "0",
		SignedSolanaTransaction: "signed:whatever",
		AmountUSD:               decimal.NewFromInt(100),
	}
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrDisabled)
	assert.Empty(t, h.store.enrollments)
	assert.Equal(t, 0, h.funding.transfers, "a disabled engine must never move real money")
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

func TestUserEnrollCompleteRejectsSwappedAccountIndex(t *testing.T) {
	h := newHarness(t)
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
	}
	ready := h.confirmUserPrepare(t, prep)

	// account_index is NOT part of the confirmation binding, so the token
	// stays valid and only the stage-1 round-trip guard can catch the swap.
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
	tampered.AccountIndex = "999"
	tampered.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, &tampered, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a swapped account_index must be rejected by the round-trip guard")
	assert.Empty(t, h.store.enrollments)
	assert.Equal(t, 0, h.funding.transfers)
}

func TestUserEnrollCompleteRejectsChainIDsChange(t *testing.T) {
	h := newHarness(t)
	h.service.cfg.SolanaChainIDs = []int{101, 102}
	strategy := h.confirmCreate(t, h.createRequest("USDC", "SOL"))

	prep := &UserEnrollPrepareRequest{
		StrategyID:     strategy.Strategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(500),
	}
	ready := h.confirmUserPrepare(t, prep)
	require.Equal(t, []int{101, 102}, ready.ChainIDs, "test setup: prepare must echo the configured chains")

	comp := &UserEnrollCompleteRequest{
		StrategyID:              strategy.Strategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		ChainIDs:                ready.ChainIDs,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(500),
	}
	staged, err := h.service.CompleteUserEnrollment(context.Background(), h.userID, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)

	tampered := *comp
	tampered.ChainIDs = []int{999}
	tampered.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CompleteUserEnrollment(context.Background(), h.userID, &tampered, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "changed chain_ids must be rejected by the round-trip guard")
	assert.Empty(t, h.store.enrollments)
}

// railStrategySharingProviderBinding inserts a Rail-owned strategy row that
// points at an already-created provider strategy, so tests can enroll without
// owning the strategy.
func railStrategySharingProviderBinding(t *testing.T, h *harness, gliderStrategyID string) *entities.InvestmentStrategy {
	t.Helper()
	created := h.confirmCreate(t, h.createRequest("USDC", "SOL"))
	legs := created.Version.TargetAllocation
	railStrategy := &entities.InvestmentStrategy{
		ID:               uuid.New(),
		UserID:           nil,
		OwnerType:        entities.InvestmentOwnerRail,
		GliderStrategyID: &gliderStrategyID,
		Name:             "Rail test sleeve",
		Risk:             "medium",
		Horizon:          "long",
		Status:           entities.InvestmentStrategyActive,
		CurrentVersion:   1,
		CreatedBy:        entities.InvestmentActorSystem,
	}
	require.NoError(t, h.store.Create(context.Background(), railStrategy))
	require.NoError(t, h.store.CreateVersion(context.Background(), &entities.InvestmentStrategyVersion{
		ID:               uuid.New(),
		StrategyID:       railStrategy.ID,
		Version:          1,
		TargetAllocation: legs,
		Risk:             "medium",
		Horizon:          "long",
		CreatedBy:        entities.InvestmentActorSystem,
	}))
	return railStrategy
}

func TestUserEnrollCompleteRejectsForeignFlow(t *testing.T) {
	h := newHarness(t)
	created := h.confirmCreate(t, h.createRequest("USDC", "SOL"))
	railStrategy := railStrategySharingProviderBinding(t, h, *created.Strategy.GliderStrategyID)

	// User A prepares stage 1 against the Rail strategy.
	prep := &UserEnrollPrepareRequest{
		StrategyID:     railStrategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(100),
	}
	ready := h.confirmUserPrepare(t, prep)

	// User B replays A's flowId through their own confirmation. The token is
	// valid for B's payload, so only the stored-request ownership check stops
	// B from enrolling through A's authorization.
	otherUser := uuid.New()
	comp := &UserEnrollCompleteRequest{
		StrategyID:              railStrategy.ID.String(),
		OwnerAccountID:          testOwnerAccount,
		FlowID:                  ready.FlowID,
		AccountIndex:            ready.AccountIndex,
		AgentAccountID:          ready.AgentAccount,
		SignedSolanaTransaction: "signed:" + ready.SignPayload,
		AmountUSD:               decimal.NewFromInt(100),
	}
	staged, err := h.service.CompleteUserEnrollment(context.Background(), otherUser, comp, entities.InvestmentActorMiriam)
	require.NoError(t, err)
	require.Equal(t, entities.InvestmentActionAwaitingConfirmation, staged.Status)

	comp.ConfirmationToken = staged.Confirmation.Token
	_, err = h.service.CompleteUserEnrollment(context.Background(), otherUser, comp, entities.InvestmentActorMiriam)
	require.ErrorIs(t, err, ErrConfirmationInvalid, "a flow prepared by another account must be rejected")
	assert.Empty(t, h.store.enrollments)
	assert.Equal(t, 0, h.funding.transfers)
}

func TestGetOwnerAccountReturnsNotFoundWithoutWallet(t *testing.T) {
	h := newHarness(t)
	h.funding.recipient = ""
	_, err := h.service.GetOwnerAccount(context.Background(), h.userID)
	require.ErrorIs(t, err, ErrNotFound, "a missing wallet is an expected account state, not a 500")

	h.funding.recipient = "solana:testnet:RailSettlementAddress"
	account, err := h.service.GetOwnerAccount(context.Background(), h.userID)
	require.NoError(t, err)
	assert.Equal(t, "solana:testnet:RailSettlementAddress", account)
}

func TestListRailStrategiesSurfacesSeededSleeve(t *testing.T) {
	h := newHarness(t)
	created := h.confirmCreate(t, h.createRequest("USDC", "SOL"))
	railStrategy := railStrategySharingProviderBinding(t, h, *created.Strategy.GliderStrategyID)

	// The sleeve has no owning user, so it is invisible to ListStrategies...
	mine, err := h.service.ListStrategies(context.Background(), h.userID, "")
	require.NoError(t, err)
	for _, s := range mine {
		assert.NotEqual(t, railStrategy.ID, s.ID)
	}

	// ...but discoverable through the Rail listing the new endpoint serves.
	rail, err := h.service.ListRailStrategies(context.Background())
	require.NoError(t, err)
	require.Len(t, rail, 1)
	assert.Equal(t, railStrategy.ID, rail[0].ID)

	// And enrollable by id through the user-signed flow.
	prep := &UserEnrollPrepareRequest{
		StrategyID:     railStrategy.ID.String(),
		OwnerAccountID: testOwnerAccount,
		AmountUSD:      decimal.NewFromInt(100),
	}
	ready := h.confirmUserPrepare(t, prep)
	require.NotEmpty(t, ready.FlowID)
}
