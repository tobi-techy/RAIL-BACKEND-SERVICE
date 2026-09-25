package database

import (
	"strings"
	"testing"
)

func TestBuildWhereClauseEmpty(t *testing.T) {
	where, args, err := BuildWhereClause(nil)
	if err != nil {
		t.Fatalf("expected no error for nil map, got %v", err)
	}
	if where != "" || args != nil {
		t.Fatalf("expected empty clause, got %q, %v", where, args)
	}

	where, args, err = BuildWhereClause(map[string]interface{}{})
	if err != nil {
		t.Fatalf("expected no error for empty map, got %v", err)
	}
	if where != "" || args != nil {
		t.Fatalf("expected empty clause, got %q, %v", where, args)
	}
}

func TestBuildWhereClauseValidKeys(t *testing.T) {
	where, args, err := BuildWhereClause(map[string]interface{}{
		"user_id": "abc",
		"status":  "active",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Keys are sorted, so status ($1) comes before user_id ($2).
	want := " WHERE status = $1 AND user_id = $2"
	if where != want {
		t.Fatalf("got %q, want %q", where, want)
	}
	if len(args) != 2 || args[0] != "active" || args[1] != "abc" {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildWhereClauseTableQualified(t *testing.T) {
	where, args, err := BuildWhereClause(map[string]interface{}{
		"users.id":        1,
		"public.users.id": 2,
		"_col1":           3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(where, " WHERE ") {
		t.Fatalf("missing WHERE prefix: %q", where)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %v", args)
	}
	for _, col := range []string{"users.id = $", "public.users.id = $", "_col1 = $"} {
		if !strings.Contains(where, col) {
			t.Fatalf("expected clause to contain %q, got %q", col, where)
		}
	}
}

func TestBuildWhereClauseRejectsInjection(t *testing.T) {
	malicious := []string{
		"id = 1 OR 1=1 --",
		`"; DROP TABLE users; --`,
		"id; DELETE FROM users",
		"1",
		"123abc",
		`"users"."id"`,
		"users[id]",
		"a b",
		"a-b",
		"a/b",
		"a*",
		"",
		".id",
		"id.",
		"users..id",
	}
	for _, key := range malicious {
		where, args, err := BuildWhereClause(map[string]interface{}{key: "x"})
		if err == nil {
			t.Errorf("key %q: expected error, got clause %q", key, where)
		}
		if where != "" || args != nil {
			t.Errorf("key %q: expected empty result on error, got %q, %v", key, where, args)
		}
	}
}

func TestBuildWhereClauseMixedValidAndInvalidFailsClosed(t *testing.T) {
	where, args, err := BuildWhereClause(map[string]interface{}{
		"status":           "active",
		"id = 1 OR 1=1 --": "x",
	})
	if err == nil {
		t.Fatal("expected error when any key is invalid")
	}
	// Fail-loud: no partial clause that would silently widen the query.
	if where != "" || args != nil {
		t.Fatalf("expected empty result on error, got %q, %v", where, args)
	}
}

func TestBuildWhereClauseDeterministic(t *testing.T) {
	conds := map[string]interface{}{"b": 1, "a": 2, "c": 3}
	first, _, err := BuildWhereClause(conds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, _, err := BuildWhereClause(conds)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != first {
			t.Fatalf("non-deterministic output: %q vs %q", first, got)
		}
	}
	want := " WHERE a = $1 AND b = $2 AND c = $3"
	if first != want {
		t.Fatalf("got %q, want %q", first, want)
	}
}
