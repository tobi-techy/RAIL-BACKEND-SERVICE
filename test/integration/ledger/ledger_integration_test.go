//go:build integration
// +build integration

// Ledger integration tests require a running PostgreSQL database at the
// DSN below. Run migrations first: make migrate-up
//
// Usage:
//
//	go test -tags=integration -run TestLedger ./test/integration/ledger/ -v -count=1

package ledger_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	ledgersvc "github.com/rail-service/rail_service/internal/domain/services/ledger"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

const ledgerTestDSN = "postgres://test:test@localhost:5432/stack_test?sslmode=disable"

// --- helpers ---------------------------------------------------------------

func newLedgerService(t *testing.T) (*sqlx.DB, *ledgersvc.Service) {
	t.Helper()
	db, err := sqlx.Connect("postgres", ledgerTestDSN)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	// Clean ledger tables for test isolation
	_, _ = db.Exec(`TRUNCATE ledger_velocity_buckets, ledger_outbox, ledger_balance_snapshots, ledger_entries, ledger_transactions, ledger_accounts CASCADE`)

	log := logger.New("debug", "test")
	repo := repositories.NewLedgerRepository(db)
	outbox := ledgersvc.NewOutboxWriter(repo)
	svc := ledgersvc.NewService(repo, db, log, outbox)
	return db, svc
}

func uniqueUserID() uuid.UUID {
	return uuid.Must(uuid.NewRandom())
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("%s-%s-%d", prefix, uuid.NewString(), time.Now().UnixNano())
}

func strPtr(s string) *string { return &s }

func seedBalance(ctx context.Context, t *testing.T, db *sqlx.DB, accountID uuid.UUID, amount decimal.Decimal) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`UPDATE ledger_accounts SET balance = $1, updated_at = NOW() WHERE id = $2`,
		amount, accountID)
	require.NoError(t, err)
}

