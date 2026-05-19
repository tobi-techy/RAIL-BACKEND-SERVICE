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

type diditSessionUserRepo struct {
	user            *entities.User
	profile         *entities.UserProfile
	updateCalls     int
	getUserCalls    int
	getProfileCalls int
}

func (r *diditSessionUserRepo) GetByID(context.Context, uuid.UUID) (*entities.User, error) {
	r.getUserCalls++
	return r.user, nil
}

func (r *diditSessionUserRepo) GetProfileByUserID(context.Context, uuid.UUID) (*entities.UserProfile, error) {
	r.getProfileCalls++
	return r.profile, nil
}

func (r *diditSessionUserRepo) Update(_ context.Context, user *entities.User) error {
	r.updateCalls++
	r.user = user
	return nil
}

type diditSessionSubmissionRepo struct {
	createCalls int
	updateCalls int
}

func (r *diditSessionSubmissionRepo) Create(context.Context, *entities.KYCSubmission) error {
	r.createCalls++
	return nil
}

func (r *diditSessionSubmissionRepo) GetByProviderRef(context.Context, string) (*entities.KYCSubmission, error) {
	return nil, fmt.Errorf("KYC submission not found")
}

func (r *diditSessionSubmissionRepo) GetByUserID(context.Context, uuid.UUID) ([]*entities.KYCSubmission, error) {
	return nil, nil
}

func (r *diditSessionSubmissionRepo) Update(context.Context, *entities.KYCSubmission) error {
	r.updateCalls++
	return nil
}

type diditSessionBridge struct {
	err error
}

func (b *diditSessionBridge) UpdateCustomer(context.Context, string, *bridge.UpdateCustomerRequest) (*bridge.Customer, error) {
	if b.err != nil {
		return nil, b.err
	}
	return &bridge.Customer{}, nil
}

type diditSessionAdapter struct {
	resp *didit.SessionResponse
	err  error
}

func (a *diditSessionAdapter) CreateSession(context.Context, string) (*didit.SessionResponse, error) {
	return a.resp, a.err
}

func (a *diditSessionAdapter) GetSessionDecision(context.Context, string) (*didit.SessionDecision, error) {
	return nil, nil
}

func (a *diditSessionAdapter) VerifyWebhookSignature([]byte, string, string) error {
	return nil
}

func TestStartDiditSessionRejectsMalformedProviderResponse(t *testing.T) {
	userID := uuid.New()
	userRepo := newDiditSessionUserRepo(userID)
	submissionRepo := &diditSessionSubmissionRepo{}
	svc := NewService(
		userRepo,
		submissionRepo,
		&diditSessionBridge{},
		nil,
		nil,
		nil,
		nil,
		"",
		"",
		zap.NewNop(),
		&diditSessionAdapter{resp: &didit.SessionResponse{SessionToken: "token-only"}},
	)

	resp, err := svc.StartDiditSession(context.Background(), userID, validDiditSessionRequest())

	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrDiditSessionFailed)
	require.Zero(t, submissionRepo.createCalls)
	require.Zero(t, submissionRepo.updateCalls)
	require.Zero(t, userRepo.updateCalls)
}

func TestStartDiditSessionClassifiesBridgeAndDiditProviderFailures(t *testing.T) {
	userID := uuid.New()

	bridgeFailureSvc := NewService(
		newDiditSessionUserRepo(userID),
		&diditSessionSubmissionRepo{},
		&diditSessionBridge{err: errors.New("upstream rejected request")},
		nil,
		nil,
		nil,
		nil,
		"",
		"",
		zap.NewNop(),
		&diditSessionAdapter{resp: &didit.SessionResponse{SessionID: "sess_123", SessionToken: "token_123"}},
	)

	resp, err := bridgeFailureSvc.StartDiditSession(context.Background(), userID, validDiditSessionRequest())
	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrBridgeSubmissionFailed)

	diditFailureSvc := NewService(
		newDiditSessionUserRepo(userID),
		&diditSessionSubmissionRepo{},
		&diditSessionBridge{},
		nil,
		nil,
		nil,
		nil,
		"",
		"",
		zap.NewNop(),
		&diditSessionAdapter{err: errors.New("didit unavailable")},
	)

	resp, err = diditFailureSvc.StartDiditSession(context.Background(), userID, validDiditSessionRequest())
	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrDiditSessionFailed)
}

func TestStartDiditSessionHandlesMissingBridgeAdapter(t *testing.T) {
	userID := uuid.New()
	svc := NewService(
		newDiditSessionUserRepo(userID),
		&diditSessionSubmissionRepo{},
		nil,
		nil,
		nil,
		nil,
		nil,
		"",
		"",
		zap.NewNop(),
		&diditSessionAdapter{resp: &didit.SessionResponse{SessionID: "sess_123", SessionToken: "token_123"}},
	)

	resp, err := svc.StartDiditSession(context.Background(), userID, validDiditSessionRequest())

	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrBridgeNotConfigured)
}

func newDiditSessionUserRepo(userID uuid.UUID) *diditSessionUserRepo {
	firstName := "Test"
	lastName := "User"
	dob := time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC)
	street := "1 Market St"
	city := "San Francisco"
	postalCode := "94105"
	country := "USA"
	bridgeCustomerID := "bridge_customer_123"

	return &diditSessionUserRepo{
		user: &entities.User{
			ID:               userID,
			Email:            "test@example.com",
			KYCStatus:        string(entities.KYCStatusPending),
			BridgeCustomerID: &bridgeCustomerID,
		},
		profile: &entities.UserProfile{
			ID:                userID,
			Email:             "test@example.com",
			FirstName:         &firstName,
			LastName:          &lastName,
			DateOfBirth:       &dob,
			AddressStreet:     &street,
			AddressCity:       &city,
			AddressPostalCode: &postalCode,
			AddressCountry:    &country,
			KYCStatus:         string(entities.KYCStatusPending),
			BridgeCustomerID:  &bridgeCustomerID,
		},
	}
}

func validDiditSessionRequest() *entities.KYCDigitSessionRequest {
	return &entities.KYCDigitSessionRequest{
		TaxID:                      "123456789",
		TaxIDType:                  "ssn",
		IssuingCountry:             "USA",
		SourceOfFunds:              "salary",
		EmploymentStatus:           "employed",
		ExpectedMonthlyPaymentsUSD: "1000_4999",
		AccountPurpose:             "personal",
	}
}
