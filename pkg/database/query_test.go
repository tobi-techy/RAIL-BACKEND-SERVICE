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

func TestBuildWhereClauseErrorDoesNotReflectInput(t *testing.T) {
	evil := "id = 1 OR 1=1 --"
	_, _, err := BuildWhereClause(map[string]interface{}{evil: "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), evil) {
		t.Fatalf("error must not echo attacker input, got: %v", err)
	}
}

func TestBuildWhereClauseValuesNeverInterpolated(t *testing.T) {
	payload := "x' OR '1'='1'; DROP TABLE users; --"
	where, args, err := BuildWhereClause(map[string]interface{}{"status": payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(where, payload) {
		t.Fatalf("value must never appear in clause, got %q", where)
	}
	if len(args) != 1 || args[0] != payload {
		t.Fatalf("value must be passed as arg, got %v", args)
	}
	if want := " WHERE status = $1"; where != want {
		t.Fatalf("got %q, want %q", where, want)
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

func TestBuildOrderByClauseStrict(t *testing.T) {
	// Empty is OK (no ordering).
	if clause, err := BuildOrderByClauseStrict("", []string{"id"}); err != nil || clause != "" {
		t.Fatalf("empty should return empty+nil, got %q, %v", clause, err)
	}
	// Allowed column works.
	clause, err := BuildOrderByClauseStrict("id DESC", []string{"id", "created_at"})
	if err != nil || clause != " ORDER BY id DESC" {
		t.Fatalf("got %q, %v", clause, err)
	}
	// Non-allowlisted must fail loud.
	if clause, err := BuildOrderByClauseStrict("evil; DROP", []string{"id"}); err == nil || clause != "" {
		t.Fatalf("expected error for invalid column, got %q, %v", clause, err)
	}
	// Legacy wrapper stays fail-silent for compat.
	if got := BuildOrderByClause("evil", []string{"id"}); got != "" {
		t.Fatalf("legacy wrapper should return empty for invalid, got %q", got)
	}
}

func TestBuildOrderByClauseStrictRejectsTrailingTokens(t *testing.T) {
	for _, input := range []string{"id DESC extra", "id ASC foo bar", "id  DESC  now"} {
		if clause, err := BuildOrderByClauseStrict(input, []string{"id"}); err == nil || clause != "" {
			t.Fatalf("input %q: expected error for trailing tokens, got %q, %v", input, clause, err)
		}
	}
	// Extra whitespace between the two valid tokens is fine.
	if clause, err := BuildOrderByClauseStrict("id   DESC", []string{"id"}); err != nil || clause != " ORDER BY id DESC" {
		t.Fatalf("multi-space should be accepted, got %q, %v", clause, err)
	}
}

func TestBuildOrderByClauseStrictRejectsBadDirection(t *testing.T) {
	if clause, err := BuildOrderByClauseStrict("id SIDEWAYS", []string{"id"}); err == nil || clause != "" {
		t.Fatalf("expected error for bad direction, got %q, %v", clause, err)
	}
	if clause, err := BuildOrderByClauseStrict("id desc", []string{"id"}); err != nil || clause != " ORDER BY id DESC" {
		t.Fatalf("lowercase desc should be accepted, got %q, %v", clause, err)
	}
	if clause, err := BuildOrderByClauseStrict("id", []string{"id"}); err != nil || clause != " ORDER BY id ASC" {
		t.Fatalf("bare column should default to ASC, got %q, %v", clause, err)
	}
	// Nil allowlist means every column is rejected, loudly.
	if clause, err := BuildOrderByClauseStrict("id", nil); err == nil || clause != "" {
		t.Fatalf("nil allowlist should reject, got %q, %v", clause, err)
	}
}