func insertTestUser(ctx context.Context, t *testing.T, db *sqlx.DB, userID uuid.UUID) {
	t.Helper()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, onboarding_status) VALUES ($1, $2, 'started') ON CONFLICT (id) DO NOTHING`,
		userID, fmt.Sprintf("%s@test.rail", userID.String()))
	require.NoError(t, err)
}

func createAndSeed(ctx context.Context, t *testing.T, svc *ledgersvc.Service, db *sqlx.DB, userID uuid.UUID, acctType entities.AccountType, initialBalance decimal.Decimal) *entities.LedgerAccount {
	t.Helper()
	insertTestUser(ctx, t, db, userID)
	acct, err := svc.GetOrCreateUserAccount(ctx, userID, acctType)
	require.NoError(t, err)
	if initialBalance.IsPositive() {
		seedBalance(ctx, t, db, acct.ID, initialBalance)
	}
	return acct
}

func cleanupLedgerData(ctx context.Context, t *testing.T, db *sqlx.DB, userID uuid.UUID) {
	t.Helper()
	db.MustExecContext(ctx, `DELETE FROM ledger_balance_snapshots WHERE account_id IN (SELECT id FROM ledger_accounts WHERE user_id = $1)`, userID)
	db.MustExecContext(ctx, `DELETE FROM ledger_velocity_buckets WHERE account_id IN (SELECT id FROM ledger_accounts WHERE user_id = $1)`, userID)
	db.MustExecContext(ctx, `DELETE FROM ledger_entries WHERE transaction_id IN (SELECT id FROM ledger_transactions WHERE user_id = $1)`, userID)
	db.MustExecContext(ctx, `DELETE FROM ledger_entries WHERE account_id IN (SELECT id FROM ledger_accounts WHERE user_id = $1)`, userID)
	db.MustExecContext(ctx, `DELETE FROM ledger_transactions WHERE user_id = $1`, userID)
	db.MustExecContext(ctx, `DELETE FROM ledger_accounts WHERE user_id = $1`, userID)
	db.MustExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
}

type mockStashLockChecker struct {
	canWithdraw bool
	nextWindow  time.Time
}

func (m *mockStashLockChecker) CanWithdraw(_ context.Context, _ uuid.UUID) (bool, time.Time, error) {
	return m.canWithdraw, m.nextWindow, nil
}

// --- P0: Core double-entry integrity ---------------------------------------

func TestLedger_CreateTransaction_BasicDebitCredit(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	desc := "basic transfer"
	tx, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("basic"),
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, entities.TransactionStatusCompleted, tx.Status)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(70).Equal(spendBal), "spending: expected 70, got %s", spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(30).Equal(stashBal), "stash: expected 30, got %s", stashBal)
}

func TestLedger_CreateTransaction_Idempotency(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	key := uniqueKey("idempotency")
	desc := "idempotent transfer"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  key,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	}

	tx1, err := svc.CreateTransaction(ctx, req)
	require.NoError(t, err)

	tx2, err := svc.CreateTransaction(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, tx1.ID, tx2.ID, "same idempotency key must return same transaction")

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(70).Equal(spendBal), "spending: expected 70, got %s (double debit = idempotency broken)", spendBal)

	var count int
	err = db.GetContext(ctx, &count, `SELECT COUNT(*) FROM ledger_transactions WHERE idempotency_key = $1`, key)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "exactly one row for the idempotency key")
}

func TestLedger_CreateTransaction_UnbalancedEntries_Rejected(t *testing.T) {
	_, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()

	desc := "unbalanced"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("unbalanced"),
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: uuid.New(), EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(100), Currency: "USD"},
			{AccountID: uuid.New(), EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(50), Currency: "USD"},
		},
	}

	_, err := svc.CreateTransaction(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unbalanced")
}

func TestLedger_CreateTransaction_InvalidRequest_Rejected(t *testing.T) {
	_, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()

	tests := []struct {
		name string
		req  *entities.CreateTransactionRequest
	}{
		{
			name: "missing idempotency key",
			req: &entities.CreateTransactionRequest{
				UserID:          &userID,
				TransactionType: entities.TransactionTypeInternalTransfer,
				Entries: []entities.CreateEntryRequest{
					{AccountID: uuid.New(), EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
					{AccountID: uuid.New(), EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
				},
			},
		},
		{
			name: "less than 2 entries",
			req: &entities.CreateTransactionRequest{
				UserID:          &userID,
				TransactionType: entities.TransactionTypeInternalTransfer,
				IdempotencyKey:  uniqueKey("single-entry"),
				Entries: []entities.CreateEntryRequest{
					{AccountID: uuid.New(), EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
				},
			},
		},
		{
			name: "invalid currency",
			req: &entities.CreateTransactionRequest{
				UserID:          &userID,
				TransactionType: entities.TransactionTypeInternalTransfer,
				IdempotencyKey:  uniqueKey("bad-currency"),
				Entries: []entities.CreateEntryRequest{
					{AccountID: uuid.New(), EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "EUR"},
					{AccountID: uuid.New(), EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "EUR"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.CreateTransaction(ctx, tt.req)
			require.Error(t, err)
		})
	}
}

// --- P1: Stash transfers ---------------------------------------------------

func TestLedger_TransferSpendingToStash(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(200))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	err := svc.TransferSpendingToStash(ctx, userID, decimal.NewFromInt(50), uniqueKey("spend-to-stash"))
	require.NoError(t, err)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(150).Equal(spendBal), "spending: expected 150, got %s", spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(50).Equal(stashBal), "stash: expected 50, got %s", stashBal)
}

func TestLedger_TransferStashToSpending_WithLockChecker(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	svc.SetStashLockChecker(&mockStashLockChecker{canWithdraw: true})

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.Zero)
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.NewFromInt(100))

	err := svc.TransferStashToSpending(ctx, userID, decimal.NewFromInt(30), uniqueKey("stash-to-spend-allowed"))
	require.NoError(t, err)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(70).Equal(stashBal), "stash: expected 70, got %s", stashBal)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(30).Equal(spendBal), "spending: expected 30, got %s", spendBal)
}

func TestLedger_TransferStashToSpending_LockEnforced(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	svc.SetStashLockChecker(&mockStashLockChecker{canWithdraw: false})

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.Zero)
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.NewFromInt(100))

	err := svc.TransferStashToSpending(ctx, userID, decimal.NewFromInt(30), uniqueKey("stash-to-spend-denied"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locked")

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(stashBal), "stash unchanged: expected 100, got %s", stashBal)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(spendBal), "spending unchanged: expected 0, got %s", spendBal)
}

// --- P1: Pending transaction lifecycle -------------------------------------

func TestLedger_PendingTransactionLifecycle_CreateAndCommit(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	key := uniqueKey("pending-commit")
	desc := "pending then commit"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  key,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(25), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(25), Currency: "USD"},
		},
	}

	err := svc.CreatePendingTransaction(ctx, req)
	require.NoError(t, err)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(spendBal), "spending unchanged after pending: expected 100, got %s", spendBal)

	err = svc.CommitPendingTransaction(ctx, key)
	require.NoError(t, err)

	spendBal, err = svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(75).Equal(spendBal), "spending after commit: expected 75, got %s", spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(25).Equal(stashBal), "stash after commit: expected 25, got %s", stashBal)

	status, err := svc.GetLedgerTransactionStatus(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, entities.TransactionStatusCompleted, status)
}

func TestLedger_PendingTransactionLifecycle_CreateAndFail(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	key := uniqueKey("pending-fail")
	desc := "pending then fail"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  key,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(40), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(40), Currency: "USD"},
		},
	}

	err := svc.CreatePendingTransaction(ctx, req)
	require.NoError(t, err)

	err = svc.FailPendingTransaction(ctx, key)
	require.NoError(t, err)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(spendBal), "spending unchanged after fail: expected 100, got %s", spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(stashBal), "stash unchanged after fail: expected 0, got %s", stashBal)

	status, err := svc.GetLedgerTransactionStatus(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, entities.TransactionStatusFailed, status)
}

func TestLedger_PendingTransactionLifecycle_DoubleCommit(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	key := uniqueKey("double-commit")
	desc := "commit twice"
	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  key,
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(20), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(20), Currency: "USD"},
		},
	}

	require.NoError(t, svc.CreatePendingTransaction(ctx, req))
	require.NoError(t, svc.CommitPendingTransaction(ctx, key))

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(80).Equal(spendBal), "after first commit: expected 80, got %s", spendBal)

	require.NoError(t, svc.CommitPendingTransaction(ctx, key))

	spendBal2, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(80).Equal(spendBal2), "after second commit: expected 80, got %s (double debit)", spendBal2)
}

// --- P1: Reversal ----------------------------------------------------------

func TestLedger_ReverseTransaction(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// Create original transaction: debit spending $40, credit stash
	desc := "original transfer"
	originalTx, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("original"),
		Description:     &desc,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(40), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(40), Currency: "USD"},
		},
	})
	require.NoError(t, err)

	spendBal, _ := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	assert.True(t, decimal.NewFromInt(60).Equal(spendBal), "after original: spending=60, got %s", spendBal)
	stashBal, _ := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	assert.True(t, decimal.NewFromInt(40).Equal(stashBal), "after original: stash=40, got %s", stashBal)

	// Reverse
	err = svc.ReverseTransaction(ctx, originalTx.ID, "test reversal")
	require.NoError(t, err)

	spendBal, err = svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(spendBal), "after reversal: spending=100, got %s", spendBal)

	stashBal, err = svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(stashBal), "after reversal: stash=0, got %s", stashBal)

	// Original tx should be marked reversed
	tx, err := svc.GetTransactionByIdempotencyKey(ctx, originalTx.IdempotencyKey)
	require.NoError(t, err)
	assert.Equal(t, entities.TransactionStatusReversed, tx.Status)
}

// --- P2: Yield distribution ------------------------------------------------

func TestLedger_CreditYieldToSavings(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Seed stash with $1000
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.NewFromInt(1000))

	// Create two goal accounts and seed them with $500 each
	goalID1 := uuid.New()
	goalID2 := uuid.New()
	goalAcct1, err := svc.GetOrCreateGoalAccount(ctx, userID, goalID1)
	require.NoError(t, err)
	seedBalance(ctx, t, db, goalAcct1.ID, decimal.NewFromInt(500))

	goalAcct2, err := svc.GetOrCreateGoalAccount(ctx, userID, goalID2)
	require.NoError(t, err)
	seedBalance(ctx, t, db, goalAcct2.ID, decimal.NewFromInt(500))

	// Ensure system buffer exists (seeded by migrations)
	_, err = svc.GetSystemAccount(ctx, entities.AccountTypeSystemBufferUSDC)
	if err != nil {
		t.Skip("system_buffer_usdc account not found — run migrations first")
	}

	// Distribute $100 yield
	distributionID := uuid.New().String()
	err = svc.CreditYieldToSavings(ctx, userID, decimal.NewFromInt(100), distributionID)
	require.NoError(t, err)

	// Stash share: 1000/2000 * $100 = $50
	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(1050).Equal(stashBal), "stash: expected 1050, got %s", stashBal)

	// Goal1 share: 500/2000 * $100 = $25
	g1Bal, err := svc.GetGoalBalance(ctx, userID, goalID1)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(525).Equal(g1Bal), "goal1: expected 525, got %s", g1Bal)

	// Goal2 share: 500/2000 * $100 = $25
	g2Bal, err := svc.GetGoalBalance(ctx, userID, goalID2)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(525).Equal(g2Bal), "goal2: expected 525, got %s", g2Bal)

	// Verify idempotency — calling again should be a no-op
	err = svc.CreditYieldToSavings(ctx, userID, decimal.NewFromInt(100), distributionID)
	require.NoError(t, err)

	stashBal2, _ := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	assert.True(t, decimal.NewFromInt(1050).Equal(stashBal2), "stash after idempotent yield: expected 1050, got %s", stashBal2)
}

func TestLedger_CreditYieldToSavings_ZeroYieldNoOp(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.NewFromInt(500))

	err := svc.CreditYieldToSavings(ctx, userID, decimal.Zero, uuid.New().String())
	require.NoError(t, err)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(500).Equal(stashBal), "stash unchanged: expected 500, got %s", stashBal)
}

// --- P2: Investment reservation --------------------------------------------

func TestLedger_ReserveAndReleaseInvestment(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeUSDCBalance, decimal.NewFromInt(500))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypePendingInvestment, decimal.Zero)

	// Reserve $200
	err := svc.ReserveForInvestment(ctx, userID, decimal.NewFromInt(200))
	require.NoError(t, err)

	usdcBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeUSDCBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(300).Equal(usdcBal), "USDC after reserve: expected 300, got %s", usdcBal)

	pendingBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypePendingInvestment)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(200).Equal(pendingBal), "pending after reserve: expected 200, got %s", pendingBal)

	// Release $100
	err = svc.ReleaseReservation(ctx, userID, decimal.NewFromInt(100))
	require.NoError(t, err)

	usdcBal, err = svc.GetAccountBalance(ctx, userID, entities.AccountTypeUSDCBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(400).Equal(usdcBal), "USDC after release: expected 400, got %s", usdcBal)

	pendingBal, err = svc.GetAccountBalance(ctx, userID, entities.AccountTypePendingInvestment)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(100).Equal(pendingBal), "pending after release: expected 100, got %s", pendingBal)
}

func TestLedger_ReserveForInvestment_InsufficientBalance(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeUSDCBalance, decimal.NewFromInt(50))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypePendingInvestment, decimal.Zero)

	// Trying to reserve $200 with only $50 should fail
	err := svc.ReserveForInvestment(ctx, userID, decimal.NewFromInt(200))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient balance")
}

// --- P2: Concurrency -------------------------------------------------------

func TestLedger_ConcurrentTransfers(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Seed enough balance: 10 transfers * $50 each = $500
	const numGoroutines = 10
	const transferAmount = 50
	const seedAmount = 1000

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(seedAmount))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := uniqueKey(fmt.Sprintf("concurrent-%d", idx))
			errs <- svc.TransferSpendingToStash(ctx, userID, decimal.NewFromInt(transferAmount), key)
		}(i)
	}
	wg.Wait()
	close(errs)

	var failures int
	for err := range errs {
		if err != nil {
			failures++
			t.Logf("concurrent transfer error: %v", err)
		}
	}
	// All should succeed with sufficient balance and SELECT FOR UPDATE serialization
	assert.Zero(t, failures, "all %d concurrent transfers should succeed", numGoroutines)

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	expectedSpend := decimal.NewFromInt(seedAmount - numGoroutines*transferAmount)
	assert.True(t, expectedSpend.Equal(spendBal), "spending: expected %s, got %s", expectedSpend, spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	expectedStash := decimal.NewFromInt(numGoroutines * transferAmount)
	assert.True(t, expectedStash.Equal(stashBal), "stash: expected %s, got %s", expectedStash, stashBal)
}

func TestLedger_ConcurrentTransfers_OverdraftProtected(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Only enough for 5 of 10 goroutines — the rest should fail with insufficient balance
	const numGoroutines = 10
	const transferAmount = 50
	const seedAmount = 250

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(seedAmount))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := uniqueKey(fmt.Sprintf("overdraft-%d", idx))
			errs <- svc.TransferSpendingToStash(ctx, userID, decimal.NewFromInt(transferAmount), key)
		}(i)
	}
	wg.Wait()
	close(errs)

	var successes int
	for err := range errs {
		if err == nil {
			successes++
		}
	}

	assert.Equal(t, 5, successes, "exactly 5 transfers should succeed (SELECT FOR UPDATE + serialization)")

	spendBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(spendBal), "spending should be fully consumed, got %s", spendBal)

	stashBal, err := svc.GetAccountBalance(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(250).Equal(stashBal), "stash should be 250, got %s", stashBal)
}

// --- Additional coverage: GetUserBalances ----------------------------------

func TestLedger_GetUserBalances(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.NewFromInt(200))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeUSDCBalance, decimal.NewFromInt(300))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeFiatExposure, decimal.NewFromInt(400))
	createAndSeed(ctx, t, svc, db, userID, entities.AccountTypePendingInvestment, decimal.NewFromInt(500))

	goalID := uuid.New()
	goalAcct, err := svc.GetOrCreateGoalAccount(ctx, userID, goalID)
	require.NoError(t, err)
	seedBalance(ctx, t, db, goalAcct.ID, decimal.NewFromInt(50))

	balances, err := svc.GetUserBalances(ctx, userID)
	require.NoError(t, err)

	assert.True(t, decimal.NewFromInt(100).Equal(balances.SpendingBalance), "spending: expected 100, got %s", balances.SpendingBalance)
	assert.True(t, decimal.NewFromInt(200).Equal(balances.StashBalance), "stash: expected 200, got %s", balances.StashBalance)
	assert.True(t, decimal.NewFromInt(300).Equal(balances.USDCBalance), "USDC: expected 300, got %s", balances.USDCBalance)
	assert.True(t, decimal.NewFromInt(400).Equal(balances.FiatExposure), "fiat: expected 400, got %s", balances.FiatExposure)
	assert.True(t, decimal.NewFromInt(500).Equal(balances.PendingInvestment), "pending: expected 500, got %s", balances.PendingInvestment)
	assert.True(t, decimal.NewFromInt(50).Equal(balances.GoalBalance), "goal: expected 50, got %s", balances.GoalBalance)

	expectedTotal := decimal.NewFromInt(100 + 200 + 300 + 400 + 500)
	assert.True(t, expectedTotal.Equal(balances.TotalUSDEquivalent), "total: expected %s, got %s", expectedTotal, balances.TotalUSDEquivalent)
}

// --- Additional coverage: edge cases on basic operations -------------------

func TestLedger_TransferSpendingToStash_InvalidAmount(t *testing.T) {
	_, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()

	err := svc.TransferSpendingToStash(ctx, userID, decimal.Zero, uniqueKey("zero"))
	assert.ErrorContains(t, err, "invalid transfer amount")

	err = svc.TransferSpendingToStash(ctx, userID, decimal.NewFromInt(-10), uniqueKey("negative"))
	assert.ErrorContains(t, err, "invalid transfer amount")
}

func TestLedger_TransferStashToSpending_InvalidAmount(t *testing.T) {
	_, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()

	err := svc.TransferStashToSpending(ctx, userID, decimal.Zero, uniqueKey("zero"))
	assert.ErrorContains(t, err, "invalid transfer amount")
}

// --- Outbox tests -----------------------------------------------------------

func TestLedger_Outbox_TransactionWritesOutboxEvent(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	amount := decimal.NewFromInt(100)

	insertTestUser(ctx, t, db, userID)
	spendAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, spendAcct.ID, amount)

	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("outbox_test"),
		IdempotencyKey:  uniqueKey("outbox"),
		Description:     strPtr("outbox test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	}

	_, err = svc.CreateTransaction(ctx, req)
	require.NoError(t, err)

	events, err := db.DB.QueryContext(ctx,
		`SELECT event_type FROM ledger_outbox WHERE aggregate_type = 'ledger_transaction' ORDER BY created_at DESC LIMIT 1`)
	require.NoError(t, err)
	defer events.Close()
	require.True(t, events.Next(), "expected at least one outbox event")
	var eventType string
	require.NoError(t, events.Scan(&eventType))
	assert.Equal(t, string(ledgersvc.EventTransactionCompleted), eventType)
}

// --- Snapshot tests ---------------------------------------------------------

func TestLedger_Snapshot_RecordAndVerify(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)

	acct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, acct.ID, decimal.NewFromInt(250))

	count, err := svc.RecordDailySnapshots(ctx, time.Now())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1)

	var snapshotCount int
	err = db.GetContext(ctx, &snapshotCount,
		`SELECT COUNT(*) FROM ledger_balance_snapshots WHERE account_id = $1 AND snapshot_date = CURRENT_DATE`, acct.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, snapshotCount)
}

func TestLedger_Snapshot_Idempotent(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)

	acct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, acct.ID, decimal.NewFromInt(100))

	count1, err := svc.RecordDailySnapshots(ctx, time.Now())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count1, 1)

	count2, err := svc.RecordDailySnapshots(ctx, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, count2, "second call should be a no-op")
}

// --- Integrity check tests --------------------------------------------------

func TestLedger_IntegrityCheck_CleanLedger(t *testing.T) {
	_, svc := newLedgerService(t)
	ctx := context.Background()

	report := svc.CheckIntegrity(ctx)
	require.NotNil(t, report)
	assert.True(t, report.Balanced, "debits should always equal credits in a correctly kept ledger")
}

func TestLedger_IntegrityCheck_UnbalancedFails(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()

	// Create a transaction with only a credit entry (no matching debit) to unbalance the ledger
	dummyUserID := uuid.Must(uuid.NewRandom())
	insertTestUser(ctx, t, db, dummyUserID)
	acct, err := svc.GetOrCreateUserAccount(ctx, dummyUserID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	txID := uuid.Must(uuid.NewRandom())
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_transactions (id, transaction_type, status, idempotency_key, created_at)
		 VALUES ($1, 'internal_transfer', 'completed', $2, NOW())`, txID, uniqueKey("unbal"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, created_at)
		 VALUES (gen_random_uuid(), $1, $2, 'credit', 1, 'USD', NOW())`, txID, acct.ID)
	require.NoError(t, err)

	report := svc.CheckIntegrity(ctx)
	require.NotNil(t, report)
	assert.False(t, report.Balanced, "manipulated ledger should be unbalanced")
	assert.NotEmpty(t, report.Errors, "should report errors on corrupted ledger")
}

func TestLedger_IntegrityCheck_DetectsNegativeBalance(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()

	// System accounts allow negative balance (for overdraft tracking).
	// Insert one with a deficit to verify the integrity check catches it.
	userID := uniqueUserID()
	insertTestUser(ctx, t, db, userID)
	acctID := uuid.Must(uuid.NewRandom())
	_, err := db.ExecContext(ctx,
		`INSERT INTO ledger_accounts (id, user_id, account_type, currency, balance, created_at, updated_at)
		 VALUES ($1, $2, 'system_buffer_fiat', 'USD', -50000, NOW(), NOW())`, acctID, userID)
	require.NoError(t, err)

	report := svc.CheckIntegrity(ctx)
	require.NotNil(t, report)
	assert.GreaterOrEqual(t, report.NegativeBalanceAccounts, 1)
}

func TestLedger_Outbox_BalanceUpdatedEventWritten(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	amount := decimal.NewFromInt(75)

	insertTestUser(ctx, t, db, userID)
	spendAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, spendAcct.ID, amount)

	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("bal_evt_test"),
		IdempotencyKey:  uniqueKey("bal_evt"),
		Description:     strPtr("balance event test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	}

	_, err = svc.CreateTransaction(ctx, req)
	require.NoError(t, err)

	// Verify balance.updated events were written for both accounts
	var balEventCount int
	err = db.GetContext(ctx, &balEventCount,
		`SELECT COUNT(*) FROM ledger_outbox WHERE event_type = 'balance.updated'`)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, balEventCount, 2, "expected >=2 balance.updated events (one per account)")
}

func TestLedger_Outbox_IncrementRetry(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	amount := decimal.NewFromInt(50)

	insertTestUser(ctx, t, db, userID)
	spendAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, spendAcct.ID, amount)
	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	req := &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("retry_test"),
		IdempotencyKey:  uniqueKey("retry"),
		Description:     strPtr("retry test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	}

	_, err = svc.CreateTransaction(ctx, req)
	require.NoError(t, err)

	// Find the transaction outbox event
	var outboxID uuid.UUID
	err = db.GetContext(ctx, &outboxID,
		`SELECT id FROM ledger_outbox WHERE event_type = 'transaction.completed' LIMIT 1`)
	require.NoError(t, err)

	// Simulate a failed publish by calling the DB directly
	_, err = db.ExecContext(ctx,
		`UPDATE ledger_outbox SET retry_count = retry_count + 1, last_error = $2, published_at = NULL WHERE id = $1`,
		outboxID, "test error")
	require.NoError(t, err)

	// Verify retry was tracked
	var retryCount int
	err = db.GetContext(ctx, &retryCount,
		`SELECT retry_count FROM ledger_outbox WHERE id = $1`, outboxID)
	require.NoError(t, err)
	assert.Equal(t, 1, retryCount)

	// published_at should have been cleared by the retry
	var publishedAt *time.Time
	err = db.GetContext(ctx, &publishedAt,
		`SELECT published_at FROM ledger_outbox WHERE id = $1`, outboxID)
	require.NoError(t, err)
	assert.Nil(t, publishedAt, "published_at should be NULL after retry")

	// last_error should be set
	var lastErr string
	err = db.GetContext(ctx, &lastErr,
		`SELECT COALESCE(last_error, '') FROM ledger_outbox WHERE id = $1`, outboxID)
	require.NoError(t, err)
	assert.Contains(t, lastErr, "test error")
}

// Regression: MarkOutboxPublished used to pass []uuid.UUID straight to lib/pq,
// which rejects it as "a slice of array", so published_at was never set and the
// publisher re-dispatched the same events forever.
func TestLedger_Outbox_MarkPublishedSetsTimestamp(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	repo := repositories.NewLedgerRepository(db)
	userID := uniqueUserID()
	amount := decimal.NewFromInt(40)

	spendAcct := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, amount)
	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("mark_published_test"),
		IdempotencyKey:  uniqueKey("mark_published"),
		Description:     strPtr("mark published test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	})
	require.NoError(t, err)

	pending, err := repo.GetUnpublishedOutboxEvents(ctx, 100)
	require.NoError(t, err)
	require.NotEmpty(t, pending, "transaction should have written outbox events")

	ids := make([]uuid.UUID, 0, len(pending))
	for _, rec := range pending {
		ids = append(ids, rec.ID)
	}

	require.NoError(t, repo.MarkOutboxPublished(ctx, ids))

	remaining, err := repo.CountUnpublishedOutbox(ctx)
	require.NoError(t, err)
	assert.Zero(t, remaining, "every marked event must have published_at set")

	// Empty input must be a no-op rather than a malformed query.
	require.NoError(t, repo.MarkOutboxPublished(ctx, nil))
}

func TestLedger_Outbox_ClaimIsExclusive(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	repo := repositories.NewLedgerRepository(db)
	userID := uniqueUserID()
	amount := decimal.NewFromInt(30)

	spendAcct := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, amount)
	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("claim_test"),
		IdempotencyKey:  uniqueKey("claim"),
		Description:     strPtr("claim test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	})
	require.NoError(t, err)

	claimOnce := func() []repositories.OutboxRecord {
		txCtx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		claimed, err := repo.ClaimUnpublishedOutbox(txCtx, 100, 10)
		require.NoError(t, err)
		require.NoError(t, repo.CommitTx(txCtx))
		return claimed
	}

	first := claimOnce()
	require.NotEmpty(t, first, "first claim should reserve the pending batch")

	second := claimOnce()
	assert.Empty(t, second, "claimed events must not be handed out twice")

	// A dispatch failure returns the event to the queue for a later tick.
	require.NoError(t, repo.IncrementOutboxRetry(ctx, first[0].ID, "dispatch failed"))
	third := claimOnce()
	require.Len(t, third, 1)
	assert.Equal(t, first[0].ID, third[0].ID)
	assert.Equal(t, 1, third[0].RetryCount)
}

func TestLedger_Outbox_ClaimSkipsExhaustedRetries(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	repo := repositories.NewLedgerRepository(db)
	userID := uniqueUserID()
	amount := decimal.NewFromInt(20)

	spendAcct := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, amount)
	stashAcct, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeStashBalance)
	require.NoError(t, err)

	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		ReferenceType:   strPtr("exhausted_test"),
		IdempotencyKey:  uniqueKey("exhausted"),
		Description:     strPtr("exhausted retries test"),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spendAcct.ID, EntryType: entities.EntryTypeCredit, Amount: amount, Currency: "USD"},
			{AccountID: stashAcct.ID, EntryType: entities.EntryTypeDebit, Amount: amount, Currency: "USD"},
		},
	})
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE ledger_outbox SET retry_count = 10 WHERE published_at IS NULL`)
	require.NoError(t, err)

	txCtx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	claimed, err := repo.ClaimUnpublishedOutbox(txCtx, 100, 10)
	require.NoError(t, err)
	require.NoError(t, repo.CommitTx(txCtx))

	assert.Empty(t, claimed, "events at the retry ceiling are dead-lettered, not re-claimed")
}

