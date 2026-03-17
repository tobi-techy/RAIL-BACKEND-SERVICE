package unit

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	p2pservice "github.com/rail-service/rail_service/internal/domain/services/p2p"
	repositoryadapters "github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

type mockP2PLedgerService struct {
	lastReq       *entities.CreateTransactionRequest
	requests      []*entities.CreateTransactionRequest
	userAccounts  map[uuid.UUID]*entities.LedgerAccount
	systemAccount *entities.LedgerAccount
}

func newMockP2PLedgerService() *mockP2PLedgerService {
	return &mockP2PLedgerService{
		userAccounts: map[uuid.UUID]*entities.LedgerAccount{},
		systemAccount: &entities.LedgerAccount{
			ID:          uuid.New(),
			AccountType: entities.AccountTypeSystemBufferUSDC,
		},
	}
}

func (m *mockP2PLedgerService) CreateTransaction(ctx context.Context, req *entities.CreateTransactionRequest) (*entities.LedgerTransaction, error) {
	m.lastReq = req
	m.requests = append(m.requests, req)
	return &entities.LedgerTransaction{ID: uuid.New()}, nil
}

func (m *mockP2PLedgerService) GetOrCreateUserAccount(ctx context.Context, userID uuid.UUID, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	if account, ok := m.userAccounts[userID]; ok {
		return account, nil
	}
	account := &entities.LedgerAccount{ID: uuid.New(), UserID: &userID, AccountType: accountType}
	m.userAccounts[userID] = account
	return account, nil
}

func (m *mockP2PLedgerService) GetSystemAccount(ctx context.Context, accountType entities.AccountType) (*entities.LedgerAccount, error) {
	return m.systemAccount, nil
}

func TestP2PTransferExecutor_TransferBetweenUsers_UsesCorrectLedgerDirections(t *testing.T) {
	ledger := newMockP2PLedgerService()
	executor := repositoryadapters.NewP2PTransferExecutor(ledger)

	senderID := uuid.New()
	recipientID := uuid.New()
	err := executor.TransferBetweenUsers(context.Background(), senderID, recipientID, decimal.NewFromInt(25), "P2P transfer", "p2p-send-test")

	require.NoError(t, err)
	require.NotNil(t, ledger.lastReq)
	require.Len(t, ledger.lastReq.Entries, 2)

	assert.Equal(t, "p2p-send-test", ledger.lastReq.IdempotencyKey)
	assert.Equal(t, ledger.userAccounts[senderID].ID, ledger.lastReq.Entries[0].AccountID)
	assert.Equal(t, entities.EntryTypeCredit, ledger.lastReq.Entries[0].EntryType)
	assert.Equal(t, ledger.userAccounts[recipientID].ID, ledger.lastReq.Entries[1].AccountID)
	assert.Equal(t, entities.EntryTypeDebit, ledger.lastReq.Entries[1].EntryType)
}

func TestP2PTransferExecutor_ReserveFunds_DebitsSender(t *testing.T) {
	ledger := newMockP2PLedgerService()
	executor := repositoryadapters.NewP2PTransferExecutor(ledger)

	userID := uuid.New()
	err := executor.ReserveFunds(context.Background(), userID, decimal.NewFromInt(10), "P2P reserve", "p2p-reserve-test")

	require.NoError(t, err)
	require.NotNil(t, ledger.lastReq)
	require.Len(t, ledger.lastReq.Entries, 2)

	assert.Equal(t, "p2p-reserve-test", ledger.lastReq.IdempotencyKey)
	assert.Equal(t, ledger.userAccounts[userID].ID, ledger.lastReq.Entries[0].AccountID)
	assert.Equal(t, entities.EntryTypeCredit, ledger.lastReq.Entries[0].EntryType)
	assert.Equal(t, ledger.systemAccount.ID, ledger.lastReq.Entries[1].AccountID)
	assert.Equal(t, entities.EntryTypeDebit, ledger.lastReq.Entries[1].EntryType)
}

