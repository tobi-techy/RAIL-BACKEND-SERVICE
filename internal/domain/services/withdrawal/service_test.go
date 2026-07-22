package withdrawal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/rail-service/rail_service/pkg/logger"
)

// --- mock implementations of every interface the service depends on ---

type mockLedger struct {
	mock.Mock
}

func (m *mockLedger) GetAccountBalance(_ context.Context, _ uuid.UUID, _ entities.AccountType) (decimal.Decimal, error) {
	args := m.Called()
	return args.Get(0).(decimal.Decimal), args.Error(1)
}
func (m *mockLedger) CreateTransaction(_ context.Context, _ uuid.UUID, _ entities.AccountType, _ entities.TransactionType, _ decimal.Decimal, _ map[string]interface{}) error {
	return m.Called().Error(0)
}
func (m *mockLedger) CreatePendingTransaction(_ context.Context, userID uuid.UUID, acct entities.AccountType, txType entities.TransactionType, amount decimal.Decimal, key string, meta map[string]interface{}) error {
	return m.Called(userID, acct, txType, amount, key, meta).Error(0)
}
func (m *mockLedger) CommitPendingTransaction(_ context.Context, key string) error {
	return m.Called(key).Error(0)
}
func (m *mockLedger) FailPendingTransaction(_ context.Context, key string) error {
	return m.Called(key).Error(0)
}
func (m *mockLedger) GetLedgerTransactionStatus(_ context.Context, key string) (entities.TransactionStatus, error) {
	args := m.Called(key)
	return args.Get(0).(entities.TransactionStatus), args.Error(1)
}
func (m *mockLedger) ReverseTransaction(_ context.Context, userID uuid.UUID, acct entities.AccountType, origTxID string, amount decimal.Decimal, meta map[string]interface{}) error {
	return m.Called(userID, acct, origTxID, amount, meta).Error(0)
}
func (m *mockLedger) TransferSpendingToStash(_ context.Context, _ uuid.UUID, _ decimal.Decimal, _ string) error {
	return m.Called().Error(0)
}

type mockWithdrawalRepo struct {
	mock.Mock
}

func (m *mockWithdrawalRepo) Create(_ context.Context, _ *entities.Withdrawal) error {
	return m.Called().Error(0)
}
func (m *mockWithdrawalRepo) GetByID(_ context.Context, id uuid.UUID) (*entities.Withdrawal, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Withdrawal), args.Error(1)
}
func (m *mockWithdrawalRepo) GetByUserID(_ context.Context, _ uuid.UUID, _, _ int) ([]*entities.Withdrawal, error) {
	return nil, m.Called().Error(0)
}
func (m *mockWithdrawalRepo) GetByIdempotencyKey(_ context.Context, key string) (*entities.Withdrawal, error) {
	args := m.Called(key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Withdrawal), args.Error(1)
}
func (m *mockWithdrawalRepo) GetByProviderTransferID(_ context.Context, transferID string) (*entities.Withdrawal, error) {
	args := m.Called(transferID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Withdrawal), args.Error(1)
}
func (m *mockWithdrawalRepo) GetByProviderTransferIDPrefix(_ context.Context, prefix string) (*entities.Withdrawal, error) {
	args := m.Called(prefix)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*entities.Withdrawal), args.Error(1)
}
func (m *mockWithdrawalRepo) UpdateStatus(_ context.Context, id uuid.UUID, status entities.WithdrawalStatus) error {
	return m.Called(id, status).Error(0)
}
func (m *mockWithdrawalRepo) UpdateBridgeTransfer(_ context.Context, id uuid.UUID, transferID string) error {
	return m.Called(id, transferID).Error(0)
}
func (m *mockWithdrawalRepo) UpdateTxHash(_ context.Context, id uuid.UUID, txHash string) error {
	return m.Called(id, txHash).Error(0)
}
func (m *mockWithdrawalRepo) UpdateCompletedAt(_ context.Context, id uuid.UUID, completedAt time.Time) error {
	return m.Called(id, completedAt).Error(0)
}
func (m *mockWithdrawalRepo) ForceComplete(_ context.Context, id uuid.UUID, completedAt time.Time) error {
	return m.Called(id, completedAt).Error(0)
}
func (m *mockWithdrawalRepo) MarkCompleted(_ context.Context, id uuid.UUID) error {
	return m.Called(id).Error(0)
}
func (m *mockWithdrawalRepo) MarkFailed(_ context.Context, id uuid.UUID, reason string) error {
	return m.Called(id, reason).Error(0)
}
func (m *mockWithdrawalRepo) MarkCancelled(_ context.Context, id uuid.UUID) error {
	return m.Called(id).Error(0)
}
func (m *mockWithdrawalRepo) GetPendingWithdrawalsTotal(_ context.Context, _ uuid.UUID) (decimal.Decimal, error) {
	return m.Called().Get(0).(decimal.Decimal), m.Called().Error(1)
}