// --- Security Layer Tests: Hash Chain, Audit Trail, Velocity -----------------

func TestLedger_HashChain_ComputedOnCreate(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// First transaction
	desc := "first tx"
	tx1, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("hash-chain-1"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, tx1.TransactionHash, "first tx must have a hash")
	assert.Empty(t, tx1.PreviousTransactionHash, "first tx must have empty previous hash")

	// Second transaction — should link to first
	tx2, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("hash-chain-2"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(5), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(5), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, tx1.TransactionHash, tx2.PreviousTransactionHash,
		"second tx must link to first tx hash")
	assert.NotEmpty(t, tx2.TransactionHash, "second tx must have a hash")
	assert.NotEqual(t, tx1.TransactionHash, tx2.TransactionHash,
		"different transactions must have different hashes")
}

func TestLedger_HashChain_VerificationDetectsBreak(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// Create a few transactions
	for i := 0; i < 3; i++ {
		_, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
			UserID:          &userID,
			TransactionType: entities.TransactionTypeInternalTransfer,
			IdempotencyKey:  uniqueKey(fmt.Sprintf("hash-verify-%d", i)),
			InitiatedBy:     entities.InitiatedByUser.String(),
			Entries: []entities.CreateEntryRequest{
				{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
				{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			},
		})
		require.NoError(t, err)
	}

	// Tamper with the second transaction's hash in the database
	// Use a subquery to pick the second-oldest tx for this user
	_, err := db.ExecContext(ctx,
		`UPDATE ledger_transactions SET transaction_hash = 'tampered'
		 WHERE id = (
		     SELECT id FROM ledger_transactions WHERE user_id = $1
		     ORDER BY created_at LIMIT 1 OFFSET 1
		 )`, userID)
	require.NoError(t, err)

	// Verify hash chain detects the break (the tx after the tampered one
	// has previous_transaction_hash that doesn't match "tampered").
	broken, err := svc.VerifyHashChain(ctx, 100)
	require.NoError(t, err)
	assert.Len(t, broken, 1, "subsequent tx after tampered one should be broken")
}

