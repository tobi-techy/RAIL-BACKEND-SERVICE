package onboarding

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type basicCompleteUserRepo struct {
	user                 *entities.UserProfile
	updatePasswordCalls  int
	updateProfileCalls   int
	updateStatusCalls    int
	updateKYCStatusCalls int
	updateSOFCalls       int
	passwordHash         string
}

func (r *basicCompleteUserRepo) Create(ctx context.Context, user *entities.UserProfile) error {
	return nil
}

func (r *basicCompleteUserRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	if r.user == nil || r.user.ID != id {
		return nil, errors.New("user not found")
	}
	return r.user, nil
}

func (r *basicCompleteUserRepo) GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error) {
	return nil, errors.New("user not found")
}

func (r *basicCompleteUserRepo) GetByAuthProviderID(ctx context.Context, authProviderID string) (*entities.UserProfile, error) {
	return nil, errors.New("user not found")
}

func (r *basicCompleteUserRepo) Update(ctx context.Context, user *entities.UserProfile) error {
	r.updateProfileCalls++
	r.user.FirstName = user.FirstName
	r.user.LastName = user.LastName
	r.user.UpdatedAt = user.UpdatedAt
	return nil
}

func (r *basicCompleteUserRepo) UpdateOnboardingStatus(ctx context.Context, userID uuid.UUID, status entities.OnboardingStatus) error {
	r.updateStatusCalls++
	r.user.OnboardingStatus = status
	return nil
}

func (r *basicCompleteUserRepo) UpdateKYCStatus(ctx context.Context, userID uuid.UUID, status entities.KYCStatus, approvedAt *time.Time, rejectionReason *string) error {
	r.updateKYCStatusCalls++
	r.user.KYCStatus = string(status)
	return nil
}

func (r *basicCompleteUserRepo) UpdateBridgeKYCStatus(ctx context.Context, userID uuid.UUID, status string) error {
	return nil
}

func (r *basicCompleteUserRepo) UpdatePassword(ctx context.Context, userID uuid.UUID, hash string) error {
	r.updatePasswordCalls++
	r.passwordHash = hash
	return nil
}

func (r *basicCompleteUserRepo) UpdateSourceOfFunds(ctx context.Context, userID uuid.UUID, employmentStatus, sourceOfFunds, accountPurpose *string) error {
	r.updateSOFCalls++
	return nil
}

type basicCompleteAuditService struct {
	calls int
}

func (s *basicCompleteAuditService) LogOnboardingEvent(ctx context.Context, userID uuid.UUID, action, entity string, before, after interface{}) error {
	s.calls++
	return nil
}

func newBasicCompleteService(user *entities.UserProfile, audit *basicCompleteAuditService) (*Service, *basicCompleteUserRepo) {
	userRepo := &basicCompleteUserRepo{user: user}
	return NewService(
		userRepo,
		nil,
		nil,
		nil,
		nil,
		audit,
		nil,
		nil,
		nil,
		zap.NewNop(),
		nil,
	), userRepo
}

func TestBasicCompleteOnboardingCompletesStartedUser(t *testing.T) {
	userID := uuid.New()
	user := &entities.UserProfile{
		ID:               userID,
		Email:            "tester@example.com",
		EmailVerified:    true,
		OnboardingStatus: entities.OnboardingStatusStarted,
		KYCStatus:        string(entities.KYCStatusPending),
		IsActive:         true,
	}
	audit := &basicCompleteAuditService{}
	service, userRepo := newBasicCompleteService(user, audit)

	resp, err := service.BasicCompleteOnboarding(context.Background(), &entities.BasicCompleteRequest{
		UserID:    userID,
		FirstName: "Test",
		LastName:  "User",
	})

	require.NoError(t, err)
	require.Equal(t, string(entities.OnboardingStatusBasicComplete), resp.OnboardingStatus)
	require.Equal(t, entities.OnboardingStatusBasicComplete, user.OnboardingStatus)
	require.Equal(t, string(entities.KYCStatusNonKYC), user.KYCStatus)
	require.Equal(t, 1, userRepo.updateProfileCalls)
	require.Equal(t, 1, userRepo.updateStatusCalls)
	require.Equal(t, 1, userRepo.updateKYCStatusCalls)
	require.Equal(t, 1, audit.calls)
}

func TestBasicCompleteOnboardingIsIdempotentAfterCompletion(t *testing.T) {
	userID := uuid.New()
	firstName := "Test"
	lastName := "User"
	user := &entities.UserProfile{
		ID:               userID,
		Email:            "tester@example.com",
		FirstName:        &firstName,
		LastName:         &lastName,
		EmailVerified:    true,
		OnboardingStatus: entities.OnboardingStatusCompleted,
		KYCStatus:        string(entities.KYCStatusNonKYC),
		IsActive:         true,
	}
	audit := &basicCompleteAuditService{}
	service, userRepo := newBasicCompleteService(user, audit)

	resp, err := service.BasicCompleteOnboarding(context.Background(), &entities.BasicCompleteRequest{
		UserID:    userID,
		FirstName: "Test",
		LastName:  "User",
	})

	require.NoError(t, err)
	require.Equal(t, string(entities.OnboardingStatusCompleted), resp.OnboardingStatus)
	require.Equal(t, 0, userRepo.updatePasswordCalls)
	require.Equal(t, 0, userRepo.updateProfileCalls)
	require.Equal(t, 0, userRepo.updateStatusCalls)
	require.Equal(t, 0, userRepo.updateKYCStatusCalls)
	require.Equal(t, 0, audit.calls)
}

func TestBasicCompleteOnboardingRejectsUnverifiedEmail(t *testing.T) {
	userID := uuid.New()
	user := &entities.UserProfile{
		ID:               userID,
		Email:            "tester@example.com",
		EmailVerified:    false,
		OnboardingStatus: entities.OnboardingStatusStarted,
		KYCStatus:        string(entities.KYCStatusPending),
		IsActive:         true,
	}
	audit := &basicCompleteAuditService{}
	service, userRepo := newBasicCompleteService(user, audit)

	_, err := service.BasicCompleteOnboarding(context.Background(), &entities.BasicCompleteRequest{
		UserID:    userID,
		FirstName: "Test",
		LastName:  "User",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "email must be verified")
	require.Equal(t, 0, userRepo.updatePasswordCalls)
	require.Equal(t, 0, userRepo.updateProfileCalls)
	require.Equal(t, 0, userRepo.updateStatusCalls)
	require.Equal(t, 0, audit.calls)
}
