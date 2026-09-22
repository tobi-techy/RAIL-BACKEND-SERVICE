//go:build integration
// +build integration

// Vault table integration tests require a running PostgreSQL database.
// Run migrations first: make migrate-up
//
// Usage:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/rail_test?sslmode=disable \
//	go test -tags=integration -run TestVaultTables ./test/integration/vault/ -v -count=1
//
// These tests exist because the in-memory service fakes cannot catch
// driver-level encoding failures. In particular, vault_health.flags is a
// Postgres TEXT[] column: lib/pq rejects a raw []string in both directions,
// so the repository must round-trip it through pq.Array / pq.StringArray.
// A regression here silently disables every health flag (refreshes are
// best-effort), which is exactly how the bug shipped once already.

package vault_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
)

func vaultTestDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://postgres:postgres@localhost:5432/rail_test?sslmode=disable"
}

func newVaultRepo(t *testing.T) (*sqlx.DB, *repositories.VaultRepository) {
	t.Helper()
	db, err := sqlx.Connect("postgres", vaultTestDSN(t))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db, repositories.NewVaultRepository(db)
}

func seedVaultUser(t *testing.T, db *sqlx.DB) (context.Context, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID := uuid.New()
	vaultID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, onboarding_status) VALUES ($1, $2, 'started') ON CONFLICT (id) DO NOTHING`,
		userID, userID.String()+"@test.rail")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO retirement_vaults (id, user_id, tier, status) VALUES ($1, $2, 'balanced_wealth', 'active')`,
		vaultID, userID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM vault_health WHERE vault_id = $1`, vaultID)
		_, _ = db.Exec(`DELETE FROM vault_skips WHERE vault_id = $1`, vaultID)
		_, _ = db.Exec(`DELETE FROM vault_enroll_failures WHERE user_id = $1`, userID)
		_, _ = db.Exec(`DELETE FROM retirement_vaults WHERE id = $1`, vaultID)
		_, _ = db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	return ctx, userID, vaultID
}

func TestVaultHealthFlagsRoundTrip(t *testing.T) {
	db, repo := newVaultRepo(t)
	ctx, userID, vaultID := seedVaultUser(t, db)

	// Missing row reads as (nil, nil).
	health, err := repo.GetHealth(ctx, vaultID)
	require.NoError(t, err)
	require.Nil(t, health)

	err = repo.UpsertHealth(ctx, &entities.VaultHealth{
		VaultID:     vaultID,
		UserID:      userID,
		LedgerUSD:   decimal.NewFromInt(100),
		ProviderUSD: decimal.NewFromInt(85),
		Flags:       []string{"lot_without_fund", "drift_over_threshold"},
	})
	require.NoError(t, err, "upserting health with flags must not fail on TEXT[] encoding")

	health, err = repo.GetHealth(ctx, vaultID)
	require.NoError(t, err, "reading health must not fail on TEXT[] decoding")
	require.NotNil(t, health)
	assert.Equal(t, []string{"lot_without_fund", "drift_over_threshold"}, health.Flags)
	assert.True(t, health.LedgerUSD.Equal(decimal.NewFromInt(100)))
	assert.True(t, health.ProviderUSD.Equal(decimal.NewFromInt(85)))

	// An empty flag set round-trips as empty, and the upsert overwrites.
	err = repo.UpsertHealth(ctx, &entities.VaultHealth{
		VaultID:     vaultID,
		UserID:      userID,
		LedgerUSD:   decimal.NewFromInt(100),
		ProviderUSD: decimal.NewFromInt(101),
		Flags:       []string{},
	})
	require.NoError(t, err)
	health, err = repo.GetHealth(ctx, vaultID)
	require.NoError(t, err)
	require.NotNil(t, health)
	assert.Empty(t, health.Flags)
}

func TestVaultSkipsRoundTrip(t *testing.T) {
	db, repo := newVaultRepo(t)
	ctx, userID, vaultID := seedVaultUser(t, db)
	paymentID := uuid.New()

	found, err := repo.FindSkipByPayment(ctx, paymentID)
	require.NoError(t, err)
	require.Nil(t, found)

	require.NoError(t, repo.CreateSkip(ctx, &entities.VaultSkip{
		VaultID:   vaultID,
		UserID:    userID,
		PaymentID: paymentID,
		Reason:    entities.VaultSkipFloor,
		WouldHave: decimal.NewFromInt(50),
		Spendable: decimal.NewFromInt(30),
		Floor:     decimal.NewFromInt(20),
	}))

	found, err = repo.FindSkipByPayment(ctx, paymentID)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, entities.VaultSkipFloor, found.Reason)
	assert.True(t, found.WouldHave.Equal(decimal.NewFromInt(50)))

	skips, err := repo.ListSkips(ctx, vaultID, 10)
	require.NoError(t, err)
	require.Len(t, skips, 1)
}

func TestVaultTiersRoundTrip(t *testing.T) {
	db, repo := newVaultRepo(t)
	ctx, _, _ := seedVaultUser(t, db)
	strategyID := uuid.New()

	binding, err := repo.GetTier(ctx, entities.VaultTierBalanced)
	require.NoError(t, err)
	require.Nil(t, binding)

	require.NoError(t, repo.UpsertTier(ctx, &entities.VaultTierBinding{
		Tier:           entities.VaultTierBalanced,
		RailStrategyID: strategyID,
		Version:        1,
	}))
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM vault_tiers WHERE tier = 'balanced_wealth'`) })

	binding, err = repo.GetTier(ctx, entities.VaultTierBalanced)
	require.NoError(t, err)
	require.NotNil(t, binding)
	assert.Equal(t, strategyID, binding.RailStrategyID)
	assert.Equal(t, 1, binding.Version)

	bindings, err := repo.ListTiers(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, bindings)
}

func TestVaultEnrollFailureRoundTrip(t *testing.T) {
	db, repo := newVaultRepo(t)
	ctx, userID, _ := seedVaultUser(t, db)

	failure, err := repo.GetEnrollFailure(ctx, userID)
	require.NoError(t, err)
	require.Nil(t, failure)

	require.NoError(t, repo.UpsertEnrollFailure(ctx, &entities.VaultEnrollFailure{
		UserID:    userID,
		LastError: "provider unreachable",
		Tries:     1,
	}))

	failure, err = repo.GetEnrollFailure(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, failure)
	assert.Equal(t, "provider unreachable", failure.LastError)
	assert.Equal(t, 1, failure.Tries)
}
