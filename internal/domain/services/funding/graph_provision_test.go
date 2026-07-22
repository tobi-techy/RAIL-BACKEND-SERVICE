package funding

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// ── fakes ─────────────────────────────────────────────────────────

type fakeUserProvider struct {
	profile      *entities.UserProfile
	tier         int
	bvnMarked    bool
	ninMarked    bool
	personID     string
	personSetHit int
}

func (f *fakeUserProvider) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	return f.profile, nil
}
func (f *fakeUserProvider) UpdateGraphPersonID(ctx context.Context, userID uuid.UUID, personID string) error {
	f.personID = personID
	f.personSetHit++
	return nil
}
func (f *fakeUserProvider) UpdateKYCTier(ctx context.Context, userID uuid.UUID, tier int) error {
	f.tier = tier
	return nil
}
func (f *fakeUserProvider) MarkBVNVerified(ctx context.Context, userID uuid.UUID, last4 string) error {
	f.bvnMarked = true
	return nil
}
func (f *fakeUserProvider) MarkNINVerified(ctx context.Context, userID uuid.UUID, last4 string) error {
	f.ninMarked = true
	return nil
}

type fakeProvisionVARepo struct {
	fakeVARepo
	mu       sync.Mutex
	byUserCC map[string]*entities.VirtualAccount
}

func newFakeProvisionVARepo() *fakeProvisionVARepo {
	return &fakeProvisionVARepo{
		fakeVARepo: *newFakeVARepo(),
		byUserCC:   map[string]*entities.VirtualAccount{},
	}
}

func (r *fakeProvisionVARepo) Create(ctx context.Context, a *entities.VirtualAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byUserCC[a.UserID.String()+a.Currency] = a
	return nil
}
func (r *fakeProvisionVARepo) GetActiveByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if acct := r.byUserCC[userID.String()+currency]; acct != nil && acct.Status == entities.VirtualAccountStatusActive {
		return acct, nil
	}
	return nil, nil
}
func (r *fakeProvisionVARepo) GetProvisionedByUserIDAndCurrency(ctx context.Context, userID uuid.UUID, currency string) (*entities.VirtualAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byUserCC[userID.String()+currency], nil
}

type fakeLiveness struct {
	mu    sync.Mutex
	calls int
	done  chan struct{}
}

func (f *fakeLiveness) InitiateLiveness(ctx context.Context, userID uuid.UUID) error {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.done != nil {
		close(f.done)
	}
	return nil
}

func strPtr(s string) *string { return &s }

func newProvisionUser() *entities.UserProfile {
	dob := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	return &entities.UserProfile{
		ID:            uuid.New(),
		Email:         "test@example.com",
		FirstName:     strPtr("Ada"),
		LastName:      strPtr("Obi"),
		Phone:         strPtr("08012345678"),
		DateOfBirth:   &dob,
		AddressStreet: strPtr("1 Marina Rd"),
		AddressCity:   strPtr("Lagos"),
		AddressState:  strPtr("Lagos"),
		KYCTier:       entities.KYCTierLevelNonKYC,
	}
}

func newProvisionService(up GraphUserProvider, va GraphVirtualAccountRepository) *GraphVirtualAccountService {
	return NewGraphVirtualAccountService(&fakeGraphClient{}, va, newFakeDepositRepo(), up, &fakeAllocation{}, &fakeLedger{}, 0, logger.NewLogger(zap.NewNop()))
}

// ── tests ─────────────────────────────────────────────────────────

