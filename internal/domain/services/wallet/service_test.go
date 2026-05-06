package wallet

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

func TestMapBridgeStatusToWalletStatus(t *testing.T) {
	tests := []struct {
		name         string
		bridgeStatus string
		want         entities.WalletStatus
	}{
		{
			name:         "active maps to live",
			bridgeStatus: "active",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "live maps to live",
			bridgeStatus: "live",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "ready maps to live",
			bridgeStatus: "ready",
			want:         entities.WalletStatusLive,
		},
		{
			name:         "failed maps to failed",
			bridgeStatus: "failed",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "error maps to failed",
			bridgeStatus: "error",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "rejected maps to failed",
			bridgeStatus: "rejected",
			want:         entities.WalletStatusFailed,
		},
		{
			name:         "creating maps to creating",
			bridgeStatus: "creating",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "pending maps to creating",
			bridgeStatus: "pending",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "processing maps to creating",
			bridgeStatus: "processing",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "initializing maps to creating",
			bridgeStatus: "initializing",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "unknown status maps to creating",
			bridgeStatus: "unknown",
			want:         entities.WalletStatusCreating,
		},
		{
			name:         "empty string maps to creating",
			bridgeStatus: "",
			want:         entities.WalletStatusCreating,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapBridgeStatusToWalletStatus(tt.bridgeStatus)
			if got != tt.want {
				t.Errorf("mapBridgeStatusToWalletStatus(%q) = %v, want %v", tt.bridgeStatus, got, tt.want)
			}
		})
	}
}

func TestGetWalletAddressesPrefersCircleWalletPerChain(t *testing.T) {
	userID := uuid.New()
	now := time.Now()
	repo := &fakeWalletRepo{wallets: []*entities.ManagedWallet{
		{
			ID:             uuid.New(),
			UserID:         userID,
			Chain:          entities.WalletChainSolana,
			Address:        "bridge-address",
			BridgeWalletID: "bridge-wallet",
			AccountType:    entities.AccountTypeBridgeWallet,
			Status:         entities.WalletStatusLive,
			CreatedAt:      now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
		},
		{
			ID:             uuid.New(),
			UserID:         userID,
			Chain:          entities.WalletChainSolana,
			Address:        "circle-address",
			CircleWalletID: "circle-wallet",
			AccountType:    entities.AccountTypeEOA,
			Status:         entities.WalletStatusLive,
			CreatedAt:      now.Add(-time.Hour),
			UpdatedAt:      now.Add(-time.Hour),
		},
	}}
	svc := NewService(repo, &fakeProvisioningJobRepo{}, nil, nil, nil, nil, nil, zap.NewNop(), Config{})

	resp, err := svc.GetWalletAddresses(context.Background(), userID, nil)
	if err != nil {
		t.Fatalf("GetWalletAddresses returned error: %v", err)
	}
	if len(resp.Wallets) != 1 {
		t.Fatalf("expected one wallet after per-chain collapse, got %d", len(resp.Wallets))
	}
	if resp.Wallets[0].Address != "circle-address" {
		t.Fatalf("expected Circle address, got %q", resp.Wallets[0].Address)
	}
}