type mockNotification struct {
	mock.Mock
}

func (m *mockNotification) NotifyWithdrawalCompleted(_ context.Context, userID uuid.UUID, amount decimal.Decimal, dest string) error {
	return m.Called(userID, amount, dest).Error(0)
}
func (m *mockNotification) NotifyWithdrawalFailed(_ context.Context, userID uuid.UUID, amount decimal.Decimal, reason string) error {
	return m.Called(userID, amount, reason).Error(0)
}
func (m *mockNotification) NotifyWithdrawalSubmitted(_ context.Context, userID uuid.UUID, amount string) error {
	return m.Called(userID, amount).Error(0)
}
func (m *mockNotification) NotifyLargeBalanceChange(_ context.Context, userID uuid.UUID, changeType string, amount decimal.Decimal, newBalance decimal.Decimal) error {
	return m.Called(userID, changeType, amount, newBalance).Error(0)
}
func (m *mockNotification) NotifyEmergencyWithdrawal(_ context.Context, userID uuid.UUID, amount decimal.Decimal, fee decimal.Decimal) error {
	return m.Called(userID, amount, fee).Error(0)
}

type mockCircleTransfer struct {
	mock.Mock
}

func (m *mockCircleTransfer) TransferUSDC(_ context.Context, _, _, _ string, _ string) (*circlepkg.Transaction, error) {
	return nil, fmt.Errorf("not used in these tests")
}
func (m *mockCircleTransfer) GetWalletBalance(_ context.Context, _ string) (string, error) {
	return "0", nil
}
func (m *mockCircleTransfer) FindWalletWithUSDC(_ context.Context, _ string) (string, string, string, string, error) {
	return "", "", "", "", nil
}
func (m *mockCircleTransfer) GetTransaction(_ context.Context, txID string) (*circlepkg.Transaction, error) {
	args := m.Called(txID)
	var tx *circlepkg.Transaction
	if args.Get(0) != nil {
		tx = args.Get(0).(*circlepkg.Transaction)
	}
	return tx, args.Error(1)
}

type mockAdminAlerter struct {
	mock.Mock
}

func (m *mockAdminAlerter) SendErrorAlert(_ context.Context, payload AdminErrorPayload) {
	m.Called(payload)
}

// --- helpers ---

func testLogger() *logger.Logger {
	l, _ := zap.NewDevelopment()
	return logger.NewLogger(l)
}

func testWithdrawal(status entities.WithdrawalStatus) *entities.Withdrawal {
	addr := "0xABCDEF1234567890"
	return &entities.Withdrawal{
		ID:                 uuid.New(),
		UserID:             uuid.New(),
		WithdrawalType:     entities.WithdrawalTypeCrypto,
		Currency:           entities.WithdrawalCurrencyUSDC,
		Amount:             decimal.NewFromInt(50),
		FeeAmount:          decimal.NewFromFloat(0.10),
		SourceAccount:      entities.WithdrawalSourceSpendingBalance,
		DestinationType:    entities.DestinationTypeCryptoWallet,
		DestinationChain:   "SOL",
		DestinationAddress: &addr,
		Status:             status,
		IdempotencyKey:     strPtr("idem-" + uuid.New().String()),
	}
}