func TestProvisionNGNAccount_BVNNINPromotesTier2(t *testing.T) {
	up := &fakeUserProvider{profile: newProvisionUser()}
	va := newFakeProvisionVARepo()
	liveness := &fakeLiveness{done: make(chan struct{})}
	svc := newProvisionService(up, va)
	svc.SetLivenessInitiator(liveness)

	acct, err := svc.ProvisionNGNAccount(context.Background(), &ProvisionNGNAccountRequest{
		UserID:   up.profile.ID,
		BVN:      "12345678901",
		IDType:   "nin",
		IDNumber: "98765432109",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acct == nil || acct.Currency != "NGN" {
		t.Fatalf("expected an NGN account, got %+v", acct)
	}
	if up.tier != entities.KYCTierLevelBasic {
		t.Errorf("expected tier promoted to basic (2), got %d", up.tier)
	}
	if !up.bvnMarked {
		t.Error("expected BVN marked verified")
	}
	if !up.ninMarked {
		t.Error("expected NIN marked verified")
	}

	// Async liveness must fire post-provision.
	select {
	case <-liveness.done:
	case <-time.After(2 * time.Second):
		t.Error("expected liveness initiator to be called")
	}
}

func TestProvisionNGNAccount_MissingBVNNIN(t *testing.T) {
	up := &fakeUserProvider{profile: newProvisionUser()}
	svc := newProvisionService(up, newFakeProvisionVARepo())

	_, err := svc.ProvisionNGNAccount(context.Background(), &ProvisionNGNAccountRequest{
		UserID: up.profile.ID,
		BVN:    "", // missing
	})
	if err != ErrNGNRequiresBVNNIN {
		t.Fatalf("expected ErrNGNRequiresBVNNIN, got %v", err)
	}
	if up.tier != 0 {
		t.Errorf("tier must not be promoted on missing prerequisites, got %d", up.tier)
	}
}

func TestProvisionNGNAccount_Idempotent(t *testing.T) {
	up := &fakeUserProvider{profile: newProvisionUser()}
	va := newFakeProvisionVARepo()
	svc := newProvisionService(up, va)

	req := &ProvisionNGNAccountRequest{
		UserID:   up.profile.ID,
		BVN:      "12345678901",
		IDType:   "nin",
		IDNumber: "98765432109",
	}
	first, err := svc.ProvisionNGNAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("first provision error: %v", err)
	}
	second, err := svc.ProvisionNGNAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("second provision error: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected idempotent provision to return the same account, got %s vs %s", first.ID, second.ID)
	}
}

func TestProvisionNGNAccount_PendingAccountIsReturnedForGetAndRetry(t *testing.T) {
	up := &fakeUserProvider{profile: newProvisionUser()}
	va := newFakeProvisionVARepo()
	svc := newProvisionService(up, va)

	req := &ProvisionNGNAccountRequest{
		UserID:   up.profile.ID,
		BVN:      "12345678901",
		IDType:   "nin",
		IDNumber: "98765432109",
	}
	first, err := svc.ProvisionNGNAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("provision error: %v", err)
	}
	if first.Status != entities.VirtualAccountStatusPending {
		t.Fatalf("expected pending account from fake Graph, got %s", first.Status)
	}

	got, err := svc.GetNGNAccount(context.Background(), up.profile.ID)
	if err != nil {
		t.Fatalf("get NGN account error: %v", err)
	}
	if got == nil || got.ID != first.ID {
		t.Fatalf("expected pending account to be visible, got %+v", got)
	}

	second, err := svc.ProvisionNGNAccount(context.Background(), req)
	if err != nil {
		t.Fatalf("retry provision error: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected retry to return pending account, got %s vs %s", second.ID, first.ID)
	}
}

func TestProvisionNGNAccount_InvalidNIN(t *testing.T) {
	up := &fakeUserProvider{profile: newProvisionUser()}
	svc := newProvisionService(up, newFakeProvisionVARepo())

	_, err := svc.ProvisionNGNAccount(context.Background(), &ProvisionNGNAccountRequest{
		UserID:   up.profile.ID,
		BVN:      "12345678901",
		IDType:   "nin",
		IDNumber: "not-a-nin",
	})
	if !errors.Is(err, ErrNGNInvalidIdentity) {
		t.Fatalf("expected ErrNGNInvalidIdentity, got %v", err)
	}
}
