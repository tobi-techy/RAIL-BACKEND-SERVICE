package paj_offramp_recovery

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
	"go.uber.org/zap/zaptest/observer"
)

func newPAJTestWorker(t *testing.T) (*Worker, *sql.DB, *observer.ObservedLogs) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: Paj offramp recovery test needs Postgres (CI provides it)")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("no Postgres available: %v", err)
	}

	schema := "paj_recovery_" + strings.NewReplacer(".", "", "-", "").Replace(time.Now().UTC().Format("20060102150405.000000000"))
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

	if _, err := db.Exec(`CREATE TABLE paj_orders (
		paj_order_id TEXT PRIMARY KEY,
		user_id UUID NOT NULL,
		order_type TEXT NOT NULL,
		status TEXT NOT NULL,
		deposit_id UUID,
		bridge_transfer_id TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		manual_review_flagged_at TIMESTAMPTZ
	)`); err != nil {
		t.Fatalf("create paj_orders: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})

	core, logs := observer.New(zap.ErrorLevel)
	return NewWorker(db, nil, zap.New(core)), db, logs
}

func seedPAJOrder(t *testing.T, db *sql.DB, orderID, status string, transferID *string, age time.Duration) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO paj_orders (paj_order_id, user_id, order_type, status, bridge_transfer_id, created_at)
		VALUES ($1, $2, 'offramp', $3, $4, NOW() - make_interval(secs => $5))
	`, orderID, uuid.New(), status, transferID, int(age.Seconds()))
	require.NoError(t, err)
}

func strPtr(s string) *string { return &s }

// The escalation used to have no memory, so every pass re-reported every stuck
// order — ~18 of them every 2 minutes, forever, burying the rest of the log.
func TestFlagStartedButStuck_EscalatesEachOrderOnce(t *testing.T) {
	worker, db, logs := newPAJTestWorker(t)
	ctx := context.Background()

	seedPAJOrder(t, db, "stuck-1", "pending", strPtr("circle:"), 12*time.Hour)
	seedPAJOrder(t, db, "stuck-2", "pending", strPtr("circle-cr:abc:7"), 12*time.Hour)

	worker.flagStartedButStuck(ctx, int((6 * time.Hour).Seconds()))
	require.Equal(t, 2, logs.Len(), "both stuck orders should be escalated on the first pass")

	// Everything still stuck, but already reported: no further escalation.
	logs.TakeAll()
	worker.flagStartedButStuck(ctx, int((6 * time.Hour).Seconds()))
	require.Equal(t, 0, logs.Len(), "an already-escalated order must not be re-reported every cycle")

	var flagged int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM paj_orders WHERE manual_review_flagged_at IS NOT NULL`).Scan(&flagged))
	require.Equal(t, 2, flagged, "the escalation is recorded on the row, so it stays queryable")
}

func TestFlagStartedButStuck_IgnoresOrdersOtherPathsHandle(t *testing.T) {
	worker, db, logs := newPAJTestWorker(t)
	ctx := context.Background()

	// No transfer reference: reverseAbandonedOrders/failExpiredOrders own this
	// one and may safely refund it. Escalating it would be wrong.
	seedPAJOrder(t, db, "never-started", "pending", nil, 12*time.Hour)
	// Already terminal.
	seedPAJOrder(t, db, "completed", "completed", strPtr("circle:abc"), 12*time.Hour)
	// Too recent to have hit the hard timeout.
	seedPAJOrder(t, db, "recent", "pending", strPtr("circle:abc"), time.Minute)

	worker.flagStartedButStuck(ctx, int((6 * time.Hour).Seconds()))
	require.Equal(t, 0, logs.Len())
}

func TestFlagStartedButStuck_RespectsTheAgeCutoff(t *testing.T) {
	worker, db, logs := newPAJTestWorker(t)
	ctx := context.Background()

	seedPAJOrder(t, db, "just-over", "pending", strPtr("circle:"), 2*time.Hour)
	seedPAJOrder(t, db, "not-yet", "pending", strPtr("circle:"), 30*time.Minute)

	worker.flagStartedButStuck(ctx, int(time.Hour.Seconds()))

	require.Equal(t, 1, logs.Len())
	require.Contains(t, logs.All()[0].Message, "needs manual review")
	require.Equal(t, "just-over", logs.All()[0].ContextMap()["paj_order_id"])

	var notYetFlagged bool
	require.NoError(t, db.QueryRow(`SELECT manual_review_flagged_at IS NOT NULL FROM paj_orders WHERE paj_order_id = 'not-yet'`).Scan(&notYetFlagged))
	require.False(t, notYetFlagged)
}
