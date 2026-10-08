package kyc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/bridge"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/didit"
)

type repairUserRepo struct {
	user    *entities.User
	profile *entities.UserProfile
}

func (r *repairUserRepo) GetByID(context.Context, uuid.UUID) (*entities.User, error) {
	return r.user, nil
}

func (r *repairUserRepo) GetProfileByUserID(context.Context, uuid.UUID) (*entities.UserProfile, error) {
	return r.profile, nil
}

func (r *repairUserRepo) Update(_ context.Context, user *entities.User) error {
	r.user = user
	return nil
}

func (r *repairUserRepo) UpdateKYCTier(_ context.Context, _ uuid.UUID, tier int) error {
	if r.profile != nil {
		r.profile.KYCTier = tier
	}
	return nil
}

type repairSubmissionRepo struct {
	submissions []*entities.KYCSubmission
	updated     []*entities.KYCSubmission
	updateErr   error
}

func (r *repairSubmissionRepo) Create(_ context.Context, sub *entities.KYCSubmission) error {
	r.submissions = append(r.submissions, sub)
	return nil
}

func (r *repairSubmissionRepo) GetByProviderRef(_ context.Context, ref string) (*entities.KYCSubmission, error) {
	for _, sub := range r.submissions {
		if sub.ProviderRef == ref {
			return sub, nil
		}
	}
	return nil, fmt.Errorf("KYC submission not found")
}

func (r *repairSubmissionRepo) GetByUserID(_ context.Context, _ uuid.UUID) ([]*entities.KYCSubmission, error) {
	return r.submissions, nil
}

func (r *repairSubmissionRepo) Update(_ context.Context, sub *entities.KYCSubmission) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = append(r.updated, sub)
	return nil
}

type repairBridge struct {
	err            error
	calls          int
	lastCustomerID string
}

func (b *repairBridge) UpdateCustomer(_ context.Context, customerID string, _ *bridge.UpdateCustomerRequest) (*bridge.Customer, error) {
	b.calls++
	b.lastCustomerID = customerID
	if b.err != nil {
		return nil, b.err
	}
	return &bridge.Customer{}, nil
}

type repairDidit struct {
	decision *didit.SessionDecision
	err      error
}

func (a *repairDidit) CreateSession(context.Context, string) (*didit.SessionResponse, error) {
	return &didit.SessionResponse{SessionID: "sess_repair_123"}, nil
}

func (a *repairDidit) GetSessionDecision(context.Context, string) (*didit.SessionDecision, error) {
	return a.decision, a.err
}

func (a *repairDidit) VerifyWebhookSignature([]byte, string, string) error { return nil }

func newRepairFixture(bridgeErr error, seedMarker map[string]any) (*Service, uuid.UUID, *entities.KYCSubmission, *repairBridge, *repairSubmissionRepo) {
	userID := uuid.New()
	sessionRef := "sess_repair_123"
	customerID := "cust_repair_123"

	sub := &entities.KYCSubmission{
		ID:               uuid.New(),
		UserID:           userID,
		Provider:         "didit",
		ProviderRef:      sessionRef,
		Status:           entities.KYCStatusApproved,
		VerificationData: map[string]any{},
		CreatedAt:        time.Now(),
	}
	if seedMarker != nil {
		sub.VerificationData["bridge_govid_repair"] = seedMarker
	}

	userRepo := &repairUserRepo{
		user:    &entities.User{ID: userID, Email: "repair@example.com", KYCProviderRef: &sessionRef},
		profile: &entities.UserProfile{ID: userID, Email: "repair@example.com", BridgeCustomerID: &customerID},
	}
	subRepo := &repairSubmissionRepo{submissions: []*entities.KYCSubmission{sub}}
	br := &repairBridge{err: bridgeErr}
	diditAdapter := &repairDidit{decision: &didit.SessionDecision{
		IDVerifications: []didit.IDVerification{
			{DocumentType: "drivers_license", DocumentNumber: "D1234567"},
		},
	}}

	svc := NewService(userRepo, subRepo, br, nil, nil, nil, "", "", zap.NewNop(), diditAdapter)
	return svc, userID, sub, br, subRepo
}

func repairMarker(sub *entities.KYCSubmission) map[string]any {
	marker, ok := sub.VerificationData["bridge_govid_repair"].(map[string]any)
	if !ok {
		return nil
	}
	return marker
}

func TestRepairBridgeGovIDTerminalBridgeErrorStopsRetries(t *testing.T) {
	// Bridge deleted the customer: every PUT 404s. The repair must fail with
	// the terminal sentinel and mark the submission non-retryable so the
	// 10-minute worker never selects this user again.
	terminalErr := fmt.Errorf("update customer failed: %w",
		&bridge.ErrorResponse{StatusCode: 404, Code: "not_found", Message: "customer not found"})
	svc, userID, sub, br, _ := newRepairFixture(terminalErr, nil)

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.ErrorIs(t, err, ErrBridgeCustomerTerminal)
	require.Equal(t, 1, br.calls, "exactly one Bridge PUT should have been attempted")
	marker := repairMarker(sub)
	require.NotNil(t, marker)
	require.Equal(t, "failed", marker["status"])
	require.Equal(t, true, marker["non_retryable"])
}