func TestP2PTransferExecutor_CreditUserFromSystem_CreditsRecipient(t *testing.T) {
	ledger := newMockP2PLedgerService()
	executor := repositoryadapters.NewP2PTransferExecutor(ledger)

	userID := uuid.New()
	err := executor.CreditUserFromSystem(context.Background(), userID, decimal.NewFromInt(10), "P2P release", "p2p-release-test")

	require.NoError(t, err)
	require.NotNil(t, ledger.lastReq)
	require.Len(t, ledger.lastReq.Entries, 2)

	assert.Equal(t, "p2p-release-test", ledger.lastReq.IdempotencyKey)
	assert.Equal(t, ledger.systemAccount.ID, ledger.lastReq.Entries[0].AccountID)
	assert.Equal(t, entities.EntryTypeCredit, ledger.lastReq.Entries[0].EntryType)
	assert.Equal(t, ledger.userAccounts[userID].ID, ledger.lastReq.Entries[1].AccountID)
	assert.Equal(t, entities.EntryTypeDebit, ledger.lastReq.Entries[1].EntryType)
}

type mockP2PRepo struct {
	transfer      *entities.P2PTransfer
	created       *entities.P2PTransfer
	updated       *entities.P2PTransfer
	releasedID    uuid.UUID
	acquiredByID  []uuid.UUID
	acquiredToken []string
}

func (m *mockP2PRepo) Create(ctx context.Context, transfer *entities.P2PTransfer) error {
	copy := *transfer
	m.created = &copy
	return nil
}
func (m *mockP2PRepo) GetByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error) {
	return m.transfer, nil
}
func (m *mockP2PRepo) GetByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error) {
	return m.transfer, nil
}
func (m *mockP2PRepo) GetBySender(ctx context.Context, senderID uuid.UUID, limit, offset int) ([]*entities.P2PTransfer, error) {
	return nil, nil
}
func (m *mockP2PRepo) GetPendingByIdentifier(ctx context.Context, email, phone string) ([]*entities.P2PTransfer, error) {
	return nil, nil
}
func (m *mockP2PRepo) GetExpired(ctx context.Context) ([]*entities.P2PTransfer, error) {
	return nil, nil
}
func (m *mockP2PRepo) AcquirePendingByID(ctx context.Context, id uuid.UUID) (*entities.P2PTransfer, error) {
	m.acquiredByID = append(m.acquiredByID, id)
	if m.transfer == nil || m.transfer.ID != id || m.transfer.Status != entities.P2PStatusPending {
		return nil, sql.ErrNoRows
	}
	copy := *m.transfer
	copy.Status = entities.P2PStatusProcessing
	m.transfer = &copy
	return &copy, nil
}
func (m *mockP2PRepo) AcquirePendingByClaimToken(ctx context.Context, token string) (*entities.P2PTransfer, error) {
	m.acquiredToken = append(m.acquiredToken, token)
	if m.transfer == nil || m.transfer.ClaimToken == nil || *m.transfer.ClaimToken != token || m.transfer.Status != entities.P2PStatusPending {
		return nil, sql.ErrNoRows
	}
	copy := *m.transfer
	copy.Status = entities.P2PStatusProcessing
	m.transfer = &copy
	return &copy, nil
}
func (m *mockP2PRepo) ReleaseProcessing(ctx context.Context, id uuid.UUID) error {
	m.releasedID = id
	if m.transfer != nil && m.transfer.ID == id {
		copy := *m.transfer
		copy.Status = entities.P2PStatusPending
		m.transfer = &copy
	}
	return nil
}
func (m *mockP2PRepo) Update(ctx context.Context, transfer *entities.P2PTransfer) error {
	copy := *transfer
	m.updated = &copy
	m.transfer = &copy
	return nil
}
func (m *mockP2PRepo) UpsertRecentRecipient(ctx context.Context, userID, recipientID uuid.UUID) error {
	return nil
}
func (m *mockP2PRepo) GetRecentRecipients(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.P2PRecentRecipientWithUser, error) {
	return nil, nil
}

type mockP2PUserLookup struct {
	usersByID map[uuid.UUID]*entities.UserProfile
}

