package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// isolateSchema points a DSN at one schema. lib/pq forwards unrecognised URL
// query keys to the server as runtime parameters, so search_path applies to
// every connection the pool opens.
func isolateSchema(dsn, schema string) string {
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

// CountSystemAccountDeficits binds its threshold as $1. Dropping that argument
// makes Postgres reject the statement with "there is no parameter $1" — which
// is exactly how the ledger integrity check failed in production: the real
// deficit count was replaced by a SQL error on every run.
func TestLedgerRepository_CountSystemAccountDeficits(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: ledger repository test needs Postgres (CI provides it)")
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		t.Skipf("no Postgres available: %v", err)
	}

	schema := fmt.Sprintf("ledger_deficits_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })

	if _, err := admin.Exec(`CREATE TABLE ` + schema + `.ledger_accounts (
		id UUID PRIMARY KEY,
		user_id UUID,
		balance NUMERIC NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// user_id NULL identifies a system account; only those are counted.
	for _, row := range []struct {
		name    string
		userID  any
		balance string
	}{
		{"system account past the threshold", nil, "-200000"},
		{"system account within it", nil, "-50"},
		{"user account, never counted", uuid.NewString(), "-200000"},
	} {
		if _, err := admin.Exec(
			`INSERT INTO `+schema+`.ledger_accounts (id, user_id, balance) VALUES ($1, $2, $3)`,
			uuid.NewString(), row.userID, row.balance); err != nil {
			t.Fatalf("seed %s: %v", row.name, err)
		}
	}

	db, err := sqlx.Connect("postgres", isolateSchema(dsn, schema))
	if err != nil {
		t.Fatalf("connect repository pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewLedgerRepository(db)

	ctx := context.Background()
	count, err := repo.CountSystemAccountDeficits(ctx, decimal.NewFromInt(-100000))
	if err != nil {
		t.Fatalf("CountSystemAccountDeficits: %v", err)
	}
	if count != 1 {
		t.Fatalf("count past -100000 = %d, want 1", count)
	}

	// A looser threshold must widen the count: proves the value is actually
	// transmitted rather than a constant baked into the query.
	count, err = repo.CountSystemAccountDeficits(ctx, decimal.NewFromInt(-10))
	if err != nil {
		t.Fatalf("CountSystemAccountDeficits (loose threshold): %v", err)
	}
	if count != 2 {
		t.Fatalf("count past -10 = %d, want 2", count)
	}
}