func TestLedger_AuditTrail_InitiatedByDefault(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	desc := "default initiated_by"
	tx1, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("audit-default"),
		Description:     &desc,
		// InitiatedBy omitted — should default to "system"
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, entities.InitiatedBySystem.String(), tx1.InitiatedBy)
}

func TestLedger_AuditTrail_InitiatedByCustom(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	reason := "user wanted to save"
	desc := "custom initiated_by"
	tx1, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("audit-custom"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Reason:          &reason,
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, entities.InitiatedByUser.String(), tx1.InitiatedBy)
	require.NotNil(t, tx1.Reason)
	assert.Equal(t, reason, *tx1.Reason)

	// Verify stored in DB
	var storedInitiatedBy string
	err = db.GetContext(ctx, &storedInitiatedBy,
		`SELECT initiated_by FROM ledger_transactions WHERE id = $1`, tx1.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.InitiatedByUser.String(), storedInitiatedBy)

	var storedReason string
	err = db.GetContext(ctx, &storedReason,
		`SELECT COALESCE(reason, '') FROM ledger_transactions WHERE id = $1`, tx1.ID)
	require.NoError(t, err)
	assert.Equal(t, reason, storedReason)
}

func TestLedger_VelocityLimit_BlocksExcessiveOutflow(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Set a very low velocity limit for testing
	svc.SetVelocityConfig(&entities.VelocityConfig{
		MaxDailyOutflow: decimal.NewFromInt(50),
		MaxDailyTxCount: 0, // no tx count limit
	})

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(1000))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// First transfer: $30 — should succeed
	desc := "under limit"
	_, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("velocity-1"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	})
	require.NoError(t, err)

	// Second transfer: $30 — total would be $60 > $50 limit
	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("velocity-2"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocity limit")
}