func (m *mockP2PUserLookup) GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error) {
	return m.usersByID[id], nil
}
func (m *mockP2PUserLookup) GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error) {
	return nil, nil
}
func (m *mockP2PUserLookup) GetByPhone(ctx context.Context, phone string) (*entities.UserProfile, error) {
	return nil, nil
}
func (m *mockP2PUserLookup) GetByRailTag(ctx context.Context, railTag string) (*entities.UserProfile, error) {
	return nil, nil
}

type mockP2PBalance struct {
	balance decimal.Decimal
}

func (m *mockP2PBalance) GetSpendBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return m.balance, nil
}

type mockP2PWalletLookup struct {
	wallets []*entities.ManagedWallet
}

func (m *mockP2PWalletLookup) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*entities.ManagedWallet, error) {
	return m.wallets, nil
}

type mockP2PTransferExecutor struct {
	transferCalled      bool
	transferIdempotency string
	reserveCalled       bool
	reserveIdempotency  string
	creditCalled        bool
	creditIdempotency   string
}

func (m *mockP2PTransferExecutor) TransferBetweenUsers(ctx context.Context, fromUserID, toUserID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	m.transferCalled = true
	m.transferIdempotency = idempotencyKey
	return nil
}

func (m *mockP2PTransferExecutor) ReserveFunds(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	m.reserveCalled = true
	m.reserveIdempotency = idempotencyKey
	return nil
}

func (m *mockP2PTransferExecutor) CreditUserFromSystem(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, description, idempotencyKey string) error {
	m.creditCalled = true
	m.creditIdempotency = idempotencyKey
	return nil
}

type mockP2PNotification struct{}

func (m *mockP2PNotification) SendP2PInvite(ctx context.Context, identifier, identifierType string, senderName string, amount decimal.Decimal, claimToken string) error {
	return nil
}
func (m *mockP2PNotification) SendP2PReceived(ctx context.Context, recipientID uuid.UUID, senderName string, amount decimal.Decimal, note *string) error {
	return nil
}
func (m *mockP2PNotification) SendP2PClaimed(ctx context.Context, senderID uuid.UUID, recipientName string, amount decimal.Decimal) error {
	return nil
}
func (m *mockP2PNotification) SendP2PExpired(ctx context.Context, senderID uuid.UUID, identifier string, amount decimal.Decimal) error {
	return nil
}

type mockP2PBridgeOfframp struct {
	createReq   map[string]interface{}
	initiateReq map[string]interface{}
}

func (m *mockP2PBridgeOfframp) CreateRecipient(ctx context.Context, req map[string]interface{}) (string, error) {
	m.createReq = req
	return "bridge-customer:external-account", nil
}

func (m *mockP2PBridgeOfframp) InitiateTransfer(ctx context.Context, req map[string]interface{}) (map[string]interface{}, error) {
	m.initiateReq = req
	return map[string]interface{}{"id": "transfer-123", "status": "payment_submitted"}, nil
}

