package repositories

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newPasscodeSessionTestRepo(t *testing.T) (*PasscodeSessionRepository, *sql.DB, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: passcode session repository test needs Postgres (CI provides it)")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("no Postgres available: %v", err)
	}

	schema := "passcode_sessions_" + strings.NewReplacer(".", "", "-", "").Replace(time.Now().UTC().Format("20060102150405.000000000"))
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		admin.Close()
		t.Fatalf("open schema-scoped pool: %v", err)
	}
	require.NoError(t, db.Ping())

	if _, err := db.Exec(`CREATE TABLE passcode_sessions (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id UUID NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		expires_at TIMESTAMPTZ NOT NULL
	)`); err != nil {
		t.Fatalf("create passcode_sessions: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})

	return NewPasscodeSessionRepository(db, zap.NewNop()), db, uuid.New()
}

func TestPasscodeSessionRepository_CreateValidateConsume(t *testing.T) {
	repo, _, userID := newPasscodeSessionTestRepo(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.CreatePasscodeSession(ctx, userID, "hash-a", now.Add(10*time.Minute)))

	exists, err := repo.PasscodeSessionExists(ctx, userID, "hash-a", now)
	require.NoError(t, err)
	require.True(t, exists)

	// Scoped to the user: another user cannot spend this token.
	other, err := repo.PasscodeSessionExists(ctx, uuid.New(), "hash-a", now)
	require.NoError(t, err)
	require.False(t, other)

	// Single-use: consuming it removes it.
	require.NoError(t, repo.DeletePasscodeSession(ctx, userID, "hash-a"))
	exists, err = repo.PasscodeSessionExists(ctx, userID, "hash-a", now)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestPasscodeSessionRepository_ExpiryIsEnforced(t *testing.T) {
	repo, _, userID := newPasscodeSessionTestRepo(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.CreatePasscodeSession(ctx, userID, "hash-expired", now.Add(time.Minute)))

	exists, err := repo.PasscodeSessionExists(ctx, userID, "hash-expired", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.False(t, exists, "an expired session must not validate")
}

func TestPasscodeSessionRepository_PrunesExpired(t *testing.T) {
	repo, db, userID := newPasscodeSessionTestRepo(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.CreatePasscodeSession(ctx, userID, "hash-old", now.Add(-time.Minute)))
	require.NoError(t, repo.CreatePasscodeSession(ctx, userID, "hash-live", now.Add(time.Hour)))

	deleted, err := repo.DeleteExpiredPasscodeSessions(ctx, now)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	var remaining int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM passcode_sessions`).Scan(&remaining))
	require.Equal(t, 1, remaining)
}