func TestLedger_VelocityLimit_BlocksExcessiveTxCount(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Set zero outflow limit (disabled) and max 1 tx per day
	svc.SetVelocityConfig(&entities.VelocityConfig{
		MaxDailyOutflow: decimal.Zero, // disabled
		MaxDailyTxCount: 1,
	})

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(1000))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	desc := "tx count test"
	// First tx — should succeed
	_, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("tx-count-1"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.NoError(t, err)

	// Second tx — should be blocked
	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("tx-count-2"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocity limit")
}

func TestLedger_VelocityLimit_BlocksOnUserAccounts(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	// Set very restrictive limit
	svc.SetVelocityConfig(&entities.VelocityConfig{
		MaxDailyOutflow: decimal.NewFromInt(10),
		MaxDailyTxCount: 1,
	})

	desc := "velocity check works on user accounts"

	// Create two user accounts so we can transfer between them
	insertTestUser(ctx, t, db, userID)
	spend, err := svc.GetOrCreateUserAccount(ctx, userID, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)
	seedBalance(ctx, t, db, spend.ID, decimal.NewFromInt(100))

	userID2 := uniqueUserID()
	insertTestUser(ctx, t, db, userID2)
	spend2, err := svc.GetOrCreateUserAccount(ctx, userID2, entities.AccountTypeSpendingBalance)
	require.NoError(t, err)

	// This transfer debits spend (user account) — should succeed under limit
	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("velo-sys-1"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(5), Currency: "USD"},
			{AccountID: spend2.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(5), Currency: "USD"},
		},
	})
	require.NoError(t, err)

	// Second transfer exceeding the limit
	_, err = svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("velo-sys-2"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(10), Currency: "USD"},
			{AccountID: spend2.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(10), Currency: "USD"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocity limit")
}