func TestP2PService_ClaimToBank_UsesSenderBridgeCustomerAndStableDebit(t *testing.T) {
	senderID := uuid.New()
	bridgeCustomerID := "bridge-customer-123"
	token := "claim-token"
	transferID := uuid.New()

	repo := &mockP2PRepo{
		transfer: &entities.P2PTransfer{
			ID:                  transferID,
			SenderID:            senderID,
			RecipientIdentifier: "test@example.com",
			IdentifierType:      entities.P2PIdentifierEmail,
			Amount:              decimal.NewFromInt(42),
			Currency:            "USD",
			Status:              entities.P2PStatusPending,
			ClaimToken:          &token,
			ExpiresAt:           time.Now().Add(24 * time.Hour),
		},
	}
	userLookup := &mockP2PUserLookup{
		usersByID: map[uuid.UUID]*entities.UserProfile{
			senderID: {
				ID:               senderID,
				Email:            "sender@example.com",
				BridgeCustomerID: &bridgeCustomerID,
			},
		},
	}
	transferExec := &mockP2PTransferExecutor{}
	bridgeOfframp := &mockP2PBridgeOfframp{}

	svc := p2pservice.NewService(
		repo,
		userLookup,
		&mockP2PBalance{balance: decimal.NewFromInt(100)},
		transferExec,
		&mockP2PNotification{},
		zap.NewNop(),
	)
	svc.SetWalletLookup(&mockP2PWalletLookup{
		wallets: []*entities.ManagedWallet{
			{UserID: senderID, Chain: entities.WalletChainSolana, BridgeWalletID: "bridge-wallet-123"},
		},
	})
	svc.SetBridgeOfframp(bridgeOfframp)

	err := svc.ClaimToBank(context.Background(), token, p2pservice.ClaimToBankRequest{
		AccountHolderName: "Test User",
		RoutingNumber:     "021000021",
		AccountNumber:     "123456789",
	})

	require.NoError(t, err)
	require.NotNil(t, bridgeOfframp.createReq)
	require.NotNil(t, bridgeOfframp.initiateReq)
	require.NotNil(t, repo.updated)

	assert.Equal(t, bridgeCustomerID, bridgeOfframp.createReq["customer_id"])
	assert.Equal(t, "bridge-wallet-123", bridgeOfframp.initiateReq["source_wallet_id"])
	assert.Equal(t, senderID.String(), bridgeOfframp.initiateReq["on_behalf_of"])
	assert.False(t, transferExec.reserveCalled)
	assert.False(t, transferExec.creditCalled)
	assert.Equal(t, entities.P2PStatusClaimed, repo.updated.Status)
	require.NotNil(t, repo.updated.ProviderTransferID)
	require.NotNil(t, repo.updated.ProviderStatus)
	assert.Equal(t, "transfer-123", *repo.updated.ProviderTransferID)
	assert.Equal(t, "payment_submitted", *repo.updated.ProviderStatus)
}

func TestP2PService_SendPending_ReservesFunds(t *testing.T) {
	senderID := uuid.New()

	repo := &mockP2PRepo{}
	transferExec := &mockP2PTransferExecutor{}
	userLookup := &mockP2PUserLookup{
		usersByID: map[uuid.UUID]*entities.UserProfile{
			senderID: {ID: senderID, Email: "sender@example.com"},
		},
	}

	svc := p2pservice.NewService(
		repo,
		userLookup,
		&mockP2PBalance{balance: decimal.NewFromInt(100)},
		transferExec,
		&mockP2PNotification{},
		zap.NewNop(),
	)

	resp, err := svc.Send(context.Background(), senderID, &entities.P2PSendRequest{
		Identifier: "friend@example.com",
		Amount:     "15.00",
		Note:       "Dinner",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, repo.created)
	assert.Equal(t, entities.P2PStatusPending, repo.created.Status)
	assert.True(t, transferExec.reserveCalled)
	assert.Equal(t, "p2p-reserve-"+repo.created.ID.String(), transferExec.reserveIdempotency)
}

func TestP2PService_ClaimByToken_RejectsWrongRecipient(t *testing.T) {
	senderID := uuid.New()
	claimerID := uuid.New()
	token := "claim-token"
	transferID := uuid.New()
	claimerEmail := "claimer@example.com"

	repo := &mockP2PRepo{
		transfer: &entities.P2PTransfer{
			ID:                  transferID,
			SenderID:            senderID,
			RecipientIdentifier: "intended@example.com",
			IdentifierType:      entities.P2PIdentifierEmail,
			Amount:              decimal.NewFromInt(25),
			Currency:            "USD",
			Status:              entities.P2PStatusPending,
			ClaimToken:          &token,
			ExpiresAt:           time.Now().Add(24 * time.Hour),
		},
	}
	userLookup := &mockP2PUserLookup{
		usersByID: map[uuid.UUID]*entities.UserProfile{
			claimerID: {ID: claimerID, Email: claimerEmail},
		},
	}
	transferExec := &mockP2PTransferExecutor{}

	svc := p2pservice.NewService(
		repo,
		userLookup,
		&mockP2PBalance{balance: decimal.NewFromInt(100)},
		transferExec,
		&mockP2PNotification{},
		zap.NewNop(),
	)

	_, err := svc.ClaimByToken(context.Background(), token, claimerID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not eligible")
	assert.Equal(t, transferID, repo.releasedID)
	assert.False(t, transferExec.creditCalled)
	assert.Nil(t, repo.updated)
}
