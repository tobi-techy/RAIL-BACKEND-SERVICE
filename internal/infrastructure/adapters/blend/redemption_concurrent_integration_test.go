//go:build integration
// +build integration

package blend

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	circlepkg "github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeBalanceCircle implements CircleWalletProvider, returning a fixed on-chain USDC
// balance. Only GetTokenBalanceOnchain is exercised by finalizeRedemption.
type fakeBalanceCircle struct{ balance decimal.Decimal }

func (f *fakeBalanceCircle) GetWallet(context.Context, string) (*circlepkg.Wallet, error) {
	return &circlepkg.Wallet{}, nil
}
func (f *fakeBalanceCircle) GetTransaction(context.Context, string) (*circlepkg.Transaction, error) {
	return &circlepkg.Transaction{}, nil
}
func (f *fakeBalanceCircle) GetTokenBalanceOnchain(context.Context, string) ([]circlepkg.TokenBalance, error) {
	return []circlepkg.TokenBalance{{Token: circlepkg.TokenInfo{Symbol: "USDC"}, Amount: f.balance.String()}}, nil
}
func (f *fakeBalanceCircle) GetUSDCTokenIDOnchain(context.Context, string) (string, error) {
	return "usdc-token", nil
}
func (f *fakeBalanceCircle) ListCircleWalletsByRefID(context.Context, string) ([]circlepkg.Wallet, error) {
	return nil, nil
}
func (f *fakeBalanceCircle) TransferUSDCWithIdempotency(context.Context, string, string, string, string, string) (*circlepkg.Transaction, error) {
	return &circlepkg.Transaction{}, nil
}

func testDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://test:test@localhost:5432/stack_test?sslmode=disable"
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("integration DB not reachable (%v); skipping", err)
	}
	return db
}

func insertTestUser(t *testing.T, db *sqlx.DB, userID uuid.UUID) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO users (id, email, phone, password_hash, is_active, onboarding_status, kyc_status, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'x', true, 'completed', 'approved', 'user', $4, $4)`,
		userID, "redeem-"+userID.String()+"@test.local", "+1"+userID.String()[:10], time.Now())
	require.NoError(t, err)
}

func insertRedemption(t *testing.T, db *sqlx.DB, userID uuid.UUID, key string, amount, preBalance decimal.Decimal) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO blend_yield_redemptions
			(id, user_id, blend_account_id, amount, destination_chain_id, idempotency_key, status, next_retry_at, pre_redeem_eoa_balance)
		VALUES ($1, $2, 'acct-1', $3, 8453, $4, 'submitted', NOW(), $5)`,
		uuid.New(), userID, amount, key, preBalance)
	require.NoError(t, err)
}

// TestConcurrentRedemptionFinalize_OnlyArrivedFundsSettle proves the delta check: when two
// redemptions for the same user finalize concurrently but the EOA balance only rose enough
// for one of them, exactly one settles and the other is rejected (its funds never arrived).
func TestConcurrentRedemptionFinalize_OnlyArrivedFundsSettle(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	ctx := context.Background()

	userID := uuid.New()
	insertTestUser(t, db, userID)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM blend_yield_redemptions WHERE user_id = $1`, userID)
		db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})

	amount := decimal.NewFromInt(10)
	// R1 snapshotted pre=0 (first to execute); R2 snapshotted pre=10 (after R1's funds were
	// already present). Only R1's funds actually arrived → on-chain balance is 10.
	insertRedemption(t, db, userID, "redeem-r1", amount, decimal.Zero)
	insertRedemption(t, db, userID, "redeem-r2", amount, decimal.NewFromInt(10))

	router := NewDepositRouter(db, nil, &fakeBalanceCircle{balance: decimal.NewFromInt(10)}, nil, 8453, "", zap.NewNop())
	acct := &blendUserAccount{UserID: userID, CircleWalletID: "wallet-1", EOAAddress: "0x0000000000000000000000000000000000000001"}
	session := &Session{Status: IntentStatusSettled}

	red1, err := router.getRedemption(ctx, "redeem-r1")
	require.NoError(t, err)
	red2, err := router.getRedemption(ctx, "redeem-r2")
	require.NoError(t, err)

	reds := []*redemption{red1, red2}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range reds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = router.finalizeRedemption(ctx, acct, reds[i], reds[i].Amount, session)
		}(i)
	}
	wg.Wait()

	nilCount := 0
	for _, e := range errs {
		if e == nil {
			nilCount++
		}
	}
	require.Equal(t, 1, nilCount, "exactly one redemption should finalize; got errs=%v", errs)

	var completed int
	require.NoError(t, db.Get(&completed, `SELECT COUNT(*) FROM blend_yield_redemptions WHERE user_id=$1 AND status='complete'`, userID))
	require.Equal(t, 1, completed, "exactly one redemption row should be complete")
	// The one that settled must be R1 (the funds that actually arrived).
	require.NoError(t, errs[0], "R1 (funds arrived) should settle")
	require.Error(t, errs[1], "R2 (funds did not arrive) must be rejected")
}

// TestConcurrentRedemptionFinalize_BothArrivedSettle proves the check does not reject
// legitimate concurrent redemptions: when both sets of funds arrived (balance rose by the
// full total), both finalize.
func TestConcurrentRedemptionFinalize_BothArrivedSettle(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	ctx := context.Background()

	userID := uuid.New()
	insertTestUser(t, db, userID)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM blend_yield_redemptions WHERE user_id = $1`, userID)
		db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})

	amount := decimal.NewFromInt(10)
	insertRedemption(t, db, userID, "ok-r1", amount, decimal.Zero)
	insertRedemption(t, db, userID, "ok-r2", amount, decimal.NewFromInt(10))

	// Both arrived → on-chain balance is 20.
	router := NewDepositRouter(db, nil, &fakeBalanceCircle{balance: decimal.NewFromInt(20)}, nil, 8453, "", zap.NewNop())
	acct := &blendUserAccount{UserID: userID, CircleWalletID: "wallet-1", EOAAddress: "0x0000000000000000000000000000000000000001"}
	session := &Session{Status: IntentStatusSettled}

	red1, err := router.getRedemption(ctx, "ok-r1")
	require.NoError(t, err)
	red2, err := router.getRedemption(ctx, "ok-r2")
	require.NoError(t, err)

	reds := []*redemption{red1, red2}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range reds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = router.finalizeRedemption(ctx, acct, reds[i], reds[i].Amount, session)
		}(i)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	var completed int
	require.NoError(t, db.Get(&completed, `SELECT COUNT(*) FROM blend_yield_redemptions WHERE user_id=$1 AND status='complete'`, userID))
	require.Equal(t, 2, completed)
}