func strPtr(s string) *string { return &s }

// =====================================================================
// Tests: withdrawalLedgerIdempotencyKey
// =====================================================================

func TestWithdrawalLedgerIdempotencyKey(t *testing.T) {
	id := uuid.New()
	key := withdrawalLedgerIdempotencyKey(id)
	assert.Equal(t, "withdrawal-ledger-"+id.String(), key)

	// Deterministic — same input gives same output.
	assert.Equal(t, key, withdrawalLedgerIdempotencyKey(id))
}

// =====================================================================
// Tests: createPendingWithdrawalLedgerEntry
// =====================================================================

func TestCreatePendingWithdrawalLedgerEntry_Success(t *testing.T) {
	ledger := new(mockLedger)
	ledger.On("CreatePendingTransaction",
		mock.AnythingOfType("uuid.UUID"),
		entities.AccountTypeSpendingBalance,
		entities.TransactionTypeWithdrawal,
		mock.AnythingOfType("decimal.Decimal"),
		mock.AnythingOfType("string"),
		mock.Anything,
	).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)

	err := svc.createPendingWithdrawalLedgerEntry(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertExpectations(t)
}

func TestCreatePendingWithdrawalLedgerEntry_StashSource(t *testing.T) {
	ledger := new(mockLedger)
	ledger.On("CreatePendingTransaction",
		mock.AnythingOfType("uuid.UUID"),
		entities.AccountTypeStashBalance,
		entities.TransactionTypeWithdrawal,
		mock.AnythingOfType("decimal.Decimal"),
		mock.AnythingOfType("string"),
		mock.Anything,
	).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	w.SourceAccount = entities.WithdrawalSourceStashBalance

	err := svc.createPendingWithdrawalLedgerEntry(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertExpectations(t)
}

func TestCreatePendingWithdrawalLedgerEntry_NilLedger(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)

	err := svc.createPendingWithdrawalLedgerEntry(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ledger service not configured")
}

func TestCreatePendingWithdrawalLedgerEntry_LedgerError(t *testing.T) {
	ledger := new(mockLedger)
	ledger.On("CreatePendingTransaction",
		mock.AnythingOfType("uuid.UUID"), mock.Anything, mock.Anything,
		mock.AnythingOfType("decimal.Decimal"), mock.AnythingOfType("string"),
		mock.Anything,
	).Return(fmt.Errorf("db timeout"))

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)

	err := svc.createPendingWithdrawalLedgerEntry(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db timeout")
}

// =====================================================================
// Tests: commitPendingWithdrawalLedgerEntry
// =====================================================================

func TestCommitPendingWithdrawalLedgerEntry_Success(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	expectedKey := withdrawalLedgerIdempotencyKey(w.ID)
	ledger.On("CommitPendingTransaction", expectedKey).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.commitPendingWithdrawalLedgerEntry(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertCalled(t, "CommitPendingTransaction", expectedKey)
}

func TestCommitPendingWithdrawalLedgerEntry_NilLedger(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	err := svc.commitPendingWithdrawalLedgerEntry(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ledger service not configured")
}

// =====================================================================
// Tests: failPendingWithdrawalLedgerEntry
// =====================================================================

func TestFailPendingWithdrawalLedgerEntry_Success(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	expectedKey := withdrawalLedgerIdempotencyKey(w.ID)
	ledger.On("FailPendingTransaction", expectedKey).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.failPendingWithdrawalLedgerEntry(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertCalled(t, "FailPendingTransaction", expectedKey)
}

func TestFailPendingWithdrawalLedgerEntry_NilLedger_NoOp(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	err := svc.failPendingWithdrawalLedgerEntry(context.Background(), w)
	require.NoError(t, err) // nil ledger → no-op, not an error
}

// =====================================================================
// Tests: handleLedgerCancellation
// =====================================================================

func TestHandleLedgerCancellation_Pending_FailsLedger(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusPending, nil)
	ledger.On("FailPendingTransaction", key).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.handleLedgerCancellation(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertCalled(t, "FailPendingTransaction", key)
}

func TestHandleLedgerCancellation_Completed_Reverses(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusCompleted, nil)
	ledger.On("ReverseTransaction",
		w.UserID, entities.AccountTypeSpendingBalance,
		w.ID.String(), mock.AnythingOfType("decimal.Decimal"),
		mock.Anything,
	).Return(nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.handleLedgerCancellation(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertCalled(t, "ReverseTransaction",
		w.UserID, entities.AccountTypeSpendingBalance,
		w.ID.String(), mock.AnythingOfType("decimal.Decimal"),
		mock.Anything,
	)
}

func TestHandleLedgerCancellation_AlreadyFailed_NoOp(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusFailed, nil)

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.handleLedgerCancellation(context.Background(), w)
	require.NoError(t, err)
	// Neither FailPendingTransaction nor ReverseTransaction should be called
	ledger.AssertNotCalled(t, "FailPendingTransaction", mock.Anything)
	ledger.AssertNotCalled(t, "ReverseTransaction", mock.Anything)
}

func TestHandleLedgerCancellation_NilLedger_NoOp(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	err := svc.handleLedgerCancellation(context.Background(), w)
	require.NoError(t, err)
}

func TestHandleLedgerCancellation_StatusError(t *testing.T) {
	ledger := new(mockLedger)
	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).
		Return(entities.TransactionStatus(""), fmt.Errorf("db connection lost"))

	svc := NewWithdrawalService(nil, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.handleLedgerCancellation(context.Background(), w)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get ledger status")
	assert.Contains(t, err.Error(), "db connection lost")
}

// =====================================================================
// Tests: failWithdrawal
// =====================================================================

func TestFailWithdrawal_NilWithdrawal(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	err := svc.failWithdrawal(context.Background(), nil, "test")
	require.NoError(t, err)
}

func TestFailWithdrawal_TerminalWithdrawal_NoOp(t *testing.T) {
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())

	w := testWithdrawal(entities.WithdrawalStatusCompleted)
	err := svc.failWithdrawal(context.Background(), w, "already done")
	require.NoError(t, err)
	repo.AssertNotCalled(t, "MarkFailed")
}

func TestFailWithdrawal_WithPendingLedger_FailsPendingAndMarksFailed(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
notif := new(mockNotification)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, notif, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusPending, nil)
	ledger.On("FailPendingTransaction", key).Return(nil)
	repo.On("MarkFailed", w.ID, "provider timeout").Return(nil)
	notif.On("NotifyWithdrawalFailed", w.UserID, w.Amount, "provider timeout").Return(nil)

	err := svc.failWithdrawal(context.Background(), w, "provider timeout")
	require.NoError(t, err)
	ledger.AssertCalled(t, "FailPendingTransaction", key)
	repo.AssertCalled(t, "MarkFailed", w.ID, "provider timeout")
	assert.Equal(t, entities.WithdrawalStatusFailed, w.Status)
}

func TestFailWithdrawal_WithCommittedLedger_ReversesAndMarksFailed(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	notif := new(mockNotification)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, notif, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusCompleted, nil)
	ledger.On("ReverseTransaction",
		w.UserID, entities.AccountTypeSpendingBalance,
		w.ID.String(), mock.AnythingOfType("decimal.Decimal"),
		mock.Anything,
	).Return(nil)
	repo.On("MarkFailed", w.ID, "double-spend guard").Return(nil)
	notif.On("NotifyWithdrawalFailed", w.UserID, w.Amount, "double-spend guard").Return(nil)

	err := svc.failWithdrawal(context.Background(), w, "double-spend guard")
	require.NoError(t, err)
	ledger.AssertCalled(t, "ReverseTransaction",
		w.UserID, entities.AccountTypeSpendingBalance,
		w.ID.String(), mock.AnythingOfType("decimal.Decimal"),
		mock.Anything,
	)
	repo.AssertCalled(t, "MarkFailed", w.ID, "double-spend guard")
}

func TestFailWithdrawal_CircleGuardSettlesWhenOnChainConfirmed(t *testing.T) {
	circle := new(mockCircleTransfer)
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	notif := new(mockNotification)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, notif, nil, nil, nil, testLogger())
	svc.circleTransfer = circle
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	txHash := "0xtxhash123"
	w.ProviderTransferID = &txHash

	// Circle says the transfer actually completed on-chain
	circle.On("GetTransaction", txHash).Return(&circlepkg.Transaction{
		State:  "COMPLETED",
		TxHash: "0xonchain",
	}, nil)
	// Should commit ledger (not fail it) because funds actually left
	key := withdrawalLedgerIdempotencyKey(w.ID)
	ledger.On("CommitPendingTransaction", key).Return(nil)
	// settleCompletedCryptoWithdrawal path: re-reads withdrawal, marks completed
	repo.On("GetByID", w.ID).Return(w, nil)
	repo.On("MarkCompleted", w.ID).Return(nil)
	repo.On("UpdateTxHash", w.ID, "0xonchain").Return(nil)

	err := svc.failWithdrawal(context.Background(), w, "polling timeout")
	require.NoError(t, err)
	// Verify: committed ledger (not failed), and withdrawal settled
	ledger.AssertCalled(t, "CommitPendingTransaction", key)
	ledger.AssertNotCalled(t, "FailPendingTransaction", mock.Anything)
	repo.AssertCalled(t, "MarkCompleted", w.ID)
}