func TestLedger_ReconcileDay_Accurate(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	now := time.Now()
	today := now.Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// Record opening snapshots (balances before today's activity)
	_, err := db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		spend.ID, decimal.NewFromInt(100), yesterday)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		stash.ID, decimal.Zero, yesterday)
	require.NoError(t, err)

	// Execute a transfer: spend → stash $30
	desc := "reconciliation test transfer"
	tx, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("reconcile-acc"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, tx)

	// Record closing snapshots (balances after the transfer)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		spend.ID, decimal.NewFromInt(70), today)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		stash.ID, decimal.NewFromInt(30), today)
	require.NoError(t, err)

	// Reconcile — should pass since snapshots match transaction deltas
	checked, failures, errs, err := svc.ReconcileDay(ctx, today)
	require.NoError(t, err)
	assert.Equal(t, 2, checked, "both accounts should be checked")
	assert.Empty(t, failures, "no reconciliation failures expected: %v", errs)
	assert.Empty(t, errs)
}

func TestLedger_ReconcileDay_DetectsCorruption(t *testing.T) {
	db, svc := newLedgerService(t)
	ctx := context.Background()
	userID := uniqueUserID()
	t.Cleanup(func() { cleanupLedgerData(ctx, t, db, userID) })

	now := time.Now()
	today := now.Truncate(24 * time.Hour)
	yesterday := today.AddDate(0, 0, -1)

	spend := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeSpendingBalance, decimal.NewFromInt(100))
	stash := createAndSeed(ctx, t, svc, db, userID, entities.AccountTypeStashBalance, decimal.Zero)

	// Record opening snapshots
	_, err := db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		spend.ID, decimal.NewFromInt(100), yesterday)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		stash.ID, decimal.Zero, yesterday)
	require.NoError(t, err)

	// Execute a transfer: spend → stash $30
	desc := "reconciliation corruption test"
	tx, err := svc.CreateTransaction(ctx, &entities.CreateTransactionRequest{
		UserID:          &userID,
		TransactionType: entities.TransactionTypeInternalTransfer,
		IdempotencyKey:  uniqueKey("reconcile-corrupt"),
		Description:     &desc,
		InitiatedBy:     entities.InitiatedByUser.String(),
		Entries: []entities.CreateEntryRequest{
			{AccountID: spend.ID, EntryType: entities.EntryTypeCredit, Amount: decimal.NewFromInt(30), Currency: "USD"},
			{AccountID: stash.ID, EntryType: entities.EntryTypeDebit, Amount: decimal.NewFromInt(30), Currency: "USD"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, tx)

	// Record closing snapshots with a corrupted value for spend
	// (should be 70, we set 80 to simulate tampering)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		spend.ID, decimal.NewFromInt(80), today)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO ledger_balance_snapshots (account_id, balance, snapshot_date) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		stash.ID, decimal.NewFromInt(30), today)
	require.NoError(t, err)

	// Reconcile — must detect the spend account mismatch
	checked, failures, errs, err := svc.ReconcileDay(ctx, today)
	require.NoError(t, err)
	assert.Equal(t, 2, checked, "both accounts should be checked")
	assert.Equal(t, 1, failures, "should detect 1 corruption")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], spend.ID.String())
	assert.Contains(t, errs[0], "reconciliation mismatch")
}
