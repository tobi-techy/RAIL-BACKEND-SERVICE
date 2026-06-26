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

// TestConcurrentRedemptionFinalize_PartialArrivalSettlesNone proves the airtight
// sum-of-claims check: when two redemptions for the same user finalize concurrently but the
// EOA balance only rose enough for ONE of them, NEITHER settles. The funds are fungible — we
// can't tell whose arrived — so refusing both (until enough arrives for both, or a sibling is
// marked failed) is the safe choice: two redemptions can never settle against the same dollars.
func TestConcurrentRedemptionFinalize_PartialArrivalSettlesNone(t *testing.T) {
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
	k1, k2 := userID.String()+"-r1", userID.String()+"-r2"
	// The dangerous case: both snapshotted the same baseline (pre=0) before either's funds
	// arrived. Only one batch of 10 actually landed → on-chain balance is 10, but the two
	// redemptions claim 20 total.
	insertRedemption(t, db, userID, k1, amount, decimal.Zero)
	insertRedemption(t, db, userID, k2, amount, decimal.Zero)

	router := NewDepositRouter(db, nil, &fakeBalanceCircle{balance: decimal.NewFromInt(10)}, nil, 8453, "", zap.NewNop())
	acct := &blendUserAccount{UserID: userID, CircleWalletID: "wallet-1", EOAAddress: "0x0000000000000000000000000000000000000001"}
	session := &Session{Status: IntentStatusSettled}

	red1, err := router.getRedemption(ctx, k1)
	require.NoError(t, err)
	red2, err := router.getRedemption(ctx, k2)
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

	require.Error(t, errs[0], "redemption must not settle: balance covers only one of two claims")
	require.Error(t, errs[1], "redemption must not settle: balance covers only one of two claims")

	var completed int
	require.NoError(t, db.Get(&completed, `SELECT COUNT(*) FROM blend_yield_redemptions WHERE user_id=$1 AND status='complete'`, userID))
	require.Equal(t, 0, completed, "no redemption should settle when only one batch of funds arrived")
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
	k1, k2 := userID.String()+"-ok1", userID.String()+"-ok2"
	insertRedemption(t, db, userID, k1, amount, decimal.Zero)
	insertRedemption(t, db, userID, k2, amount, decimal.NewFromInt(10))

	// Both arrived → on-chain balance is 20.
	router := NewDepositRouter(db, nil, &fakeBalanceCircle{balance: decimal.NewFromInt(20)}, nil, 8453, "", zap.NewNop())
	acct := &blendUserAccount{UserID: userID, CircleWalletID: "wallet-1", EOAAddress: "0x0000000000000000000000000000000000000001"}
	session := &Session{Status: IntentStatusSettled}

	red1, err := router.getRedemption(ctx, k1)
	require.NoError(t, err)
	red2, err := router.getRedemption(ctx, k2)
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