func TestFailWithdrawal_MarkFailedError(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusPending, nil)
	ledger.On("FailPendingTransaction", key).Return(nil)
	repo.On("MarkFailed", w.ID, "oops").Return(fmt.Errorf("db write fail"))

	err := svc.failWithdrawal(context.Background(), w, "oops")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db write fail")
}

// =====================================================================
// Tests: alertAdmin
// =====================================================================

func TestAlertAdmin_NilAlerter_NoPanic(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	// Should not panic — nil-safe helper
	svc.alertAdmin(AdminErrorPayload{
		UserID:    "user-123",
		Operation: "test",
		Error:     fmt.Errorf("test error"),
	})
}

func TestAlertAdmin_WithAlerter(t *testing.T) {
	alerter := new(mockAdminAlerter)
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	svc.SetAdminAlerter(alerter)

	payload := AdminErrorPayload{
		UserID:    "user-456",
		Operation: "crypto_withdrawal",
		Error:     fmt.Errorf("panic: nil pointer"),
		PanicStack: []byte("goroutine 1 [running]:\nmain.main()"),
		ExtraFields: map[string]string{
			"withdrawal_id": "abc-123",
		},
	}
	alerter.On("SendErrorAlert", payload).Return()

	svc.alertAdmin(payload)
	alerter.AssertCalled(t, "SendErrorAlert", payload)
}

