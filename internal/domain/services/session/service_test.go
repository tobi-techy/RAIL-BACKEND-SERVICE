package session

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

// newSessionTestService gives back a Service wired to an isolated Postgres
// schema holding a `sessions` table, with Redis intentionally absent: every
// assertion below is about what the service does WITHOUT Redis.
func newSessionTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: session service test needs Postgres (CI provides it)")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("no Postgres available: %v", err)
	}

	schema := "session_svc_" + strings.NewReplacer(".", "", "-", "").Replace(time.Now().UTC().Format("20060102150405.000000000"))
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	db, err := sql.Open("postgres", dsn+func() string {
		if strings.Contains(dsn, "?") {
			return "&search_path=" + schema
		}
		return "?search_path=" + schema
	}())
	if err != nil {
		admin.Close()
		t.Fatalf("open schema-scoped pool: %v", err)
	}
	require.NoError(t, db.Ping())

	if _, err := db.Exec(`CREATE TABLE sessions (
		id UUID PRIMARY KEY,
		user_id UUID NOT NULL,
		token_hash VARCHAR(64) NOT NULL,
		refresh_token_hash VARCHAR(64) NOT NULL,
		ip_address VARCHAR(45),
		user_agent TEXT,
		device_fingerprint VARCHAR(64),
		location VARCHAR(255),
		is_active BOOLEAN NOT NULL DEFAULT TRUE,
		expires_at TIMESTAMPTZ NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_used_at TIMESTAMPTZ,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create sessions: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})

	return NewService(db, nil, zap.NewNop()), db
}

func seedSession(t *testing.T, db *sql.DB, token string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	// hashToken is sha256-hex; use the service's own hashing by round-tripping
	// through it so the test never reimplements the scheme.
	svc := &Service{}
	_, err := db.Exec(`
		INSERT INTO sessions (id, user_id, token_hash, refresh_token_hash, ip_address, user_agent, device_fingerprint, location, expires_at)
		VALUES ($1, $2, $3, $4, '127.0.0.1', 'go-test', 'fingerprint', 'Lagos', NOW() + INTERVAL '1 hour')
	`, id, uuid.New(), svc.hashToken(token), svc.hashToken(token+"-refresh"))
	require.NoError(t, err)
	return id
}

// Session validation spends one Redis GET per authenticated request. The
// in-process cache removes it for repeat traffic, which is the bulk of it.
func TestValidateSession_ServesRepeatLookupsFromProcessCache(t *testing.T) {
	svc, db := newSessionTestService(t)
	ctx := context.Background()
	token := "access-token-abc"

	id := seedSession(t, db, token)

	first, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err)
	require.Equal(t, id, first.ID)

	// Remove the row: a second lookup can only succeed from the process cache.
	_, err = db.Exec(`DELETE FROM sessions WHERE id = $1`, id)
	require.NoError(t, err)

	second, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err, "second lookup should be served from the in-process cache")
	require.Equal(t, id, second.ID)

	// Once the entry expires the service must fall through to Postgres, which no
	// longer has the row — proving the cache is bounded in time, not permanent.
	svc.localMu.Lock()
	entry := svc.localCache[svc.hashToken(token)]
	entry.expires = time.Now().Add(-time.Second)
	svc.localCache[svc.hashToken(token)] = entry
	svc.localMu.Unlock()

	_, err = svc.ValidateSession(ctx, token)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestInvalidateSessionCache_DropsProcessEntry(t *testing.T) {
	svc, db := newSessionTestService(t)
	ctx := context.Background()
	token := "access-token-logout"
	seedSession(t, db, token)

	_, err := svc.ValidateSession(ctx, token)
	require.NoError(t, err)
	require.NotNil(t, svc.localGet(svc.hashToken(token)))

	// Logout must not leave the session answerable from this process.
	require.NoError(t, svc.InvalidateSession(ctx, token))
	require.Nil(t, svc.localGet(svc.hashToken(token)))
}

// last_used_at used to be written on every authenticated request. It is now
// written at most once per gap, which is what keeps the write out of the hot
// path while still reflecting activity.
func TestMaybeUpdateLastUsed_ThrottlesWrites(t *testing.T) {
	svc, db := newSessionTestService(t)
	id := seedSession(t, db, "access-token-throttle")

	lastUsed := func() *time.Time {
		var v *time.Time
		require.NoError(t, db.QueryRow(`SELECT last_used_at FROM sessions WHERE id = $1`, id).Scan(&v))
		return v
	}

	require.Nil(t, lastUsed(), "precondition: the row starts untouched")

	svc.maybeUpdateLastUsed(id)
	require.Eventually(t, func() bool { return lastUsed() != nil }, 2*time.Second, 20*time.Millisecond,
		"the first call should write last_used_at")

	// Reset the column, then call again inside the throttle window: the second
	// call must NOT write.
	_, err := db.Exec(`UPDATE sessions SET last_used_at = NULL WHERE id = $1`, id)
	require.NoError(t, err)

	svc.maybeUpdateLastUsed(id)
	time.Sleep(300 * time.Millisecond)
	require.Nil(t, lastUsed(), "a second call inside the gap must not write")
}