func TestRepairBridgeGovIDRetryableErrorKeepsRetryingWithAttempts(t *testing.T) {
	svc, userID, sub, br, _ := newRepairFixture(errors.New("connection reset"), nil)

	err := svc.RepairBridgeGovID(context.Background(), userID)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrBridgeCustomerTerminal)
	marker := repairMarker(sub)
	require.Equal(t, "retryable_error", marker["status"])
	require.Equal(t, false, marker["non_retryable"])
	require.Equal(t, 1, marker["attempts"])

	err = svc.RepairBridgeGovID(context.Background(), userID)
	require.Error(t, err)
	require.Equal(t, 2, repairMarker(sub)["attempts"])
	require.Equal(t, 2, br.calls)
}

func TestRepairBridgeGovIDGivesUpAfterMaxAttempts(t *testing.T) {
	seed := map[string]any{
		"status":        "retryable_error",
		"reason":        "connection reset",
		"non_retryable": false,
		"attempts":      maxBridgeGovIDRepairAttempts - 1,
	}
	svc, userID, sub, br, _ := newRepairFixture(errors.New("connection reset"), seed)

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrBridgeCustomerTerminal)
	require.Equal(t, 1, br.calls)
	marker := repairMarker(sub)
	require.Equal(t, "failed", marker["status"])
	require.Equal(t, true, marker["non_retryable"], "chronically failing repair must stop automatic retries")
	require.Equal(t, maxBridgeGovIDRepairAttempts, marker["attempts"])
}

func TestRepairBridgeGovIDSuccessResetsAttempts(t *testing.T) {
	seed := map[string]any{
		"status":        "retryable_error",
		"reason":        "connection reset",
		"non_retryable": false,
		"attempts":      5,
	}
	svc, userID, sub, br, _ := newRepairFixture(nil, seed)

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.NoError(t, err)
	require.Equal(t, 1, br.calls)
	marker := repairMarker(sub)
	require.Equal(t, "succeeded", marker["status"])
	require.Equal(t, false, marker["non_retryable"])
	require.Equal(t, 0, marker["attempts"])
}

func TestRepairBridgeGovIDHydrateFailureCountsAttempts(t *testing.T) {
	// A sustained Didit outage used to write retryable_error with the attempt
	// count unchanged, so the worker reselected the user on every tick forever.
	svc, userID, sub, br, _ := newRepairFixture(nil, nil)
	svc.diditAdapter = &repairDidit{err: errors.New("didit unavailable")}

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.Error(t, err)
	require.Zero(t, br.calls, "hydrate failed, so Bridge must not have been called")
	marker := repairMarker(sub)
	require.Equal(t, "retryable_error", marker["status"])
	require.Equal(t, 1, marker["attempts"])
	require.Equal(t, false, marker["non_retryable"])
}

func TestRepairBridgeGovIDHydrateFailureGivesUpAtCap(t *testing.T) {
	seed := map[string]any{
		"status":        "retryable_error",
		"reason":        "didit unavailable",
		"non_retryable": false,
		"attempts":      maxBridgeGovIDRepairAttempts - 1,
	}
	svc, userID, sub, br, _ := newRepairFixture(nil, seed)
	svc.diditAdapter = &repairDidit{err: errors.New("didit unavailable")}

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.Error(t, err)
	require.Zero(t, br.calls)
	marker := repairMarker(sub)
	require.Equal(t, "failed", marker["status"])
	require.Equal(t, true, marker["non_retryable"], "hydrate failures must also stop the retry loop")
	require.Equal(t, maxBridgeGovIDRepairAttempts, marker["attempts"])
}

func TestRepairBridgeGovIDStopMarkerFailureIsSurfaced(t *testing.T) {
	// If the stop marker never reaches Postgres, FindApprovedNotActiveBridge
	// keeps selecting the user, so the repair must not report a clean stop.
	terminalErr := fmt.Errorf("update customer failed: %w",
		&bridge.ErrorResponse{StatusCode: 404, Code: "not_found", Message: "customer not found"})
	svc, userID, _, br, subRepo := newRepairFixture(terminalErr, nil)
	subRepo.updateErr = errors.New("database is down")

	err := svc.RepairBridgeGovID(context.Background(), userID)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrBridgeStopMarkerNotPersisted)
	require.ErrorIs(t, err, ErrBridgeCustomerTerminal, "classification is preserved for the worker")
	require.Equal(t, 1, br.calls)
}