// =====================================================================
// Tests: SetAdminAlerter
// =====================================================================

func TestSetAdminAlerter(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	assert.Nil(t, svc.adminAlerter)

	alerter := new(mockAdminAlerter)
	svc.SetAdminAlerter(alerter)
	assert.Equal(t, alerter, svc.adminAlerter)
}

// =====================================================================
// Tests: settleCompletedCryptoWithdrawal ledger commit
// =====================================================================

func TestSettleCompletedCryptoWithdrawal_CommitsPendingLedger(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	key := withdrawalLedgerIdempotencyKey(w.ID)

	// Withdrawal is not yet completed → should commit ledger + mark completed
	repo.On("GetByID", w.ID).Return(w, nil)
	ledger.On("CommitPendingTransaction", key).Return(nil)
	repo.On("MarkCompleted", w.ID).Return(nil)

	err := svc.settleCompletedCryptoWithdrawal(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertCalled(t, "CommitPendingTransaction", key)
	repo.AssertCalled(t, "MarkCompleted", w.ID)
}

func TestSettleCompletedCryptoWithdrawal_AlreadyCompleted_NoDoubleCommit(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	completed := *w
	completed.Status = entities.WithdrawalStatusCompleted

	// Re-read returns already-completed → should skip commit and mark
	repo.On("GetByID", w.ID).Return(&completed, nil)

	err := svc.settleCompletedCryptoWithdrawal(context.Background(), w)
	require.NoError(t, err)
	ledger.AssertNotCalled(t, "CommitPendingTransaction", mock.Anything)
	repo.AssertNotCalled(t, "MarkCompleted", mock.Anything)
}

// =====================================================================
// Tests: CompleteWithdrawalByTransferID commits ledger before settling
// =====================================================================

func TestCompleteWithdrawalByTransferID_CommitsPendingLedger(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, nil, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	txID := "transfer-789"
	w.ProviderTransferID = &txID
	key := withdrawalLedgerIdempotencyKey(w.ID)

	repo.On("GetByProviderTransferID", txID).Return(w, nil)
	// settleCompletedCryptoWithdrawal re-reads and finds it not yet completed
	repo.On("GetByID", w.ID).Return(w, nil)
	ledger.On("CommitPendingTransaction", key).Return(nil)
	repo.On("MarkCompleted", w.ID).Return(nil)

	err := svc.CompleteWithdrawalByTransferID(context.Background(), txID)
	require.NoError(t, err)
	ledger.AssertCalled(t, "CommitPendingTransaction", key)
}

func TestCompleteWithdrawalByTransferID_NilWithdrawal_NoOp(t *testing.T) {
	repo := new(mockWithdrawalRepo)
	svc := NewWithdrawalService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())

	repo.On("GetByProviderTransferID", "nonexistent").Return(nil, nil)
	err := svc.CompleteWithdrawalByTransferID(context.Background(), "nonexistent")
	require.NoError(t, err)
}

