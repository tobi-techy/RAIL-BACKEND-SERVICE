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

// The integrity report only counted entries-less transactions, so an operator
// saw "2 transaction(s) with 0 entries" and had no way to find them. It now
// names the rows, and this pins that the list matches the count.
func TestLedgerRepository_ListTransactionsWithoutEntries(t *testing.T) {
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

	schema := fmt.Sprintf("ledger_empty_tx_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })

	for _, ddl := range []string{
		`CREATE TABLE ` + schema + `.ledger_transactions (
			id UUID PRIMARY KEY,
			transaction_type TEXT NOT NULL,
			status TEXT NOT NULL,
			reference_type TEXT,
			reference_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE ` + schema + `.ledger_entries (
			id UUID PRIMARY KEY,
			transaction_id UUID NOT NULL
		)`,
	} {
		if _, err := admin.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}

	withEntries := uuid.NewString()
	emptyOld := uuid.NewString()
	emptyNew := uuid.NewString()

	// A balanced transaction: one transaction with two entries.
	if _, err := admin.Exec(
		`INSERT INTO `+schema+`.ledger_transactions (id, transaction_type, status, created_at)
		 VALUES ($1, 'internal_transfer', 'completed', NOW() - INTERVAL '2 hours')`, withEntries); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := admin.Exec(
			`INSERT INTO `+schema+`.ledger_entries (id, transaction_id) VALUES ($1, $2)`,
			uuid.NewString(), withEntries); err != nil {
			t.Fatalf("seed entry: %v", err)
		}
	}

	for _, row := range []struct {
		id string
		at string
	}{{emptyOld, "3 hours"}, {emptyNew, "1 hour"}} {
		if _, err := admin.Exec(
			`INSERT INTO `+schema+`.ledger_transactions (id, transaction_type, status, created_at)
			 VALUES ($1, 'withdrawal', 'completed', NOW() - INTERVAL '`+row.at+`')`, row.id); err != nil {
			t.Fatalf("seed empty transaction: %v", err)
		}
	}

	db, err := sqlx.Connect("postgres", isolateSchema(dsn, schema))
	if err != nil {
		t.Fatalf("connect repository pool: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewLedgerRepository(db)
	ctx := context.Background()

	count, err := repo.CountTransactionsWithoutEntries(ctx)
	if err != nil {
		t.Fatalf("CountTransactionsWithoutEntries: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	empty, err := repo.ListTransactionsWithoutEntries(ctx, 10)
	if err != nil {
		t.Fatalf("ListTransactionsWithoutEntries: %v", err)
	}
	if len(empty) != count {
		t.Fatalf("list returned %d rows for a count of %d", len(empty), count)
	}
	// Newest first, and the transaction that HAS entries must not appear.
	if empty[0].ID.String() != emptyNew {
		t.Fatalf("first row = %s, want the newest empty transaction %s", empty[0].ID, emptyNew)
	}
	if empty[1].ID.String() != emptyOld {
		t.Fatalf("second row = %s, want %s", empty[1].ID, emptyOld)
	}
	for _, e := range empty {
		if e.ID.String() == withEntries {
			t.Fatal("a transaction with entries must not be listed as empty")
		}
		if e.TransactionType != "withdrawal" || e.Status != "completed" {
			t.Fatalf("row detail not populated: %+v", e)
		}
	}

	// The limit is honoured so the report can stay bounded.
	limited, err := repo.ListTransactionsWithoutEntries(ctx, 1)
	if err != nil {
		t.Fatalf("ListTransactionsWithoutEntries(1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited list returned %d rows, want 1", len(limited))
	}
}