func TestCreateWalletsForUserCreatesCircleWhenOnlyBridgeExists(t *testing.T) {
	userID := uuid.New()
	repo := &fakeWalletRepo{wallets: []*entities.ManagedWallet{
		{
			ID:             uuid.New(),
			UserID:         userID,
			Chain:          entities.WalletChainSolana,
			Address:        "bridge-address",
			BridgeWalletID: "bridge-wallet",
			AccountType:    entities.AccountTypeBridgeWallet,
			Status:         entities.WalletStatusLive,
			CreatedAt:      time.Now().Add(-time.Hour),
			UpdatedAt:      time.Now().Add(-time.Hour),
		},
	}}
	circleProvider := &fakeCircleWalletProvider{}
	svc := NewService(repo, &fakeProvisioningJobRepo{}, nil, nil, nil, circleProvider, nil, zap.NewNop(), Config{
		DefaultWalletSetID: "wallet-set",
	})

	if err := svc.CreateWalletsForUser(context.Background(), userID, []entities.WalletChain{entities.WalletChainSolana}); err != nil {
		t.Fatalf("CreateWalletsForUser returned error: %v", err)
	}
	if len(circleProvider.createdChains) != 1 {
		t.Fatalf("expected Circle wallet creation for legacy Bridge-only chain, got %d creations", len(circleProvider.createdChains))
	}
	if len(repo.wallets) != 2 {
		t.Fatalf("expected Circle wallet to be saved alongside Bridge wallet, got %d wallets", len(repo.wallets))
	}
	if repo.wallets[0].BridgeWalletID == "" {
		t.Fatal("expected legacy Bridge wallet ID to be preserved")
	}
	if repo.wallets[1].CircleWalletID == "" || repo.wallets[1].Address != "circle-address" {
		t.Fatalf("expected new wallet row to point at Circle wallet, got id=%q address=%q", repo.wallets[1].CircleWalletID, repo.wallets[1].Address)
	}
}