// =====================================================================
// Tests: FailWithdrawalByTransferID
// =====================================================================

func TestFailWithdrawalByTransferID_UsesFailWithdrawal(t *testing.T) {
	ledger := new(mockLedger)
	repo := new(mockWithdrawalRepo)
	notif := new(mockNotification)
	svc := NewWithdrawalService(repo, nil, ledger, nil, nil, nil, notif, nil, nil, nil, testLogger())
	svc.logger = testLogger()

	w := testWithdrawal(entities.WithdrawalStatusProcessing)
	txID := "transfer-fail-1"
	w.ProviderTransferID = &txID
	key := withdrawalLedgerIdempotencyKey(w.ID)

	repo.On("GetByProviderTransferID", txID).Return(w, nil)
	ledger.On("GetLedgerTransactionStatus", key).Return(entities.TransactionStatusPending, nil)
	ledger.On("FailPendingTransaction", key).Return(nil)
	repo.On("MarkFailed", w.ID, "provider error").Return(nil)
	notif.On("NotifyWithdrawalFailed", w.UserID, w.Amount, "provider error").Return(nil)

	err := svc.FailWithdrawalByTransferID(context.Background(), txID, "provider error")
	require.NoError(t, err)
	ledger.AssertCalled(t, "FailPendingTransaction", key)
	repo.AssertCalled(t, "MarkFailed", w.ID, "provider error")
}

// =====================================================================
// Tests: mapWithdrawalSourceToAccountType
// =====================================================================

