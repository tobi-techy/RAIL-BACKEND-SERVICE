//go:build integration
// +build integration

package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"github.com/rail-service/rail_service/pkg/logger"
)

func TestAnonymizeUserPreservesAuditTrail(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			URL: "postgres://test:test@localhost:5432/stack_test?sslmode=disable",
		},
	}

	db, err := database.NewConnection(cfg.Database)
	require.NoError(t, err)
	defer db.Close()

	log := logger.New("debug", "test")
	userRepo := repositories.NewUserRepository(db, log.Zap())
	ctx := context.Background()

	// Create a test user with PII
	userID := uuid.New()
	email := generateTestEmail()
	phone := "+15551234567"
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email, phone, password_hash, is_active, onboarding_status, kyc_status, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'hashed_pw', true, 'completed', 'approved', 'user', $4, $4)`,
		userID, email, phone, time.Now())
	require.NoError(t, err)

	// Create a ledger account linked to this user
	accountID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_accounts (id, user_id, account_type, balance, currency, created_at, updated_at)
		VALUES ($1, $2, 'usdc_balance', 100.00, 'USDC', $3, $3)`,
		accountID, userID, time.Now())
	require.NoError(t, err)

	// Create a ledger entry linked to the account (requires a ledger_transaction)
	txnID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, transaction_type, status, idempotency_key, description, created_at)
		VALUES ($1, 'deposit', 'completed', $2, 'test deposit', $3)`,
		txnID, "test-anon-"+userID.String(), time.Now())
	require.NoError(t, err)

	entryID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount, currency, description, created_at)
		VALUES ($1, $2, $3, 'credit', 100.00, 'USDC', 'test deposit', $4)`,
		entryID, txnID, accountID, time.Now())
	require.NoError(t, err)

	// Anonymize the user
	err = userRepo.AnonymizeUser(ctx, userID)
	require.NoError(t, err)

	// Verify PII is cleared
	var (
		dbEmail       string
		dbPhone       sql.NullString
		dbPassword    string
		dbActive      bool
		dbAnonymized  sql.NullTime
	)
	err = db.QueryRowContext(ctx, `
		SELECT email, phone, password_hash, is_active, anonymized_at
		FROM users WHERE id = $1`, userID).Scan(&dbEmail, &dbPhone, &dbPassword, &dbActive, &dbAnonymized)
	require.NoError(t, err)

	assert.Contains(t, dbEmail, "anonymized-")
	assert.Contains(t, dbEmail, "@deleted.rail.app")
	assert.False(t, dbPhone.Valid, "phone should be NULL")
	assert.Empty(t, dbPassword, "password_hash should be empty")
	assert.False(t, dbActive, "user should be deactivated")
	assert.True(t, dbAnonymized.Valid, "anonymized_at should be set")

	// Verify financial records still exist (RESTRICT FK preserved them)
	var accountCount, entryCount int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_accounts WHERE user_id = $1`, userID).Scan(&accountCount)
	require.NoError(t, err)
	assert.Equal(t, 1, accountCount, "ledger account must be preserved")

	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE account_id = $1`, accountID).Scan(&entryCount)
	require.NoError(t, err)
	assert.Equal(t, 1, entryCount, "ledger entries must be preserved")

	// Verify user UUID still exists (pseudonymous identifier for audit trail)
	var exists bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists, "user row must be preserved for FK references")

	// Verify double-anonymization is rejected
	err = userRepo.AnonymizeUser(ctx, userID)
	assert.Error(t, err, "should reject re-anonymization")

	// Cleanup
	db.ExecContext(ctx, `DELETE FROM ledger_entries WHERE id = $1`, entryID)
	db.ExecContext(ctx, `DELETE FROM ledger_transactions WHERE id = $1`, txnID)
	db.ExecContext(ctx, `DELETE FROM ledger_accounts WHERE id = $1`, accountID)
	db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
}