func TestCreateWalletsForUserReusesExistingCircleWalletFromProvider(t *testing.T) {
	userID := uuid.New()
	repo := &fakeWalletRepo{}
	circleProvider := &fakeCircleWalletProvider{listedWallets: []*entities.ManagedWallet{
		{
			ID:             uuid.New(),
			UserID:         userID,
			Chain:          entities.WalletChainSolana,
			Address:        "existing-circle-address",
			CircleWalletID: "existing-circle-wallet",
			AccountType:    entities.AccountTypeEOA,
			Status:         entities.WalletStatusLive,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	}}
	svc := NewService(repo, &fakeProvisioningJobRepo{}, nil, nil, nil, circleProvider, nil, zap.NewNop(), Config{
		DefaultWalletSetID: "wallet-set",
	})

	if err := svc.CreateWalletsForUser(context.Background(), userID, []entities.WalletChain{entities.WalletChainSolana}); err != nil {
		t.Fatalf("CreateWalletsForUser returned error: %v", err)
	}
	if len(circleProvider.createdChains) != 0 {
		t.Fatalf("expected no new Circle wallet creation, got %d creations", len(circleProvider.createdChains))
	}
	if len(repo.wallets) != 1 {
		t.Fatalf("expected existing Circle wallet to be saved, got %d wallets", len(repo.wallets))
	}
	if repo.wallets[0].CircleWalletID != "existing-circle-wallet" {
		t.Fatalf("expected existing Circle wallet ID, got %q", repo.wallets[0].CircleWalletID)
	}
}

func TestCreateWalletsForUserErrorsWhenProviderReturnsNoWallets(t *testing.T) {
	userID := uuid.New()
	repo := &fakeWalletRepo{}
	circleProvider := &fakeCircleWalletProvider{returnNoWallets: true}
	svc := NewService(repo, &fakeProvisioningJobRepo{}, nil, nil, nil, circleProvider, nil, zap.NewNop(), Config{
		DefaultWalletSetID: "wallet-set",
	})

	err := svc.CreateWalletsForUser(context.Background(), userID, []entities.WalletChain{entities.WalletChainSolana})
	if err == nil {
		t.Fatal("expected error when Circle returns no wallets")
	}
	if len(repo.wallets) != 0 {
		t.Fatalf("expected no wallets persisted, got %d", len(repo.wallets))
	}
}

type fakeWalletRepo struct {
	wallets []*entities.ManagedWallet
}

func (r *fakeWalletRepo) Create(_ context.Context, wallet *entities.ManagedWallet) error {
	r.wallets = append(r.wallets, wallet)
	return nil
}

func (r *fakeWalletRepo) GetByID(_ context.Context, id uuid.UUID) (*entities.ManagedWallet, error) {
	for _, w := range r.wallets {
		if w.ID == id {
			return w, nil
		}
	}
	return nil, nil
}

func (r *fakeWalletRepo) GetByUserID(_ context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	var out []*entities.ManagedWallet
	for _, w := range r.wallets {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out, nil
}

func (r *fakeWalletRepo) GetByUserAndChain(_ context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	for _, w := range r.wallets {
		if w.UserID == userID && w.Chain == chain {
			return w, nil
		}
	}
	return nil, nil
}

func (r *fakeWalletRepo) GetByBridgeWalletID(_ context.Context, bridgeWalletID string) (*entities.ManagedWallet, error) {
	for _, w := range r.wallets {
		if w.BridgeWalletID == bridgeWalletID {
			return w, nil
		}
	}
	return nil, nil
}

func (r *fakeWalletRepo) Update(_ context.Context, wallet *entities.ManagedWallet) error {
	for i, w := range r.wallets {
		if w.ID == wallet.ID {
			r.wallets[i] = wallet
			return nil
		}
	}
	return nil
}

func (r *fakeWalletRepo) UpdateStatus(_ context.Context, id uuid.UUID, status entities.WalletStatus) error {
	for _, w := range r.wallets {
		if w.ID == id {
			w.Status = status
			return nil
		}
	}
	return nil
}

type fakeProvisioningJobRepo struct{}

func (r *fakeProvisioningJobRepo) Create(context.Context, *entities.WalletProvisioningJob) error {
	return nil
}

func (r *fakeProvisioningJobRepo) GetByID(context.Context, uuid.UUID) (*entities.WalletProvisioningJob, error) {
	return nil, nil
}

func (r *fakeProvisioningJobRepo) GetByUserID(context.Context, uuid.UUID) (*entities.WalletProvisioningJob, error) {
	return nil, nil
}

func (r *fakeProvisioningJobRepo) GetRetryableJobs(context.Context, int) ([]*entities.WalletProvisioningJob, error) {
	return nil, nil
}

func (r *fakeProvisioningJobRepo) Update(context.Context, *entities.WalletProvisioningJob) error {
	return nil
}

func (r *fakeProvisioningJobRepo) GetOrCreateForUser(context.Context, uuid.UUID, []string) (*entities.WalletProvisioningJob, bool, error) {
	return nil, false, nil
}

type fakeCircleWalletProvider struct {
	createdChains   []entities.WalletChain
	listedWallets   []*entities.ManagedWallet
	returnNoWallets bool
}

func (p *fakeCircleWalletProvider) CreateWalletForUser(_ context.Context, userID uuid.UUID, walletSetID string, chain entities.WalletChain) (*entities.ManagedWallet, error) {
	p.createdChains = append(p.createdChains, chain)
	return &entities.ManagedWallet{
		ID:             uuid.New(),
		UserID:         userID,
		WalletSetID:    uuid.NewSHA1(uuid.NameSpaceOID, []byte(walletSetID)),
		Chain:          chain,
		Address:        "circle-address",
		CircleWalletID: "circle-wallet",
		AccountType:    entities.AccountTypeEOA,
		Status:         entities.WalletStatusLive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}, nil
}

func (p *fakeCircleWalletProvider) CreateMultiChainWallets(ctx context.Context, userID uuid.UUID, walletSetID string, chains []entities.WalletChain) ([]*entities.ManagedWallet, error) {
	if p.returnNoWallets {
		return nil, nil
	}
	wallets := make([]*entities.ManagedWallet, 0, len(chains))
	for _, chain := range chains {
		wallet, err := p.CreateWalletForUser(ctx, userID, walletSetID, chain)
		if err != nil {
			return nil, err
		}
		wallets = append(wallets, wallet)
	}
	return wallets, nil
}

func (p *fakeCircleWalletProvider) GetWalletBalance(context.Context, string) (string, error) {
	return "0", nil
}

func (p *fakeCircleWalletProvider) ListWalletsForUser(context.Context, uuid.UUID, string) ([]*entities.ManagedWallet, error) {
	return p.listedWallets, nil
}