func TestMapWithdrawalSourceToAccountType(t *testing.T) {
	tests := []struct {
		input entities.WithdrawalSourceAccount
		want  entities.AccountType
		err   bool
	}{
		{entities.WithdrawalSourceSpendingBalance, entities.AccountTypeSpendingBalance, false},
		{entities.WithdrawalSourceStashBalance, entities.AccountTypeStashBalance, false},
		{"unknown", "", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got, err := mapWithdrawalSourceToAccountType(tt.input)
			if tt.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// =====================================================================
// Tests: resolveWithdrawalRoute
// =====================================================================

func TestResolveWithdrawalRoute(t *testing.T) {
	tests := []struct {
		src, dst, want string
	}{
		{"SOL", "ETH", "cctp"},
		{"ETH", "SOL", "cctp"},
		{"ETH", "MATIC", "direct"},
		{"SOL", "SOL", "direct"},
		{"sol", "eth", "cctp"},
	}
	for _, tt := range tests {
		t.Run(tt.src+"→"+tt.dst, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveWithdrawalRoute(tt.src, tt.dst))
		})
	}
}

// =====================================================================
// Tests: isChainRailsChain
// =====================================================================

func TestIsChainRailsChain(t *testing.T) {
	assert.Equal(t, "STARKNET_MAINNET", isChainRailsChain("STARKNET"))
	assert.Equal(t, "BSC_MAINNET", isChainRailsChain("BSC"))
	assert.Equal(t, "BSC_MAINNET", isChainRailsChain("BNB"))
	assert.Equal(t, "", isChainRailsChain("SOL"))
	assert.Equal(t, "", isChainRailsChain("ETH"))
}

// =====================================================================
// Tests: isSameChainFamily
// =====================================================================

func TestIsSameChainFamily(t *testing.T) {
	assert.True(t, isSameChainFamily("SOL", "SOLANA"))
	assert.True(t, isSameChainFamily("eth", "ETHEREUM"))
	assert.True(t, isSameChainFamily("BASE", "base-sepolia"))
	assert.False(t, isSameChainFamily("SOL", "ETH"))
	assert.False(t, isSameChainFamily("MATIC", "AVAX"))
}

// =====================================================================
// Tests: calculateFiatWithdrawalFee
// =====================================================================

func TestCalculateFiatWithdrawalFee(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	amount := decimal.NewFromInt(100)

	tests := []struct {
		currency entities.WithdrawalCurrency
		want     string
	}{
		{entities.WithdrawalCurrencyUSD, "1"},
		{entities.WithdrawalCurrencyEUR, "1"},
		{entities.WithdrawalCurrencyGBP, "1"},
		{entities.WithdrawalCurrencyNGN, "0.02"},
		{entities.WithdrawalCurrencyUSDC, "1"}, // default → USD
	}
	for _, tt := range tests {
		t.Run(string(tt.currency), func(t *testing.T) {
			fee := svc.calculateFiatWithdrawalFee(amount, tt.currency)
			assert.True(t, fee.Equal(decimal.RequireFromString(tt.want)),
				"got %s, want %s", fee, tt.want)
		})
	}
}

// =====================================================================
// Tests: calculateCryptoWithdrawalFee
// =====================================================================

func TestCalculateCryptoWithdrawalFee(t *testing.T) {
	svc := NewWithdrawalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, testLogger())
	amount := decimal.NewFromInt(100)

	tests := []struct {
		destChain string
		want      string
	}{
		{"SOL", "0.1"},
		{"ETH", "0.1"},
		{"MATIC", "0.1"},
		{"BASE", "0.1"},
		{"", "0.1"}, // empty → SOL default
	}
	for _, tt := range tests {
		t.Run("dest="+tt.destChain, func(t *testing.T) {
			fee, err := svc.calculateCryptoWithdrawalFee(context.Background(), amount, "SOL", tt.destChain)
			require.NoError(t, err)
			assert.True(t, fee.Equal(decimal.RequireFromString(tt.want)))
		})
	}
}
