package app

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/pkg/logger"
)

// The boot-time schema fixers used to run an unconditional
// DROP CONSTRAINT + ADD CONSTRAINT on every replica at every boot. Replicas
// boot together on deploy, so one replica's ADD landed between another's DROP
// and ADD, producing the "constraint already exists" and "connection slots"
// warn storm. These tests pin the two properties that stopped it: the fixer is
// idempotent (a correct constraint is left untouched) and it still repairs a
// stale one.
// withSearchPath points a DSN at one schema. lib/pq forwards an unrecognised
// URL query key to the server as a runtime parameter, so search_path applies to
// every connection the pool opens.
func withSearchPath(dsn, schema string) string {
	if i := strings.Index(dsn, "?"); i >= 0 {
		return dsn + "&search_path=" + schema
	}
	if strings.Contains(dsn, "://") {
		return dsn + "?search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

func newSchemaFixTestApp(t *testing.T) (*Application, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: schema fixer test needs Postgres (CI provides it)")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Skipf("no Postgres available: %v", err)
	}

	schema := "schema_fix_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}

	// lib/pq forwards the unknown search_path query key as a server runtime
	// parameter, so every pooled connection resolves unqualified names inside
	// the isolated schema. It must be appended to the query string, never after
	// sslmode.
	db, err := sql.Open("postgres", withSearchPath(dsn, schema))
	if err != nil {
		admin.Close()
		t.Fatalf("open schema-scoped pool: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		admin.Close()
	})

	return &Application{
		container: &di.Container{DB: db},
		log:       logger.New("error", "test"),
	}, db
}

func constraintDef(t *testing.T, db *sql.DB, table, name string) (string, int) {
	t.Helper()
	var def string
	var oid int
	err := db.QueryRow(`
		SELECT pg_get_constraintdef(c.oid), c.oid
		FROM pg_constraint c
		WHERE c.conrelid = $1::regclass AND c.conname = $2
	`, table, name).Scan(&def, &oid)
	if err == sql.ErrNoRows {
		return "", 0
	}
	if err != nil {
		t.Fatalf("read constraint %s: %v", name, err)
	}
	return def, oid
}

func TestFixOnboardingStatusConstraint_RepairsStaleAndIsIdempotent(t *testing.T) {
	app, db := newSchemaFixTestApp(t)

	if _, err := db.Exec(`CREATE TABLE users (
		id UUID PRIMARY KEY,
		onboarding_status TEXT NOT NULL DEFAULT 'started',
		CONSTRAINT chk_onboarding_status CHECK (onboarding_status IN ('started', 'kyc_pending'))
	)`); err != nil {
		t.Fatalf("create users: %v", err)
	}

	app.fixOnboardingStatusConstraint()

	def, firstOID := constraintDef(t, db, "users", "chk_onboarding_status")
	if !strings.Contains(def, "basic_complete") {
		t.Fatalf("stale constraint not repaired: %s", def)
	}
	if firstOID == 0 {
		t.Fatal("chk_onboarding_status missing after repair")
	}

	// Second boot: everything is already correct, so the constraint must not be
	// dropped and recreated (a new OID would mean the table was locked again).
	app.fixOnboardingStatusConstraint()

	def, secondOID := constraintDef(t, db, "users", "chk_onboarding_status")
	if secondOID != firstOID {
		t.Fatalf("constraint was recreated on a no-op boot: oid %d -> %d", firstOID, secondOID)
	}
	if !strings.Contains(def, "basic_complete") {
		t.Fatalf("constraint lost a status after second boot: %s", def)
	}
}

func TestFixOnboardingStatusConstraint_AddsWhenMissing(t *testing.T) {
	app, db := newSchemaFixTestApp(t)

	if _, err := db.Exec(`CREATE TABLE users (
		id UUID PRIMARY KEY,
		onboarding_status TEXT NOT NULL DEFAULT 'started'
	)`); err != nil {
		t.Fatalf("create users: %v", err)
	}

	app.fixOnboardingStatusConstraint()

	for _, name := range []string{"chk_onboarding_status", "users_onboarding_status_check"} {
		def, _ := constraintDef(t, db, "users", name)
		if !strings.Contains(def, "basic_complete") {
			t.Fatalf("constraint %s not created with the full status set: %q", name, def)
		}
	}
}

func TestDropLegacyVirtualAccountConstraints(t *testing.T) {
	app, db := newSchemaFixTestApp(t)

	if _, err := db.Exec(`CREATE TABLE virtual_accounts (
		id UUID PRIMARY KEY,
		due_account_id UUID,
		account_number TEXT NOT NULL,
		CONSTRAINT virtual_accounts_due_account_id_key UNIQUE (due_account_id),
		CONSTRAINT virtual_accounts_account_number_key UNIQUE (account_number)
	)`); err != nil {
		t.Fatalf("create virtual_accounts: %v", err)
	}

	app.dropLegacyVirtualAccountConstraints()
	// Second run must not fail: the statements are idempotent and now share one
	// transaction with an advisory lock instead of racing other replicas.
	app.dropLegacyVirtualAccountConstraints()

	for _, name := range []string{"virtual_accounts_due_account_id_key", "virtual_accounts_account_number_key"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pg_constraint WHERE conname = $1 AND conrelid = 'virtual_accounts'::regclass`, name).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("legacy constraint %s still present", name)
		}
	}

	// account_number must be nullable now.
	var nullable string
	if err := db.QueryRow(`
		SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'virtual_accounts' AND column_name = 'account_number'
	`).Scan(&nullable); err != nil {
		t.Fatalf("read is_nullable: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("account_number is_nullable = %q, want YES", nullable)
	}
}
